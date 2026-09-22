package invoice

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewInvoicePreferencesReachTheProviderUnchanged(t *testing.T) {
	for _, tt := range []struct {
		name                                         string
		preference                                   Preference
		carrier, donation, wireCarrier, wireDonation string
	}{
		{"citizen", PreferenceCitizen, "AB12345678901234", "", "2", "0"},
		{"donation", PreferenceDonate, "", "00123", "", "1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var seen issueRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/B2CInvoice/Issue" {
					t.Errorf("unexpected provider path %s", r.URL.Path)
				}
				g, _ := NewGateway(testMerchantID, testHashKey, testHashIV, "")
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request: %v", err)
					return
				}
				var env envelope
				if envelopeErr := json.Unmarshal(raw, &env); envelopeErr != nil {
					t.Errorf("envelope: %v", envelopeErr)
					return
				}
				plain, err := g.open(env.Data)
				if err != nil {
					t.Errorf("open request: %v", err)
					return
				}
				if payloadErr := json.Unmarshal(plain, &seen); payloadErr != nil {
					t.Errorf("payload: %v", payloadErr)
					return
				}
				reply(t, w, result{RtnCode: 1, InvoiceNo: "AB12345678", InvoiceDate: "2026-08-07 10:30:00", RandomNumber: "1234"})
			}))
			defer srv.Close()
			g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, err = g.Issue(t.Context(), IssueRequest{OrderNumber: "GO260101000001", CustomerName: "Buyer", Email: "buyer@example.com", Preference: tt.preference, CarrierCode: tt.carrier, DonationCode: tt.donation, AmountCents: 10000, Lines: []Line{{Description: "Item", Quantity: 1, UnitPriceCents: 10000, AmountCents: 10000}}})
			if err != nil {
				t.Fatalf("Issue: %v", err)
			}
			if seen.Donation != tt.wireDonation || seen.LoveCode != tt.donation || seen.CarrierT != tt.wireCarrier || seen.CarrierNum != tt.carrier || seen.Print != "0" || seen.CustomerIdentifier != "" {
				t.Errorf("provider preference = donation %q/%q, carrier %q/%q, print %q, tax ID %q", seen.Donation, seen.LoveCode, seen.CarrierT, seen.CarrierNum, seen.Print, seen.CustomerIdentifier)
			}
		})
	}
}

func TestNewInvoicePreferencesRefuseMalformedProviderRequests(t *testing.T) {
	for _, tt := range []struct {
		name                     string
		preference               Preference
		carrier, donation, taxID string
	}{
		{"short citizen", PreferenceCitizen, "AB1234567890123", "", ""},
		{"lowercase citizen", PreferenceCitizen, "ab12345678901234", "", ""},
		{"long citizen", PreferenceCitizen, "AB123456789012345", "", ""},
		{"mobile is not citizen", PreferenceCitizen, "/ABC+123", "", ""},
		{"missing donation", PreferenceDonate, "", "", ""},
		{"short donation", PreferenceDonate, "", "12", ""},
		{"long donation", PreferenceDonate, "", "12345678", ""},
		{"non-digit donation", PreferenceDonate, "", "12A", ""},
		{"donation with carrier", PreferenceDonate, "/ABC+123", "00123", ""},
		{"donation with tax ID", PreferenceDonate, "", "00123", "04595252"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := IssueRequest{OrderNumber: "GO260101000001", CustomerName: "Buyer", Email: "buyer@example.com", Preference: tt.preference, CarrierCode: tt.carrier, DonationCode: tt.donation, TaxID: tt.taxID, AmountCents: 10000, Lines: []Line{{Description: "Item", Quantity: 1, UnitPriceCents: 10000, AmountCents: 10000}}}
			if err := req.validate(); !errors.Is(err, ErrRejected) {
				t.Fatalf("validation=%v, want ErrRejected", err)
			}
		})
	}
}
