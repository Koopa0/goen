package account

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/koopa0/goen/internal/outbound"
)

type localGoogleTransport struct {
	target *url.URL
	next   http.RoundTripper
}

func (l localGoogleTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = l.target.Scheme, l.target.Host
	return l.next.RoundTrip(r)
}

func TestGoogleRecordsProtocolStatusRatherThanErrorWording(t *testing.T) {
	for _, status := range []int{400, 500} {
		for _, profile := range []bool{false, true} {
			t.Run(strconv.Itoa(status)+"/profile="+strconv.FormatBool(profile), func(t *testing.T) {
				var events []outbound.Event
				outbound.Recorder = func(e outbound.Event) { events = append(events, e) }
				t.Cleanup(func() { outbound.Recorder = nil })
				peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"error":"refused"}`))
				}))
				defer peer.Close()
				g, err := NewGoogle("fixture-client", "fixture-secret", "https://goen.example")
				if err != nil {
					t.Fatal(err)
				}
				target, err := url.Parse(peer.URL)
				if err != nil {
					t.Fatal(err)
				}
				g.http.Transport = localGoogleTransport{target: target, next: g.http.Transport}
				if profile {
					_, err = g.userInfo(t.Context(), "fixture-token")
				} else {
					_, err = g.token(t.Context(), "fixture-code", "fixture-verifier")
				}
				if err == nil {
					t.Fatal("provider error accepted")
				}
				want := outbound.OutcomeRefused
				if status == 500 {
					want = outbound.OutcomeTransport
				}
				if len(events) != 1 || events[0].Outcome != want || events[0].Attempts != 1 {
					t.Errorf("Google events = %+v, want one outcome %v after one actual HTTP attempt", events, want)
				}
			})
		}
	}
}
