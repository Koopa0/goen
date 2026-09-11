package invoice

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// ECPay's published staging credentials: public, documented and usable without
// a contract.
const (
	testMerchantID = "2000132"
	testHashKey    = "ejCk326UnaZWKisg"
	testHashIV     = "q9jcZX8Ib9LM8wYk"
)

type filingContextKey struct{}

// result is a compact test fixture for the endpoint-specific success structs.
// Production decoding is generic and typed per endpoint.
type result struct {
	RtnCode            int    `json:"RtnCode"`
	RtnMsg             string `json:"RtnMsg"`
	InvoiceNo          string `json:"InvoiceNo"`
	InvoiceDate        string `json:"InvoiceDate"`
	RandomNumber       string `json:"RandomNumber"`
	AllowanceNo        string `json:"IA_Allow_No"`
	AllowanceInvoiceNo string `json:"IA_Invoice_No"`
	AllowanceDate      string `json:"IA_Date"`
}

// These allocation helpers mirror an itemisation policy whose production
// authority is canonical_invoice_lines in PostgreSQL; keeping the reference
// implementation test-only avoids a second live source of truth.
func relateNumber(orderNumber string, attempt int32) string {
	orderNumber = strings.ReplaceAll(orderNumber, "-", "")
	if attempt <= 0 {
		return orderNumber
	}
	return orderNumber + "R" + strconv.FormatInt(int64(attempt), 10)
}

func discountLines(lines []Line, discountCents int64) []Line {
	if discountCents <= 0 || len(lines) == 0 {
		return lines
	}
	total := sumLines(lines)
	if total <= 0 {
		return lines
	}
	if discountCents >= total {
		for i := range lines {
			lines[i].AmountCents = 0
			lines[i].UnitPriceCents = 0
		}
		return lines
	}
	type share struct {
		at        int
		remainder int64
	}
	shares := make([]share, 0, len(lines))
	var given int64
	for i := range lines {
		cut, remainder := proportionalShare(lines[i].AmountCents, discountCents, total)
		lines[i].AmountCents -= cut
		given += cut
		shares = append(shares, share{at: i, remainder: remainder})
	}
	slices.SortFunc(shares, func(a, b share) int {
		if byRemainder := cmp.Compare(b.remainder, a.remainder); byRemainder != 0 {
			return byRemainder
		}
		return cmp.Compare(a.at, b.at)
	})
	for i := 0; given < discountCents; i++ {
		lines[shares[i%len(shares)].at].AmountCents--
		given++
	}
	for i := range lines {
		if lines[i].Quantity > 0 {
			lines[i].UnitPriceCents = lines[i].AmountCents / int64(lines[i].Quantity)
		}
	}
	return lines
}

func proportionalShare(amount, part, whole int64) (quotient, remainder int64) {
	if amount < 0 || part < 0 || whole <= 0 || part > whole {
		panic("proportionalShare requires 0 <= part <= whole and a nonnegative amount")
	}
	numerator := new(big.Int).Mul(big.NewInt(amount), big.NewInt(part))
	quotientBig, remainderBig := new(big.Int), new(big.Int)
	quotientBig.QuoRem(numerator, big.NewInt(whole), remainderBig)
	return quotientBig.Int64(), remainderBig.Int64()
}

func snapToDollars(lines []Line, headerCents int64) []Line {
	if len(lines) == 0 {
		return lines
	}
	wantDollars := headerCents / 100
	var given int64
	for i := range lines {
		quantity := max(int64(lines[i].Quantity), 1)
		unit := lines[i].AmountCents / quantity / 100
		lines[i].UnitPriceCents = unit * 100
		lines[i].AmountCents = unit * 100 * quantity
		given += unit * quantity
	}
	if short := wantDollars - given; short > 0 {
		lines = append(lines, Line{
			Description: "折扣尾數調整", Quantity: 1,
			UnitPriceCents: short * 100, AmountCents: short * 100,
		})
	}
	return lines
}

func sumLines(lines []Line) int64 {
	var total int64
	for _, line := range lines {
		total += line.AmountCents
	}
	return total
}

func TestFilingContextKeepsValuesDropsCancellationAndAddsADeadline(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		parent := context.WithValue(t.Context(), filingContextKey{}, "request-value")
		parent, cancelParent := context.WithCancel(parent)
		cancelParent()

		ctx, cancel := filingContext(parent)
		defer cancel()
		if err := ctx.Err(); err != nil {
			t.Fatalf("filingContext() inherited request cancellation: %v", err)
		}
		if got := ctx.Value(filingContextKey{}); got != "request-value" {
			t.Errorf("filingContext() value = %v, want request-value", got)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) != filingTimeout {
			t.Fatalf("filingContext() deadline = %v, ok=%t; want %s from now",
				deadline, ok, filingTimeout)
		}

		time.Sleep(filingTimeout)
		synctest.Wait()
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Errorf("filingContext() after deadline = %v, want context deadline exceeded", ctx.Err())
		}
	})
}

