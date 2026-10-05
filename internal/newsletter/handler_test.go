package newsletter

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/email"
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
				if _, ok := l.Allow(ratelimit.ClientKey(r)); !ok {
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
	if _, ok := limit.Allow(ratelimit.ClientKey(req)); !ok {
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
		strings.NewReader(url.Values{"email": {addr}}.Encode()))
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

func TestInvalidUnsubscribeOffersTheOwnedMailbox(t *testing.T) {
	h := &Handler{store: &Store{}, log: slog.New(slog.DiscardHandler)}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			req := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), locale), http.MethodPost, "/newsletter/unsubscribe", http.NoBody)
			res := httptest.NewRecorder()
			h.Unsubscribe(res, req)
			body := res.Body.String()
			if strings.Count(body, "contact@koopa0.dev") != 3 {
				t.Error("invalid unsubscribe must name the owned mailbox in its recovery message and footer")
			}
			if strings.Contains(body, "support@goen.tw") || strings.Contains(body, "%s") {
				t.Error("invalid unsubscribe exposes an unowned or unformatted contact")
			}
		})
	}
}

// An IPv6 sender chooses the low 64 bits of its address freely, so a limit
// keyed on the whole address is no limit.
func TestTheNewsletterLimitCoversAWholeIPv6Slash64(t *testing.T) {
	limit := ratelimit.New(ratelimit.Config{
		Every: time.Hour, Burst: 1, TTL: time.Hour, MaxKeys: 8,
	})
	h := &Handler{limit: limit, log: slog.New(slog.DiscardHandler)}

	// An empty address that clears the limit is refused at 422 before it
	// reaches the store, which this handler does not have.
	submit := func(remoteAddr string) int {
		req := newsletterSubmit(t, "", false)
		req.RemoteAddr = remoteAddr
		res := httptest.NewRecorder()
		h.Submit(res, req)
		return res.Code
	}

	if got := submit("[2001:db8:1:2::1]:1000"); got != http.StatusUnprocessableEntity {
		t.Fatalf("the first submission answered %d, want 422", got)
	}
	if got := submit("[2001:db8:1:2:aaaa:bbbb:cccc:dddd]:1001"); got != http.StatusTooManyRequests {
		t.Errorf("another address in the same /64 answered %d, want 429; "+
			"rotating the low bits bought a fresh allowance", got)
	}
	if got := submit("[2001:db8:1:3::1]:1002"); got != http.StatusUnprocessableEntity {
		t.Errorf("an address in another /64 answered %d, want 422; it shared a bucket", got)
	}
}

func TestRefusedAddressesRenderTheirMessage(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			tests := []struct {
				name string
				addr string
				want string
			}{
				{name: "empty", want: i18n.T(ctx, i18n.KeyEmailRequired)},
				{name: "malformed", addr: "bad@x", want: i18n.T(ctx, i18n.KeyEmailMalformed)},
				{name: "consecutive dots", addr: "a..b@example.com", want: i18n.T(ctx, i18n.KeyEmailMalformed)},
				{name: "too long", addr: strings.Repeat("a", email.Max) + "@example.com",
					want: fmt.Sprintf(i18n.T(ctx, i18n.KeyEmailTooLong), email.Max)},
			}
			for _, tt := range tests {
				for _, htmx := range []bool{true, false} {
					t.Run(fmt.Sprintf("%s/htmx=%t", tt.name, htmx), func(t *testing.T) {
						limit := ratelimit.New(ratelimit.Config{
							Every: time.Hour, Burst: 8, TTL: time.Hour, MaxKeys: 8,
						})
						h := &Handler{limit: limit, log: slog.New(slog.DiscardHandler)}
						req := newsletterSubmit(t, tt.addr, htmx).WithContext(ctx)
						res := httptest.NewRecorder()
						h.Submit(res, req)

						if res.Code != http.StatusUnprocessableEntity {
							t.Fatalf("status = %d, want 422", res.Code)
						}
						body := res.Body.String()
						if strings.Contains(body, "%!") {
							t.Errorf("body carries a formatting error:\n%s", body)
						}
						if got := strings.Contains(body, "<html"); got == htmx {
							t.Errorf("full document = %t, want %t", got, !htmx)
						}
						got := newsletterRefusal(t, body)
						want := newsletterRefusalFacts{
							Forms: 1, Inputs: 1, Labels: 1, Messages: 1,
							Method: "post", Action: "/newsletter", Value: tt.addr,
							Invalid: "true", Describes: "newsletter-error", Message: tt.want,
							MessageRole: "alert", OwnMessage: true,
						}
						if diff := cmp.Diff(want, got); diff != "" {
							t.Errorf("newsletter refusal (-want +got):\n%s", diff)
						}
					})
				}
			}
		})
	}
}

type newsletterRefusalFacts struct {
	Forms, Inputs, Labels, Messages int
	Method, Action, Value           string
	Invalid, Describes, Message     string
	MessageRole                     string
	OwnMessage                      bool
}

func newsletterRefusal(t *testing.T, body string) newsletterRefusalFacts {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	attr := func(n *html.Node, key string) string {
		for _, a := range n.Attr {
			if a.Key == key {
				return a.Val
			}
		}
		return ""
	}
	var got newsletterRefusalFacts
	var form, input, message *html.Node
	for n := range doc.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		switch {
		case n.Data == "form" && attr(n, "id") == "newsletter-form":
			got.Forms++
			got.Method, got.Action = attr(n, "method"), attr(n, "action")
			form = n
		case n.Data == "input" && attr(n, "name") == "email":
			got.Inputs++
			got.Value = attr(n, "value")
			got.Invalid, got.Describes = attr(n, "aria-invalid"), attr(n, "aria-describedby")
			input = n
		case n.Data == "label" && attr(n, "for") == "newsletter-email":
			got.Labels++
		case attr(n, "id") == "newsletter-error":
			got.Messages++
			got.MessageRole = attr(n, "role")
			var text strings.Builder
			for child := range n.Descendants() {
				if child.Type == html.TextNode {
					text.WriteString(child.Data)
				}
			}
			got.Message = text.String()
			message = n
		}
	}
	got.OwnMessage = form != nil && input != nil && message != nil &&
		input.Parent == form && message.Parent == form && attr(input, "id") == "newsletter-email"
	return got
}
