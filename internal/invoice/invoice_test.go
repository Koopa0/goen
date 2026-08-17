package invoice

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ECPay's published staging credentials: public, documented and usable without
// a contract.
const (
	testMerchantID = "2000132"
	testHashKey    = "ejCk326UnaZWKisg"
	testHashIV     = "q9jcZX8Ib9LM8wYk"
)

// TestTheEnvelopeRoundTrips proves seal and open are inverses. Weak on its own —
// a pair of no-ops round-trips too — so TestTheEnvelopeIsAESNotSomethingElse
// fixes the wire format against independent expectations.
func TestTheEnvelopeRoundTrips(t *testing.T) {
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, StagingBaseURL)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	// Chinese, a slash, a plus and an ampersand: every character the URL-encode
	// step treats specially.
	want := `{"ItemName":"保護殼 & 傳輸線","CarrierNum":"/ABC+123"}`
	sealed, err := g.seal([]byte(want))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if sealed == want {
		t.Fatal("seal returned its input; nothing was encrypted")
	}
	got, err := g.open(sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(got) != want {
		t.Errorf("round trip gave %q, want %q", got, want)
	}
}

// TestTheEnvelopeIsAESNotSomethingElse fixes the wire format against a value
// computed OUTSIDE this code — the .NET-UrlEncoded form of {"a":1}:
//
//	printf '%%7b%%22a%%22%%3a1%%7d' |
//	  openssl enc -aes-128-cbc \
//	    -K $(printf 'ejCk326UnaZWKisg' | xxd -p) \
//	    -iv $(printf 'q9jcZX8Ib9LM8wYk' | xxd -p) | base64
func TestTheEnvelopeIsAESNotSomethingElse(t *testing.T) {
	const wantSealed = "GnpdFuAYZzKPghwgjwaxrwiQQYnT2LIzACtF7pm98Sk="

	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, StagingBaseURL)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	sealed, err := g.seal([]byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if sealed != wantSealed {
		t.Errorf("seal produced\n  %s\nwant\n  %s\n"+
			"The envelope no longer matches what ECPay decrypts. Check, in order: "+
			"the URL-encode step and its .NET casing, AES-128-CBC, PKCS#7, and the "+
			"key/IV being the raw ASCII of the credentials rather than a digest of them.",
			sealed, wantSealed)
	}
}

// TestDotNetURLEncodeMatchesTheirEncoder holds the step that is easy to get
// almost right: .NET's UrlEncode lower-cases hex digits and leaves !()* alone,
// where Go's url.QueryEscape upper-cases and escapes all four.
func TestDotNetURLEncodeMatchesTheirEncoder(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "hex is lower case", in: "{", want: "%7b"},
		{name: "the four .NET leaves alone", in: "!()*", want: "!()*"},
		{name: "space is a plus", in: "a b", want: "a+b"},
		{name: "a slash is escaped", in: "/ABC", want: "%2fABC"},
		{name: "ascii passes through", in: "abcXYZ019-_.", want: "abcXYZ019-_."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dotNetURLEncode(tt.in); got != tt.want {
				t.Errorf("dotNetURLEncode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestPaddingRefusesWhatAThirdPartySends proves the unpad checks rather than
// trusts: the last byte of a decrypted reply is a length from somebody else's
// server.
func TestPaddingRefusesWhatAThirdPartySends(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
	}{
		{name: "empty", in: nil},
		{name: "not a whole block", in: make([]byte, 7)},
		{name: "zero length byte", in: append(make([]byte, 15), 0)},
		{name: "longer than the block", in: append(make([]byte, 15), 200)},
		{name: "padding bytes disagree", in: append(append(make([]byte, 13), 3), 3, 9)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pkcs7Unpad(tt.in, 16); err == nil {
				t.Errorf("pkcs7Unpad(%v) was accepted", tt.in)
			}
		})
	}

	// The control: a function that refused everything would pass every case
	// above.
	valid := append([]byte("goen"), make([]byte, 12)...)
	for i := 4; i < 16; i++ {
		valid[i] = 12
	}
	got, err := pkcs7Unpad(valid, 16)
	if err != nil {
		t.Fatalf("valid padding was refused: %v", err)
	}
	if string(got) != "goen" {
		t.Errorf("unpadded to %q, want %q", got, "goen")
	}
}

// TestAnUnconfiguredGatewayIssuesNothingAndSaysSo holds the off-switch.
func TestAnUnconfiguredGatewayIssuesNothingAndSaysSo(t *testing.T) {
	g, err := NewGateway("", "", "", "")
	if err != nil {
		t.Fatalf("an empty configuration must be legal: %v", err)
	}
	if g.Enabled() {
		t.Error("a gateway with no credentials reports itself enabled")
	}
	if _, err := g.Issue(t.Context(), IssueRequest{}); !errors.Is(err, ErrDisabled) {
		t.Errorf("Issue on an unconfigured gateway = %v, want ErrDisabled", err)
	}
	if err := g.Void(t.Context(), "AB12345678", "測試"); !errors.Is(err, ErrDisabled) {
		t.Errorf("Void on an unconfigured gateway = %v, want ErrDisabled", err)
	}
	if _, err := g.Allowance(t.Context(), AllowanceRequest{}); !errors.Is(err, ErrDisabled) {
		t.Errorf("Allowance on an unconfigured gateway = %v, want ErrDisabled", err)
	}
}

// TestHalfAConfigurationDoesNotStart is the other half of the off-switch: a
// merchant id without its keys cannot sign a request, and would otherwise fail
// at the first issue, after an order was placed and money taken.
func TestHalfAConfigurationDoesNotStart(t *testing.T) {
	tests := []struct {
		name              string
		merchant, key, iv string
	}{
		{name: "merchant only", merchant: testMerchantID},
		{name: "no IV", merchant: testMerchantID, key: testHashKey},
		{name: "no merchant", key: testHashKey, iv: testHashIV},
		{name: "key of the wrong length", merchant: testMerchantID, key: "short", iv: testHashIV},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewGateway(tt.merchant, tt.key, tt.iv, ""); err == nil {
				t.Error("a half configuration was accepted")
			}
		})
	}
}

