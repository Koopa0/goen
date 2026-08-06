package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// proxied runs r through Resolve and reports the address the limiters would key
// on — which is the only thing about this middleware that is observable, and
// the only thing that matters.
func proxied(t *testing.T, p *Proxies, r *http.Request) string {
	t.Helper()

	var got string
	p.Resolve(http.HandlerFunc(func(_ http.ResponseWriter, seen *http.Request) {
		got = ClientIP(seen)
	})).ServeHTTP(httptest.NewRecorder(), r)
	return got
}

// request is one arriving from remoteAddr carrying the given X-Forwarded-For
// lines, in the order a proxy chain would have written them.
func request(t *testing.T, remoteAddr string, forwarded ...string) *http.Request {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/signin", http.NoBody)
	r.RemoteAddr = remoteAddr
	for _, line := range forwarded {
		r.Header.Add("X-Forwarded-For", line)
	}
	return r
}

// TestNothingIsTrustedUntilSomethingIsConfigured proves the default did not
// move.
//
// This is the half that must not regress. A header read on faith is strictly
// worse than no limiter because it looks like there is one, so the state a
// deployment reaches by omission has to be the old behaviour exactly — no
// header consulted, no context value in play, and Resolve handing back the
// handler it was given.
func TestNothingIsTrustedUntilSomethingIsConfigured(t *testing.T) {
	t.Parallel()

	empty, err := ParseProxies("")
	if err != nil {
		t.Fatalf("ParseProxies(%q) = %v", "", err)
	}

	r := request(t, "203.0.113.9:54321", "198.51.100.1, 198.51.100.2")
	if got := proxied(t, empty, r); got != "203.0.113.9" {
		t.Errorf("ClientIP through an empty Proxies = %q, want the connection's "+
			"own address — an unconfigured deployment must not start believing "+
			"headers", got)
	}
	if got := proxied(t, nil, r); got != "203.0.113.9" {
		t.Errorf("ClientIP through a nil Proxies = %q, want %q", got, "203.0.113.9")
	}

	// Resolve wraps nothing when it has nothing to decide: the request that
	// reaches the handler is the request that arrived.
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if got := empty.Resolve(next); got == nil {
		t.Error("Resolve returned nil")
	}
}

