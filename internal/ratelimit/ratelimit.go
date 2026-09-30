// Package ratelimit bounds how often one client may do something expensive.
//
// argon2id runs at 64 MiB a hash, so the limiter must run BEFORE the hash or it
// defends nothing. It is a throttle and never a lockout — every key recovers on
// its own — and its state is per process, so N replicas allow N times the rate.
//
// Every request runs under one mutex, and some keys are chosen by the client,
// so a client that can fill the table must not also be able to make each of its
// misses walk it. A hit, and a miss that makes room at capacity, cost the same
// whatever the table holds. Dropping expired keys is amortised instead: one
// miss per interval drops every key that has expired, which can be the whole
// table, but each key is dropped once, and was paid for by the miss that
// inserted it.
package ratelimit

import (
	"container/list"
	"crypto/sha256"
	"encoding/base64"
	"net"
	"net/http"
	"net/netip"
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

	// mu guards the four fields below together: a bucket, its place in the
	// eviction order and the sweep clock are one invariant.
	mu      sync.Mutex
	buckets map[bucketKey]*bucket
	// order holds every bucket, the most recently seen at the front, so both
	// the expired keys and the eviction victim are at the back.
	order     *list.List
	lastSweep time.Time
	// sweeps counts the sweeps run, so a test can lock how rarely they run.
	sweeps int
}

// bucketKey keeps bounded digests in a namespace separate from raw keys. The
// distinction matters because a caller can submit the digest representation of
// a long key as a logical key in its own right.
type bucketKey struct {
	value  string
	hashed bool
}

type bucket struct {
	key     bucketKey
	limiter *rate.Limiter
	seen    time.Time
	place   *list.Element // this bucket in Limiter.order
}

// New returns a Limiter.
func New(cfg Config) *Limiter {
	if cfg.Every <= 0 || cfg.Burst < 1 || cfg.TTL <= 0 || cfg.MaxKeys < 1 {
		panic("ratelimit: New requires a positive Every, Burst, TTL and MaxKeys")
	}
	return &Limiter{cfg: cfg, buckets: make(map[bucketKey]*bucket), order: list.New()}
}

// Allow reports whether this key may proceed, and how long to wait if not. A
// sign-in checks two keys — the client's IP and the submitted email — because
// per-IP limiting cannot see a distributed attack on one account.
func (l *Limiter) Allow(key string) (retryAfter time.Duration, ok bool) {
	mapKey := clampKey(key)

	l.mu.Lock()
	defer l.mu.Unlock()
	// Read under the lock, so order and seen agree exactly: a clock read before
	// the wait would let a later arrival carry an earlier time.
	now := time.Now()

	b, found := l.buckets[mapKey]
	if found {
		l.order.MoveToFront(b.place)
	} else {
		b = l.admitLocked(mapKey, now)
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

// Spent reports whether key has nothing left to spend now, without spending
// and without tracking a key it has not seen. It is for a limit charged only
// when what it guards turns out to be a miss: the check has to come before the
// guarded work, or a refusal would arrive only on a miss and say which it was.
func (l *Limiter) Spent(key string) (retryAfter time.Duration, spent bool) {
	mapKey := clampKey(key)

	l.mu.Lock()
	defer l.mu.Unlock()
	b, found := l.buckets[mapKey]
	if !found {
		return 0, false
	}
	now := time.Now()
	reservation := b.limiter.ReserveN(now, 1)
	delay := reservation.DelayFrom(now)
	reservation.CancelAt(now)
	return delay, delay > 0
}

// admitLocked makes room for a key not yet tracked and tracks it. Called with mu
// held.
func (l *Limiter) admitLocked(mapKey bucketKey, now time.Time) *bucket {
	// Expired keys are dropped on an interval and not on every miss: during a
	// sustained arrival no key is old enough to free, and a walk per new key is
	// O(N^2) across N keys.
	if now.Sub(l.lastSweep) >= l.cfg.TTL/8 {
		l.sweepLocked(now)
		l.lastSweep = now
	}
	// At capacity the back of the order is the least recently seen key, and it
	// is an expired one if any key is. One removal is sufficient: every previous
	// insertion left len(buckets) <= MaxKeys.
	if len(l.buckets) >= l.cfg.MaxKeys {
		l.dropLocked(l.order.Back())
	}
	// A short key may be a small substring of a large allocation. Clone it only
	// on insertion so the map does not pin the caller's whole backing
	// allocation, without allocating on a hit.
	if !mapKey.hashed {
		mapKey.value = strings.Clone(mapKey.value)
	}
	b := &bucket{key: mapKey, limiter: rate.NewLimiter(rate.Every(l.cfg.Every), l.cfg.Burst)}
	b.place = l.order.PushFront(b)
	l.buckets[mapKey] = b
	return b
}

// clampKey bounds one stored representation without merging long keys that
// share a prefix. Hashed keys occupy a separate map-key namespace, so a raw key
// equal to this representation remains independent.
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

// sweepLocked drops every expired key. Called with mu held. The order is by last
// seen, so the expired keys are one run at the back and the walk ends at the
// first live key: each key is examined once on its way out, plus that one.
func (l *Limiter) sweepLocked(now time.Time) {
	l.sweeps++
	for el := l.order.Back(); el != nil; el = l.order.Back() {
		if b, isBucket := el.Value.(*bucket); isBucket && now.Sub(b.seen) <= l.cfg.TTL {
			return
		}
		l.dropLocked(el)
	}
}

// dropLocked forgets one bucket. Called with mu held.
func (l *Limiter) dropLocked(el *list.Element) {
	l.order.Remove(el)
	if b, isBucket := el.Value.(*bucket); isBucket {
		delete(l.buckets, b.key)
	}
}

// ClientIP is the address an HTTP request came from: r.RemoteAddr, unless
// [Proxies.Resolve] has run and decided otherwise. A header read on faith hands
// every attacker an unlimited supply of keys, so no header is read here. It is
// the address to record; a limit keys on [ClientKey].
func ClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey{}).(string); ok && ip != "" {
		return ip
	}
	return remoteHost(r)
}

// clientPrefixBits is how much of an IPv6 address identifies a client. A /64 is
// the smallest network a host autoconfigures in, so it is the least any
// subscriber is routed, and the low 64 bits are the subscriber's to choose.
const clientPrefixBits = 64

// ClientKey is what to key a per-client limit on: [ClientIP], with an IPv6
// address reduced to its /64. Keyed on the whole address, a client that rotates
// the low 64 bits has 2^64 keys and a fresh allowance on every request. An IPv4
// address, and an IPv6 address that only spells one, keep the dotted form.
func ClientKey(r *http.Request) string {
	host := ClientIP(r)
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = normalise(addr)
	if addr.Is4() {
		return addr.String()
	}
	return netip.PrefixFrom(addr, clientPrefixBits).Masked().String()
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// No port, which happens in tests and with some transports.
		return r.RemoteAddr
	}
	return host
}