// TestIssueRefusesWhatTheProviderWould stops a malformed document reaching
// ECPay as an error code nobody can act on.
func TestIssueRefusesWhatTheProviderWould(t *testing.T) {
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, StagingBaseURL)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	good := IssueRequest{
		OrderNumber: "GO-260101-000001", CustomerName: "王小明",
		Email: "a@example.com", Preference: "member_carrier",
		AmountCents: 67000,
		Lines:       []Line{{Description: "保護殼", Quantity: 1, UnitPriceCents: 59000, AmountCents: 59000}},
	}

	tests := []struct {
		name  string
		alter func(*IssueRequest)
	}{
		{name: "no order", alter: func(r *IssueRequest) { r.OrderNumber = "" }},
		{name: "nothing to invoice", alter: func(r *IssueRequest) { r.AmountCents = 0 }},
		{name: "no items", alter: func(r *IssueRequest) { r.Lines = nil }},
		{name: "no email to carry it", alter: func(r *IssueRequest) { r.Email = "" }},
		{name: "a 統編 that is not eight digits", alter: func(r *IssueRequest) {
			r.Preference, r.TaxID = "company", "1234"
		}},
		{name: "a carrier that is not a barcode", alter: func(r *IssueRequest) {
			r.Preference, r.CarrierCode = "mobile_carrier", "ABC123"
		}},
		{name: "an invoice type this shop does not offer", alter: func(r *IssueRequest) {
			r.Preference = "printed"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := good
			tt.alter(&req)
			if _, err := g.Issue(t.Context(), req); !errors.Is(err, ErrRejected) {
				t.Errorf("Issue = %v, want ErrRejected", err)
			}
		})
	}
}

