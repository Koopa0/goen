package ratelimit

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

// Proxies is the set of hops whose forwarding header goen believes. The empty
// set is the default and reads no header at all, so a deployment that has not
// been told what its proxies are keeps [ClientIP] on the connection's address.
type Proxies struct {
	// nets is what may forward. Masked at parse time so Contains compares the
	// network and not what somebody typed.
	nets []netip.Prefix
}

// clientIPKey is the context key the resolved address travels on. Unexported,
// so nothing outside this package can write the value [ClientIP] reads.
type clientIPKey struct{}

// ParseProxies reads a comma-separated list of CIDR blocks and bare addresses,
// where a bare address means that host alone. A prefix covering everything is
// refused: it is "believe every client's headers" written to look like a config.
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
// Outermost middleware, so the answer is reached once where the trusted set is
// known; with nothing trusted it returns next unwrapped and stamps nothing.
func (p *Proxies) Resolve(next http.Handler) http.Handler {
	if p == nil || len(p.nets) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), clientIPKey{}, p.clientIP(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// clientIP is which hop to believe for r: the RIGHTMOST entry, and only from a
// trusted peer. Skipping entries that are themselves trusted lets a client
// inside the trusted CIDR pick its own key; two proxies deep, all share one.
func (p *Proxies) clientIP(r *http.Request) string {
	host := remoteHost(r)
	peer, err := netip.ParseAddr(host)
	if err != nil || !p.trusts(peer) {
		return host
	}

	if hops := forwardedFor(r); len(hops) > 0 {
		return hops[len(hops)-1].String()
	}
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
// Header.Values, not Get: RFC 9110 makes repeated field lines equivalent to one
// comma-joined field, so the first line alone misses the hop that connected.
func forwardedFor(r *http.Request) []netip.Addr {
	var hops []netip.Addr
	for _, line := range r.Header.Values("X-Forwarded-For") {
		for field := range strings.SplitSeq(line, ",") {
			// An unparseable entry is dropped rather than ending the walk: it
			// sits left of the hop really observed and cannot displace it.
			if addr, ok := parseHop(strings.TrimSpace(field)); ok {
				hops = append(hops, addr)
			}
		}
	}
	return hops
}

// parseHop reads one X-Forwarded-For entry, with or without a port — some
// proxies append `1.2.3.4:56789`.
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

// normalise makes two spellings of one address one key: an IPv4-mapped IPv6
// address and its IPv4 form are the same machine, and left in, each would be a
// second bucket the same client reaches by changing nothing about itself.
func normalise(addr netip.Addr) netip.Addr {
	return addr.Unmap().WithZone("")
}
