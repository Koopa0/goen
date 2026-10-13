package ratelimit

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
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

// The empty string is a key like any other, and its stored form is the zero
// bucketKey, so nothing may read it as "no key to evict".
func TestTheOldestKeyMakesRoomAtTheCap(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: 2})
	l.Allow("")
	l.Allow("newer")

	l.Allow("fresh")
	if held(t, l, "") || !held(t, l, "newer") || !held(t, l, "fresh") || bucketCount(l) != 2 {
		t.Fatalf("after capacity eviction: empty=%v newer=%v fresh=%v size=%d; "+
			"want the empty-string oldest key gone",
			held(t, l, ""), held(t, l, "newer"), held(t, l, "fresh"), bucketCount(l))
	}
}

func TestAHitRefreshesTheOldestKey(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: 2})
	l.Allow("")
	l.Allow("untouched")

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

// A miss at capacity must cost the same whatever the table holds. Some keys are
// chosen by the client, so a full table is something a client can arrange, and
// every later miss then runs under the one mutex every request needs.
//
// The cost is measured, not counted: a count the implementation keeps of its
// own work is satisfied by a walk that keeps the count. The two tables differ
// 256-fold in size, so a miss that walks the table costs hundreds of times more
// in the large one, while a miss that makes room at the back costs within a
// small factor in both, the large table's cache misses being that factor. The
// bound sits far from both. The sizes are measured in turn, so load on the
// machine falls on both, and each keeps its fastest batch, the one the
// scheduler and the collector disturbed least; the race detector slows both
// alike.
func TestAMissAtCapacityCostsTheSameWhateverTheTableSize(t *testing.T) {
	const (
		small    = 256
		large    = small * 256 // what cmd/goen configures
		misses   = 256
		rounds   = 9
		maxRatio = 16
	)
	full := func(maxKeys int) *Limiter {
		l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: maxKeys})
		for i := range maxKeys {
			l.Allow("fill-" + strconv.Itoa(i))
		}
		return l
	}
	tables := []*Limiter{full(small), full(large)}
	fastest := []time.Duration{time.Hour, time.Hour}

	keys := make([]string, misses)
	for round := range rounds {
		for i, l := range tables {
			for j := range keys {
				keys[j] = "miss-" + strconv.Itoa(round) + "-" + strconv.Itoa(j)
			}
			runtime.GC()
			start := time.Now()
			for _, key := range keys {
				l.Allow(key)
			}
			fastest[i] = min(fastest[i], time.Since(start))
		}
	}

	for i, maxKeys := range []int{small, large} {
		if got := bucketCount(tables[i]); got != maxKeys {
			t.Errorf("%d keys held, want the capacity %d", got, maxKeys)
		}
	}
	perMiss := func(d time.Duration) time.Duration { return d / misses }
	ratio := float64(fastest[1]) / float64(fastest[0])
	t.Logf("a miss costs %v at a capacity of %d and %v at %d (%.1f times)",
		perMiss(fastest[0]), small, perMiss(fastest[1]), large, ratio)
	if ratio > maxRatio {
		t.Errorf("a miss at a capacity of %d costs %v and at %d costs %v, %.0f times as "+
			"much, want at most %d times — making room is walking the table",
			small, perMiss(fastest[0]), large, perMiss(fastest[1]), ratio, maxRatio)
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

// reserveThen reserves one of key's tokens and settles it at once, reporting
// whether the reservation was granted.
func reserveThen(l *Limiter, key string, keep bool) bool {
	r, _, ok := l.Reserve(key)
	if !ok {
		return false
	}
	if keep {
		r.Keep()
	} else {
		r.Refund()
	}
	return true
}

// TestARefundedReservationSpendsNothing: a reservation given back leaves the
// allowance where it was, however often, and a key that has only ever been
// given back is not tracked; only a kept one is charged.
func TestARefundedReservationSpendsNothing(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 2, TTL: time.Hour, MaxKeys: testMaxKeys})

	if !reserveThen(l, "unseen", false) {
		t.Error("a key never charged was refused")
	}
	if got := bucketCount(l); got != 0 {
		t.Errorf("a reservation given back left its key tracked: %d keys held, want 0", got)
	}
	for range 50 {
		if !reserveThen(l, "k", false) {
			t.Fatal("an uncharged key was refused")
		}
	}
	if !reserveThen(l, "k", true) {
		t.Fatal("the first charge was refused")
	}
	for range 50 {
		if !reserveThen(l, "k", false) {
			t.Fatal("a key with one charge left was refused")
		}
	}
	if !reserveThen(l, "k", true) {
		t.Fatal("the second charge was refused; a refund spent the allowance")
	}
	_, retryAfter, ok := l.Reserve("k")
	if ok {
		t.Fatal("a key with nothing left was granted a reservation")
	}
	if retryAfter <= 0 || retryAfter > time.Minute {
		t.Errorf("retry-after is %v, want something inside the refill interval", retryAfter)
	}
}