// TestTheRequestCarriesWhatTheInvoiceNeeds reads the actual wire bytes, against
// an httptest.Server rather than a fake that would agree with whatever goen
// believes.
func TestTheRequestCarriesWhatTheInvoiceNeeds(t *testing.T) {
	var seen issueRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g, _ := NewGateway(testMerchantID, testHashKey, testHashIV, "")
		body, _ := io.ReadAll(r.Body)
		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			t.Errorf("the request is not JSON: %v", err)
			return
		}
		if env.MerchantID != testMerchantID {
			t.Errorf("envelope MerchantID = %q, want %q", env.MerchantID, testMerchantID)
		}
		if env.RqHeader.Timestamp == 0 {
			t.Error("the envelope carries no timestamp; ECPay rejects that as a replay")
		}
		opened, err := g.open(env.Data)
		if err != nil {
			t.Errorf("ECPay could not decrypt what we sent: %v", err)
			return
		}
		if err := json.Unmarshal(opened, &seen); err != nil {
			t.Errorf("the decrypted payload is not the JSON we think: %v", err)
		}
		reply(t, w, result{RtnCode: 1, InvoiceNo: "AB12345678",
			InvoiceDate: "2026-08-07 10:30:00", RandomNumber: "1234"})
	}))
	defer srv.Close()

	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	doc, err := g.Issue(t.Context(), IssueRequest{
		OrderNumber: "GO-260101-000001", CustomerName: "王小明",
		Email: "a@example.com", Preference: "mobile_carrier", CarrierCode: "/ABC+123",
		AmountCents: 67000,
		Lines: []Line{
			{Description: "保護殼", Quantity: 2, UnitPriceCents: 29500, AmountCents: 59000},
		},
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// The amounts are WHOLE DOLLARS: goen holds cents and ECPay wants dollars.
	if seen.SalesAmount != 670 {
		t.Errorf("SalesAmount = %d, want 670 — goen's 67000 cents in dollars", seen.SalesAmount)
	}
	if len(seen.Items) != 1 || seen.Items[0].ItemAmount != 590 || seen.Items[0].ItemPrice != 295 {
		t.Errorf("items = %+v, want one line at 295 x 2 = 590", seen.Items)
	}
	if seen.Vat != "1" {
		t.Errorf("vat = %q, want \"1\" — item prices are tax-inclusive", seen.Vat)
	}
	if seen.Print != "0" {
		t.Errorf("Print = %q, want \"0\" — goen never prints", seen.Print)
	}
	if seen.CarrierT != CarrierMobile || seen.CarrierNum != "/ABC+123" {
		t.Errorf("carrier = %q/%q, want the mobile barcode the customer gave",
			seen.CarrierT, seen.CarrierNum)
	}

	if doc.Number != "AB12345678" {
		t.Errorf("the invoice number is %q, want AB12345678", doc.Number)
	}
	// The RandomNumber is kept because a VOID needs it alongside the number.
	if doc.ProviderRef != "1234" {
		t.Errorf("provider ref is %q, want the RandomNumber 1234", doc.ProviderRef)
	}
	if doc.IssuedAt.IsZero() {
		t.Error("the document has no issue time")
	}
}

// TestACompanyInvoiceCarriesTheTaxIDAndNoCarrier holds a rule ECPay enforces
// and a reader would not guess.
func TestACompanyInvoiceCarriesTheTaxIDAndNoCarrier(t *testing.T) {
	var seen issueRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g, _ := NewGateway(testMerchantID, testHashKey, testHashIV, "")
		body, _ := io.ReadAll(r.Body)
		var env envelope
		_ = json.Unmarshal(body, &env)
		opened, _ := g.open(env.Data)
		_ = json.Unmarshal(opened, &seen)
		reply(t, w, result{RtnCode: 1, InvoiceNo: "AB99999999", RandomNumber: "5678"})
	}))
	defer srv.Close()

	g, _ := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if _, err := g.Issue(t.Context(), IssueRequest{
		OrderNumber: "GO-260101-000002", CustomerName: "測試股份有限公司",
		Email: "ap@example.com", Preference: "company", TaxID: "12345678",
		AmountCents: 100000,
		Lines:       []Line{{Description: "耳機", Quantity: 1, UnitPriceCents: 100000, AmountCents: 100000}},
	}); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if seen.CustomerIdentifier != "12345678" {
		t.Errorf("CustomerIdentifier = %q, want the 統編", seen.CustomerIdentifier)
	}
	// A business-tax-number invoice STILL needs a carrier (ECPay RtnCode
	// 5000028), which is the opposite of what it looks like.
	if seen.CarrierT != CarrierMember {
		t.Errorf("CarrierType = %q on a 統編 invoice, want the member carrier: "+
			"ECPay refuses a 統編 with no carrier and goen does not print",
			seen.CarrierT)
	}
}

