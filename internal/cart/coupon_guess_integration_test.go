//go:build integration

package cart_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
)

// TestWrongCouponCodesAreBoundedWithoutSayingWhichCodeWasRight: a wrong code and
// a right one answer differently at checkout, so each shopper may be told a
// code is wrong only so many times. The refusal comes before the lookup, so a
// shopper who has run out learns nothing about the next code, right or wrong,
// and a new cart from the same client starts with nothing.
func TestWrongCouponCodesAreBoundedWithoutSayingWhichCodeWasRight(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "coupon-guess")
	valid := "GUESSRIGHT" + strings.ToUpper(uuid.NewString()[:6])
	coupon(t, valid, "amount", 1000, 0, 0, 0, 0)
	ship := shipVersionFor(t, "home_delivery")
	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false,
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}),
		nil, nil)

	guestCart := func() string {
		token, err := cart.NewToken()
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		id, err := s.Create(ctx, token, uuid.NullUUID{})
		if err != nil {
			t.Fatalf("create cart: %v", err)
		}
		if err := s.Add(ctx, id, vid, 1); err != nil {
			t.Fatalf("add item: %v", err)
		}
		return token
	}
	// A chooser change: the coupon is looked up and the page re-rendered, and
	// nothing is placed, which is the cheapest way to ask about a code.
	ask := func(token, remote, code string) *httptest.ResponseRecorder {
		form := url.Values{"coupon": {code}, "update": {"shipping"}, "shipping": {ship.String()}}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = remote
		req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cart cookie under test
		res := httptest.NewRecorder()
		h.PlaceOrder(res, req)
		return res
	}
	unknown := i18n.T(ctx, i18n.KeyCouponUnknown)

	shopper := guestCart()
	const client = "198.51.100.20:5000"
	for i := range 3 {
		res := ask(shopper, client, "TYPO"+strconv.Itoa(i))
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), unknown) {
			t.Fatalf("mistyped code %d answered %d without the unknown-code message", i+1, res.Code)
		}
	}
	// A code that applies is never charged, however often the page re-renders.
	for i := range 30 {
		res := ask(shopper, client, valid)
		if res.Code != http.StatusOK || strings.Contains(res.Body.String(), unknown) {
			t.Fatalf("re-render %d with a code that applies answered %d", i+1, res.Code)
		}
	}

	refusedAfter := 0
	for i := range 40 {
		if res := ask(shopper, client, "WRONG"+strconv.Itoa(i)); res.Code == http.StatusTooManyRequests {
			refusedAfter = i
			break
		}
	}
	if refusedAfter == 0 {
		t.Fatal("43 wrong codes from one cart were never refused")
	}
	for name, code := range map[string]string{"a wrong code": "WRONGAGAIN", "the right code": valid} {
		res := ask(shopper, client, code)
		if res.Code != http.StatusTooManyRequests {
			t.Errorf("once refused, %s answered %d; the answer must not depend on the code", name, res.Code)
		}
		if res.Header().Get("Retry-After") == "" {
			t.Errorf("the refusal of %s carries no Retry-After", name)
		}
	}
	if res := ask(shopper, client, ""); res.Code == http.StatusTooManyRequests {
		t.Error("a checkout with no code was refused; only codes are charged")
	}

	if res := ask(guestCart(), client, "WRONGFRESHCART"); res.Code != http.StatusTooManyRequests {
		t.Errorf("a fresh cart from the same client answered %d, want 429: a new cart "+
			"must not buy a new allowance", res.Code)
	}
	if res := ask(guestCart(), "198.51.100.21:5000", "WRONGELSEWHERE"); res.Code != http.StatusOK {
		t.Errorf("another client with its own cart answered %d, want 200", res.Code)
	}
}

// couponMissBudget is how many wrong codes NewHandler lets one shopper be told
// about before the first refusal.
const couponMissBudget = 20

