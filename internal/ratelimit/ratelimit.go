// Package ratelimit bounds how often one client may do something expensive.
//
// # What this is defending
//
// goen hashes passwords with argon2id at 64 MiB per call — deliberately, so a
// stolen database resists GPU cracking. That parameter is also the attack: a
// hundred concurrent POSTs to /signin ask the process for 6.4 GB, and nothing
// else in goen needs to go wrong for that to take it down. The limiter has to
// run BEFORE the hash or it defends nothing.
//
// Slowing credential stuffing is the second reason and the more familiar one.
// It is served by the same mechanism keyed differently — see [Limiter.Allow].
//
// # What this is NOT
//
// It is not a lockout. An account is never disabled by failed attempts, because
// that hands an attacker a way to lock a real customer out of their own account
// by guessing at it. Every key recovers on its own, and the worst an attacker
// can do to somebody else is make their login briefly slower.
//
// It is not shared between processes. State is in memory, so N replicas allow
// N times the rate. That is a weakening rather than a hole — the per-process
// bound still stops one client exhausting one process — and moving it to
// PostgreSQL would put a write on the login path, which is its own amplifier.
package ratelimit

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Config is one limiter's shape.
type Config struct {
	// Every is how often one token is added back.
	Every time.Duration
	// Burst is how many may be spent at once. A burst of 1 with a 10s refill
	// means every request after the first waits 10 seconds, which locks out a
	// person who mistypes a password twice; a small burst is what makes the
	// limit invisible to a human and expensive to a script.
	Burst int
	// TTL is how long an idle key is kept. It bounds memory: without it the map
	// grows with every distinct IP, which is a slower version of the attack
	// this package exists to stop.
	TTL time.Duration
}

// Limiter allows a bounded rate per key.
//
// The zero value is not usable; [New] is the constructor.
type Limiter struct {
	cfg Config

	// mu guards both fields together — a compound invariant (a bucket and its
	// last-seen time must be inserted and evicted as one), which is a mutex and
	// not an atomic.
	mu      sync.Mutex
	buckets map[string]*bucket
}

// bucket is one key's allowance and when it was last used.
type bucket struct {
	limiter *rate.Limiter
	seen    time.Time
}

// New returns a Limiter.
func New(cfg Config) *Limiter {
	if cfg.Every <= 0 || cfg.Burst < 1 || cfg.TTL <= 0 {
		panic("ratelimit: New requires a positive Every, Burst and TTL")
	}
	return &Limiter{cfg: cfg, buckets: make(map[string]*bucket)}
}

// Allow reports whether this key may proceed, and how long to wait if not.
//
// The key decides what is being defended, and goen uses two:
//
//   - The client's IP stops one machine hammering one endpoint. It is what
//     bounds the argon2 memory a single connection can demand.
//   - The submitted EMAIL stops a distributed attack on one account, which
//     per-IP limiting cannot see at all. Unlike an IP it is not spoofable in
//     any useful way — varying it is exactly what the attacker is trying to
//     avoid, because they want one specific account.
//
// Both are checked for a sign-in, because either alone leaves the other attack
// wide open.
func (l *Limiter) Allow(key string) (retryAfter time.Duration, ok bool) {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, found := l.buckets[key]
	if !found {
		// Swept here rather than on a ticker: a goroutine owned by this package
		// would have to be stopped by somebody, and the only moment the map can
		// grow is this one. Sweeping when it grows is when it is needed.
		l.evictLocked(now)
		b = &bucket{limiter: rate.NewLimiter(rate.Every(l.cfg.Every), l.cfg.Burst)}
		l.buckets[key] = b
	}
	b.seen = now

	reservation := b.limiter.ReserveN(now, 1)
	if delay := reservation.DelayFrom(now); delay > 0 {
		// Cancelled, so the token goes back: a refused request must not also
		// consume the allowance a permitted one would have used, or a client
		// held at the limit is pushed further behind by its own retries and
		// never recovers.
		reservation.CancelAt(now)
		return delay, false
	}
	return 0, true
}

// evictLocked drops keys nobody has used inside the TTL.
//
// Called with mu held. O(n) over the map, which is the right trade at goen's
// scale: it runs only when a NEW key arrives, and a map that has grown large is
// exactly when the sweep is worth its cost.
func (l *Limiter) evictLocked(now time.Time) {
	for key, b := range l.buckets {
		if now.Sub(b.seen) > l.cfg.TTL {
			delete(l.buckets, key)
		}
	}
}

// Size is how many keys are held, for a test to assert eviction happens.
func (l *Limiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// ClientIP is the address to key an HTTP request on.
//
// r.RemoteAddr, unless [Proxies.Resolve] has run and decided otherwise. A
// header is set by the client, so keying on one with nothing behind it hands
// every attacker an unlimited supply of keys — strictly worse than no limiter,
// because it looks like there is one. That is why the decision is not made
// here: this function reads an answer that the edge already reached with the
// trusted set in hand, and falls back to the connection's own address when
// nobody reached one.
//
// With no trusted proxies configured — the default, and the only state a
// deployment reaches by omission — nothing is stamped, nothing is read, and
// this is byte for byte the address the socket came from.
func ClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey{}).(string); ok && ip != "" {
		return ip
	}
	return remoteHost(r)
}

// remoteHost is the address half of r.RemoteAddr.
func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// No port, which happens in tests and with some transports. The whole
		// value is the best key available.
		return r.RemoteAddr
	}
	return host
}
