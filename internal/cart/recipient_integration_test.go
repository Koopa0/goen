//go:build integration

package cart_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/cart"
)

// aMember is a signed-in customer with a name and phone on file, a cart holding
// one thing, and the request context that says who they are.
func aMember(t *testing.T, s *cart.Store, label string) (token string, user account.User) {
	t.Helper()
	email := label + "-" + uuid.NewString() + "@example.com"
	var userID uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, full_name, phone) VALUES ($1, '王小明', '0912345678')
		RETURNING id`, email).Scan(&userID); err != nil {
		t.Fatalf("create member: %v", err)
	}
	tok, err := cart.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	cartID, err := s.Create(t.Context(), tok, uuid.NullUUID{UUID: userID, Valid: true})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if err := s.Add(t.Context(), cartID, freshVariant(t, label), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	return tok, account.User{ID: userID.String(), Email: email, Name: "王小明", Role: account.RoleCustomer}
}

func checkoutAs(
	t *testing.T, h *cart.Handler, token string, who *account.User, query string,
) string {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/checkout"+query, http.NoBody)
	//nolint:gosec // G124: the browser's own cart cookie
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
	if who != nil {
		req = req.WithContext(account.WithUser(req.Context(), *who))
	}
	res := httptest.NewRecorder()
	h.Checkout(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("checkout = %d, want 200; body=%s", res.Code, res.Body.String())
	}
	return res.Body.String()
}

// TestAMemberCheckoutIsFilledFromTheAccount: the recipient is the member by
// default, from the account's own name, phone and email.
func TestAMemberCheckoutIsFilledFromTheAccount(t *testing.T) {
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false, testLimiter(), nil, nil)
	token, user := aMember(t, s, "recipient-member")

	page := checkoutAs(t, h, token, &user, "")
	for field, want := range map[string]string{
		"name": "王小明", "phone": "0912345678", "email": user.Email,
	} {
		if got, ok := inputValue(page, field); !ok || got != want {
			t.Errorf("the %s field is %q (present %v), want %q", field, got, ok, want)
		}
	}
	box := recipientBox(t, page)
	if !strings.Contains(box, "checked") {
		t.Errorf("the recipient box is not checked for a member with a name and phone on file: %s", box)
	}
}

func TestAGuestCheckoutHasNeitherControlAndNoPrefill(t *testing.T) {
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false, testLimiter(), nil, nil)
	// A cart nobody owns: a signed-out request for a member's cart is turned away.
	token, err := cart.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	cartID, err := s.Create(t.Context(), token, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if err := s.Add(t.Context(), cartID, freshVariant(t, "recipient-guest"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	page := checkoutAs(t, h, token, nil, "")
	if strings.Contains(page, "data-recipient-me") || strings.Contains(page, "data-address-book") {
		t.Error("a guest was offered the member's controls")
	}
	for _, field := range []string{"name", "phone"} {
		if got, _ := inputValue(page, field); got != "" {
			t.Errorf("a guest's %s field was filled with %q", field, got)
		}
	}
}

// TestTheRecipientBoxAppliedByTheServerTicksAndRestores: ticking the box without
// scripting puts the account's name and phone over what was typed, and the form
// carries what was there so unticking can put it back.
func TestTheRecipientBoxAppliedByTheServerTicksAndRestores(t *testing.T) {
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false, testLimiter(), nil, nil)
	token, user := aMember(t, s, "recipient-typed")

	apply := func(fields url.Values) string {
		fields.Set("update", "recipient")
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout",
			strings.NewReader(fields.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		//nolint:gosec // G124: the browser's own cart cookie
		req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
		req = req.WithContext(account.WithUser(req.Context(), user))
		res := httptest.NewRecorder()
		h.PlaceOrder(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("applying the recipient box = %d, want 200", res.Code)
		}
		return res.Body.String()
	}

	ticked := apply(url.Values{"recipient_me": {"1"}, "name": {"林小美"}, "phone": {"0987654321"}})
	for field, want := range map[string]string{
		"name": "王小明", "phone": "0912345678",
		"recipient_prev_name": "林小美", "recipient_prev_phone": "0987654321",
	} {
		if got, _ := inputValue(ticked, field); got != want {
			t.Errorf("after ticking, %s is %q, want %q", field, got, want)
		}
	}
	if box := recipientBox(t, ticked); !strings.Contains(box, "checked") {
		t.Errorf("the box is not ticked over the account's own values: %s", box)
	}

	restored := apply(url.Values{
		"name": {"王小明"}, "phone": {"0912345678"},
		"recipient_prev_name": {"林小美"}, "recipient_prev_phone": {"0987654321"},
	})
	for field, want := range map[string]string{"name": "林小美", "phone": "0987654321"} {
		if got, _ := inputValue(restored, field); got != want {
			t.Errorf("after unticking, %s is %q, want %q", field, got, want)
		}
	}
	if box := recipientBox(t, restored); strings.Contains(box, "checked") {
		t.Errorf("the box is ticked over someone else's name: %s", box)
	}
}

// TestARestoredDraftIsNotOverwrittenByTheAccount: what the member typed before
// the map stands, and the box reports that it is not the account's.
func TestARestoredDraftIsNotOverwrittenByTheAccount(t *testing.T) {
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false, testLimiter(), nil, configuredMap(t))
	token, user := aMember(t, s, "recipient-draft")

	shipping := shipVersionFor(t, "store_pickup")
	fields := aStart(shipping)
	fields.Set("name", "林小美")
	fields.Set("phone", "0987654321")
	fields.Set("email", user.Email)
	body, cookie, status := startPickupAs(t, h, token, &user, fields)
	if status != http.StatusOK {
		t.Fatalf("the hand-off page = %d, want 200", status)
	}
	nonce, _ := hiddenInputValue(body, "ExtraData")

	target, _, _ := theMapAnswers(t, h, nonce, "131386", "南港園區", "台北市南港區三重路19-2號")
	back := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	//nolint:gosec // G124: the browser's own cart cookie
	back.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
	back.AddCookie(cookie)
	back = back.WithContext(account.WithUser(back.Context(), user))
	res := httptest.NewRecorder()
	h.Checkout(res, back)

	page := res.Body.String()
	if got, _ := inputValue(page, "name"); got != "林小美" {
		t.Errorf("the account overwrote the restored name with %q", got)
	}
	if got, _ := inputValue(page, "phone"); got != "0987654321" {
		t.Errorf("the account overwrote the restored phone with %q", got)
	}
	if box := recipientBox(t, page); strings.Contains(box, "checked") {
		t.Errorf("the box is checked though the recipient is not the member: %s", box)
	}
}

func recipientBox(t *testing.T, body string) string {
	t.Helper()
	m := regexp.MustCompile(`<input[^>]*data-recipient-me[^>]*>`).FindString(body)
	if m == "" {
		t.Fatal("the page has no recipient box")
	}
	return m
}
