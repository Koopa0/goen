package issuer

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/koopa0/goen/internal/invoice"
)

func TestIssuingUsesTheFrozenLineTaxTypeAndUnit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tax     invoice.TaxType
		wireTax string
		special bool
	}{
		{name: "taxable", tax: invoice.Taxable, wireTax: "1"},
		{name: "exempt", tax: invoice.Exempt, wireTax: "3", special: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sent := make(chan map[string]any, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				g, err := NewGateway(testMerchantID, testHashKey, testHashIV, "")
				if err != nil {
					t.Error(err)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				var env envelope
				if err = json.Unmarshal(body, &env); err != nil {
					t.Error(err)
					return
				}
				raw, err := g.open(env.Data)
				if err != nil {
					t.Error(err)
					return
				}
				var payload map[string]any
				if err = json.Unmarshal(raw, &payload); err != nil {
					t.Error(err)
					return
				}
				sent <- payload
				reply(t, w, result{RtnCode: 1, InvoiceNo: "AB12345678", InvoiceDate: "2026-08-07 10:30:00", RandomNumber: "1234"})
			}))
			defer srv.Close()
			g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, err = g.Issue(t.Context(), IssueRequest{
				OrderNumber: "GO260101000001", CustomerName: "Buyer", Email: "buyer@example.com", Preference: invoice.PreferenceMember,
				AmountCents: 30000,
				Lines: []invoice.Line{
					{Description: "First", Quantity: 1, UnitPriceCents: 10000, AmountCents: 10000, TaxType: tc.tax, Unit: "包"},
					{Description: "Second", Quantity: 1, UnitPriceCents: 20000, AmountCents: 20000, TaxType: tc.tax, Unit: "六字中文單位"},
				},
			})
			if err != nil {
				t.Fatalf("Issue(%s) = %v, want issued document", tc.name, err)
			}
			payload := <-sent
			if payload["TaxType"] != tc.wireTax {
				t.Errorf("TaxType = %v, want %q", payload["TaxType"], tc.wireTax)
			}
			special, exists := payload["SpecialTaxType"]
			if exists != tc.special || exists && special != float64(8) {
				t.Errorf("SpecialTaxType = %v, present %t, want present %t with integer 8", special, exists, tc.special)
			}
			items, ok := payload["Items"].([]any)
			if !ok || len(items) != 2 {
				t.Fatalf("Items = %v, want two lines", payload["Items"])
			}
			for i, unit := range []string{"包", "六字中文單位"} {
				item, ok := items[i].(map[string]any)
				if !ok {
					t.Fatalf("Items[%d] = %v, want item object", i, items[i])
				}
				if item["ItemWord"] != unit || item["ItemTaxType"] != tc.wireTax {
					t.Errorf("Items[%d] unit/tax = %v/%v, want %q/%q", i, item["ItemWord"], item["ItemTaxType"], unit, tc.wireTax)
				}
			}
		})
	}
}

func TestLookupPreservesTheLineTaxTypeAndUnit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		wireTax providerDecimal
		tax     invoice.TaxType
	}{
		{name: "taxable", wireTax: "1", tax: invoice.Taxable},
		{name: "exempt", wireTax: "3", tax: invoice.Exempt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := (lookupItem{Name: "First", Count: "1", Price: "100", Amount: "100", TaxType: tc.wireTax, Unit: "包"}).line()
			if err != nil {
				t.Fatalf("provider line tax %q = %v, want supported tax type", tc.wireTax, err)
			}
			if got.TaxType != tc.tax || got.Unit != "包" {
				t.Errorf("provider line tax/unit = %q/%q, want %q/包", got.TaxType, got.Unit, tc.tax)
			}
		})
	}
}

func TestProviderNeverReceivesUnsupportedOrMixedLineTerms(t *testing.T) {
	for _, mutate := range []struct {
		name   string
		change func([]invoice.Line)
	}{
		{"mixed", func(lines []invoice.Line) { lines[1].TaxType = invoice.Exempt }},
		{"zero rated", func(lines []invoice.Line) { lines[0].TaxType = invoice.ZeroRated; lines[1].TaxType = invoice.ZeroRated }},
		{"unknown", func(lines []invoice.Line) { lines[0].TaxType = "unknown" }},
		{"blank unit", func(lines []invoice.Line) { lines[0].Unit = "" }},
		{"long unit", func(lines []invoice.Line) { lines[0].Unit = "七個字中文單位" }},
		{"control in unit", func(lines []invoice.Line) { lines[0].Unit = "個\n" }},
	} {
		for _, endpoint := range []string{"issue", "allowance"} {
			t.Run(endpoint+"/"+mutate.name, func(t *testing.T) {
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls++
					reply(t, w, result{RtnCode: 1, InvoiceNo: "AB12345678", InvoiceDate: "2026-08-21 10:00:00", RandomNumber: "1234", AllowanceNo: "2026080715227214", AllowanceInvoiceNo: "AB12345678", AllowanceExpiresAt: "2026-08-22 10:00:00"})
				}))
				defer srv.Close()
				g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
				if err != nil {
					t.Fatal(err)
				}
				lines := []invoice.Line{{Description: "Item", Quantity: 1, UnitPriceCents: 10000, AmountCents: 10000, TaxType: invoice.Taxable, Unit: invoice.DefaultUnit}, {Description: "Other", Quantity: 1, UnitPriceCents: 10000, AmountCents: 10000, TaxType: invoice.Taxable, Unit: invoice.DefaultUnit}}
				mutate.change(lines)
				if endpoint == "issue" {
					_, err = g.Issue(t.Context(), IssueRequest{OrderNumber: "GO260101000001", CustomerName: "Buyer", Email: "buyer@example.com", Preference: invoice.PreferenceMember, AmountCents: 20000, Lines: lines})
				} else {
					err = g.RequestAllowance(t.Context(), AllowanceRequest{InvoiceNumber: "AB12345678", CustomerName: "Buyer", Email: "buyer@example.com", AmountCents: 20000, Lines: lines})
				}
				if !errors.Is(err, ErrRejected) || calls != 0 {
					t.Errorf("invalid %s=%v, provider calls=%d, want local rejection", endpoint, err, calls)
				}
			})
		}
	}
}
