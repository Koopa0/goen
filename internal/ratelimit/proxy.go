package ratelimit

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

// Proxies is the set of hops whose forwarding header goen believes.
//
// # Why this exists and why it is empty by default
//
// [ClientIP] reads the connection's own address, which behind a TLS-terminating
// proxy is the PROXY for every visitor on the site. Every customer then shares
// one bucket: one attacker exhausts the sign-in, newsletter and order-lookup
// allowance for everybody at once, and the limiter that was defending argon2
// becomes the thing denying service.
//
// The fix is not to read X-Forwarded-For. A header is written by whoever is
// speaking, so believing it unconditionally gives an attacker a fresh key per
// request — no limiter at all, wearing the appearance of one. The fix is to say
// WHICH hop may write it, which is a fact about the deployment and can only come
// from configuration.
//
// So the empty set is the default and the empty set reads no header: a
// deployment that has not been told what its proxies are keeps exactly the
// behaviour it had before this type existed, which is the honest failure mode
// rather than a silent downgrade to trusting strangers.
type Proxies struct {
	// nets is what may forward. Masked at parse time so Contains compares the
	// network and not what somebody typed.
	nets []netip.Prefix
}

// clientIPKey is the context key the resolved address travels on.
//
// An unexported struct type, so nothing outside this package can write the value
// [ClientIP] reads — the point of resolving at the edge is that there is one
// answer, and a key anybody could forge would be a second way to choose a bucket.
type clientIPKey struct{}

// ParseProxies reads a comma-separated list of CIDR blocks and bare addresses.
//
// A bare address means that host alone, so `10.0.0.7` and `10.0.0.7/32` are the
// same statement — an operator naming one load balancer should not have to
// remember the mask.
//
// A prefix covering everything is REFUSED rather than accepted. `0.0.0.0/0` is
// not a configuration, it is the sentence "believe every client's headers"
// written in a way that looks like one, and it turns the limiter into
// decoration for the exact reason the package doc gives. An operator who cannot
// name their proxies is better off with the per-IP limit degraded to a global
// one, which is what the empty set already gives them.
func ParseProxies(list string) (*Proxies, error) {
	p := &Proxies{nets: nil}
	for _, field := range strings.FieldsFunc(list, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	}) {
		prefix, err := parseTrusted(field)
		if err != nil {
			return nil, err
		}
		p.nets = append(p.nets, prefix)
	}
	return p, nil
}