// TestTheEnvelopeRoundTrips proves seal and open are inverses.
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
	if err := g.Void(t.Context(), "AB12345678", time.Now(), "測試"); !errors.Is(err, ErrDisabled) {
		t.Errorf("Void on an unconfigured gateway = %v, want ErrDisabled", err)
	}
	if _, err := g.Allowance(t.Context(), AllowanceRequest{}); !errors.Is(err, ErrDisabled) {
		t.Errorf("Allowance on an unconfigured gateway = %v, want ErrDisabled", err)
	}
}

// TestHalfAConfigurationDoesNotStart is the other half of the off-switch: a
// merchant id without its keys cannot sign a request.
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

func TestGatewayRefusesUnsafeBaseURLs(t *testing.T) {
	t.Parallel()
	for _, baseURL := range []string{
		"ecpay.example.test",
		"/relative",
		"ftp://ecpay.example.test",
		"https://merchant:secret@ecpay.example.test",
	} {
		t.Run(baseURL, func(t *testing.T) {
			t.Parallel()
			if _, err := NewGateway(testMerchantID, testHashKey, testHashIV, baseURL); err == nil {
				t.Errorf("NewGateway accepted unsafe base URL %q", baseURL)
			}
		})
	}

	if _, err := NewGateway(testMerchantID, testHashKey, testHashIV, "http://127.0.0.1:8080"); err != nil {
		t.Fatalf("NewGateway refused an absolute HTTP test endpoint: %v", err)
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
		OrderNumber: "GO260101000001", CustomerName: "王小明",
		Email: "a@example.com", Preference: "member_carrier",
		AmountCents: 67000,
		Lines:       []Line{{Description: "保護殼", Quantity: 1, UnitPriceCents: 59000, AmountCents: 59000}},
	}

	tests := []struct {
		name  string
		alter func(*IssueRequest)
	}{
		{name: "no order", alter: func(r *IssueRequest) { r.OrderNumber = "" }},
		{name: "provider-unsafe order", alter: func(r *IssueRequest) {
			r.OrderNumber = "GO-260101-000001"
		}},
		{name: "nothing to invoice", alter: func(r *IssueRequest) { r.AmountCents = 0 }},
		{name: "no items", alter: func(r *IssueRequest) { r.Lines = nil }},
		{name: "too many items", alter: func(r *IssueRequest) {
			r.Lines = make([]Line, MaxIssueItems+1)
		}},
		{name: "no buyer name", alter: func(r *IssueRequest) { r.CustomerName = "" }},
		{name: "buyer name with a control", alter: func(r *IssueRequest) {
			r.CustomerName = "買受\n公司"
		}},
		{name: "no email to carry it", alter: func(r *IssueRequest) { r.Email = "" }},
		{name: "malformed email", alter: func(r *IssueRequest) { r.Email = "not-an-address" }},
		{name: "display-name email", alter: func(r *IssueRequest) {
			r.Email = "Buyer <a@example.com>"
		}},
		{name: "email with surrounding space", alter: func(r *IssueRequest) {
			r.Email = "a@example.com "
		}},
		{name: "a 統編 that is not eight digits", alter: func(r *IssueRequest) {
			r.Preference, r.TaxID = "company", "1234"
		}},
		{name: "a carrier that is not a barcode", alter: func(r *IssueRequest) {
			r.Preference, r.CarrierCode = "mobile_carrier", "ABC123"
		}},
		{name: "an eight-character carrier with an invalid symbol", alter: func(r *IssueRequest) {
			r.Preference, r.CarrierCode = "mobile_carrier", "/ABC_123"
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

// TestEveryOfferedPreferenceCanBecomeAValidIssueRequest binds the closed set
// shown at checkout to the issuer's validation rules. Adding a preference is
// therefore not complete until this test supplies everything that preference
// semantically requires.
func TestEveryOfferedPreferenceCanBecomeAValidIssueRequest(t *testing.T) {
	want := []Preference{
		PreferenceMember,
		PreferenceMobile,
		PreferenceCompany,
	}
	got := OfferedPreferences()
	if !slices.Equal(got, want) {
		t.Fatalf("OfferedPreferences() = %v, want the canonical display order %v", got, want)
	}
	got[0] = Preference("mutated")
	if !slices.Equal(OfferedPreferences(), want) {
		t.Fatal("mutating OfferedPreferences() changed the canonical set")
	}

	seen := make(map[Preference]bool, len(want))
	for _, preference := range OfferedPreferences() {
		t.Run(string(preference), func(t *testing.T) {
			if seen[preference] {
				t.Fatalf("OfferedPreferences contains %q more than once", preference)
			}
			seen[preference] = true
			if !preference.Known() {
				t.Fatalf("offered preference %q is not Known", preference)
			}

			req := IssueRequest{
				OrderNumber:  "GO260101000001",
				CustomerName: "王小明",
				Email:        "buyer@example.com",
				Preference:   preference,
				AmountCents:  10000,
				Lines: []Line{{
					Description:    "契約測試商品",
					Quantity:       1,
					UnitPriceCents: 10000,
					AmountCents:    10000,
				}},
			}
			if preference.NeedsCarrier() {
				req.CarrierCode = "/AB12345"
			}
			if preference.NeedsTaxID() {
				req.TaxID = "04595252"
			}
			if err := req.validate(); err != nil {
				t.Errorf("valid request for offered preference %q: %v", preference, err)
			}
		})
	}

	unknown := Preference("paper")
	if unknown.Known() {
		t.Fatalf("unknown preference %q is Known", unknown)
	}
	if err := (IssueRequest{
		OrderNumber:  "GO260101000002",
		CustomerName: "王小明",
		Email:        "buyer@example.com",
		Preference:   unknown,
		AmountCents:  10000,
		Lines:        []Line{{Description: "契約測試商品", Quantity: 1, UnitPriceCents: 10000, AmountCents: 10000}},
	}).validate(); !errors.Is(err, ErrRejected) {
		t.Errorf("unknown preference validation = %v, want ErrRejected", err)
	}
}

// TestTheRequestCarriesWhatTheInvoiceNeeds reads the actual wire bytes off an
// httptest.Server.
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
		OrderNumber: "GO260101000001", CustomerName: "王小明",
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

// TestAVoidSendsTheInvoicesOwnIssueDate fixes ECPay's strict Invalid wire
// field. A mismatched date is reported as 1600003 ("no invoice number"), even
// when the number itself is right.
func TestAVoidSendsTheInvoicesOwnIssueDate(t *testing.T) {
	var seen invalidRequest
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		seen = openInvalid(t, r)
		reply(t, w, result{RtnCode: 1, InvoiceNo: "LA25024809"})
	}))
	defer srv.Close()

	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	if err := g.Void(t.Context(), "LA25024809", time.Time{}, "資料錯誤"); !errors.Is(err, ErrRejected) {
		t.Fatalf("Void with no issue date = %v, want ErrRejected", err)
	}
	if calls != 0 {
		t.Fatalf("a void with no issue date reached ECPay %d times", calls)
	}
	// This instant is 25 August in Taipei and 24 August in UTC. ECPay returned
	// the latter calendar date, so a plain Format after the database round trip
	// is one day late.
	issuedAt := time.Date(2026, time.August, 25, 4, 0, 0, 0,
		time.FixedZone("Asia/Taipei", 8*60*60))
	if err := g.Void(t.Context(), "LA25024809", issuedAt, "資料錯誤"); err != nil {
		t.Fatalf("Void: %v", err)
	}
	if want := "2026-08-24"; seen.InvoiceDate != want {
		t.Errorf("InvoiceDate = %q, want the invoice's own date %q", seen.InvoiceDate, want)
	}
}

func TestAVoidRequiresTheExactProviderSuccessIdentity(t *testing.T) {
	for _, returned := range []string{"", "ZZ99999999"} {
		t.Run(returned, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reply(t, w, result{RtnCode: 1, InvoiceNo: returned})
			}))
			defer srv.Close()

			g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
			if err != nil {
				t.Fatalf("NewGateway: %v", err)
			}
			err = g.Void(t.Context(), "LA25024809",
				time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC), "資料錯誤")
			if !errors.Is(err, errProviderIdentity) {
				t.Fatalf("Void success identity %q = %v, want provider identity mismatch",
					returned, err)
			}
		})
	}
}

