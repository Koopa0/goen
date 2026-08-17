package ratelimit

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
)

// TestTheBurstIsSpentThenRefused proves the allowance is real and the numbers
// are the ones intended.
func TestTheBurstIsSpentThenRefused(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 3, TTL: time.Hour})

	for i := range 3 {
		if _, ok := l.Allow("k"); !ok {
			t.Fatalf("attempt %d was refused inside the burst of 3", i+1)
		}
	}
	retryAfter, ok := l.Allow("k")
	if ok {
		t.Fatal("the fourth attempt was allowed; the burst is not bounding anything")
	}
	if retryAfter <= 0 || retryAfter > time.Minute {
		t.Errorf("retry-after is %v, want something inside the refill interval", retryAfter)
	}
}

// TestARefusedAttemptDoesNotSpendTheAllowance proves a throttle never becomes
// a lockout.
func TestARefusedAttemptDoesNotSpendTheAllowance(t *testing.T) {
	l := New(Config{Every: 10 * time.Millisecond, Burst: 1, TTL: time.Hour})

	if _, ok := l.Allow("k"); !ok {
		t.Fatal("the first attempt was refused")
	}
	// Each of these eats a future token if the reservation is not cancelled.
	for range 50 {
		if _, ok := l.Allow("k"); ok {
			t.Fatal("an attempt was allowed inside the refill interval")
		}
	}

	// One token back, not the zero fifty uncancelled reservations would leave.
	time.Sleep(30 * time.Millisecond)
	if _, ok := l.Allow("k"); !ok {
		t.Error("the key never recovered; refused attempts are consuming the " +
			"allowance, which turns a throttle into a lockout")
	}
}

// TestKeysAreIndependent proves one client at its limit does not affect
// another, and that per-IP and per-account keys do not collide.
func TestKeysAreIndependent(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour})

	if _, ok := l.Allow("ip:203.0.113.1"); !ok {
		t.Fatal("the first key was refused")
	}
	if _, ok := l.Allow("ip:203.0.113.1"); ok {
		t.Fatal("the first key was not limited")
	}
	if _, ok := l.Allow("ip:203.0.113.2"); !ok {
		t.Error("a second IP was refused because a first one was at its limit")
	}
	if _, ok := l.Allow("account:a@example.com"); !ok {
		t.Error("an account key collided with an IP key")
	}
}

// TestIdleKeysAreEvicted proves the limiter does not leak the memory it
// exists to protect.
func TestIdleKeysAreEvicted(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 1, TTL: 20 * time.Millisecond})

	for i := range 100 {
		l.Allow("old-" + strconv.Itoa(i))
	}
	if got := l.Size(); got != 100 {
		t.Fatalf("%d keys held, want 100", got)
	}

	time.Sleep(40 * time.Millisecond)
	// A new key triggers the sweep: the map can only grow here.
	l.Allow("fresh")
	if got := l.Size(); got != 1 {
		t.Errorf("%d keys held after the TTL passed, want 1 — idle keys are not "+
			"being evicted and the map grows without bound", got)
	}
}

// TestTheLimiterIsSafeUnderConcurrency proves the shared state is guarded.
func TestTheLimiterIsSafeUnderConcurrency(t *testing.T) {
	l := New(Config{Every: time.Millisecond, Burst: 5, TTL: time.Hour})

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			for range 20 {
				l.Allow("shared")
				l.Allow("k" + strconv.Itoa(i))
			}
		})
	}
	wg.Wait()
	// The assertion is that -race saw nothing; Size stops this passing on
	// absence alone.
	if l.Size() == 0 {
		t.Error("no keys were recorded")
	}
}

// TestGuardAnswers429WithARetryAfter proves a refused request never reaches
// the handler.
func TestGuardAnswers429WithARetryAfter(t *testing.T) {
	l := New(Config{Every: 30 * time.Second, Burst: 1, TTL: time.Hour})
	reached := 0
	h := Guard(l, discardLogger(), func(http.ResponseWriter, *http.Request) { reached++ })

	call := func() *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/signin", http.NoBody)
		r.RemoteAddr = "203.0.113.9:54321"
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}

	if got := call().Code; got != http.StatusOK {
		t.Fatalf("the first request answered %d", got)
	}
	w := call()
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("the second request answered %d, want 429", w.Code)
	}
	if reached != 1 {
		t.Errorf("the handler ran %d times; a refused request must not reach it, "+
			"or argon2 has already been paid for", reached)
	}

	retry := w.Header().Get("Retry-After")
	seconds, err := strconv.Atoi(retry)
	if err != nil {
		t.Fatalf("Retry-After is %q, which is not whole seconds", retry)
	}
	if seconds < 1 || seconds > 30 {
		t.Errorf("Retry-After is %d, want something inside the refill interval", seconds)
	}
}

