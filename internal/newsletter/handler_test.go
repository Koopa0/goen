package newsletter

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
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

func TestHTMXOverLimitKeepsTheFooterForm(t *testing.T) {
	const addr = "me@example.com"
	tests := []struct {
		name  string
		spend func(*ratelimit.Limiter, *http.Request)
	}{
		{
			name: "per-IP",
			spend: func(l *ratelimit.Limiter, r *http.Request) {
				if _, ok := l.Allow(ratelimit.ClientIP(r)); !ok {
					t.Fatal("setup could not spend the per-IP token")
				}
			},
		},
		{
			name: "per-address",
			spend: func(l *ratelimit.Limiter, _ *http.Request) {
				if _, ok := l.Allow("newsletter:" + addr); !ok {
					t.Fatal("setup could not spend the per-address token")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit := ratelimit.New(ratelimit.Config{
				Every: time.Hour, Burst: 1, TTL: time.Hour, MaxKeys: 8,
			})
			h := &Handler{limit: limit, log: slog.New(slog.DiscardHandler)}
			req := newsletterSubmit(t, addr, true)
			tt.spend(limit, req)

			res := httptest.NewRecorder()
			h.Submit(res, req)

			if res.Code != http.StatusTooManyRequests {
				t.Fatalf("status = %d, want 429", res.Code)
			}
			if ct := res.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
				t.Errorf("Content-Type = %q, want HTML so htmx can swap the form", ct)
			}
			assertRetryAfter(t, res)
			body := res.Body.String()
			if strings.HasPrefix(strings.TrimSpace(body), "429 ") {
				t.Fatalf("body is the plain refusal, which htmx would swap over the form:\n%s", body)
			}
			if strings.Contains(body, "<html") {
				t.Error("htmx response contains a full document; want the footer form only")
			}
			if !strings.Contains(body, `id="newsletter-form"`) {
				t.Error("body is missing the footer form")
			}
			want := i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyTooManyRequests)
			if !strings.Contains(body, want) {
				t.Errorf("body does not name the retry:\n%s", body)
			}
		})
	}
}

func TestPlainOverLimitIsStillText(t *testing.T) {
	limit := ratelimit.New(ratelimit.Config{
		Every: time.Hour, Burst: 1, TTL: time.Hour, MaxKeys: 8,
	})
	h := &Handler{limit: limit, log: slog.New(slog.DiscardHandler)}
	req := newsletterSubmit(t, "me@example.com", false)
	if _, ok := limit.Allow(ratelimit.ClientIP(req)); !ok {
		t.Fatal("setup could not spend the per-IP token")
	}

	res := httptest.NewRecorder()
	h.Submit(res, req)

	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", res.Code)
	}
	if ct := res.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want the inexpensive plain refusal", ct)
	}
	assertRetryAfter(t, res)
	if !strings.Contains(res.Body.String(), "429 ") {
		t.Errorf("plain body = %q, want the text refusal", res.Body.String())
	}
}

func newsletterSubmit(t *testing.T, addr string, htmx bool) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(
		i18n.WithLocale(t.Context(), i18n.ZhHant),
		http.MethodPost, "/newsletter",
		strings.NewReader("email="+addr))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.20:1234"
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	return req
}

func assertRetryAfter(t *testing.T, res *httptest.ResponseRecorder) {
	t.Helper()
	seconds, err := strconv.Atoi(res.Header().Get("Retry-After"))
	if err != nil || seconds < 1 {
		t.Errorf("Retry-After = %q, want a positive second count", res.Header().Get("Retry-After"))
	}
}
