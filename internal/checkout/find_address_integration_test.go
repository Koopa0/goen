//go:build integration

package checkout_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/web/ratelimit"
)

// TestBuyerHandler_FindOrderAddressBound: buyer.Handler.FindOrder bounds the
// lookups of one address from any client: the address runs out whatever the
// clients, the right number is then refused like a wrong one, and another
// address from the same clients is untouched. An address is guessable from
// outside, so it is the weak half of the pair the lookup asks for, and a bound
// per client alone is lifted by asking from somewhere else. Each ask here comes
// from a different client, IPv4 and IPv6 alike; the address runs out anyway,
// the right number is then refused like a wrong one, and another address from
// the same clients is untouched.
func TestBuyerHandler_FindOrderAddressBound(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	victim := "find-bound-" + uuid.NewString() + "@example.com"
	number := placeUnpaidOrderFor(t, s, victim)
	// A per-client bound too loose to be what refuses anything below.
	h := buyer.NewHandler(buyer.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler),
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}),
		false)

	client := 0
	find := func(orderNumber, addr string) *httptest.ResponseRecorder {
		client++
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/find",
			strings.NewReader(url.Values{"number": {orderNumber}, "email": {addr}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if client%2 == 0 {
			req.RemoteAddr = fmt.Sprintf("192.0.2.%d:4000", client)
		} else {
			req.RemoteAddr = fmt.Sprintf("[2001:db8:%x::1]:4000", client)
		}
		res := httptest.NewRecorder()
		h.FindOrder(res, req)
		return res
	}

	refusedAt := 0
	for i := range 20 {
		wrong := fmt.Sprintf("GO-000101-%06d", i+1)
		addr := victim
		if i%2 == 1 {
			addr = " " + strings.ToUpper(victim) + " "
		}
		res := find(wrong, addr)
		if res.Code == http.StatusTooManyRequests {
			refusedAt = i + 1
			break
		}
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("a wrong number answered %d, want the uniform 422", res.Code)
		}
	}
	if refusedAt == 0 {
		t.Fatal("20 lookups of one address from 20 clients were never refused")
	}
	if refusedAt < 3 {
		t.Errorf("the address was refused at ask %d; a customer retyping a number needs a few", refusedAt)
	}

	right := find(number, victim)
	if right.Code != http.StatusTooManyRequests {
		t.Errorf("once the address ran out, the right number answered %d; the answer "+
			"must not depend on the number", right.Code)
	}
	if right.Header().Get("Retry-After") == "" {
		t.Error("the refusal carries no Retry-After")
	}
	for _, c := range right.Result().Cookies() {
		if c.Name == "goen_placed" {
			t.Error("a refused lookup granted access to the order")
		}
	}

	if res := find("GO-000101-000999", "someone-else-"+uuid.NewString()+"@example.com"); res.Code != http.StatusUnprocessableEntity {
		t.Errorf("another address answered %d, want the uniform 422: the bound is per address", res.Code)
	}
}

