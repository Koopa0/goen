//go:build integration

package account_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/user"
)

// fileReturn writes a return of the order in the state the doors would reach
// through the transition machine, which the fixture skips: a request, an
// approval that owes goodsCents, or a rejection or completion. refunded adds
// the event the payout writes once every source has landed.
func fileReturn(t *testing.T, number, status string, goodsCents int64, refunded bool) {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the return fixture: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatalf("skip the transition triggers: %v", err)
	}
	var orderID, requestID uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM orders WHERE order_number = $1`, number).Scan(&orderID); err != nil {
		t.Fatalf("read order %s: %v", number, err)
	}
	switch status {
	case "requested":
		err = tx.QueryRow(ctx, `
			INSERT INTO return_requests (order_id, reason, status)
			VALUES ($1, '不合用', 'requested') RETURNING id`, orderID).Scan(&requestID)
	case "rejected":
		err = tx.QueryRow(ctx, `
			INSERT INTO return_requests (order_id, reason, status, decided_at)
			VALUES ($1, '不合用', 'rejected', now()) RETURNING id`, orderID).Scan(&requestID)
	default:
		err = tx.QueryRow(ctx, `
			INSERT INTO return_requests (order_id, reason, status, decided_at,
			                             goods_refund_cents, card_refund_cents, credit_refund_cents)
			VALUES ($1, '不合用', $2, now(), $3, 0, $3) RETURNING id`, orderID, status, goodsCents).Scan(&requestID)
	}
	if err != nil {
		t.Fatalf("file a %s return on %s: %v", status, number, err)
	}
	if refunded {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_events (order_id, kind, return_request_id)
			VALUES ($1, 'refunded', $2)`, orderID, requestID); err != nil {
			t.Fatalf("settle the return on %s: %v", number, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the return fixture: %v", err)
	}
}

// TestStore_OpenReturnOrder: only a return still being processed on one of the
// account's own orders is named, and only when it is the one such return. The
// number is never read from the request.
func TestStore_OpenReturnOrder(t *testing.T) {
	s := account.NewStore(pool)
	type fixture struct {
		status     string
		goodsCents int64
		refunded   bool
	}
	for _, tt := range []struct {
		name  string
		files []fixture
		// want is the index of the order that must be named, or -1.
		want int
	}{
		{name: "no return", want: -1},
		{name: "undecided", files: []fixture{{status: "requested"}}, want: 0},
		{name: "approved and unpaid", files: []fixture{{status: "approved", goodsCents: 5000}}, want: 0},
		{name: "approved and paid", files: []fixture{{status: "approved", goodsCents: 5000, refunded: true}}, want: -1},
		{name: "approved owing nothing", files: []fixture{{status: "approved"}}, want: -1},
		{name: "completed", files: []fixture{{status: "completed", goodsCents: 5000, refunded: true}}, want: -1},
		{name: "rejected", files: []fixture{{status: "rejected"}}, want: -1},
		{name: "one open among settled", files: []fixture{{status: "rejected"}, {status: "requested"}}, want: 1},
		{name: "two open", files: []fixture{{status: "requested"}, {status: "approved", goodsCents: 5000}}, want: -1},
	} {
		t.Run(strings.ReplaceAll(tt.name, " ", "_"), func(t *testing.T) {
			u := register(t, "open-return-"+uuid.NewString()+"@example.com")
			numbers := make([]string, len(tt.files))
			for i, f := range tt.files {
				numbers[i] = placeOrderFor(t, u.ID)
				fileReturn(t, numbers[i], f.status, f.goodsCents, f.refunded)
			}
			got, err := s.OpenReturnOrder(t.Context(), u.ID)
			if err != nil {
				t.Fatalf("OpenReturnOrder: %v", err)
			}
			want := ""
			if tt.want >= 0 {
				want = numbers[tt.want]
			}
			if got != want {
				t.Errorf("OpenReturnOrder = %q, want %q", got, want)
			}
		})
	}

	// Each account is told its own return and never another's, whatever else is
	// open in the shop.
	t.Run("another_account", func(t *testing.T) {
		first := register(t, "open-return-first-"+uuid.NewString()+"@example.com")
		second := register(t, "open-return-second-"+uuid.NewString()+"@example.com")
		firstOrder, secondOrder := placeOrderFor(t, first.ID), placeOrderFor(t, second.ID)
		fileReturn(t, firstOrder, "requested", 0, false)
		fileReturn(t, secondOrder, "requested", 0, false)
		for who, want := range map[string]string{first.ID: firstOrder, second.ID: secondOrder} {
			if got, err := s.OpenReturnOrder(t.Context(), who); err != nil || got != want {
				t.Errorf("OpenReturnOrder(%s) = %q, %v; want its own order %q", who, got, err, want)
			}
		}
	})
}