// parseTrusted turns one field into the network it names.
func parseTrusted(field string) (netip.Prefix, error) {
	if prefix, err := netip.ParsePrefix(field); err == nil {
		if prefix.Bits() == 0 {
			return netip.Prefix{}, fmt.Errorf(
				"ratelimit: %q trusts every address, which is a header read on faith", field)
		}
		return prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(field)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf(
			"ratelimit: %q is not an address or a CIDR block", field)
	}
	addr = normalise(addr)
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

// Resolve stamps each request with the address the limiters will key on.
//
// Middleware, and outermost, because the answer has to be reached once where the
// trusted set is known. Every limiter in goen — the sign-in throttle, the
// newsletter signup, the order lookup, the second factor — asks [ClientIP], and
// a question each of them had to remember to ask differently is a question one
// of them gets wrong. It is the same reason the cart badge and the promotional
// strip are decided by middleware rather than by each handler.
//
// With nothing trusted this returns next unwrapped. Not an optimisation: it is
// the guarantee that a deployment which has configured nothing runs the code it
// ran before, with no context value in play at all.
func (p *Proxies) Resolve(next http.Handler) http.Handler {
	if p == nil || len(p.nets) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), clientIPKey{}, p.clientIP(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// clientIP is which hop to believe for r.
//
// The RIGHTMOST entry, when and only when the connection itself came from a
// trusted hop. A proxy APPENDS the address it is talking to, so the last entry
// is the one goen's own proxy wrote about the peer IT saw — and everything to
// the left of it is whatever that peer chose to send.
//
// # Why not "the rightmost entry that is not itself trusted"
//
// That was the first rule here, and a reviewer broke it. It is correct only if
// the trusted set contains nothing but proxies, and `.env.example` suggests
// `10.0.0.0/8,192.168.0.0/16` — under which every pod, VM, VPN user and internal
// service is "trusted". A client at 10.0.0.55 sending
// `X-Forwarded-For: 8.8.8.8` then produced:
//
//	hops = [8.8.8.8, 10.0.0.55]   ← the proxy appended the real peer
//	walk right, skip 10.0.0.55 (trusted), return 8.8.8.8
//
// so one machine picked its own rate-limit key, a different one per request.
// That defeats the per-IP limiter this package exists to provide — the one
// bounding argon2id at 64 MiB a hash — and turns /orders/find, which is bounded
// per-IP only, into an unbounded oracle for a customer's delivery address.
//
// Taking the rightmost entry outright cannot be gamed that way: the attacker
// can write as many entries as it likes, and every one of them lands to the LEFT
// of what the trusted proxy appended about it.
//
// # What this costs, stated rather than hidden
//
// A chain of TWO trusted proxies makes the rightmost entry the inner proxy's
// address, so everybody behind it shares one bucket. That is the same failure as
// configuring no proxies at all — degraded, never forged — and it is the safe
// direction to be wrong in. Closing it needs a hop COUNT rather than a CIDR set,
// because no set of addresses can distinguish a proxy from a client that happens
// to sit in the same range. If goen ever runs behind two hops, that is the
// change: a count, not a wider CIDR.
func (p *Proxies) clientIP(r *http.Request) string {
	host := remoteHost(r)
	peer, err := netip.ParseAddr(host)
	if err != nil || !p.trusts(peer) {
		// The connection did not come from a hop goen believes, so whatever it
		// wrote in a header is its own writing. This is the default path for
		// every request in a deployment with no proxy in front of it.
		return host
	}

	if hops := forwardedFor(r); len(hops) > 0 {
		return hops[len(hops)-1].String()
	}
	// A trusted proxy that forwarded no header. Its own address is all there is,
	// and it is the same answer this package gave before any of this existed.
	return host
}

// trusts reports whether addr is one of the hops goen believes.
func (p *Proxies) trusts(addr netip.Addr) bool {
	if p == nil || !addr.IsValid() {
		return false
	}
	addr = normalise(addr)
	for _, n := range p.nets {
		if n.Contains(addr) {
			return true
		}
	}
	return false
}

// forwardedFor is every hop named in X-Forwarded-For, left to right.
//
// Header.Values rather than Header.Get, because a proxy chain may append its own
// field LINE instead of extending the first one. RFC 9110 says repeated fields
// are equivalent to one comma-joined field in the order received, so reading
// only the first line would take the client's own writing and miss the hop that
// actually connected — the entry that matters most, since it is the one a
// trusted proxy wrote.
//
// Only X-Forwarded-For. RFC 7239's `Forwarded` is the standard and almost
// nothing sends it; supporting a second spelling would mean deciding which wins
// when they disagree, which is a question with no safe answer and no deployment
// asking it.
func forwardedFor(r *http.Request) []netip.Addr {
	var hops []netip.Addr
	for _, line := range r.Header.Values("X-Forwarded-For") {
		for field := range strings.SplitSeq(line, ",") {
			// An entry that is not an address — "unknown", an obfuscated
			// identifier, junk a client typed — is dropped rather than ending
			// the walk. A proxy appends to the RIGHT, so anything unparseable
			// sits left of the hop that was really observed and cannot displace
			// it.
			if addr, ok := parseHop(strings.TrimSpace(field)); ok {
				hops = append(hops, addr)
			}
		}
	}
	return hops
}

// parseHop reads one X-Forwarded-For entry.
//
// With or without a port: some proxies append `1.2.3.4:56789`, and dropping
// those would silently lose the hop that decides the answer.
func parseHop(field string) (netip.Addr, bool) {
	if field == "" {
		return netip.Addr{}, false
	}
	if addr, err := netip.ParseAddr(field); err == nil {
		return normalise(addr), true
	}
	if hostPort, err := netip.ParseAddrPort(field); err == nil {
		return normalise(hostPort.Addr()), true
	}
	return netip.Addr{}, false
}

// normalise makes two spellings of one address one key.
//
// An IPv4-mapped IPv6 address and its IPv4 form are the same machine, and a
// zone is a property of the local interface rather than of the peer. Left in,
// each would be a second bucket the same client could reach by changing nothing
// about itself.
func normalise(addr netip.Addr) netip.Addr {
	return addr.Unmap().WithZone("")
}
