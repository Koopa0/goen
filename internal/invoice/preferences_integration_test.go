//go:build integration

package invoice

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewInvoicePreferencesSurviveCanonicalSnapshotAndIssue(t *testing.T) {
	for at, preference := range []Preference{PreferenceCitizen, PreferenceDonate} {
		t.Run(string(preference), func(t *testing.T) {
			ctx := t.Context()
			var seen issueRequest
			number := orderToInvoiceFor(t, 10000, 0, 0, preference, "Buyer", "")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/B2CInvoice/GetIssue":
					replyNoIssue(t, w)
				case "/B2CInvoice/Issue":
					seen = openIssue(t, r)
					invoiceNumber := []string{"PC12345678", "PD12345678"}[at]
					reply(t, w, result{RtnCode: 1, InvoiceNo: invoiceNumber, InvoiceDate: "2026-08-07 10:30:00", RandomNumber: "1234"})
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
			if preference == PreferenceDonate {
				if frozen.DonationCode != "00123" || seen.Donation != "1" || seen.LoveCode != "00123" || seen.CarrierT != "" {
					t.Fatalf("donation snapshot/wire = %+v / %+v", frozen, seen)
				}
			} else if frozen.CarrierCode != "AB12345678901234" || seen.CarrierT != "2" || seen.CarrierNum != frozen.CarrierCode {
				t.Fatalf("citizen snapshot/wire = %+v / %+v", frozen, seen)
			}
		})
	}
}
