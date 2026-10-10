package contact

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/user"
)

func TestHTMXOverLimitKeepsTheContactPanel(t *testing.T) {
	h, req := throttledContact(t, true)
	res := httptest.NewRecorder()
	h.Submit(res, req)

	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", res.Code)
	}
	if ct := res.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want HTML so htmx can swap the panel", ct)
	}
	assertRetryAfter(t, res)
	body := res.Body.String()
	if strings.HasPrefix(strings.TrimSpace(body), "429 ") {
		t.Fatalf("body is the plain refusal, which htmx would swap over the form:\n%s", body)
	}
	if strings.Contains(body, "<html") {
		t.Error("htmx response contains a full document; want the panel only")
	}
	if !strings.Contains(body, `id="contact-form"`) {
		t.Error("body is missing the contact form")
	}
	if !strings.Contains(body, "me@example.com") {
		t.Error("body does not preserve the submitted email")
	}
	want := i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyTooManyRequests)
	if !strings.Contains(body, want) {
		t.Errorf("body does not name the retry:\n%s", body)
	}
}

func TestPlainOverLimitIsStillText(t *testing.T) {
	h, req := throttledContact(t, false)
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

func throttledContact(t *testing.T, htmx bool) (*Handler, *http.Request) {
	t.Helper()
	limit := ratelimit.New(ratelimit.Config{
		Every: time.Hour, Burst: 1, TTL: time.Hour, MaxKeys: 8,
	})
	h := &Handler{limit: limit, log: slog.New(slog.DiscardHandler)}
	form := url.Values{
		"name":    {"王小明"},
		"email":   {"me@example.com"},
		"subject": {"訂單問題"},
		"message": {"訂單已經三天沒有出貨了。"},
	}
	req := httptest.NewRequestWithContext(
		i18n.WithLocale(t.Context(), i18n.ZhHant),
		http.MethodPost, "/contact",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.20:1234"
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	if _, ok := limit.Allow(ratelimit.ClientKey(req)); !ok {
		t.Fatal("setup could not spend the per-IP token")
	}
	return h, req
}

func assertRetryAfter(t *testing.T, res *httptest.ResponseRecorder) {
	t.Helper()
	seconds, err := strconv.Atoi(res.Header().Get("Retry-After"))
	if err != nil || seconds < 1 {
		t.Errorf("Retry-After = %q, want a positive second count", res.Header().Get("Retry-After"))
	}
}

// An IPv6 sender chooses the low 64 bits of its address freely, so a limit
// keyed on the whole address is no limit.
func TestTheContactLimitCoversAWholeIPv6Slash64(t *testing.T) {
	limit := ratelimit.New(ratelimit.Config{
		Every: time.Hour, Burst: 1, TTL: time.Hour, MaxKeys: 8,
	})
	h := &Handler{limit: limit, log: slog.New(slog.DiscardHandler)}

	// An empty form that clears the limit is refused at 422 before it reaches
	// the store, which this handler does not have.
	submit := func(remoteAddr string) int {
		req := httptest.NewRequestWithContext(
			i18n.WithLocale(t.Context(), i18n.ZhHant),
			http.MethodPost, "/contact", http.NoBody)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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

func TestTheSentPageOffersOrdersOnlyToASignedInSender(t *testing.T) {
	for _, signedIn := range []bool{false, true} {
		h := &Handler{log: slog.New(slog.DiscardHandler)}
		req := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), i18n.ZhHant), http.MethodGet, "/contact?sent=1", http.NoBody)
		if signedIn {
			req = req.WithContext(user.NewContext(req.Context(), user.User{ID: "reader", Role: user.RoleCustomer}))
		}
		res := httptest.NewRecorder()
		h.Page(res, req)
		if got := strings.Contains(res.Body.String(), `href="/account#orders-heading"`); got != signedIn {
			t.Errorf("signed in %t: own-orders link = %t", signedIn, got)
		}
	}
}