// openInvalid unseals the Invalid request ECPay would receive.
func openInvalid(t *testing.T, r *http.Request) invalidRequest {
	t.Helper()
	g, gatewayErr := NewGateway(testMerchantID, testHashKey, testHashIV, "")
	if gatewayErr != nil {
		t.Fatalf("gateway: %v", gatewayErr)
	}
	var env envelope
	if decodeErr := json.NewDecoder(r.Body).Decode(&env); decodeErr != nil {
		t.Fatalf("decode envelope: %v", decodeErr)
	}
	plain, openErr := g.open(env.Data)
	if openErr != nil {
		t.Fatalf("open envelope: %v", openErr)
	}
	var out invalidRequest
	if decodeErr := json.Unmarshal(plain, &out); decodeErr != nil {
		t.Fatalf("decode Invalid request: %v", decodeErr)
	}
	return out
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
		reply(t, w, result{RtnCode: 1, InvoiceNo: "AB99999999",
			InvoiceDate: "2026-08-07 10:30:00", RandomNumber: "5678"})
	}))
	defer srv.Close()

	g, _ := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if _, err := g.Issue(t.Context(), IssueRequest{
		OrderNumber: "GO260101000002", CustomerName: "測試股份有限公司",
		Email: "ap@example.com", Preference: "company", TaxID: "04595252",
		AmountCents: 100000,
		Lines:       []Line{{Description: "耳機", Quantity: 1, UnitPriceCents: 100000, AmountCents: 100000}},
	}); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if seen.CustomerIdentifier != "04595252" {
		t.Errorf("CustomerIdentifier = %q, want the 統編", seen.CustomerIdentifier)
	}
	if seen.CustomerName != "測試股份有限公司" {
		t.Errorf("CustomerName = %q, want the company registered for that 統編",
			seen.CustomerName)
	}
	// A business-tax-number invoice STILL needs a carrier (ECPay RtnCode
	// 5000028), which is the opposite of what it looks like.
	if seen.CarrierT != CarrierMember {
		t.Errorf("CarrierType = %q on a 統編 invoice, want the member carrier: "+
			"ECPay refuses a 統編 with no carrier and goen does not print",
			seen.CarrierT)
	}
}

