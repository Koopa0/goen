// Package ratelimit bounds how often one client may do something expensive.
//
// argon2id runs at 64 MiB a hash, so the limiter must run BEFORE the hash or it
// defends nothing. It is a throttle and never a lockout — every key recovers on
// its own — and its state is per process, so N replicas allow N times the rate.
package ratelimit

import (
	"crypto/sha256"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// maxKeyBytes is the most of one key the limiter retains. Longer logical keys
// keep a readable prefix and a SHA-256 suffix, so their attacker-controlled
// bytes are not pinned in the map for the whole TTL.
const maxKeyBytes = 128

// Config is one limiter's shape.
type Config struct {
	// Every is how often one token is added back.
	Every time.Duration
	// Burst is how many may be spent at once.
	Burst int
	// TTL is how long an idle key is kept.
	TTL time.Duration
	// MaxKeys is the hard bound on tracked keys. At capacity the least recently
	// seen live key is forgotten to make room for one new key.
	MaxKeys int
}

// Limiter allows a bounded rate per key. The zero value is not usable; [New] is
// the constructor.
type Limiter struct {
	cfg Config

	mu        sync.Mutex
	buckets   map[bucketKey]*bucket
	lastSweep time.Time
	sweeps    int // counted so a test can lock the amortisation guarantee
}

// bucketKey keeps bounded digests in a namespace separate from raw keys. The
// distinction matters because a caller can submit the digest representation of
// a long key as a logical key in its own right.
type bucketKey struct {
	value  string
	hashed bool
}

// bucket is one key's allowance and when it was last used.
type bucket struct {
	limiter *rate.Limiter
	seen    time.Time
}

// New returns a Limiter.
func New(cfg Config) *Limiter {
	if cfg.Every <= 0 || cfg.Burst < 1 || cfg.TTL <= 0 || cfg.MaxKeys < 1 {
		panic("ratelimit: New requires a positive Every, Burst, TTL and MaxKeys")
	}
	return &Limiter{cfg: cfg, buckets: make(map[bucketKey]*bucket)}
}

// Allow reports whether this key may proceed, and how long to wait if not. A
// sign-in checks two keys — the client's IP and the submitted email — because
// per-IP limiting cannot see a distributed attack on one account.
func (l *Limiter) Allow(key string) (retryAfter time.Duration, ok bool) {
	mapKey := clampKey(key)
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, found := l.buckets[mapKey]
	if !found {
		// Work proportional to N once per new key is O(N^2), and during a
		// sustained arrival no key is old enough to free. Sweep idle keys on an
		// interval instead. At full capacity an exact oldest-key choice still
		// costs O(MaxKeys) per distinct miss, but that cost and the map are bound.
		if now.Sub(l.lastSweep) >= l.cfg.TTL/8 || len(l.buckets) >= l.cfg.MaxKeys {
			l.evictLocked(now)
			l.lastSweep = now
		}
		// A short key may be a small substring of a large allocation. Clone it
		// only on insertion so the map does not pin the caller's whole backing
		// allocation, without allocating on a hit.
		if !mapKey.hashed {
			mapKey.value = strings.Clone(mapKey.value)
		}
		b = &bucket{limiter: rate.NewLimiter(rate.Every(l.cfg.Every), l.cfg.Burst)}
		l.buckets[mapKey] = b
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

// clampKey bounds one stored representation without merging long keys that
// share a prefix. Hashed keys occupy a separate map-key namespace, so a raw key
// equal to this representation remains independent. The suffix length is
// derived from the encoding rather than assuming padded base64:
// RawURLEncoding encodes a SHA-256 digest to 43 bytes.
func clampKey(key string) bucketKey {
	if len(key) <= maxKeyBytes {
		return bucketKey{value: key}
	}
	sum := sha256.Sum256([]byte(key))
	suffix := base64.RawURLEncoding.EncodeToString(sum[:])
	return bucketKey{
		value:  key[:maxKeyBytes-len(suffix)] + suffix,
		hashed: true,
	}
}

// evictLocked drops expired keys and, if the incoming miss still needs room,
// the one least recently seen live key. Called with mu held. One deletion is
// sufficient: every previous insertion left len(buckets) <= MaxKeys.
func (l *Limiter) evictLocked(now time.Time) {
	l.sweeps++
	var oldestKey bucketKey
	var oldestSeen time.Time
	haveOldest := false
	for key, b := range l.buckets {
		if now.Sub(b.seen) > l.cfg.TTL {
			delete(l.buckets, key)
			continue
		}
		if !haveOldest || b.seen.Before(oldestSeen) {
			oldestKey = key
			oldestSeen = b.seen
			haveOldest = true
		}
	}
	if len(l.buckets) >= l.cfg.MaxKeys && haveOldest {
		delete(l.buckets, oldestKey)
	}
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
