package newsletter

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/web"
)

func TestSecretBearingNewsletterPagesAreNotCompressed(t *testing.T) {
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	const token = "newsletter-token-that-must-stay-uncompressed"
	tests := []struct {
		name    string
		target  string
		handler http.HandlerFunc
	}{
		{name: "confirmation", target: "/newsletter/confirm?token=" + token, handler: h.ConfirmPage},
		{name: "unsubscribe", target: "/newsletter/unsubscribe?token=" + token, handler: h.UnsubscribePage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.target, http.NoBody)
			req.Header.Set("Accept-Encoding", "gzip")
			res := httptest.NewRecorder()
			web.Compress(tt.handler).ServeHTTP(res, req)

			if got := res.Header().Get("Content-Encoding"); got != "" {
				t.Errorf("Content-Encoding = %q, want identity", got)
			}
			if got := res.Header().Get("X-Goen-No-Compress"); got != "" {
				t.Errorf("private no-compress marker leaked as %q", got)
			}
			if res.Body.Len() < 1024 {
				t.Fatalf("response is only %d bytes; it would not prove the opt-out", res.Body.Len())
			}
			if !strings.Contains(res.Body.String(), token) {
				t.Error("response does not carry the token the test is meant to protect")
			}
		})
	}
}