// TestAnAllowanceReadsItsOwnNumberField holds the two distinct provider
// identities: IA_Allow_No is the credit note and IA_Invoice_No is its original.
func TestAnAllowanceReadsItsOwnNumberField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Exactly what staging returns.
		reply(t, w, result{RtnCode: 1, AllowanceInvoiceNo: "LA45000603", AllowanceNo: "2026080715227214",
			AllowanceDate: "2026-08-07 15:22:00"})
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

func TestAProviderSuccessMustCarryAValidDocumentIdentity(t *testing.T) {
	t.Parallel()

	issue := func(t *testing.T, res result) error {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reply(t, w, res)
		}))
		defer srv.Close()
		g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
		if err != nil {
			t.Fatalf("gateway: %v", err)
		}
		_, err = g.Issue(t.Context(), IssueRequest{
			OrderNumber: "GO260101000099", CustomerName: "測試", Email: "a@example.com",
			Preference: PreferenceMember, AmountCents: 10000,
			Lines: []Line{{Description: "商品", Quantity: 1, UnitPriceCents: 10000, AmountCents: 10000}},
		})
		return err
	}
	allowance := func(t *testing.T, res result) error {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reply(t, w, res)
		}))
		defer srv.Close()
		g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
		if err != nil {
			t.Fatalf("gateway: %v", err)
		}
		_, err = g.Allowance(t.Context(), AllowanceRequest{
			InvoiceNumber: "AB12345678", InvoiceDate: time.Now(), CustomerName: "測試",
			Email: "a@example.com", AmountCents: 10000,
			Lines: []Line{{Description: "退貨折讓", Quantity: 1, UnitPriceCents: 10000, AmountCents: 10000}},
		})
		return err
	}

	for _, tt := range []struct {
		name string
		call func(*testing.T, result) error
		res  result
	}{
		{"issue number missing", issue, result{RtnCode: 1, InvoiceDate: "2026-08-07 10:30:00", RandomNumber: "1234"}},
		{"issue number malformed", issue, result{RtnCode: 1, InvoiceNo: "../../etc", InvoiceDate: "2026-08-07 10:30:00", RandomNumber: "1234"}},
		{"issue random number malformed", issue, result{RtnCode: 1, InvoiceNo: "AB12345678", InvoiceDate: "2026-08-07 10:30:00", RandomNumber: "12x4"}},
		{"issue date malformed", issue, result{RtnCode: 1, InvoiceNo: "AB12345678", InvoiceDate: "not-a-date", RandomNumber: "1234"}},
		{"allowance number malformed", allowance, result{RtnCode: 1, AllowanceInvoiceNo: "AB12345678", AllowanceNo: "IA-1", AllowanceDate: "2026-08-07 10:30:00"}},
		{"allowance date malformed", allowance, result{RtnCode: 1, AllowanceInvoiceNo: "AB12345678", AllowanceNo: "2026080715227214", AllowanceDate: "2026-99-99 10:30:00"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.call(t, tt.res); err == nil {
				t.Fatal("malformed provider success payload was accepted")
			}
		})
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
		OrderNumber: "GO260101000003", CustomerName: "王小明",
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

func TestFetchIssueReadsCompleteProviderTruthAndDocumentedNotFound(t *testing.T) {
	var notFound bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if notFound {
			reply(t, w, map[string]any{"RtnCode": 2, "RtnMsg": "not found"})
			return
		}
		reply(t, w, map[string]any{
			"RtnCode": 1, "RtnMsg": "ok",
			"IIS_Number": "AB12345678", "IIS_Relate_Number": "GO260101000001",
			"IIS_Sales_Amount": "670.00", "IIS_Create_Date": "2026-08-07 10:30:00",
			"IIS_Issue_Status": "1", "IIS_Invalid_Status": 0,
			"IIS_Check_Number": "P", "IIS_Random_Number": "1234",
			"Items": []map[string]any{{
				"ItemName": "保護殼", "ItemCount": "2", "ItemPrice": "295",
				"ItemAmount": "590", "ItemTaxType": "1",
			}, {
				"ItemName": "運費", "ItemCount": 1, "ItemPrice": 80,
				"ItemAmount": 80, "ItemTaxType": 1,
			}},
		})
	}))
	defer srv.Close()

	g, _ := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	got, found, err := g.FetchIssue(t.Context(), "GO260101000001")
	if err != nil || !found {
		t.Fatalf("FetchIssue = found %t, err %v", found, err)
	}
	if got.RelateNumber != "GO260101000001" || got.Document.AmountCents != 67000 ||
		got.Document.ProviderRef != "1234" || len(got.Document.Lines) != 2 {
		t.Fatalf("FetchIssue = %+v", got)
	}

	notFound = true
	_, found, err = g.FetchIssue(t.Context(), "GO260101000404")
	if err != nil || found {
		t.Fatalf("documented not found = found %t, err %v", found, err)
	}
}

