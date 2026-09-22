package invoice

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/outbound"
)

func TestECPayRecordsTypedRefusalSurfaces(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		envelope bool
		want     outbound.Outcome
	}{
		{name: "http refusal", status: 400, want: outbound.OutcomeRefused},
		{name: "server failure", status: 500, want: outbound.OutcomeAmbiguous},
		{name: "envelope refusal", status: 200, envelope: true, want: outbound.OutcomeRefused},
		{name: "document refusal", status: 200, want: outbound.OutcomeRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []outbound.Event
			outbound.Recorder = func(e outbound.Event) { events = append(events, e) }
			t.Cleanup(func() { outbound.Recorder = nil })
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.status != 200 {
					w.WriteHeader(tc.status)
					return
				}
				if tc.envelope {
					_, _ = w.Write([]byte(`{"TransCode":0,"TransMsg":"fixture rejection"}`))
					return
				}
				reply(t, w, result{RtnCode: 9000})
			}))
			defer peer.Close()
			g, err := NewGateway(testMerchantID, testHashKey, testHashIV, peer.URL)
			if err != nil {
				t.Fatal(err)
			}
			err = g.Void(t.Context(), "LA25024809", time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC), "fixture")
			if err == nil {
				t.Fatal("provider error accepted")
			}
			if errors.Is(err, ErrRejected) != (tc.name == "document refusal") {
				t.Errorf("only the document error may become the editable invoice rejection: %v", err)
			}
			if len(events) != 1 || events[0].Outcome != tc.want || events[0].Attempts != 1 {
				t.Errorf("ECPay events = %+v, want one outcome %v after one actual HTTP attempt", events, tc.want)
			}
		})
	}
}
