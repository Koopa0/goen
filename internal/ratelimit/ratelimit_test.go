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

const testMaxKeys = 1000

func bucketCount(l *Limiter) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

func TestNewRequiresMaxKeys(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New accepted MaxKeys=0; an omitted count bound silently unbounds the map")
		}
	}()

	New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: 0})
}

func TestALongKeyIsNotStoredWhole(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: testMaxKeys})
	l.Allow("account:" + strings.Repeat("a", 64<<10))

	for key := range l.buckets {
		if len(key.value) > maxKeyBytes {
			t.Errorf("stored key is %d bytes, want at most %d", len(key.value), maxKeyBytes)
		}
	}
}

func TestTwoLongKeysStayDistinct(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: testMaxKeys})
	prefix := "account:" + strings.Repeat("a", 64<<10)
	l.Allow(prefix + "x")
	l.Allow(prefix + "y")

	if got := bucketCount(l); got != 2 {
		t.Errorf("%d buckets held, want 2; long keys sharing a prefix were merged", got)
	}
}

func TestALongKeyDoesNotCollideWithItsEncodedForm(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: testMaxKeys})
	longKey := "account:" + strings.Repeat("a", 64<<10)
	encoded := clampKey(longKey).value
	l.Allow(longKey)
	l.Allow(encoded)

	if got := bucketCount(l); got != 2 {
		t.Errorf("%d buckets held, want 2; a long key collided with its raw encoded form", got)
	}
}

func TestTheKeyCountIsCapped(t *testing.T) {
	const maxKeys = 100
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: maxKeys})
	for i := range 1000 {
		l.Allow("key-" + strconv.Itoa(i))
	}

	if got := bucketCount(l); got != maxKeys {
		t.Errorf("%d keys held after 1000 live inserts, want exactly the cap %d", got, maxKeys)
	}
}

func TestTheOldestKeyMakesRoomAtTheCap(t *testing.T) {
	// Use independent map seeds so a valid empty key is exercised in different
	// traversal positions; it must never be mistaken for "no oldest key yet".
	for attempt := range 100 {
		l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: 2})
		l.Allow("")
		l.Allow("newer")

		// Explicit live timestamps avoid making this a clock-resolution test.
		now := time.Now()
		l.mu.Lock()
		l.buckets[clampKey("")].seen = now.Add(-2 * time.Minute)
		l.buckets[clampKey("newer")].seen = now.Add(-time.Minute)
		l.mu.Unlock()

		l.Allow("fresh")
		l.mu.Lock()
		_, keptEmpty := l.buckets[clampKey("")]
		_, keptNewer := l.buckets[clampKey("newer")]
		_, keptFresh := l.buckets[clampKey("fresh")]
		gotSize := len(l.buckets)
		l.mu.Unlock()

		if keptEmpty || !keptNewer || !keptFresh || gotSize != 2 {
			t.Fatalf("attempt %d after capacity eviction: empty=%v newer=%v "+
				"fresh=%v size=%d; want the empty-string oldest key gone",
				attempt, keptEmpty, keptNewer, keptFresh, gotSize)
		}
	}
}

func TestAHitRefreshesTheOldestKey(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: 2})
	l.Allow("")
	l.Allow("untouched")

	now := time.Now()
	l.mu.Lock()
	l.buckets[clampKey("")].seen = now.Add(-2 * time.Minute)
	l.buckets[clampKey("untouched")].seen = now.Add(-time.Minute)
	l.mu.Unlock()

	l.Allow("") // A hit makes the logical empty key the newest.
	l.Allow("fresh")
	l.mu.Lock()
	_, keptEmpty := l.buckets[clampKey("")]
	_, keptUntouched := l.buckets[clampKey("untouched")]
	_, keptFresh := l.buckets[clampKey("fresh")]
	l.mu.Unlock()

	if !keptEmpty || keptUntouched || !keptFresh {
		t.Errorf("after refreshing empty: empty=%v untouched=%v fresh=%v; "+
			"want the untouched key evicted", keptEmpty, keptUntouched, keptFresh)
	}
}