// TestAnAllowanceReadsItsOwnNumberField holds a field name that looks
// interchangeable and is not: a credit note's number comes back in IA_Allow_No
// and the reply's InvoiceNo is EMPTY.
func TestAnAllowanceReadsItsOwnNumberField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Exactly what staging returns.
		reply(t, w, result{RtnCode: 1, AllowanceNo: "2026080715227214"})
	}))
	defer srv.Close()

	g, _ := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	doc, err := g.Allowance(t.Context(), AllowanceRequest{
		InvoiceNumber: "LA45000603", InvoiceDate: time.Now(),
		CustomerName: "王小明", Email: "a@example.com", AmountCents: 20000,
		Lines: []Line{{Description: "傳輸線", Quantity: 1, UnitPriceCents: 20000, AmountCents: 20000}},
	})
	if err != nil {
		t.Fatalf("Allowance: %v", err)
	}
	if doc.Number != "2026080715227214" {
		t.Errorf("the allowance number is %q, want the IA_Allow_No — an allowance "+
			"goen cannot name is one it cannot reconcile", doc.Number)
	}
	if doc.Kind != "allowance" {
		t.Errorf("kind = %q, want allowance", doc.Kind)
	}
}

// TestAProviderRefusalIsItsOwnError keeps the three failure surfaces apart: a
// transport failure is worth retrying, an envelope rejection is a bug here, and
// a document rejection is data a staff member fixes.
func TestAProviderRefusalIsItsOwnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reply(t, w, result{RtnCode: 2000006, RtnMsg: "CustomerIdentifier 格式錯誤"})
	}))
	defer srv.Close()

	g, _ := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	_, err := g.Issue(t.Context(), IssueRequest{
		OrderNumber: "GO-260101-000003", CustomerName: "王小明",
		Email: "a@example.com", Preference: "member_carrier", AmountCents: 10000,
		Lines: []Line{{Description: "線", Quantity: 1, UnitPriceCents: 10000, AmountCents: 10000}},
	})
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("a refused document = %v, want ErrRejected", err)
	}
	// The provider's own message, because it names what to fix.
	if !strings.Contains(err.Error(), "格式錯誤") {
		t.Errorf("the error does not carry the provider's reason: %v", err)
	}
}

// TestAnUnreadableEnvelopeIsNotADocumentRefusal proves the other half of that
// split: TransCode is about whether ECPay could READ the request.
func TestAnUnreadableEnvelopeIsNotADocumentRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response{TransCode: 0, TransMsg: "MerchantID Error"})
	}))
	defer srv.Close()

	g, _ := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	_, err := g.Issue(t.Context(), IssueRequest{
		OrderNumber: "GO-260101-000004", CustomerName: "王小明",
		Email: "a@example.com", Preference: "member_carrier", AmountCents: 10000,
		Lines: []Line{{Description: "線", Quantity: 1, UnitPriceCents: 10000, AmountCents: 10000}},
	})
	if err == nil {
		t.Fatal("an unreadable envelope was accepted")
	}
	if errors.Is(err, ErrRejected) {
		t.Errorf("an envelope failure was reported as a document refusal: %v", err)
	}
}

// reply writes an encrypted ECPay response, the way their server does.
func reply(t *testing.T, w http.ResponseWriter, res result) {
	t.Helper()
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, "")
	if err != nil {
		t.Errorf("NewGateway: %v", err)
		return
	}
	payload, err := json.Marshal(res)
	if err != nil {
		t.Errorf("encode result: %v", err)
		return
	}
	sealed, err := g.seal(payload)
	if err != nil {
		t.Errorf("seal: %v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response{TransCode: 1, Data: sealed}); err != nil {
		t.Errorf("write reply: %v", err)
	}
}

// TestAReissueCarriesADistinctRelateNumber holds ECPay's idempotency key: a
// repeat is refused with RtnCode 5070357, so the order number alone makes the
// void-then-reissue correction path impossible.
func TestAReissueCarriesADistinctRelateNumber(t *testing.T) {
	tests := []struct {
		name    string
		attempt int32
		want    string
	}{
		{name: "the first invoice is the order", attempt: 0, want: "GO-260101-000001"},
		{name: "a reissue after one void", attempt: 1, want: "GO-260101-000001-1"},
		{name: "and after two", attempt: 2, want: "GO-260101-000001-2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := relateNumber("GO-260101-000001", tt.attempt); got != tt.want {
				t.Errorf("relateNumber(%d) = %q, want %q", tt.attempt, got, tt.want)
			}
		})
	}
}