// TestTheRightmostUntrustedHopIsTheClient proves which entry is believed.
//
// A proxy APPENDS the address it is speaking to, so the list grows left to
// right and everything left of the last trusted hop is the client's own
// writing. Walking from the right past the hops goen trusts is the only rule
// that survives a client sending a header of its own; taking the leftmost —
// the shape most frameworks ship — hands the attacker the key.
func TestTheRightmostUntrustedHopIsTheClient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		trusted    string
		remoteAddr string
		forwarded  []string
		want       string
	}{
		{
			name:       "one proxy in front, one real client",
			trusted:    "10.0.0.0/8",
			remoteAddr: "10.0.0.7:443",
			forwarded:  []string{"198.51.100.4"},
			want:       "198.51.100.4",
		},
		{
			name:       "the client wrote its own header first",
			trusted:    "10.0.0.0/8",
			remoteAddr: "10.0.0.7:443",
			forwarded:  []string{"1.2.3.4, 198.51.100.4"},
			want:       "198.51.100.4",
		},
		{
			// TWO trusted hops, and the answer is the inner PROXY rather than the
			// client. That is the documented cost of taking the rightmost entry:
			// goen steps back exactly one hop, so behind a chain everybody shares
			// the inner proxy's bucket.
			//
			// It is the safe direction to be wrong in — degraded, never forged —
			// and the alternative is what a reviewer broke: skipping entries that
			// are themselves trusted lets any client inside the trusted CIDR
			// choose its own key. Closing this properly needs a hop COUNT, because
			// no set of addresses can tell a proxy from a client in the same range.
			name:       "two proxies deep: the inner proxy, not the client",
			trusted:    "10.0.0.0/8",
			remoteAddr: "10.0.0.9:443",
			forwarded:  []string{"1.2.3.4, 198.51.100.4, 10.0.0.7"},
			want:       "10.0.0.7",
		},
		{
			name:       "a chain split across two header lines is still one chain",
			trusted:    "10.0.0.0/8",
			remoteAddr: "10.0.0.9:443",
			forwarded:  []string{"1.2.3.4", "198.51.100.4, 10.0.0.7"},
			want:       "10.0.0.7",
		},
		{
			name:       "a hop carrying a port",
			trusted:    "10.0.0.0/8",
			remoteAddr: "10.0.0.7:443",
			forwarded:  []string{"198.51.100.4:51000"},
			want:       "198.51.100.4",
		},
		{
			name:       "junk to the left of the real hop is dropped, not obeyed",
			trusted:    "10.0.0.0/8",
			remoteAddr: "10.0.0.7:443",
			forwarded:  []string{"unknown, _obfuscated, 198.51.100.4"},
			want:       "198.51.100.4",
		},
		{
			name:       "an IPv4-mapped hop is the IPv4 address",
			trusted:    "10.0.0.0/8",
			remoteAddr: "10.0.0.7:443",
			forwarded:  []string{"::ffff:198.51.100.4"},
			want:       "198.51.100.4",
		},
		{
			name:       "an IPv6 proxy and an IPv6 client",
			trusted:    "2001:db8::/32",
			remoteAddr: "[2001:db8::1]:443",
			forwarded:  []string{"2001:db8:beef::9"},
			want:       "2001:db8:beef::9",
		},
		{
			// A visitor inside the trusted network, behind one proxy. The proxy
			// appended what it saw, and that is the answer.
			name:       "a request from inside the trusted network",
			trusted:    "10.0.0.0/8",
			remoteAddr: "10.0.0.9:443",
			forwarded:  []string{"10.0.0.55, 10.0.0.7"},
			want:       "10.0.0.7",
		},
		{
			name:       "a trusted proxy that forwarded nothing",
			trusted:    "10.0.0.0/8",
			remoteAddr: "10.0.0.7:443",
			forwarded:  nil,
			want:       "10.0.0.7",
		},
		{
			// The whole point of the trusted set. This peer is not a proxy goen
			// named, so its header is worth nothing whatever it says.
			name:       "an untrusted peer's header is ignored",
			trusted:    "10.0.0.0/8",
			remoteAddr: "203.0.113.9:54321",
			forwarded:  []string{"198.51.100.4"},
			want:       "203.0.113.9",
		},
		{
			name:       "a single trusted host, named without a mask",
			trusted:    "10.0.0.7",
			remoteAddr: "10.0.0.7:443",
			forwarded:  []string{"198.51.100.4"},
			want:       "198.51.100.4",
		},
		{
			name:       "a neighbour of that host is not trusted",
			trusted:    "10.0.0.7",
			remoteAddr: "10.0.0.8:443",
			forwarded:  []string{"198.51.100.4"},
			want:       "10.0.0.8",
		},
		{
			// THE ATTACK, and the case the first version of this table did not
			// have. It covered a spoof from OUTSIDE the trusted set and a request
			// where every hop was trusted; it never covered the mixed one — a
			// trusted peer, a trusted client, and an untrusted entry the client
			// wrote itself — which is the only one that broke.
			//
			// `.env.example` suggests 10.0.0.0/8, so every pod and VPN user is
			// inside it. Skipping entries that are themselves trusted walked
			// straight past the real client onto the forgery.
			name:       "a client inside the trusted CIDR cannot pick its own key",
			trusted:    "10.0.0.0/8",
			remoteAddr: "10.0.0.9:443",
			forwarded:  []string{"8.8.8.8, 10.0.0.55"},
			want:       "10.0.0.55",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, err := ParseProxies(tt.trusted)
			if err != nil {
				t.Fatalf("ParseProxies(%q) = %v", tt.trusted, err)
			}
			got := proxied(t, p, request(t, tt.remoteAddr, tt.forwarded...))
			if got != tt.want {
				t.Errorf("ClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestASpoofedHeaderBuysNoFreshAllowance is the attack this configuration must
// not open.
//
// The old behaviour was safe because it read nothing. The failure mode of
// getting this wrong is not a wrong log line — it is one attacker holding an
// unlimited supply of keys, which is the state the package doc calls strictly
// worse than having no limiter at all.
func TestASpoofedHeaderBuysNoFreshAllowance(t *testing.T) {
	t.Parallel()

	p, err := ParseProxies("10.0.0.0/8")
	if err != nil {
		t.Fatalf("ParseProxies: %v", err)
	}
	l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour})

	// One client behind the proxy, changing the part of the header it controls
	// on every request.
	spend := func(spoof string) bool {
		r := request(t, "10.0.0.7:443", spoof+", 198.51.100.4")
		_, ok := l.Allow(proxied(t, p, r))
		return ok
	}
	if !spend("1.2.3.4") {
		t.Fatal("the first attempt was refused")
	}
	for _, spoof := range []string{"5.6.7.8", "9.9.9.9", "203.0.113.1", "10.0.0.55"} {
		if spend(spoof) {
			t.Errorf("prepending %q to X-Forwarded-For bought a fresh allowance; "+
				"the client half of the header is being believed", spoof)
		}
	}

	// And a genuinely different client behind the same proxy is still its own
	// key, or the fix would have traded one collapse for another.
	other := request(t, "10.0.0.7:443", "198.51.100.5")
	if _, ok := l.Allow(proxied(t, p, other)); !ok {
		t.Error("a second client behind the proxy was refused because the first " +
			"was at its limit — every visitor is still sharing one bucket")
	}
}

// TestParseProxiesRefusesWhatCannotBeAConfiguration proves the operator's list
// is read strictly.
//
// A typo that parsed as something else would silently trust the wrong network,
// and there is no way to see that from the outside. `0.0.0.0/0` gets its own
// case because it is not a typo — it is "believe everybody" written in a form
// that looks like configuration, and accepting it would put the limiter back in
// the state this whole change exists to avoid.
func TestParseProxiesRefusesWhatCannotBeAConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		list    string
		wantErr bool
	}{
		{name: "empty", list: ""},
		{name: "one block", list: "10.0.0.0/8"},
		{name: "several, comma separated", list: "10.0.0.0/8,192.168.0.0/16,172.16.0.0/12"},
		{name: "spaces around the commas", list: "10.0.0.0/8, 192.168.0.0/16"},
		{name: "a bare host", list: "10.0.0.7"},
		{name: "an IPv6 block", list: "2001:db8::/32"},
		{name: "everything, v4", list: "0.0.0.0/0", wantErr: true},
		{name: "everything, v6", list: "::/0", wantErr: true},
		{name: "a hostname", list: "proxy.internal", wantErr: true},
		{name: "a mask that is not a number", list: "10.0.0.0/eight", wantErr: true},
		{name: "prose", list: "yes please", wantErr: true},
		{name: "one good and one bad", list: "10.0.0.0/8, nonsense", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, err := ParseProxies(tt.list)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseProxies(%q) was accepted", tt.list)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseProxies(%q) = %v", tt.list, err)
			}
			if p == nil {
				t.Fatalf("ParseProxies(%q) returned nil with no error", tt.list)
			}
		})
	}
}