// TestAnUnsettledReservationCountsAgainstTheNext: a token set aside is gone
// until it is given back, so work still in flight cannot be overtaken by more
// of the same.
func TestAnUnsettledReservationCountsAgainstTheNext(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 3, TTL: time.Hour, MaxKeys: testMaxKeys})

	inFlight := make([]*Reservation, 0, 3)
	for i := range 3 {
		r, _, ok := l.Reserve("k")
		if !ok {
			t.Fatalf("reservation %d was refused inside the burst of 3", i+1)
		}
		inFlight = append(inFlight, r)
	}
	if _, retryAfter, ok := l.Reserve("k"); ok || retryAfter <= 0 {
		t.Fatalf("a fourth reservation with three in flight answered ok=%v retry-after=%v, want a refusal",
			ok, retryAfter)
	}
	inFlight[0].Refund()
	if !reserveThen(l, "k", true) {
		t.Fatal("the token given back could not be reserved again")
	}
	inFlight[1].Keep()
	inFlight[2].Keep()
	if _, _, ok := l.Reserve("k"); ok {
		t.Error("three kept reservations left a token to reserve; the burst is 3")
	}
	requireConsistent(t, l)
}

// TestOnlyTheFirstSettlementCounts: a deferred Refund after a Keep gives
// nothing back, and a Keep after a Refund charges nothing.
func TestOnlyTheFirstSettlementCounts(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: testMaxKeys})

	r, _, ok := l.Reserve("refunded")
	if !ok {
		t.Fatal("the first reservation was refused")
	}
	r.Refund()
	r.Keep()
	if !reserveThen(l, "refunded", false) {
		t.Error("a Keep after a Refund charged the key")
	}

	r, _, ok = l.Reserve("kept")
	if !ok {
		t.Fatal("the first reservation was refused")
	}
	r.Keep()
	r.Refund()
	if _, _, ok := l.Reserve("kept"); ok {
		t.Error("a Refund after a Keep gave the token back")
	}
}

// TestAKeptReservationIsChargedAfterItsKeyWasEvicted: the table may forget a
// key while its token is set aside; the miss is still charged, to the key as it
// is tracked when the miss is known.
func TestAKeptReservationIsChargedAfterItsKeyWasEvicted(t *testing.T) {
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: 1})

	r, _, ok := l.Reserve("k")
	if !ok {
		t.Fatal("the first reservation was refused")
	}
	l.Allow("another") // at capacity, the only other key goes
	if held(t, l, "k") {
		t.Fatal("the key was not evicted; the test proves nothing")
	}
	r.Keep()
	if _, _, ok := l.Reserve("k"); ok {
		t.Error("a miss kept after its key was evicted was never charged")
	}
	requireConsistent(t, l)
}

// TestReservationsMadeAtOnceShareOneBurst: the race detector is the first
// assertion; the second is that simultaneous reservations are granted no more
// often than the burst, which a check followed by a later charge would not be.
func TestReservationsMadeAtOnceShareOneBurst(t *testing.T) {
	const burst = 16
	l := New(Config{Every: time.Hour, Burst: burst, TTL: time.Hour, MaxKeys: testMaxKeys})

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		granted []*Reservation
	)
	start := make(chan struct{})
	for range 8 * burst {
		wg.Go(func() {
			<-start
			if r, _, ok := l.Reserve("shared"); ok {
				mu.Lock()
				granted = append(granted, r)
				mu.Unlock()
			}
		})
	}
	close(start)
	wg.Wait()
	if len(granted) != burst {
		t.Fatalf("%d reservations were granted at once, want the burst of %d", len(granted), burst)
	}
	for i, r := range granted {
		wg.Go(func() {
			if i%2 == 0 {
				r.Keep()
			} else {
				r.Refund()
			}
		})
	}
	wg.Wait()
	for range burst / 2 {
		if !reserveThen(l, "shared", true) {
			t.Fatal("a refunded token could not be reserved again")
		}
	}
	if _, _, ok := l.Reserve("shared"); ok {
		t.Error("more tokens were reserved than the burst holds")
	}
	requireConsistent(t, l)
}

