//go:build integration

package cart_test

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

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/ratelimit"
)

// TestAnOrderLookupIsBoundedPerAddressFromAnyClient: order numbers are counted
// through the day, so the address is the only secret the lookup asks for, and
// a bound per client alone is lifted by asking from somewhere else. Each ask
// here comes from a different client, IPv4 and IPv6 alike; the address runs
// out anyway, the right number is then refused like a wrong one, and another
// address from the same clients is untouched.
func TestAnOrderLookupIsBoundedPerAddressFromAnyClient(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	victim := "find-bound-" + uuid.NewString() + "@example.com"
	number := placeUnpaidOrderFor(t, s, victim)
	// A per-client bound too loose to be what refuses anything below.
	h := cart.NewHandler(s, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler), false,
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}),
		nil, nil)

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
