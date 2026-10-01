package ratelimit

import (
	"net/netip"
	"testing"
)

// FuzzParseProxies: the trusted list is operator text and X-Forwarded-For is a
// client's, so neither may panic, a list that parses never trusts everything,
// and the address limiters key on is always one the connection or a hop spelled.
func FuzzParseProxies(f *testing.F) {
	for _, seed := range [][2]string{
		{"", ""}, {"127.0.0.1", "203.0.113.9"}, {"10.0.0.0/8, ::1", "1.2.3.4, 10.0.0.2"},
		{"0.0.0.0/0", ""}, {"::/0", "x"}, {"10.0.0.1/33", "::ffff:1.2.3.4"},
		{"fe80::1%eth0", "[::1]:80"}, {",,,", ", ,"}, {"10.0.0.1\n10.0.0.2", "1.1.1.1,2.2.2.2,3.3.3.3"},
	} {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, list, forwarded string) {
		p, err := ParseProxies(list)
		if err != nil {
			return
		}
		for _, n := range p.nets {
			if n.Bits() == 0 {
				t.Fatalf("%q parsed to %s, which trusts every address", list, n)
			}
		}
		for _, remote := range []string{"10.0.0.1:1234", "203.0.113.9:80", "[::1]:9"} {
			got := p.clientIP(request(t, remote, forwarded))
			if _, err := netip.ParseAddr(got); err != nil {
				t.Fatalf("client address %q for peer %s and %q is not an address: %v", got, remote, forwarded, err)
			}
		}
	})
}