// In a synctest bubble the fifty refusals share one instant, so a slow or
// loaded machine cannot refill the bucket between them and pass a refusal off
// as an allowance.
func TestARefusedAttemptDoesNotSpendTheAllowance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
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

// Keep setup at one fake instant so slow scheduling cannot expire keys before
// the idle wait.
func TestIdleKeysAreEvicted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
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

// At capacity every miss evicts, and after a quiet spell a miss sweeps, so here
// both run while other goroutines wait on the mutex. The race detector is the
// first assertion; the table's own invariants, checked after, are the second.
// synctest, so the quiet spells cost no wall-clock time.
func TestTheLimiterIsSafeUnderConcurrencyAtCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const (
			maxKeys = 8
			ttl     = 8 * time.Millisecond
		)
		l := New(Config{Every: time.Millisecond, Burst: 2, TTL: ttl, MaxKeys: maxKeys})

		var wg sync.WaitGroup
		for g := range 32 {
			wg.Go(func() {
				for round := range 20 {
					for k := range 4 {
						l.Allow("g" + strconv.Itoa(g) + "-" + strconv.Itoa(k))
					}
					l.Allow("shared")
					pause := ttl / 4
					if round%5 == 4 {
						pause = 2 * ttl // every key expires, and the next miss sweeps
					}
					time.Sleep(pause)
				}
			})
		}
		wg.Wait()

		requireConsistent(t, l)
		l.mu.Lock()
		defer l.mu.Unlock()
		if len(l.buckets) > maxKeys {
			t.Errorf("%d keys held, want at most %d", len(l.buckets), maxKeys)
		}
		if l.sweeps < 4 {
			t.Errorf("%d sweeps ran, want one after each quiet spell; the test never "+
				"swept under contention", l.sweeps)
		}
	})
}

// requireConsistent fails the test unless the map and the eviction order name
// the same buckets and the order runs from most to least recently seen: a
// bucket out of place is evicted before a key that is older, or never.
func requireConsistent(t *testing.T, l *Limiter) {
	t.Helper()
	requireAgreement(t, l)
	l.mu.Lock()
	defer l.mu.Unlock()
	var newer time.Time
	for el := l.order.Front(); el != nil; el = el.Next() {
		b, isBucket := el.Value.(*bucket)
		if !isBucket || l.buckets[b.key] != b || b.place != el {
			t.Fatalf("the order holds %v, which the map does not name at that place", el.Value)
		}
		if !newer.IsZero() && b.seen.After(newer) {
			t.Fatalf("%q, seen at %v, sits behind a key seen earlier, at %v",
				b.key.value, b.seen, newer)
		}
		newer = b.seen
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

func TestAClientKeyIsTheAddressForIPv4AndTheSlash64ForIPv6(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{name: "an IPv4 address is its own key", remoteAddr: "203.0.113.9:54321", want: "203.0.113.9"},
		{name: "an IPv4 address without a port", remoteAddr: "203.0.113.9", want: "203.0.113.9"},
		{name: "an IPv6 address is its /64", remoteAddr: "[2001:db8:1:2:dead:beef:0:9]:443", want: "2001:db8:1:2::/64"},
		{name: "the low 64 bits are not part of the key", remoteAddr: "[2001:db8:1:2::1]:443", want: "2001:db8:1:2::/64"},
		{name: "the next /64 is another key", remoteAddr: "[2001:db8:1:3::1]:443", want: "2001:db8:1:3::/64"},
		{name: "an IPv4-mapped address is its IPv4 form", remoteAddr: "[::ffff:203.0.113.9]:443", want: "203.0.113.9"},
		{name: "an IPv6 zone is not part of the key", remoteAddr: "[fe80::a:b:c:d%eth0]:443", want: "fe80::/64"},
		{name: "something that is no address is kept whole", remoteAddr: "pipe", want: "pipe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ClientKey(request(t, tt.remoteAddr)); got != tt.want {
				t.Errorf("ClientKey(%q) = %q, want %q", tt.remoteAddr, got, tt.want)
			}
		})
	}
}