// eraseRefusal is the text of the erase section's refusal and the links in it.
func eraseRefusal(t *testing.T, body string) (text string, hrefs []string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for n := range doc.Descendants() {
		if n.Type != html.ElementNode || n.Data != "p" {
			continue
		}
		if !slices.ContainsFunc(n.Attr, func(a html.Attribute) bool { return a.Key == "id" && a.Val == "erase-refusal" }) {
			continue
		}
		for c := range n.Descendants() {
			switch {
			case c.Type == html.TextNode:
				text += c.Data
			case c.Type == html.ElementNode && c.Data == "a":
				for _, a := range c.Attr {
					if a.Key == "href" {
						hrefs = append(hrefs, a.Val)
					}
				}
			}
		}
		return text, hrefs
	}
	t.Fatal("the erase section says nothing about the refusal")
	return "", nil
}

// TestHandler_Refuse: the account page answers a refusal at 422 with the
// refused section open, naming the one order whose return stands in the way of
// erasure and linking it, and saying it in words when the account has none or
// several to name.
func TestHandler_Refuse(t *testing.T) {
	ctx := t.Context()
	h := account.NewHandler(account.NewStore(pool), slog.New(slog.DiscardHandler), false, false)
	refuse := func(u user.User, refusal auth.AccountRefusal) *httptest.ResponseRecorder {
		t.Helper()
		out := httptest.NewRecorder()
		h.Refuse(out, httptest.NewRequestWithContext(user.NewContext(ctx, u), http.MethodPost, "/account/erase", http.NoBody), refusal)
		if out.Code != http.StatusUnprocessableEntity {
			t.Fatalf("Refuse = %d, want 422", out.Code)
		}
		return out
	}
	generic := auth.AccountRefusal{Form: auth.EraseForm, Notice: i18n.T(ctx, i18n.KeyEraseOpenReturn), OpenReturn: true}

	t.Run("one_return", func(t *testing.T) {
		u := register(t, "refuse-one-"+uuid.NewString()+"@example.com")
		number := placeOrderFor(t, u.ID)
		fileReturn(t, number, "requested", 0, false)
		text, hrefs := eraseRefusal(t, refuse(u, generic).Body.String())
		if want := fmt.Sprintf(i18n.T(ctx, i18n.KeyEraseOpenReturnNamed), number); text != want {
			t.Errorf("the erase refusal says %q, want %q", text, want)
		}
		if !slices.Equal(hrefs, []string{"/orders/" + number}) {
			t.Errorf("the erase refusal links %q, want the order %s", hrefs, number)
		}
	})

	t.Run("two_returns", func(t *testing.T) {
		u := register(t, "refuse-two-"+uuid.NewString()+"@example.com")
		fileReturn(t, placeOrderFor(t, u.ID), "requested", 0, false)
		fileReturn(t, placeOrderFor(t, u.ID), "requested", 0, false)
		text, hrefs := eraseRefusal(t, refuse(u, generic).Body.String())
		if text != generic.Notice || len(hrefs) != 0 {
			t.Errorf("with two open returns the refusal says %q linking %q, want the unnamed sentence with no link", text, hrefs)
		}
	})

	t.Run("not_asked_to_name", func(t *testing.T) {
		u := register(t, "refuse-plain-"+uuid.NewString()+"@example.com")
		fileReturn(t, placeOrderFor(t, u.ID), "requested", 0, false)
		last := auth.AccountRefusal{Form: auth.EraseForm, Notice: i18n.T(ctx, i18n.KeyEraseLastAdmin)}
		text, hrefs := eraseRefusal(t, refuse(u, last).Body.String())
		if text != last.Notice || len(hrefs) != 0 {
			t.Errorf("a refusal that is not about a return says %q linking %q, want its own sentence with no link", text, hrefs)
		}
	})
}