// TestRetryAfterIsNeverZero proves a sub-second delay still tells a client to
// wait. The 30-second case above stayed green with the rounding deleted.
func TestRetryAfterIsNeverZero(t *testing.T) {
	// 100ms refill: every refusal's true delay is well under one second.
	l := New(Config{Every: 100 * time.Millisecond, Burst: 1, TTL: time.Hour})
	h := Guard(l, discardLogger(), func(http.ResponseWriter, *http.Request) {})

	call := func() *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/signin", http.NoBody)
		r.RemoteAddr = "203.0.113.11:1234"
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}
	call() // spend the burst

	w := call()
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("answered %d, want 429", w.Code)
	}
	seconds, err := strconv.Atoi(w.Header().Get("Retry-After"))
	if err != nil {
		t.Fatalf("Retry-After is %q", w.Header().Get("Retry-After"))
	}
	if seconds < 1 {
		t.Errorf("Retry-After is %d for a sub-second delay; a client obeying it "+
			"retries immediately and is refused again", seconds)
	}
}

// TestTheKeyIsTheAddressAndNeverAHeader proves the key cannot be chosen by
// the client.
func TestTheKeyIsTheAddressAndNeverAHeader(t *testing.T) {
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/signin", http.NoBody)
	r.RemoteAddr = "203.0.113.9:54321"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	r.Header.Set("X-Real-IP", "198.51.100.2")
	r.Header.Set("Forwarded", "for=198.51.100.3")

	if got := ClientIP(r); got != "203.0.113.9" {
		t.Errorf("ClientIP = %q, want the connection's own address — a header "+
			"key is an unlimited supply of keys", got)
	}

	// Two requests claiming different forwarded addresses share one bucket.
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour})
	if _, ok := l.Allow(ClientIP(r)); !ok {
		t.Fatal("the first was refused")
	}
	r2 := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/signin", http.NoBody)
	r2.RemoteAddr = "203.0.113.9:60000" // same host, different source port
	r2.Header.Set("X-Forwarded-For", "198.51.100.99")
	if _, ok := l.Allow(ClientIP(r2)); ok {
		t.Error("changing X-Forwarded-For bought a fresh allowance")
	}
}

// discardLogger is a logger the tests do not read.
func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// TestARefusalIsInTheReadersLanguage holds an exemption whose reason was false.
//
// The 429's body carried an i18n-exempt saying the limiter "fires ahead of
// anything that could read a locale". withLocale is applied OUTSIDE the mux and
// every Guard is registered ON it, so the locale is on the context by the time
// Refuse runs — the comment described a middleware order that is not this one,
// and a throttled English visitor got a Chinese sentence on an endpoint whose
// whole job is to be met by somebody having trouble.
//
// Still plain text: rendering a page here is the work the limiter exists to
// avoid. That half of the reason was always sound.
func TestARefusalIsInTheReadersLanguage(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			Refuse(i18n.WithLocale(t.Context(), locale), w, 3*time.Second)

			if w.Code != http.StatusTooManyRequests {
				t.Errorf("Refuse() status = %d, want %d", w.Code, http.StatusTooManyRequests)
			}
			want := i18n.T(i18n.WithLocale(t.Context(), locale), i18n.KeyTooManyRequests)
			if got := w.Body.String(); !strings.Contains(got, want) {
				t.Errorf("Refuse(%s) body = %q, want it to contain %q", locale, got, want)
			}
		})
	}

	// The two locales must not produce the same body, or the test above passes
	// on a hard-coded string that happens to be in the catalogue.
	zh, en := httptest.NewRecorder(), httptest.NewRecorder()
	Refuse(i18n.WithLocale(t.Context(), i18n.ZhHant), zh, time.Second)
	Refuse(i18n.WithLocale(t.Context(), i18n.En), en, time.Second)
	if zh.Body.String() == en.Body.String() {
		t.Errorf("both locales get %q", zh.Body.String())
	}
}