func TestFetchAllowancesDistinguishesZeroOneAndMultiple(t *testing.T) {
	mode := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if mode == 0 {
			reply(t, w, map[string]any{"RtnCode": 7, "RtnMsg": "no data"})
			return
		}
		rows := []map[string]any{{
			"IA_Allow_No": "2026080715227214", "IA_Date": "2026-08-07 15:22:00",
			"IA_Invoice_No": "AB12345678", "IA_Invalid_Status": 0,
			"IA_Total_Tax_Amount": 200,
			"Items": []map[string]any{{
				"ItemName": "退貨折讓", "ItemCount": 1, "ItemPrice": 200,
				"ItemAmount": 200, "ItemTaxType": 1,
			}},
		}}
		if mode == 2 {
			rows = append(rows, map[string]any{
				"IA_Allow_No": "2026080715227215", "IA_Date": "2026-08-07 15:23:00",
				"IA_Invoice_No": "AB12345678", "IA_Invalid_Status": 0,
				"IA_Total_Tax_Amount": 200,
				"Items": []map[string]any{{
					"ItemName": "退貨折讓", "ItemCount": 1, "ItemPrice": 200,
					"ItemAmount": 200, "ItemTaxType": 1,
				}},
			})
		}
		reply(t, w, map[string]any{"RtnCode": 1, "RtnMsg": "ok", "AllowanceInfo": rows})
	}))
	defer srv.Close()

	g, _ := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	for want := range 3 {
		mode = want
		got, err := g.FetchAllowances(t.Context(), "AB12345678",
			time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("mode %d: %v", want, err)
		}
		if len(got) != want {
			t.Errorf("mode %d returned %d allowances", want, len(got))
		}
	}
}

func TestKnownAllowanceFactsRequireAnExactProviderDocument(t *testing.T) {
	issuedAt := time.Date(2026, 8, 7, 15, 22, 0, 0, time.UTC)
	local := knownAllowance{
		Number: "2026080715227214", AmountCents: 50000,
		Status: "issued", IssuedAt: issuedAt,
		Lines: []Line{{
			Description: "退貨折讓", Quantity: 1,
			UnitPriceCents: 50000, AmountCents: 50000,
		}},
	}
	matching := AllowanceLookup{
		InvoiceNumber: "AB12345678",
		Document: Document{
			Kind: "allowance", Number: local.Number,
			AmountCents: local.AmountCents, IssuedAt: issuedAt,
			Lines: slices.Clone(local.Lines),
		},
	}
	if !allowanceKnownFactsMatch("AB12345678", local, matching) {
		t.Fatal("an exact known provider allowance did not match")
	}

	tests := []struct {
		name   string
		mutate func(*AllowanceLookup)
	}{
		{"original invoice", func(got *AllowanceLookup) { got.InvoiceNumber = "CD12345678" }},
		{"allowance number", func(got *AllowanceLookup) { got.Document.Number = "2026080715227299" }},
		{"amount", func(got *AllowanceLookup) { got.Document.AmountCents-- }},
		{"issued time", func(got *AllowanceLookup) { got.Document.IssuedAt = got.Document.IssuedAt.Add(time.Second) }},
		{"line", func(got *AllowanceLookup) { got.Document.Lines[0].AmountCents-- }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matching
			got.Document.Lines = slices.Clone(matching.Document.Lines)
			tt.mutate(&got)
			if allowanceKnownFactsMatch("AB12345678", local, got) {
				t.Fatal("mismatched provider facts matched a known allowance")
			}
		})
	}
}

