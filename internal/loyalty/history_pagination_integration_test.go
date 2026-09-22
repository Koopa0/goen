//go:build integration

package loyalty_test

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/loyalty"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestPointsPaginationKeepsWholeSpendsAndOlderClawbacksReachable(t *testing.T) {
	userID, accountID := customer(t, 0)
	otherID, _ := customer(t, 0)
	orderID := orderFor(t, userID, 10000)
	ctx := account.WithUser(i18n.WithLocale(t.Context(), i18n.En), account.User{ID: userID})
	if _, err := pool.Exec(ctx, `
 INSERT INTO loyalty_entries (account_id,kind,points,reason,idempotency_key,expires_on,created_at)
 SELECT $1,'award',n,'pagination','page:' || ($1::uuid)::text || ':' || n,shop_today()+365,
 CASE WHEN n=1 THEN '2026-01-01'::timestamptz ELSE '2026-02-01'::timestamptz END
 FROM generate_series(1,101) n`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
 INSERT INTO loyalty_entries (account_id,kind,points,reason,idempotency_key,expires_on,lot_id,created_at)
 SELECT account_id,'spend',-15,'redeem','split:' || ($1::uuid)::text || '#' || points,expires_on,id,'2026-02-02'::timestamptz
 FROM loyalty_entries WHERE account_id=$1 AND kind='award' AND points IN (100,101)`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
 INSERT INTO loyalty_entries (account_id,kind,points,reason,idempotency_key,expires_on,lot_id,created_at)
 SELECT account_id,'spend',-1,'redeem','single:' || ($1::uuid)::text,expires_on,id,'2026-01-02'::timestamptz
 FROM loyalty_entries WHERE account_id=$1 AND kind='award' AND points=1`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
 INSERT INTO loyalty_entries (account_id,kind,points,reason,idempotency_key,expires_on,lot_id,created_at,requested_points,order_id)
 SELECT account_id,'clawback',0,'refund','clawback:' || ($1::uuid)::text,expires_on,id,'2026-01-03'::timestamptz,1,$2
 FROM loyalty_entries WHERE account_id=$1 AND kind='award' AND points=1`, accountID, orderID); err != nil {
		t.Fatal(err)
	}
	s := loyalty.NewStore(pool)
	h := loyalty.NewHandler(s, slog.New(slog.DiscardHandler))
	seen := map[string]bool{}
	target := "/account/points"
	clawbackSeen := false
	for page := range 3 {
		u, err := url.Parse(target)
		if err != nil {
			t.Fatal(err)
		}
		v, err := s.History(ctx, userID, u.Query().Get("after"))
		if err != nil {
			t.Fatal(err)
		}
		want := 50
		if page == 2 {
			want = 4
		}
		if len(v.Entries) != want {
			t.Fatalf("page %d has %d groups, want %d", page, len(v.Entries), want)
		}
		for _, e := range v.Entries {
			key := fmt.Sprintf("%s:%d", e.Kind, e.Points)
			if seen[key] {
				t.Fatalf("group repeated or split: %s", key)
			}
			seen[key] = true
			if e.Kind == pages.PointsClawedBack {
				clawbackSeen = true
				if e.RequestedPoints != 1 || e.ShortfallPoints != 1 {
					t.Fatal("older clawback lost its shortfall")
				}
			}
		}
		w := httptest.NewRecorder()
		h.Page(w, httptest.NewRequestWithContext(ctx, http.MethodGet, target, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("GET page %d: %d", page, w.Code)
		}
		if page > 0 && !strings.Contains(w.Body.String(), "Latest entries") {
			t.Fatal("later ledger page lost its restart link")
		}
		if v.HistoryNext != "" && !strings.Contains(w.Body.String(), `href="`+html.EscapeString(v.HistoryNext)+`"`) {
			t.Fatal("ledger lost its plain next link")
		}
		if page == 0 {
			if v.Balance != 5120 {
				t.Fatalf("balance=%d, want full-ledger 5120", v.Balance)
			}
			assertHistoryCursorSurvivesInsertion(t, s, userID, otherID, accountID, v.HistoryNext)
		}
		target = v.HistoryNext
	}
	if len(seen) != 104 || target != "" || !clawbackSeen || !seen["spend:-30"] {
		t.Fatalf("incomplete grouped ledger: %d groups, next=%q, clawback=%t", len(seen), target, clawbackSeen)
	}
}

func assertHistoryCursorSurvivesInsertion(t *testing.T, s *loyalty.Store, userID, otherID string, accountID uuid.UUID, target string) {
	t.Helper()
	ctx := t.Context()

	next, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.History(ctx, userID, next.Query().Get("after"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO loyalty_entries (account_id,kind,points,reason,idempotency_key,expires_on) VALUES ($1,'award',1000,'newer','new:' || ($1::uuid)::text,shop_today()+365)`, accountID); err != nil {
		t.Fatal(err)
	}
	after, err := s.History(ctx, userID, next.Query().Get("after"))
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(before.Entries) != fmt.Sprint(after.Entries) {
		t.Fatal("new entry displaced the next ledger page")
	}
	theirs, err := s.History(ctx, otherID, next.Query().Get("after"))
	if err != nil {
		t.Fatal(err)
	}
	if len(theirs.Entries) != 0 {
		t.Fatal("cursor exposed another account's points")
	}
}
