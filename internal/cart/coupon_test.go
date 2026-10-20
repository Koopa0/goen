package cart

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/order/access"
)

// A wrong coupon code is charged to the client whatever cart it brings. An IPv6
// client chooses the low 64 bits of its address freely, so a client keyed on
// the whole address would buy a fresh allowance with every rotation.
func TestAWrongCouponIsChargedToAWholeIPv6Slash64(t *testing.T) {
	t.Parallel()
	h := NewHandler(&Store{}, &access.Store{}, slog.New(slog.DiscardHandler), true, nil, nil)
	// Each ask brings a new cart, so only the client's own key can be spent.
	keys := func(remoteAddr string) [2]string {
		t.Helper()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout", http.NoBody)
		req.RemoteAddr = remoteAddr
		return couponKeys(req, uuid.New())
	}
	spent := func(remoteAddr string) bool {
		t.Helper()
		for _, key := range keys(remoteAddr) {
			reservation, _, ok := h.couponMisses.Reserve(key)
			if !ok {
				return true
			}
			reservation.Refund()
		}
		return false
	}

	for range 100 {
		for _, key := range keys("[2001:db8:1:2::1]:1000") {
			if reservation, _, ok := h.couponMisses.Reserve(key); ok {
				reservation.Keep()
			}
		}
	}
	if !spent("[2001:db8:1:2::1]:1000") {
		t.Fatal("a client that missed a hundred times still has codes to try")
	}
	if !spent("[2001:db8:1:2:aaaa:bbbb:cccc:dddd]:1001") {
		t.Error("another address in the same /64 has codes to try; rotating the low bits " +
			"bought a fresh allowance")
	}
	if spent("[2001:db8:1:3::1]:1002") {
		t.Error("an address in another /64 has no codes to try; it shared a bucket")
	}
}
