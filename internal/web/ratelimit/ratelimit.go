// Package ratelimit bounds how often one client may do something expensive.
//
// argon2id runs at 64 MiB a hash, so the limiter must run BEFORE the hash or it
// defends nothing. Every key recovers on its own, and its state is per process,
// so N replicas allow N times the rate and a restart refills every allowance.
//
// A key a client chooses, or its own address, is a throttle: it can only slow
// that client. A key shared by everyone who names one account or address is
// not: whoever spends it spends it for the person it protects, so it is a
// lockout for as long as a stranger keeps spending it. Charge such a key with
// [Limiter.Reserve] and keep the token only for a miss, so that the person
// with the right answer gives theirs back. That narrows the lockout and cannot
// remove it: while the shared key is empty, a request is refused before it is
// known to be right. A shared key that bounds what is sent to that person, a
// mail, need not refuse anyone: when it is empty, answer as if it were sent and
// send nothing. That locks nobody out only while what was sent already serves
// whoever asks, as the newest of a run of reset links does. Such a key, charged
// by a request that may send nothing, is reserved and refunded when nothing is
// sent, so it counts only what was sent.
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
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/koopa0/goen/internal/web/clientip"
)

// maxKeyBytes is the most of one key the limiter retains. Longer logical keys
// keep a readable prefix and a SHA-256 suffix, so their attacker-controlled
// bytes are not pinned in the map for the whole TTL.
const maxKeyBytes = 128

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

// Limiter is unusable as a zero value; build one with [New].
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
	// held counts the tokens [Limiter.Reserve] has set aside and nobody has
	// settled yet. They stay in limiter until kept, so a refund needs no way to
	// put a token back, and they count against every later reservation.
	held int
}

func New(cfg Config) *Limiter {
	if cfg.Every <= 0 || cfg.Burst < 1 || cfg.TTL <= 0 || cfg.MaxKeys < 1 {
		panic("ratelimit: New requires a positive Every, Burst, TTL and MaxKeys")
	}
	return &Limiter{cfg: cfg, buckets: make(map[bucketKey]*bucket), order: list.New()}
}

// Allow reports whether this key may proceed, and how long to wait if not. It
// charges every request that passes, so use it for a key that only one client
// can spend; a key shared with others is charged by [Limiter.Reserve].
func (l *Limiter) Allow(key string) (retryAfter time.Duration, ok bool) {
	mapKey := clampKey(key)

	l.mu.Lock()
	defer l.mu.Unlock()
	// Read under the lock, so order and seen agree exactly: a clock read before
	// the wait would let a later arrival carry an earlier time.
	now := time.Now()
	b := l.seeLocked(mapKey, now)

	reservation := b.limiter.ReserveN(now, 1)
	if delay := reservation.DelayFrom(now); delay > 0 {
		// The token goes back: without this a client held at the limit is pushed
		// further behind by its own retries and never recovers.
		reservation.CancelAt(now)
		return delay, false
	}
	return 0, true
}

// Reservation is one token [Limiter.Reserve] set aside. Settle it with Keep or
// Refund; only the first settlement counts, so a deferred Refund after a Keep
// gives nothing back.
type Reservation struct {
	l *Limiter
	b *bucket
	// settled is guarded by l.mu.
	settled bool
}

// Reserve sets one of key's tokens aside, or reports how long until one is
// free. It is for a limit charged only when what it guards turns out to be a
// miss. Both halves matter: the refusal has to come before the guarded work,
// or it would arrive only after a miss and say which it was; and the token has
// to be taken before the work, or every request that arrives while one token
// is left does the work. Allow on the same key does not see what is set aside,
// so a key charged by reservations is charged by nothing else.
func (l *Limiter) Reserve(key string) (r *Reservation, retryAfter time.Duration, ok bool) {
	mapKey := clampKey(key)

	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b := l.seeLocked(mapKey, now)
	if short := float64(b.held+1) - b.limiter.TokensAt(now); short > 0 {
		return nil, time.Duration(short * float64(l.cfg.Every)), false
	}
	b.held++
	return &Reservation{l: l, b: b}, 0, true
}

// Keep spends the reserved token: the work turned out to be a miss.
func (r *Reservation) Keep() {
	l := r.l
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.settled {
		return
	}
	r.settled = true
	now := time.Now()
	b := r.b
	if l.buckets[b.key] == b {
		b.held--
		b.seen = now
		l.order.MoveToFront(b.place)
	} else {
		// Evicted with its token still set aside: the miss is charged to the key
		// as it is tracked now.
		b = l.seeLocked(b.key, now)
	}
	// Never cancelled: the work this token paid for has been done.
	b.limiter.ReserveN(now, 1)
}

// Refund gives the reserved token back: the work turned out not to be a miss.
func (r *Reservation) Refund() {
	l := r.l
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.settled {
		return
	}
	r.settled = true
	b := r.b
	if l.buckets[b.key] != b {
		return
	}
	b.held--
	// A full bucket with nothing set aside answers exactly as an untracked key
	// does. Forgetting it keeps work that cost nothing from filling the table
	// and evicting keys that have missed.
	if b.held == 0 && b.limiter.TokensAt(time.Now()) >= float64(l.cfg.Burst) {
		l.dropLocked(b.place)
	}
}

// seeLocked returns key's bucket, tracking it if it is new, as seen now: at the
// front of the order. Called with mu held.
func (l *Limiter) seeLocked(mapKey bucketKey, now time.Time) *bucket {
	b, found := l.buckets[mapKey]
	if found {
		l.order.MoveToFront(b.place)
	} else {
		b = l.admitLocked(mapKey, now)
	}
	b.seen = now
	return b
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

// clientPrefixBits is how much of an IPv6 address identifies a client. A /64 is
// the smallest network a host autoconfigures in, so it is the least any
// subscriber is routed, and the low 64 bits are the subscriber's to choose.
const clientPrefixBits = 64

// ClientKey is what to key a per-client limit on: [clientip.From], with an IPv6
// address reduced to its /64. Keyed on the whole address, a client that rotates
// the low 64 bits has 2^64 keys and a fresh allowance on every request. An IPv4
// address, and an IPv6 address that only spells one, keep the dotted form.
func ClientKey(r *http.Request) string {
	host := clientip.From(r)
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = clientip.Normalise(addr)
	if addr.Is4() {
		return addr.String()
	}
	return netip.PrefixFrom(addr, clientPrefixBits).Masked().String()
}

// ipv4NetworkBits is how much of an IPv4 address identifies a client network. A
// /24 is the smallest block routed across the internet, so a client rotating
// the last octet buys nothing; a network of 256 addresses sharing one
// allowance is the price, paid only between clients of one provider block.
const ipv4NetworkBits = 24

// ClientNetwork is the network the client is on, for a limit that lets each
// network spend its own allowance of a resource all networks share: the /24 of
// an IPv4 address and the /64 of an IPv6 one. It reads the address
// [clientip.From] does, so it trusts exactly the headers
// [clientip.Proxies.Resolve] has been told to. [ClientKey] is finer for IPv4
// and the same for IPv6.
func ClientNetwork(r *http.Request) string {
	host := clientip.From(r)
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = clientip.Normalise(addr)
	if addr.Is4() {
		return netip.PrefixFrom(addr, ipv4NetworkBits).Masked().String()
	}
	return netip.PrefixFrom(addr, clientPrefixBits).Masked().String()
}
