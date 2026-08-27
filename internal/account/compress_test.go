package account

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/web"
)

func TestSecretBearingAccountPagesAreNotCompressed(t *testing.T) {
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	const token = "live-secret-token-for-compression-test"
	tests := []struct {
		name    string
		method  string
		target  string
		body    url.Values
		handler http.HandlerFunc
	}{
		{name: "reset page", method: http.MethodGet, target: "/reset?token=" + token, handler: h.ResetPage},
		{
			name: "reset rejection", method: http.MethodPost, target: "/reset", handler: h.Reset,
			body: url.Values{"token": {token}, "password": {"one value"}, "confirm": {"a different value"}},
		},
		{name: "email verification page", method: http.MethodGet, target: "/verify?token=" + token, handler: h.VerifyPage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body *strings.Reader
			if tt.body == nil {
				body = strings.NewReader("")
			} else {
				body = strings.NewReader(tt.body.Encode())
			}
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.target, body)
			if tt.body != nil {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
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
				t.Error("response does not carry the live token the test is meant to protect")
			}
		})
	}
}
