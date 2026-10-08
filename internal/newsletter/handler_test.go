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
	"github.com/koopa0/goen/internal/ui/pages/pagestest"
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
			heading := "This link is no longer valid"
			reason := "This unsubscribe link is not valid. If the newsletter keeps arriving, contact us."
			if locale == i18n.ZhHant {
				heading = "\u9019\u500b\u9023\u7d50\u5df2\u5931\u6548"
				reason = "\u9019\u500b\u9000\u8a02\u9023\u7d50\u4e0d\u6b63\u78ba\u3002\u5982\u679c\u9084\u5728\u6536\u5230\u96fb\u5b50\u5831\uff0c\u8acb\u806f\u7d61\u6211\u5011\u3002"
			}
			pagestest.AssertEmailLink(t, body, heading, reason, "mailto:contact@koopa0.dev")
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
						if got := newsletterDisplayedMessageCount(t, body, tt.want); got != 1 {
							t.Errorf("refusal message occurrences = %d, want 1", got)
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

func TestNewsletterFailuresDoNotRefuseTheAddress(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, tt := range []struct {
			name   string
			status int
			addr   string
			htmx   bool
		}{
			{name: "storage plain", status: http.StatusInternalServerError, addr: "good@example.com"},
			{name: "storage htmx", status: http.StatusInternalServerError, addr: "good@example.com", htmx: true},
			{name: "throttle address", status: http.StatusTooManyRequests, addr: "good@example.com", htmx: true},
			{name: "throttle IP", status: http.StatusTooManyRequests, htmx: true},
		} {
			t.Run(string(locale)+"/"+tt.name, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				h := &Handler{log: slog.New(slog.DiscardHandler)}
				req := newsletterSubmit(t, tt.addr, tt.htmx).WithContext(ctx)
				res := httptest.NewRecorder()
				message := i18n.T(ctx, i18n.KeyNewsletterRetry)
				if tt.status == http.StatusTooManyRequests {
					message = i18n.T(ctx, i18n.KeyTooManyRequests)
					h.throttled(res, req, tt.addr, time.Minute)
					assertRetryAfter(t, res)
				} else {
					h.fail(res, req, tt.status, tt.addr, message)
				}
				if res.Code != tt.status {
					t.Fatalf("failure status = %d, want %d", res.Code, tt.status)
				}
				got := newsletterRefusal(t, res.Body.String())
				want := newsletterRefusalFacts{
					Forms: 1, Inputs: 1, Labels: 1, Messages: 1,
					Method: "post", Action: "/newsletter", Value: tt.addr,
					MessageRole: "alert", OwnMessage: true,
					MessageHidden: true, Notices: 1, Notice: message, NoticeRole: "alert", OwnNotice: true,
				}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("newsletter failure (-want +got):\n%s", diff)
				}
				if got := newsletterDisplayedMessageCount(t, res.Body.String(), message); got != 1 {
					t.Errorf("failure message occurrences = %d, want 1", got)
				}
			})
		}
	}
}

type newsletterRefusalFacts struct {
	Forms, Inputs, Labels, Messages int
	Method, Action, Value           string
	Invalid, Describes, Message     string
	MessageRole                     string
	OwnMessage                      bool
	MessageHidden                   bool
	Notices                         int
	Notice, NoticeRole              string
	OwnNotice                       bool
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
	var form, input, message, notice *html.Node
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
			for _, a := range n.Attr {
				if a.Key == "hidden" {
					got.MessageHidden = true
				}
			}
			var text strings.Builder
			for child := range n.Descendants() {
				if child.Type == html.TextNode {
					text.WriteString(child.Data)
				}
			}
			got.Message = text.String()
			message = n
		case attr(n, "id") == "newsletter-notice":
			got.Notices++
			got.NoticeRole = attr(n, "role")
			var text strings.Builder
			for child := range n.Descendants() {
				if child.Type == html.TextNode {
					text.WriteString(child.Data)
				}
			}
			got.Notice = text.String()
			notice = n
		}
	}
	got.OwnMessage = form != nil && input != nil && message != nil &&
		input.Parent.Parent == form && attr(input.Parent, "class") == "goen-footer__field" &&
		message.Parent == form && attr(input, "id") == "newsletter-email"
	got.OwnNotice = form != nil && notice != nil && notice.Parent == form
	return got
}