func TestKnownAllowanceLinesRejectMissingOrNonTaxableFacts(t *testing.T) {
	if _, ok := knownAllowanceLines(nil, nil, nil, nil, nil); ok {
		t.Fatal("an allowance with no local lines was treated as complete")
	}
	if _, ok := knownAllowanceLines(
		[]string{"退貨折讓"}, []int32{1}, []int64{50000},
		[]int64{50000}, []string{"exempt"},
	); ok {
		t.Fatal("a provider-taxable allowance matched a non-taxable local line")
	}
}

func TestInvalidUnknownAllowanceStillRequiresTheExactFrozenSend(t *testing.T) {
	expected := AllowanceRequest{
		InvoiceNumber: "AB12345678", AmountCents: 50000,
		Lines: []Line{{
			Description: "退貨折讓", Quantity: 1,
			UnitPriceCents: 50000, AmountCents: 50000,
		}},
	}
	matching := AllowanceLookup{
		InvoiceNumber: expected.InvoiceNumber, Invalid: true,
		Document: Document{
			Kind: "allowance", Number: "2026080715227214",
			AmountCents: expected.AmountCents,
			Lines:       slices.Clone(expected.Lines),
		},
	}
	if !allowanceRequestFactsMatch(expected, matching) {
		t.Fatal("the exact invalid provider effect did not match its frozen send")
	}

	tests := []struct {
		name   string
		mutate func(*AllowanceLookup)
	}{
		{"original identity", func(got *AllowanceLookup) { got.InvoiceNumber = "CD12345678" }},
		{"amount", func(got *AllowanceLookup) { got.Document.AmountCents-- }},
		{"ordered lines", func(got *AllowanceLookup) { got.Document.Lines[0].Description = "wrong" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matching
			got.Document.Lines = slices.Clone(matching.Document.Lines)
			tt.mutate(&got)
			if allowanceRequestFactsMatch(expected, got) {
				t.Fatal("contradictory invalid provider effect matched the frozen send")
			}
		})
	}
}

func TestLookupMatchingRejectsEachAuthoritativeFieldIndependently(t *testing.T) {
	t.Parallel()
	request := IssueRequest{
		OrderNumber: "GO260101000001", CustomerName: "王小明", Email: "a@example.com",
		Preference: PreferenceMember, AmountCents: 20000,
		Lines: []Line{{Description: "A", Quantity: 2, UnitPriceCents: 10000, AmountCents: 20000}},
	}
	baseline := IssueLookup{
		RelateNumber: request.OrderNumber, Issued: true,
		Document: Document{AmountCents: request.AmountCents, Lines: slices.Clone(request.Lines)},
	}
	if !issueMatches(request, baseline, false) {
		t.Fatal("baseline Issue lookup does not match")
	}
	tests := map[string]func(*IssueLookup){
		"relate number":  func(v *IssueLookup) { v.RelateNumber += "-other" },
		"issue status":   func(v *IssueLookup) { v.Issued = false },
		"invalid status": func(v *IssueLookup) { v.Invalid = true },
		"header amount":  func(v *IssueLookup) { v.Document.AmountCents++ },
		"description":    func(v *IssueLookup) { v.Document.Lines[0].Description = "B" },
		"quantity":       func(v *IssueLookup) { v.Document.Lines[0].Quantity++ },
		"unit price":     func(v *IssueLookup) { v.Document.Lines[0].UnitPriceCents++ },
		"line amount":    func(v *IssueLookup) { v.Document.Lines[0].AmountCents++ },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			candidate := baseline
			candidate.Document.Lines = slices.Clone(baseline.Document.Lines)
			mutate(&candidate)
			if issueMatches(request, candidate, false) {
				t.Fatal("mutated provider fact was accepted")
			}
		})
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
		OrderNumber: "GO260101000004", CustomerName: "王小明",
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
func reply(t *testing.T, w http.ResponseWriter, res any) {
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
		{name: "the first invoice strips punctuation", attempt: 0, want: "GO260101000001"},
		{name: "a reissue after one void", attempt: 1, want: "GO260101000001R1"},
		{name: "and after two", attempt: 2, want: "GO260101000001R2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := relateNumber("GO-260101-000001", tt.attempt); got != tt.want {
				t.Errorf("relateNumber(%d) = %q, want %q", tt.attempt, got, tt.want)
			}
		})
	}
}

