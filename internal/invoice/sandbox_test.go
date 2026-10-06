package invoice

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestSandboxExemptInvoicePreservesSixCharacterChineseUnit(t *testing.T) {
	if os.Getenv("GOEN_INVOICE_SANDBOX") != "1" {
		t.Skip("requires the public ECPay staging service")
	}
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, StagingBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	relate := "GT" + strconv.FormatInt(time.Now().UnixNano(), 10)
	request := IssueRequest{
		OrderNumber: relate, CustomerName: "Sandbox Buyer", Email: "buyer@example.com", Preference: PreferenceMember,
		AmountCents: 10000,
		Lines:       []Line{{Description: "Sandbox Item", Quantity: 1, UnitPriceCents: 10000, AmountCents: 10000, TaxType: Exempt, Unit: "六字中文單位"}},
	}
	document, err := g.Issue(t.Context(), request)
	if err != nil {
		t.Fatalf("stage Issue: %v", err)
	}
	t.Cleanup(func() {
		voidErr := g.Void(context.WithoutCancel(t.Context()), document.Number, document.IssuedAt, "Sandbox check")
		if voidErr != nil {
			t.Errorf("void sandbox invoice: %v", voidErr)
		}
	})
	lookup, found, err := g.FetchIssue(t.Context(), relate)
	if err != nil {
		t.Fatalf("stage GetIssue: %v", err)
	}
	if !found || !issueMatches(request, lookup, false) {
		t.Fatalf("stage GetIssue did not repeat exempt line and six-character unit: %+v", lookup.Document.Lines)
	}
	t.Logf("ECPay stage Issue and GetIssue preserved exempt tax type and unit %q", request.Lines[0].Unit)
}