// TestBuyerHandler_FindOrderFullWidth: buyer.Handler.FindOrder finds an order
// by a number typed on a Chinese keyboard left in full-width mode, and charges
// a wrong number to the address like any other: alternating the two widths buys
// no second allowance.
func TestBuyerHandler_FindOrderFullWidth(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	h := buyer.NewHandler(buyer.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler),
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}),
		false)
	find := func(orderNumber, addr string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/find",
			strings.NewReader(url.Values{"number": {orderNumber}, "email": {addr}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "192.0.2.1:4000"
		res := httptest.NewRecorder()
		h.FindOrder(res, req)
		return res
	}
	// fullWidth spells ASCII as a full-width input method types it.
	fullWidth := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r > ' ' && r <= '~' {
				return r + 0xFEE0
			}
			return r
		}, s)
	}

	victim := "find-wide-" + uuid.NewString() + "@example.com"
	number := placeUnpaidOrderFor(t, s, victim)
	if res := find("\u3000"+fullWidth(strings.ToLower(number))+"\u3000", victim); res.Code != http.StatusSeeOther ||
		res.Header().Get("Location") != "/orders/"+number {
		t.Fatalf("the right number typed in full width answered %d to %q, want 303 to /orders/%s",
			res.Code, res.Header().Get("Location"), number)
	}

	// asksUntilRefused asks the same wrong number about one address, in the
	// widths wrong gives, and counts the asks the address took.
	asksUntilRefused := func(addr string, wrong func(i int) string) int {
		t.Helper()
		for i := range 20 {
			switch res := find(wrong(i), addr); res.Code {
			case http.StatusTooManyRequests:
				return i + 1
			case http.StatusUnprocessableEntity:
			default:
				t.Fatalf("a wrong number answered %d, want the uniform 422", res.Code)
			}
		}
		t.Fatalf("20 lookups of %s were never refused", addr)
		return 0
	}
	const wrong = "GO-000101-000001"
	control := asksUntilRefused("find-wide-control-"+uuid.NewString()+"@example.com",
		func(int) string { return wrong })
	if control < 3 {
		t.Fatalf("the address was refused at ask %d; a customer retyping a number needs a few", control)
	}
	alternating := "find-wide-alt-" + uuid.NewString() + "@example.com"
	altNumber := placeUnpaidOrderFor(t, s, alternating)
	got := asksUntilRefused(alternating, func(i int) string {
		if i%2 == 1 {
			return fullWidth(wrong)
		}
		return wrong
	})
	if got != control {
		t.Errorf("alternating widths refused the address at ask %d, one width at ask %d: "+
			"a full-width spelling is charged differently", got, control)
	}
	for _, typed := range []string{altNumber, fullWidth(altNumber)} {
		if res := find(typed, alternating); res.Code != http.StatusTooManyRequests {
			t.Errorf("once the address ran out, the right number %q answered %d, want 429", typed, res.Code)
		}
	}
}

// TestBuyerHandler_FindOrderRandomNumber: buyer.Handler.FindOrder does not find
// an order by the day's first counter number, GO-<day>-000001, while the real
// number finds it even typed in lower case with its 0 and 1 read as o and l. It
// holds the point of random order numbers at the door a stranger uses: knowing
// a customer's address and the day, the counter's first number of the day,
// GO-<day>-000001, names nothing, while the real number finds the order even
// typed in lower case with its 0 and 1 read as o and l.
func TestBuyerHandler_FindOrderRandomNumber(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	victim := "find-random-" + uuid.NewString() + "@example.com"
	placed := placeUnpaidOrderFor(t, s, victim)
	// A draw can hold no 0 or 1 to misread, so the order takes a number that
	// holds both: the draw's day and 01 with four hex characters.
	number := placed[:len("GO-YYMMDD-")] + "01" + strings.ToUpper(uuid.NewString()[:4])
	if _, err := pool.Exec(ctx, `UPDATE orders SET order_number = $1 WHERE order_number = $2`, number, placed); err != nil {
		t.Fatalf("give the order a number with a 0 and a 1: %v", err)
	}
	h := buyer.NewHandler(buyer.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler),
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}),
		false)
	find := func(orderNumber string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/find",
			strings.NewReader(url.Values{"number": {orderNumber}, "email": {victim}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "192.0.2.1:4000"
		res := httptest.NewRecorder()
		h.FindOrder(res, req)
		return res
	}

	guess := number[:len("GO-YYMMDD-")] + "000001"
	if res := find(guess); res.Code != http.StatusUnprocessableEntity {
		t.Errorf("the day's first counter number %s answered %d, want the uniform 422", guess, res.Code)
	}

	typed := strings.ToLower(number)
	typed = typed[:len("go-yymmdd-")] + strings.NewReplacer("0", "o", "1", "l").Replace(typed[len("go-yymmdd-"):])
	if !strings.Contains(typed, "o") || !strings.Contains(typed, "l") {
		t.Fatalf("typed %q misreads nothing", typed)
	}
	res := find(" " + typed + " ")
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/orders/"+number {
		t.Errorf("%q answered %d to %q, want 303 to /orders/%s", typed, res.Code, res.Header().Get("Location"), number)
	}
}
