package cart

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/ui/pages"
)

// sessionCloseObservation records the context at the existing SessionCloser
// boundary; it has no production role.
type sessionCloseObservation struct {
	called      bool
	contextErr  error
	value       string
	hasDeadline bool
	remaining   time.Duration
}

func (o *sessionCloseObservation) ExpireSession(ctx context.Context, _ string) error {
	o.called = true
	o.contextErr = ctx.Err()
	o.value, _ = ctx.Value(struct{ name string }{"trace"}).(string)
	deadline, ok := ctx.Deadline()
	o.hasDeadline = ok
	if ok {
		o.remaining = time.Until(deadline)
	}
	return nil
}

// TestPostCommitSessionExpiryOwnsItsContext proves a disconnected request does
// not cancel cleanup for an already-committed order. Request values survive for
// tracing, while one short deadline bounds the whole provider cleanup batch.
func TestPostCommitSessionExpiryOwnsItsContext(t *testing.T) {
	key := struct{ name string }{"trace"}
	requestCtx := context.WithValue(t.Context(), key, "request-trace")
	requestCtx, cancelRequest := context.WithCancel(requestCtx)
	cancelRequest()
	if requestCtx.Err() == nil {
		t.Fatal("test request context is not cancelled")
	}

	observed := &sessionCloseObservation{}
	h := &Handler{sessions: observed, log: slog.New(slog.DiscardHandler)}
	h.closeSessions(requestCtx, "GOEN-TEST", []string{"cs_test"})

	if !observed.called {
		t.Fatal("ExpireSession was not called")
	}
	if observed.contextErr != nil {
		t.Errorf("ExpireSession context error = %v; want request cancellation detached",
			observed.contextErr)
	}
	if observed.value != "request-trace" {
		t.Errorf("ExpireSession context value = %v, want request-trace", observed.value)
	}
	if !observed.hasDeadline {
		t.Fatal("ExpireSession context has no deadline")
	}
	if observed.remaining <= 0 || observed.remaining > 5*time.Second {
		t.Errorf("ExpireSession context had %v remaining; want a live deadline no more than 5s away",
			observed.remaining)
	}
}

// TestSignedInCartLookupFallsBackToTheAccountCart holds the HTTP resolution
// after a merge-adopt: the cookie still names the deleted guest row, and a
// signed-in add must not mint an unowned cart.
func TestSignedInCartLookupFallsBackToTheAccountCart(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "store.ForUser(") {
		t.Error("signed-in cart lookup does not resolve store.ForUser when the cookie misses")
	}
	if strings.Contains(body, "Create(r.Context(), token, uuid.NullUUID{})") {
		t.Error("signed-in add still mints an unowned cart")
	}
}

// TestRefusedCouponLeavesTheQuoteOfThePageWithoutIt locks that a typed code the
// shop did not apply does not change the quote: the shopper who clears it must
// be submitting the page they were shown.
func TestRefusedCouponLeavesTheQuoteOfThePageWithoutIt(t *testing.T) {
	t.Parallel()
	cartID := uuid.MustParse("018f0000-0000-7000-8000-000000000001")
	view := func() *pages.CheckoutView {
		return &pages.CheckoutView{
			Chosen: "018f0000-0000-7000-8000-000000000021",
			Cart: pages.CartView{
				SubtotalCents: 12000,
				Lines: []pages.CartLine{{
					VariantID: "018f0000-0000-7000-8000-000000000011",
					Quantity:  1, UnitCents: 12000,
				}},
			},
		}
	}
	quote := func(v *pages.CheckoutView) checkoutQuoteID {
		t.Helper()
		id, err := checkoutQuoteIDForView(cartID, v)
		if err != nil {
			t.Fatalf("quote: %v", err)
		}
		return id
	}

	plain := quote(view())
	refused := view()
	refused.CouponCode = "NOPE"
	if got := quote(refused); got != plain {
		t.Errorf("refused code changed the quote: got %s, want %s", got, plain)
	}

	applied := view()
	applied.CouponCode = "SAVE100"
	applied.CouponApplied = "save"
	applied.CouponDiscountCents = 10000
	if got := quote(applied); got == plain {
		t.Error("applied coupon left the quote unchanged")
	}
}

func TestAddFromTheWishlistComesBackToTheWishlist(t *testing.T) {
	t.Parallel()
	back := func(form url.Values) string {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/cart/items", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		(&Handler{}).backToProduct(rec, req, uuid.Nil, pages.AddOutcomeAdded)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", rec.Code)
		}
		return rec.Header().Get("Location")
	}
	if got, want := back(url.Values{"return": {"/account/wishlist"}}), "/account/wishlist?added=added"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if got := back(url.Values{"return": {"https://evil.example/"}}); got != "/cart" {
		t.Errorf("a return that is not the wishlist went to %q, want /cart", got)
	}
}