func TestBusinessTaxIDUsesTheCurrentMOFChecksum(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value string
		want  bool
	}{
		{value: "04595257", want: true}, // legacy /10-compatible number
		{value: "04595252", want: true}, // current /5-only example
		{value: "10458574", want: true}, // seventh digit 7, contribution 1
		{value: "10458570", want: true}, // seventh digit 7, contribution 0
		{value: "00000000", want: false}, // checksum passes but MOF forbids all-zero
		{value: "12345678", want: false},
		{value: "1045857x", want: false},
		{value: "0459525", want: false},
	}
	for _, tt := range tests {
		if got := ValidTaxID(tt.value); got != tt.want {
			t.Errorf("ValidTaxID(%q) = %t, want %t", tt.value, got, tt.want)
		}
	}
}

func TestIssueItemsStayWithinProviderFieldBounds(t *testing.T) {
	t.Parallel()
	longName := strings.Repeat("品", 101)
	lines := make([]Line, MaxIssueItems)
	for i := range lines {
		lines[i] = Line{
			Description: longName, Quantity: 1,
			UnitPriceCents: 100, AmountCents: 100,
		}
	}
	got := itemsFor(lines)
	if len(got) != MaxIssueItems || got[len(got)-1].ItemSeq != MaxIssueItems {
		t.Fatalf("Items length/last sequence = %d/%d, want %d/%d",
			len(got), got[len(got)-1].ItemSeq, MaxIssueItems, MaxIssueItems)
	}
	if n := len([]rune(got[0].ItemName)); n != 100 {
		t.Errorf("ItemName length = %d, want 100", n)
	}
}

// TestTheItemisationSumsToWhatWasCharged holds the invariant ECPay refuses a
// document for (5000022 「與商品合計金額不符」): the items are unsigned whole
// dollars summing to the header, and the delivery line carries its own figure
// rather than the residual of total − sum(lines).
//
// Every want below is hand-computed, never taken from the function.
func TestTheItemisationSumsToWhatWasCharged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		lines     []Line
		discount  int64
		shipping  int64
		header    int64 // what the customer was charged, in cents
		wantTotal int64 // the itemisation, in whole DOLLARS
		wantLines int
	}{
		{
			name:      "no discount and no delivery",
			lines:     []Line{{Description: "A", Quantity: 1, UnitPriceCents: 100000, AmountCents: 100000}},
			header:    100000,
			wantTotal: 1000,
			wantLines: 1,
		},
		{
			name:      "delivery is its own line",
			lines:     []Line{{Description: "A", Quantity: 1, UnitPriceCents: 100000, AmountCents: 100000}},
			shipping:  8000,
			header:    108000,
			wantTotal: 1080,
			wantLines: 2,
		},
		{
			name:      "a discount with free delivery",
			lines:     []Line{{Description: "A", Quantity: 1, UnitPriceCents: 100000, AmountCents: 100000}},
			discount:  20000,
			header:    80000,
			wantTotal: 800,
			wantLines: 1,
		},
		{
			name:      "a discount smaller than the delivery fee",
			lines:     []Line{{Description: "A", Quantity: 1, UnitPriceCents: 100000, AmountCents: 100000}},
			discount:  2000,
			shipping:  8000,
			header:    106000,
			wantTotal: 1060,
			wantLines: 2,
		},
		{
			// 15% of NT$999 is NT$149.85, so the order owes 84915 cents and the
			// document is filed at 849.
			name:      "a percentage coupon leaving fractional cents",
			lines:     []Line{{Description: "A", Quantity: 1, UnitPriceCents: 99900, AmountCents: 99900}},
			discount:  14985,
			header:    84915,
			wantTotal: 849,
			wantLines: 1,
		},
		{
			// Two lines and a remainder that has to land somewhere.
			name: "a discount split across lines",
			lines: []Line{
				{Description: "A", Quantity: 1, UnitPriceCents: 33300, AmountCents: 33300},
				{Description: "B", Quantity: 2, UnitPriceCents: 33300, AmountCents: 66600},
			},
			discount:  10000,
			shipping:  6000,
			header:    95900,
			wantTotal: 959,
			wantLines: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := discountLines(slices.Clone(tt.lines), tt.discount)
			if tt.shipping > 0 {
				got = append(got, Line{
					Description: "運費", Quantity: 1,
					UnitPriceCents: tt.shipping, AmountCents: tt.shipping,
				})
			}
			got = snapToDollars(got, tt.header)

			if len(got) < tt.wantLines {
				t.Fatalf("%d lines, want at least %d", len(got), tt.wantLines)
			}
			var dollars int64
			for _, l := range got {
				if l.AmountCents < 0 {
					t.Errorf("line %q is negative (%d); ECPay item amounts are unsigned",
						l.Description, l.AmountCents)
				}
				if l.AmountCents%100 != 0 {
					t.Errorf("line %q is %d cents, which is not a whole dollar — the "+
						"document is filed in dollars", l.Description, l.AmountCents)
				}
				// ECPay files ItemPrice beside ItemCount beside ItemAmount: a line
				// whose price times its count is not its amount is a document
				// contradicting itself.
				price, count := l.UnitPriceCents/100, int64(l.Quantity)
				if count > 0 && price*count != l.AmountCents/100 {
					t.Errorf("line %q files ItemPrice %d x ItemCount %d = %d against "+
						"ItemAmount %d", l.Description, price, count, price*count,
						l.AmountCents/100)
				}
				dollars += l.AmountCents / 100
			}
			if dollars != tt.wantTotal {
				t.Errorf("the itemisation sums to %d, want %d — ECPay refuses a "+
					"document whose items do not add up to its SalesAmount",
					dollars, tt.wantTotal)
			}
			if header := tt.header / 100; dollars != header {
				t.Errorf("the itemisation is %d and the header is %d", dollars, header)
			}
		})
	}
}