// couponGuesses is one handler, whose coupon budget is the only bound in play,
// and what a checkout needs besides a code.
type couponGuesses struct {
	t    *testing.T
	s    *cart.Store
	h    *cart.Handler
	vid  uuid.UUID
	ship uuid.UUID
}

func newCouponGuesses(t *testing.T, label string) *couponGuesses {
	t.Helper()
	s := cart.NewStore(pool)
	return &couponGuesses{
		t: t, s: s,
		h: cart.NewHandler(s, slog.New(slog.DiscardHandler), false,
			ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}),
			nil, nil),
		vid:  freshVariant(t, label),
		ship: shipVersionFor(t, "home_delivery"),
	}
}

// guestCart opens a cart no account owns, holding one unit, and returns its
// cookie value.
func (g *couponGuesses) guestCart() string {
	g.t.Helper()
	token, err := cart.NewToken()
	if err != nil {
		g.t.Fatalf("token: %v", err)
	}
	id, err := g.s.Create(g.t.Context(), token, uuid.NullUUID{})
	if err != nil {
		g.t.Fatalf("create cart: %v", err)
	}
	if err := g.s.Add(g.t.Context(), id, g.vid, 1); err != nil {
		g.t.Fatalf("add item: %v", err)
	}
	return token
}

// ask posts a chooser change carrying code from remote with the cart token,
// signed in as who when who is not nil. A chooser change looks the code up and
// re-renders, and places nothing.
func (g *couponGuesses) ask(token, remote, code string, who *account.User) *httptest.ResponseRecorder {
	form := url.Values{"coupon": {code}, "update": {"shipping"}, "shipping": {g.ship.String()}}
	req := httptest.NewRequestWithContext(g.t.Context(), http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = remote
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cart cookie under test
	if who != nil {
		req = req.WithContext(account.WithUser(req.Context(), *who))
	}
	res := httptest.NewRecorder()
	g.h.PlaceOrder(res, req)
	return res
}

// spendFromManyClients is told a code is wrong couponMissBudget times, each
// time from another client, so no client's own key comes near its limit.
func (g *couponGuesses) spendFromManyClients(cartFor func() string, who *account.User) {
	g.t.Helper()
	unknown := i18n.T(g.t.Context(), i18n.KeyCouponUnknown)
	for i := range couponMissBudget {
		res := g.ask(cartFor(), "203.0.113."+strconv.Itoa(i+1)+":5000", "SPREAD"+strconv.Itoa(i), who)
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), unknown) {
			g.t.Fatalf("wrong code %d from its own client answered %d without the unknown-code message",
				i+1, res.Code)
		}
	}
}

// TestWrongCouponCodesSentAtOnceAreBoundedByTheSameBudget: the budget is taken
// before the lookup, so codes sent together are no cheaper than codes sent one
// after another. However many arrive at once, no more than the budget are
// looked up and every other one is refused before it is.
func TestWrongCouponCodesSentAtOnceAreBoundedByTheSameBudget(t *testing.T) {
	g := newCouponGuesses(t, "coupon-race")
	token := g.guestCart()
	unknown := i18n.T(t.Context(), i18n.KeyCouponUnknown)
	const parallel = 3 * couponMissBudget

	var told, refused atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range parallel {
		wg.Go(func() {
			<-start
			res := g.ask(token, "198.51.100.77:5000", "RACE"+strconv.Itoa(i), nil)
			switch {
			case res.Code == http.StatusTooManyRequests:
				refused.Add(1)
			case res.Code == http.StatusOK && strings.Contains(res.Body.String(), unknown):
				told.Add(1)
			default:
				t.Errorf("a wrong code answered %d without the unknown-code message", res.Code)
			}
		})
	}
	close(start)
	wg.Wait()
	if told.Load() > couponMissBudget {
		t.Errorf("%d wrong codes sent at once were looked up and %d refused; one client and one "+
			"cart may be told about at most %d", told.Load(), refused.Load(), couponMissBudget)
	}
	if told.Load()+refused.Load() != parallel {
		t.Errorf("%d told and %d refused of %d sent", told.Load(), refused.Load(), parallel)
	}
}