// requireAgreement fails the test when the map and the eviction order disagree
// about how many keys there are: a key in one and not the other is either
// memory nothing bounds or a key nothing can evict.
func requireAgreement(t *testing.T, l *Limiter) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.order.Len() != len(l.buckets) {
		t.Fatalf("order holds %d keys and the map %d", l.order.Len(), len(l.buckets))
	}
}

// held reports whether key is tracked.
func held(t *testing.T, l *Limiter, key string) bool {
	t.Helper()
	requireAgreement(t, l)
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.buckets[clampKey(key)]
	return ok
}

func TestTheLeastRecentlySeenKeyIsEvictedFirst(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: 3})
	l.Allow("a")
	l.Allow("b")
	l.Allow("c")
	l.Allow("a") // a hit: b is now the least recently seen, and a the most.

	l.Allow("d")
	if held(t, l, "b") || !held(t, l, "a") || !held(t, l, "c") || !held(t, l, "d") {
		t.Fatalf("after d: a=%v b=%v c=%v d=%v; want b evicted and the rest kept",
			held(t, l, "a"), held(t, l, "b"), held(t, l, "c"), held(t, l, "d"))
	}

	l.Allow("e")
	if held(t, l, "c") || !held(t, l, "a") || !held(t, l, "d") || !held(t, l, "e") {
		t.Fatalf("after e: a=%v c=%v d=%v e=%v; want c evicted next and the rest kept",
			held(t, l, "a"), held(t, l, "c"), held(t, l, "d"), held(t, l, "e"))
	}
}

func TestARecentlySeenKeySurvivesAFloodOfNewKeys(t *testing.T) {
	const maxKeys = 8
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: maxKeys})
	l.Allow("busy")
	for i := range 100 {
		l.Allow("flood-" + strconv.Itoa(i))
		// Seen again before maxKeys newer keys have arrived, so it is never the
		// back of the order.
		if _, ok := l.Allow("busy"); ok {
			t.Fatalf("round %d: busy was allowed a second time; its bucket was "+
				"evicted and replaced, which resets its allowance", i)
		}
	}
	if !held(t, l, "busy") {
		t.Error("the key seen most recently was evicted before ones seen long ago")
	}
}

func TestTheTableNeverExceedsMaxKeys(t *testing.T) {
	const maxKeys = 8
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: maxKeys})
	for i := range 500 {
		l.Allow("key-" + strconv.Itoa(i))
		l.Allow("key-" + strconv.Itoa(i/2)) // a hit, or a miss on an evicted key
		requireAgreement(t, l)
		if got := bucketCount(l); got > maxKeys {
			t.Fatalf("%d keys held after %d inserts, want at most %d", got, i+1, maxKeys)
		}
	}
}

func TestASweepDropsTheExpiredAndKeepsTheLive(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: testMaxKeys})
	l.Allow("expired-first")
	l.Allow("expired-second")
	l.Allow("live")

	now := time.Now()
	l.mu.Lock()
	l.buckets[clampKey("expired-first")].seen = now.Add(-3 * time.Hour)
	l.buckets[clampKey("expired-second")].seen = now.Add(-2 * time.Hour)
	l.lastSweep = time.Time{} // the interval has passed
	l.mu.Unlock()

	l.Allow("new")
	if held(t, l, "expired-first") || held(t, l, "expired-second") {
		t.Error("an expired key survived the sweep")
	}
	if !held(t, l, "live") || !held(t, l, "new") {
		t.Error("the sweep dropped a key that had not expired")
	}
}

