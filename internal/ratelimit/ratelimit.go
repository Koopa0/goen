// Package ratelimit bounds how often one client may do something expensive.
//
// argon2id runs at 64 MiB a hash, so the limiter must run BEFORE the hash or it
// defends nothing. It is a throttle and never a lockout — every key recovers on
// its own — and its state is per process, so N replicas allow N times the rate.
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
	// Burst is how many may be spent at once.
	Burst int
	// TTL is how long an idle key is kept. It bounds memory: without it the map
	// grows with every distinct IP, which is a slower version of the attack
	// this package exists to stop.
	TTL time.Duration
}

// Limiter allows a bounded rate per key. The zero value is not usable; [New] is
// the constructor.
type Limiter struct {
	cfg Config

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

// Allow reports whether this key may proceed, and how long to wait if not. A
// sign-in checks two keys — the client's IP and the submitted email — because
// per-IP limiting cannot see a distributed attack on one account.
func (l *Limiter) Allow(key string) (retryAfter time.Duration, ok bool) {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, found := l.buckets[key]
	if !found {
		// Swept here rather than on a ticker: this is the only moment the map
		// can grow.
		l.evictLocked(now)
		b = &bucket{limiter: rate.NewLimiter(rate.Every(l.cfg.Every), l.cfg.Burst)}
		l.buckets[key] = b
	}
	b.seen = now

	reservation := b.limiter.ReserveN(now, 1)
	if delay := reservation.DelayFrom(now); delay > 0 {
		// The token goes back: without this a client held at the limit is pushed
		// further behind by its own retries and never recovers.
		reservation.CancelAt(now)
		return delay, false
	}
	return 0, true
}

// evictLocked drops keys nobody has used inside the TTL. Called with mu held.
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

// ClientIP is the address to key an HTTP request on: r.RemoteAddr, unless
// [Proxies.Resolve] has run and decided otherwise. A header read on faith hands
// every attacker an unlimited supply of keys, so no header is read here.
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
		// No port, which happens in tests and with some transports.
		return r.RemoteAddr
	}
	return host
}