// TestACodeAnsweredBeforeItsLookupCostsNothing: a checkout carrying a code can
// be answered before the code is looked up, when the cart was emptied in
// another tab. Nothing was learned about the code, so the tokens set aside for
// it go back, and no number of such answers refuses the client a code later.
func TestACodeAnsweredBeforeItsLookupCostsNothing(t *testing.T) {
	g := newCouponGuesses(t, "coupon-unlooked")
	valid := "UNLOOKED" + strings.ToUpper(uuid.NewString()[:6])
	coupon(t, valid, "amount", 1000, 0, 0, 0, 0)
	const client = "198.51.100.88:5000"

	token, err := cart.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if _, err := g.s.Create(t.Context(), token, uuid.NullUUID{}); err != nil {
		t.Fatalf("create cart: %v", err)
	}
	for i := range couponMissBudget + 5 {
		res := g.ask(token, client, "EMPTIED"+strconv.Itoa(i), nil)
		switch {
		case res.Code == http.StatusSeeOther && res.Header().Get("Location") == "/cart":
		case res.Code == http.StatusTooManyRequests:
			t.Fatalf("code %d for an empty cart was refused 429: the codes before it were charged "+
				"although none was looked up", i+1)
		default:
			t.Fatalf("a code for an empty cart answered %d to %q, want 303 to /cart; "+
				"the test needs an answer that comes before the lookup", res.Code, res.Header().Get("Location"))
		}
	}

	unknown := i18n.T(t.Context(), i18n.KeyCouponUnknown)
	if res := g.ask(g.guestCart(), client, valid, nil); res.Code != http.StatusOK ||
		strings.Contains(res.Body.String(), unknown) {
		t.Errorf("after %d answers that came before any lookup, the client's code that applies "+
			"answered %d; nothing was looked up, so nothing may be charged", couponMissBudget+5, res.Code)
	}
}

// TestACartsWrongCouponCodesAreBoundedFromEveryClient: signed out, a wrong code
// is charged to the cart as well as the client, so one cart asking through many
// addresses has one budget between them.
func TestACartsWrongCouponCodesAreBoundedFromEveryClient(t *testing.T) {
	g := newCouponGuesses(t, "coupon-cart-key")
	token := g.guestCart()
	g.spendFromManyClients(func() string { return token }, nil)

	if res := g.ask(token, "203.0.113.200:5000", "SPREADMORE", nil); res.Code != http.StatusTooManyRequests {
		t.Errorf("the cart's next code from a client it never used answered %d, want 429: "+
			"changing address bought the cart a fresh allowance", res.Code)
	}
	if res := g.ask(g.guestCart(), "203.0.113.201:5000", "ELSEWHERE", nil); res.Code != http.StatusOK {
		t.Errorf("another cart from another client answered %d, want 200", res.Code)
	}
}

// TestAnAccountsWrongCouponCodesAreBoundedFromEveryClientAndCart: signed in, a
// wrong code is charged to the account, so neither a new cart nor a new address
// buys the account a fresh allowance.
func TestAnAccountsWrongCouponCodesAreBoundedFromEveryClientAndCart(t *testing.T) {
	g := newCouponGuesses(t, "coupon-account-key")
	var userID uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"coupon-guesser-"+uuid.NewString()+"@example.com").Scan(&userID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	who := &account.User{ID: userID.String(), Role: "customer"}
	g.spendFromManyClients(g.guestCart, who)

	if res := g.ask(g.guestCart(), "203.0.113.200:5000", "SPREADMORE", who); res.Code != http.StatusTooManyRequests {
		t.Errorf("the account's next code from a new cart and a new client answered %d, want 429: "+
			"a new cart and address bought the account a fresh allowance", res.Code)
	}
	if res := g.ask(g.guestCart(), "203.0.113.201:5000", "ELSEWHERE", nil); res.Code != http.StatusOK {
		t.Errorf("a signed-out cart from another client answered %d, want 200", res.Code)
	}
}