func newsletterDisplayedMessageCount(t *testing.T, body, message string) int {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var count int
	for n := range doc.Descendants() {
		if n.Type == html.TextNode {
			count += strings.Count(n.Data, message)
		}
	}
	return count
}

func TestConfirmationAcknowledgementsAreSafeToRefresh(t *testing.T) {
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	tests := []struct {
		name    string
		handler http.HandlerFunc
		path    string
		locale  i18n.Locale
		heading string
		body    string
	}{
		{name: "confirmation-En", handler: h.ConfirmPage, path: "/newsletter/confirm", locale: i18n.En, heading: "Subscribed", body: "You are subscribed to the goen newsletter. We send occasionally. The unsubscribe link is in the email we just sent."},
		{name: "confirmation-ZhHant", handler: h.ConfirmPage, path: "/newsletter/confirm", locale: i18n.ZhHant, heading: "\u5df2\u8a02\u95b1", body: "\u4f60\u5df2\u8a02\u95b1 goen \u96fb\u5b50\u5831\u3002\u4e0d\u5b9a\u671f\u5bc4\u9001\uff1b\u9000\u8a02\u9023\u7d50\u5728\u525b\u525b\u5bc4\u51fa\u7684\u90a3\u5c01\u4fe1\u88e1\u3002"},
		{name: "unsubscribe-En", handler: h.UnsubscribePage, path: "/newsletter/unsubscribe", locale: i18n.En, heading: "Unsubscribed", body: "You will not receive the goen newsletter again. Order notices are not affected."},
		{name: "unsubscribe-ZhHant", handler: h.UnsubscribePage, path: "/newsletter/unsubscribe", locale: i18n.ZhHant, heading: "\u5df2\u9000\u8a02", body: "\u4f60\u4e0d\u6703\u518d\u6536\u5230 goen \u96fb\u5b50\u5831\u3002\u8a02\u55ae\u76f8\u95dc\u7684\u901a\u77e5\u4fe1\u4e0d\u53d7\u5f71\u97ff\u3002"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			const token = "private-confirmation-token"
			const address = "private-mailbox@example.com"
			var first string
			for attempt := range 2 {
				req := httptest.NewRequestWithContext(ctx, http.MethodGet,
					tt.path+"?done=1&token="+token+"&email="+address, http.NoBody)
				res := httptest.NewRecorder()
				tt.handler(res, req)
				if res.Code != http.StatusOK {
					t.Fatalf("acknowledgement GET = %d, want 200", res.Code)
				}
				body := res.Body.String()
				for _, want := range []string{tt.heading, tt.body} {
					if !strings.Contains(body, want) {
						t.Errorf("acknowledgement omits %q", want)
					}
				}
				for _, forbidden := range []string{token, address, `name="token"`, `class="notice__form"`, "%s", "%!("} {
					if strings.Contains(body, forbidden) {
						t.Errorf("acknowledgement contains %q", forbidden)
					}
				}
				if got := res.Header().Values("Set-Cookie"); len(got) != 0 {
					t.Errorf("acknowledgement Set-Cookie = %q, want none", got)
				}
				if attempt == 0 {
					first = body
				} else if body != first {
					t.Error("refresh changed the acknowledgement")
				}
			}
			for _, marker := range []string{"", "0", "yes"} {
				req := httptest.NewRequestWithContext(ctx, http.MethodGet,
					tt.path+"?token="+token+"&done="+marker, http.NoBody)
				res := httptest.NewRecorder()
				tt.handler(res, req)
				if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `name="token" value="`+token+`"`) ||
					!strings.Contains(res.Body.String(), `method="post" action="`+tt.path+`"`) {
					t.Errorf("unconfirmed GET done=%q lost the confirmation form: status %d", marker, res.Code)
				}
				if strings.Contains(res.Body.String(), tt.body) {
					t.Errorf("unconfirmed GET done=%q claims completion", marker)
				}
			}
		})
	}
}

