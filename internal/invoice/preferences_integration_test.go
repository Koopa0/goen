//go:build integration

package invoice

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestNewInvoicePreferencesSurviveCanonicalSnapshotAndIssue(t *testing.T) {
	for at, preference := range []Preference{PreferenceCitizen, PreferenceDonate} {
		t.Run(string(preference), func(t *testing.T) {
			ctx := t.Context()
			var seen issueRequest
			var providerMu sync.Mutex
			invoiceNumber := []string{"PC12345678", "PD12345678"}[at]
			number := orderToInvoiceFor(t, 10000, 0, 0, preference, "Buyer", "")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerMu.Lock()
				defer providerMu.Unlock()
				switch r.URL.Path {
				case "/B2CInvoice/GetIssue":
					if seen.RelateNumber == "" {
						replyNoIssue(t, w)
					} else {
						replyIssueLookup(t, w, &seen, invoiceNumber, "1234", false)
					}
				case "/B2CInvoice/Issue":
					seen = openIssue(t, r)
					reply(t, w, result{RtnCode: 1, InvoiceNo: invoiceNumber, InvoiceDate: "2026-08-21 10:00:00", RandomNumber: "1234"})
				default:
					t.Errorf("unexpected provider path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewStore(pool, g).Issue(filingTestContext(t, ctx), number); err != nil {
				t.Fatalf("Issue: %v", err)
			}
			var raw []byte
			if err := pool.QueryRow(ctx, `SELECT op.request_payload FROM invoice_operations op JOIN orders o ON o.id=op.order_id WHERE o.order_number=$1 AND op.kind='issue'`, number).Scan(&raw); err != nil {
				t.Fatalf("snapshot: %v", err)
			}
			var frozen frozenRequest
			if err := json.Unmarshal(raw, &frozen); err != nil {
				t.Fatal(err)
			}
			if frozen.Preference != preference {
				t.Fatalf("snapshot preference %q", frozen.Preference)
			}
			providerMu.Lock()
			captured := seen
			providerMu.Unlock()
			if preference == PreferenceDonate {
				if frozen.DonationCode != "00123" || captured.Donation != "1" || captured.LoveCode != "00123" || captured.CarrierT != "" {
					t.Fatalf("donation snapshot/wire = %+v / %+v", frozen, captured)
				}
			} else if frozen.CarrierCode != "AB12345678901234" || captured.CarrierT != "2" || captured.CarrierNum != frozen.CarrierCode {
				t.Fatalf("citizen snapshot/wire = %+v / %+v", frozen, captured)
			}
		})
	}
}