// ClientIP is what sessions and logs record, so it must keep the whole address
// while the limiter keys on less.
func TestTheAddressToRecordKeepsAllOfAnIPv6Address(t *testing.T) {
	t.Parallel()

	r := request(t, "[2001:db8:1:2:dead:beef:0:9]:443")
	if got := ClientIP(r); got != "2001:db8:1:2:dead:beef:0:9" {
		t.Errorf("ClientIP = %q, want the whole address", got)
	}
}

// guarded sends one request from remoteAddr through a Guard whose burst is one
// and reports whether the limiter let it reach the handler.
func guarded(t *testing.T, h http.HandlerFunc, remoteAddr string) bool {
	t.Helper()

	w := httptest.NewRecorder()
	h(w, request(t, remoteAddr))
	return w.Code != http.StatusTooManyRequests
}

func TestAnIPv6ClientCannotBuyAFreshAllowanceByRotatingItsLowBits(t *testing.T) {
	t.Parallel()

	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: testMaxKeys})
	h := Guard(l, discardLogger(), func(http.ResponseWriter, *http.Request) {})

	if !guarded(t, h, "[2001:db8:1:2::1]:443") {
		t.Fatal("the first request from the /64 was refused")
	}
	for _, addr := range []string{
		"[2001:db8:1:2::2]:443",
		"[2001:db8:1:2:ffff:ffff:ffff:ffff]:443",
		"[2001:db8:1:2:1234:5678:9abc:def0]:60000",
	} {
		if guarded(t, h, addr) {
			t.Errorf("a request from %s was allowed after another address in its /64 "+
				"spent the burst; rotating the low 64 bits buys a fresh bucket", addr)
		}
	}
}

func TestDifferentSlash64sDoNotShareABucket(t *testing.T) {
	t.Parallel()

	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: testMaxKeys})
	h := Guard(l, discardLogger(), func(http.ResponseWriter, *http.Request) {})

	for _, addr := range []string{
		"[2001:db8:1:2::1]:443",
		"[2001:db8:1:3::1]:443",
		"[2001:db8:2:2::1]:443",
	} {
		if !guarded(t, h, addr) {
			t.Errorf("the first request from %s was refused; another client's /64 "+
				"was spent", addr)
		}
	}
}

func TestIPv4ClientsKeepTheirOwnBucketsAndTheMappedFormSharesOne(t *testing.T) {
	t.Parallel()

	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: testMaxKeys})
	h := Guard(l, discardLogger(), func(http.ResponseWriter, *http.Request) {})

	if !guarded(t, h, "203.0.113.9:1000") {
		t.Fatal("the first IPv4 request was refused")
	}
	if guarded(t, h, "203.0.113.9:2000") {
		t.Error("the same IPv4 address on another port bought a fresh allowance")
	}
	if guarded(t, h, "[::ffff:203.0.113.9]:3000") {
		t.Error("the IPv4-mapped spelling of a spent address bought a fresh allowance")
	}
	if !guarded(t, h, "203.0.113.10:1000") {
		t.Error("a neighbouring IPv4 address was refused; IPv4 clients must stay apart")
	}
	// Mapped addresses all sit in one /64, which they must not be keyed as.
	if !guarded(t, h, "[::ffff:203.0.113.11]:1000") {
		t.Error("a mapped address was refused; it was keyed as an IPv6 /64 and " +
			"shares a bucket with every other mapped client")
	}
	if !guarded(t, h, "[::ffff:203.0.113.12]:1000") {
		t.Error("a second mapped address was refused; mapped clients share one bucket")
	}
}

func TestAForwardedIPv6ClientIsKeyedOnItsSlash64(t *testing.T) {
	t.Parallel()

	p, err := ParseProxies("10.0.0.0/8")
	if err != nil {
		t.Fatalf("ParseProxies: %v", err)
	}
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: testMaxKeys})

	allowed := func(client string) bool {
		var ok bool
		p.Resolve(http.HandlerFunc(func(_ http.ResponseWriter, seen *http.Request) {
			_, ok = l.Allow(ClientKey(seen))
		})).ServeHTTP(httptest.NewRecorder(), request(t, "10.0.0.9:443", client))
		return ok
	}

	if !allowed("2001:db8:1:2::1") {
		t.Fatal("the first forwarded request was refused")
	}
	if allowed("2001:db8:1:2:9:9:9:9") {
		t.Error("a forwarded address in a spent /64 bought a fresh allowance")
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