// TestTheDeliveryFeeIsNeverDiscounted holds a commercial rule the invoice must
// not quietly break. A coupon is capped at the SUBTOTAL: one that could eat the
// shipping fee would drive the order negative, which is why the discount is
// allocated before the delivery line is appended and never across it.
func TestTheDeliveryFeeIsNeverDiscounted(t *testing.T) {
	t.Parallel()

	items := []Line{{Description: "A", Quantity: 1, UnitPriceCents: 100000, AmountCents: 100000}}
	got := discountLines(slices.Clone(items), 30000)
	got = append(got, Line{Description: "運費", Quantity: 1, UnitPriceCents: 8000, AmountCents: 8000})
	got = snapToDollars(got, 78000)

	for _, l := range got {
		if l.Description == "運費" && l.AmountCents != 8000 {
			t.Errorf("the delivery line is %d, want 8000 — the discount reached the "+
				"carriage the customer actually paid", l.AmountCents)
		}
	}
}

func TestDiscountAllocationDoesNotOverflowAtSchemaLimits(t *testing.T) {
	t.Parallel()

	// One legal order line: unit_price_cents <= 1e10 and quantity <= 999. The
	// discount leaves a legal 1e10-cent capture while the intermediate
	// amount*discount exceeds MaxInt64.
	const gross int64 = 10_000_000_000 * 999
	const discount int64 = gross - 10_000_000_000
	got := discountLines([]Line{{
		Description: "schema-limit", Quantity: 999,
		UnitPriceCents: 10_000_000_000, AmountCents: gross,
	}}, discount)
	if len(got) != 1 || got[0].AmountCents != 10_000_000_000 {
		t.Fatalf("discounted line = %+v, want exactly 10000000000 cents", got)
	}
}

// TestEveryLineMultipliesOut holds ItemPrice × ItemCount = ItemAmount for the
// case the table above cannot reach: every fixture there uses quantity 1 or a
// discount that divides evenly, so the equality holds by construction.
func TestEveryLineMultipliesOut(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lines    []Line
		discount int64
		header   int64
	}{
		{
			name:     "three at NT$333 with a discount that does not divide",
			lines:    []Line{{Description: "A", Quantity: 3, UnitPriceCents: 33300, AmountCents: 99900}},
			discount: 10000,
			header:   89900,
		},
		{
			name: "two lines, both multi-quantity",
			lines: []Line{
				{Description: "A", Quantity: 3, UnitPriceCents: 33300, AmountCents: 99900},
				{Description: "B", Quantity: 7, UnitPriceCents: 14300, AmountCents: 100100},
			},
			discount: 33333,
			header:   166667,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := snapToDollars(discountLines(slices.Clone(tt.lines), tt.discount), tt.header)

			var dollars int64
			for _, l := range got {
				price, count := l.UnitPriceCents/100, int64(l.Quantity)
				if count < 1 {
					t.Errorf("line %q has quantity %d", l.Description, l.Quantity)
					continue
				}
				if price*count != l.AmountCents/100 {
					t.Errorf("line %q: ItemPrice %d x ItemCount %d = %d, ItemAmount %d",
						l.Description, price, count, price*count, l.AmountCents/100)
				}
				if l.AmountCents <= 0 {
					t.Errorf("line %q is %d; ECPay item amounts are unsigned and non-zero",
						l.Description, l.AmountCents)
				}
				dollars += l.AmountCents / 100
			}
			if want := tt.header / 100; dollars != want {
				t.Errorf("the itemisation sums to %d, want %d", dollars, want)
			}
		})
	}

	// And nothing is appended when the arithmetic already comes out even: an
	// adjustment line on a document that needs none is noise on a tax filing.
	even := snapToDollars(
		[]Line{{Description: "A", Quantity: 2, UnitPriceCents: 50000, AmountCents: 100000}}, 100000)
	if len(even) != 1 {
		t.Errorf("%d lines for an evenly-divided invoice, want 1", len(even))
	}
}
