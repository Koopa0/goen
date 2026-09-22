package cart

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCartContinuationRefusesForeignDestinations(t *testing.T) {
	for _, tt := range []struct{ target, want string }{
		{"/cart?qty=adjusted&next=%2Fcheckout%23address", "/checkout#address"},
		{"/cart?qty=adjusted&next=https%3A%2F%2Fevil.example", ""},
		{"/cart?qty=adjusted&next=%2F%2Fevil.example", ""},
		{"/cart?next=%2Fcheckout", ""},
	} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.target, http.NoBody)
		if got := cartContinuation(request); got != tt.want {
			t.Errorf("%s continuation=%q, want %q", tt.target, got, tt.want)
		}
	}
}
