package cart

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
)

// The order lookup answers whether a number and an address belong together,
// so its limit is all that stands between a client and guessing the address.
// An IPv6 client chooses the low 64 bits of its address freely, so a limit
// keyed on the whole address is no limit.
func TestTheOrderLookupLimitCoversAWholeIPv6Slash64(t *testing.T) {
	t.Parallel()
	h := NewHandler(&Store{}, slog.New(slog.DiscardHandler), true, ratelimit.New(ratelimit.Config{
		Every: time.Hour, Burst: 1, TTL: time.Hour, MaxKeys: 8,
	}), nil, nil)

	// A lookup with no address is answered 422 without reading the store,
	// which this handler does not have; the limit is spent before that.
	lookup := func(remoteAddr string) int {
		t.Helper()
		req := httptest.NewRequestWithContext(
			i18n.WithLocale(t.Context(), i18n.ZhHant),
			http.MethodPost, "/orders/find", strings.NewReader("number=GO-260721-000001&email="))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = remoteAddr
		res := httptest.NewRecorder()
		h.FindOrder(res, req)
		return res.Code
	}

	if got := lookup("[2001:db8:1:2::1]:1000"); got != http.StatusUnprocessableEntity {
		t.Fatalf("the first lookup answered %d, want 422", got)
	}
	if got := lookup("[2001:db8:1:2:aaaa:bbbb:cccc:dddd]:1001"); got != http.StatusTooManyRequests {
		t.Errorf("another address in the same /64 answered %d, want 429; "+
			"rotating the low bits bought a fresh allowance", got)
	}
	if got := lookup("[2001:db8:1:3::1]:1002"); got != http.StatusUnprocessableEntity {
		t.Errorf("an address in another /64 answered %d, want 422; it shared a bucket", got)
	}
}