// The work a miss does at capacity must not depend on how many keys the table
// holds. Some keys are chosen by the client, so a full table is something a
// client can arrange, and every later miss then runs under the one mutex every
// request needs.
func TestAMissAtCapacityExaminesOneKeyWhateverTheTableSize(t *testing.T) {
	const misses = 1000
	for _, maxKeys := range []int{16, 4096} {
		t.Run(strconv.Itoa(maxKeys), func(t *testing.T) {
			l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: maxKeys})
			for i := range maxKeys {
				l.Allow("fill-" + strconv.Itoa(i))
			}

			l.mu.Lock()
			before := l.scanned
			l.mu.Unlock()
			for i := range misses {
				l.Allow("miss-" + strconv.Itoa(i))
			}
			l.mu.Lock()
			examined := l.scanned - before
			l.mu.Unlock()

			if examined > misses {
				t.Errorf("%d keys examined for %d misses at a capacity of %d, want at "+
					"most one a miss — eviction is walking the table", examined, misses, maxKeys)
			}
			if got := bucketCount(l); got != maxKeys {
				t.Errorf("%d keys held, want the capacity %d", got, maxKeys)
			}
		})
	}
}

func BenchmarkAllowAtCapacity(b *testing.B) {
	const maxKeys = 65_536 // what cmd/goen configures
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: maxKeys})
	for i := range maxKeys {
		l.Allow("fill-" + strconv.Itoa(i))
	}
	keys := make([]string, b.N)
	for i := range keys {
		keys[i] = "miss-" + strconv.Itoa(i)
	}
	b.ResetTimer()
	for i := range b.N {
		l.Allow(keys[i])
	}
}

func TestTheSweepIsAmortised(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: 1 << 20})
	for i := range 10_000 {
		l.Allow("key-" + strconv.Itoa(i))
	}

	l.mu.Lock()
	sweeps := l.sweeps
	l.mu.Unlock()
	if sweeps > 1 {
		t.Errorf("%d sweeps for 10000 inserts, want at most the initial sweep", sweeps)
	}
}

func TestTheBurstIsSpentThenRefused(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 3, TTL: time.Hour, MaxKeys: testMaxKeys})

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

func TestARefusedAttemptDoesNotSpendTheAllowance(t *testing.T) {
	l := New(Config{Every: 10 * time.Millisecond, Burst: 1, TTL: time.Hour, MaxKeys: testMaxKeys})

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

func TestKeysAreIndependent(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: testMaxKeys})

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

func TestIdleKeysAreEvicted(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 1, TTL: 20 * time.Millisecond, MaxKeys: testMaxKeys})

	for i := range 100 {
		l.Allow("old-" + strconv.Itoa(i))
	}
	if got := bucketCount(l); got != 100 {
		t.Fatalf("%d keys held, want 100", got)
	}

	time.Sleep(40 * time.Millisecond)
	// A new key triggers the sweep: the map can only grow here.
	l.Allow("fresh")
	if got := bucketCount(l); got != 1 {
		t.Errorf("%d keys held after the TTL passed, want 1 — idle keys are not "+
			"being evicted and the map grows without bound", got)
	}
}

func TestTheLimiterIsSafeUnderConcurrency(t *testing.T) {
	l := New(Config{Every: time.Millisecond, Burst: 5, TTL: time.Hour, MaxKeys: testMaxKeys})

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
	// The real assertion is that -race saw nothing; this stops the test
	// passing on absence alone.
	if bucketCount(l) == 0 {
		t.Error("no keys were recorded")
	}
}

func TestGuardAnswers429WithARetryAfter(t *testing.T) {
	l := New(Config{Every: 30 * time.Second, Burst: 1, TTL: time.Hour, MaxKeys: testMaxKeys})
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

func TestRetryAfterIsNeverZero(t *testing.T) {
	// 100ms refill: every refusal's true delay is well under one second.
	l := New(Config{Every: 100 * time.Millisecond, Burst: 1, TTL: time.Hour, MaxKeys: testMaxKeys})
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
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: testMaxKeys})
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

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

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
