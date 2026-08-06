package ratelimit

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestTheBurstIsSpentThenRefused proves the allowance is real and the numbers
// are the ones intended.
//
// The numbers are the contract: a person who mistypes a password a few times
// must never meet this, and a script must. A limiter that refused the third
// attempt would be a support ticket, and one that never refused would be
// decoration.
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
//
// A refused request must not consume the token a permitted one would have
// used. Without the cancel, a client held at the limit is pushed further behind
// by its own retries and never recovers — the limiter turns into a lockout,
// which is the thing this package explicitly is not.
func TestARefusedAttemptDoesNotSpendTheAllowance(t *testing.T) {
	l := New(Config{Every: 10 * time.Millisecond, Burst: 1, TTL: time.Hour})

	if _, ok := l.Allow("k"); !ok {
		t.Fatal("the first attempt was refused")
	}
	// Hammer it while refused. Each of these would eat a future token if the
	// reservation were not cancelled.
	for range 50 {
		if _, ok := l.Allow("k"); ok {
			t.Fatal("an attempt was allowed inside the refill interval")
		}
	}

	// One interval later, exactly one token is back — not zero, which is what
	// fifty uncancelled reservations would have left.
	time.Sleep(30 * time.Millisecond)
	if _, ok := l.Allow("k"); !ok {
		t.Error("the key never recovered; refused attempts are consuming the " +
			"allowance, which turns a throttle into a lockout")
	}
}

// TestKeysAreIndependent proves one client at its limit does not affect
// another.
//
// One customer at the limit must not affect another, and per-IP and
// per-account keys must not collide — which is why the caller prefixes them.
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
//
// The map is keyed by client. Without eviction it grows with every distinct IP,
// which is a slower version of the memory exhaustion this package exists to
// stop — a limiter that leaks is an attack surface wearing a defence.
func TestIdleKeysAreEvicted(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 1, TTL: 20 * time.Millisecond})

	for i := range 100 {
		l.Allow("old-" + strconv.Itoa(i))
	}
	if got := l.Size(); got != 100 {
		t.Fatalf("%d keys held, want 100", got)
	}

	time.Sleep(40 * time.Millisecond)
	// A new key triggers the sweep, which is when it is needed: the map can
	// only grow here.
	l.Allow("fresh")
	if got := l.Size(); got != 1 {
		t.Errorf("%d keys held after the TTL passed, want 1 — idle keys are not "+
			"being evicted and the map grows without bound", got)
	}
}

// TestTheLimiterIsSafeUnderConcurrency proves the shared state is guarded.
//
// It sits on the sign-in path, so every request touches it at once. A data race
// here is a crash on the endpoint that most needs to stay up.
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
	// The assertion is that -race saw nothing; Size is here so the test has a
	// value to look at rather than passing on absence alone.
	if l.Size() == 0 {
		t.Error("no keys were recorded")
	}
}

// TestGuardAnswers429WithARetryAfter proves a refused request never reaches
// the handler.
//
// 429 and not 403: the client is early, not forbidden, and a status that says
// so is what lets a well-behaved one back off. Retry-After rounds UP, because
// telling a client to return before it is allowed produces a second 429 and
// looks broken.
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
// wait.
//
// The header is whole seconds. A sub-second delay rounded DOWN is "0", which
// tells a client to retry immediately — it does, gets another 429, and the
// limiter looks broken to anything that obeys the header.
//
// The 30-second case above cannot see this: rounding it down still gives a
// usable number, which is why that case stayed green with the rounding deleted.
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
//
// X-Forwarded-For is set by the client. Keying on it hands an attacker an
// unlimited supply of keys, which is strictly worse than having no limiter —
// because it looks like there is one.
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