func TestMissingEmailedNewsletterLinksOfferRecovery(t *testing.T) {
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	for _, locale := range i18n.Locales() {
		for _, tt := range []struct {
			name        string
			path        string
			destination string
			zhHeading   string
			enHeading   string
			handler     http.HandlerFunc
		}{
			{name: "confirm", zhHeading: "\u78ba\u8a8d\u8a02\u95b1 goen \u96fb\u5b50\u5831", enHeading: "Confirm your goen newsletter subscription", path: "/newsletter/confirm", destination: "/newsletter", handler: h.ConfirmPage},
			{name: "unsubscribe", zhHeading: "\u9000\u8a02 goen \u96fb\u5b50\u5831", enHeading: "Unsubscribe from the goen newsletter", path: "/newsletter/unsubscribe", destination: "mailto:contact@koopa0.dev", handler: h.UnsubscribePage},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				res := httptest.NewRecorder()
				tt.handler(res, httptest.NewRequestWithContext(ctx, http.MethodGet, tt.path, http.NoBody))
				if res.Code != http.StatusOK {
					t.Fatalf("missing link = %d, want 200", res.Code)
				}
				heading := tt.enHeading
				reason := "This link is incomplete; open it again from the button in the email."
				if locale == i18n.ZhHant {
					heading = tt.zhHeading
					reason = "\u9019\u500b\u9023\u7d50\u4e0d\u5b8c\u6574\uff0c\u8acb\u5f9e\u4fe1\u88e1\u7684\u6309\u9215\u91cd\u65b0\u6253\u958b\u3002"
				}
				pagestest.AssertEmailLink(t, res.Body.String(), heading, reason, tt.destination)
			})
		}
	}
}

func TestNewsletterInfrastructureFailuresKeepTheirOwnState(t *testing.T) {
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			res := httptest.NewRecorder()
			h.linkFailed(res, httptest.NewRequestWithContext(ctx, http.MethodPost, "/newsletter/confirm", http.NoBody),
				i18n.T(ctx, i18n.KeyTryAgainTitle), i18n.T(ctx, i18n.KeyTryAgainBody))
			body := res.Body.String()
			if res.Code != http.StatusUnprocessableEntity || strings.Count(body, `class="goen-medallion"`) != 1 {
				t.Error("infrastructure failure lost its existing notice state")
			}
			if strings.Contains(body, "newsletter-recovery-email") || strings.Contains(body, i18n.T(ctx, i18n.KeyEmailLinkDeadTitle)) {
				t.Error("infrastructure failure claims that the emailed link is dead")
			}
		})
	}
}

func TestInvalidNewsletterConfirmationOffersSignup(t *testing.T) {
	h := &Handler{store: &Store{}, log: slog.New(slog.DiscardHandler)}
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			res := httptest.NewRecorder()
			h.Confirm(res, httptest.NewRequestWithContext(ctx, http.MethodPost, "/newsletter/confirm", http.NoBody))
			if res.Code != http.StatusUnprocessableEntity {
				t.Fatalf("invalid confirmation = %d, want 422", res.Code)
			}
			heading, reason := "This link is no longer valid", "It may have been used already, or be more than two days old."
			if locale == i18n.ZhHant {
				heading = "\u9019\u500b\u9023\u7d50\u5df2\u5931\u6548"
				reason = "\u9023\u7d50\u53ef\u80fd\u5df2\u7d93\u7528\u904e\u6216\u8d85\u904e\u5169\u5929\u3002"
			}
			pagestest.AssertEmailLink(t, res.Body.String(), heading, reason, "/newsletter")
		})
	}
}
