package wishlist

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestHandler_UpdateGuestReturn holds where a refused save lands: the form's
// validated same-site return path, never a fixed /account/wishlist.
func TestHandler_UpdateGuestReturn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ret  string
		want string
	}{
		{
			name: "back to the product",
			ret:  "/p/pixelight-9-pro",
			want: "/signin?next=/p/pixelight-9-pro",
		},
		{
			// An off-site return is what siteurl.PathOr exists to refuse, and the
			// wishlist is the honest fallback rather than somebody else's host.
			name: "an off-site return is refused",
			ret:  "https://evil.example/steal",
			want: "/signin?next=/account/wishlist",
		},
		{
			name: "a protocol-relative path is refused too",
			ret:  "//evil.example/steal",
			want: "/signin?next=/account/wishlist",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := &Handler{log: slog.New(slog.DiscardHandler)}
			body := url.Values{"slug": {"pixelight-9-pro"}, "return": {tt.ret}}
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/account/wishlist",
				strings.NewReader(body.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			h.Update(w, r)

			if w.Code != http.StatusSeeOther {
				t.Fatalf("Update(guest) status = %d, want %d", w.Code, http.StatusSeeOther)
			}
			if got := w.Header().Get("Location"); got != tt.want {
				t.Errorf("Update(return=%q) redirects to %q, want %q", tt.ret, got, tt.want)
			}
		})
	}
}
