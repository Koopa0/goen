package invoice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/email"
)

var (
	invoiceNumberPattern   = regexp.MustCompile(`^[A-Z]{2}\d{8}$`)
	randomNumberPattern    = regexp.MustCompile(`^\d{4}$`)
	allowanceNumberPattern = regexp.MustCompile(`^\d{16}$`)
	relateNumberPattern    = regexp.MustCompile(`^[A-Za-z0-9]{1,30}$`)
	ecpayTimePattern       = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`)
	errProviderIdentity    = errors.New("invoice: provider success identity mismatch")
)

// Issue files a uniform invoice for one order, against the carrier preference
// the customer chose at checkout. goen never prints, so Print is always '0'.
func (g *Gateway) Issue(ctx context.Context, in IssueRequest) (Document, error) {
	if !g.Enabled() {
		return Document{}, ErrDisabled
	}
	if err := in.validate(); err != nil {
		return Document{}, err
	}

	req := issueRequest{
		MerchantID:   g.merchantID,
		RelateNumber: in.OrderNumber,
		CustomerName: truncate(in.CustomerName, 60),
		// No address: a carrier invoice does not need one, and an invoice record
		// reaches erase_user through no path at all.
		CustomerEmail: truncate(in.Email, 80),
		Print:         "0",
		Donation:      "0",
		TaxType:       "1", // taxable
		SalesAmount:   wholeDollars(in.AmountCents),
		// vat='1' says the ITEM prices already include tax, which a Taiwanese
		// shelf price does. Without it ECPay adds 5% and the invoice disagrees
		// with what the card was charged.
		Vat:      "1",
		InvType:  "07", // general tax
		Items:    itemsFor(in.Lines),
		CarrierT: CarrierNone,
	}

	switch in.Preference {
	case PreferenceCompany:
		// A business-tax-number invoice STILL needs a carrier or a printed copy
		// (ECPay RtnCode 5000028). The number says who it is FOR; the carrier
		// says where it is held.
		req.CustomerIdentifier = in.TaxID
		req.CarrierT = CarrierMember
	case PreferenceMobile:
		req.CarrierT = CarrierMobile
		req.CarrierNum = in.CarrierCode
	case PreferenceMember:
		// Member carrier: ECPay holds it against the customer's email.
		req.CarrierT = CarrierMember
	default:
		panic("invoice: validated unknown preference " + in.Preference)
	}

	res, err := g.call[issueResult](ctx, "/B2CInvoice/Issue", req)
	if err != nil {
		return Document{}, err
	}
	issuedAt, err := parseECPayTime(res.InvoiceDate)
	if err != nil {
		return Document{}, fmt.Errorf(
			"ECPay Issue returned an invalid success payload (invoice %q, date %q, random number %q): %w",
			res.InvoiceNo, res.InvoiceDate, res.RandomNumber, err)
	}
	if !invoiceNumberPattern.MatchString(res.InvoiceNo) ||
		!randomNumberPattern.MatchString(res.RandomNumber) {
		return Document{}, fmt.Errorf(
			"ECPay Issue returned an invalid success payload (invoice %q, date %q, random number %q)",
			res.InvoiceNo, res.InvoiceDate, res.RandomNumber)
	}
	return Document{
		Kind:        "invoice",
		Number:      res.InvoiceNo,
		AmountCents: in.AmountCents,
		Status:      "issued",
		IssuedAt:    issuedAt,
		ProviderRef: res.RandomNumber,
	}, nil
}

// IssueRequest is one order, as an invoice.
type IssueRequest struct {
	OrderNumber  string
	CustomerName string
	Email        string
	// Preference is invoice_preferences.invoice_type.
	Preference  Preference
	CarrierCode string
	TaxID       string
	// AmountCents is the order's total, tax included: what the customer was
	// charged, which is what the invoice records.
	AmountCents int64
	Lines       []Line
}

// validate refuses what ECPay would refuse, in words rather than a code.
func (r IssueRequest) validate() error {
	if !relateNumberPattern.MatchString(r.OrderNumber) {
		return fmt.Errorf("%w: an invoice RelateNumber must be 1-30 alphanumeric characters, got %q",
			ErrRejected, r.OrderNumber)
	}
	if r.AmountCents <= 0 {
		return fmt.Errorf("%w: an invoice for %d cannot be issued", ErrRejected, r.AmountCents)
	}
	if len(r.Lines) == 0 {
		return fmt.Errorf("%w: an invoice with no items says nothing about what was sold", ErrRejected)
	}
	if len(r.Lines) > MaxIssueItems {
		return fmt.Errorf("%w: an invoice has %d items; ECPay accepts at most %d",
			ErrRejected, len(r.Lines), MaxIssueItems)
	}
	if !ValidBuyerName(r.CustomerName) {
		return fmt.Errorf("%w: an invoice needs a buyer name of at most 60 characters",
			ErrRejected)
	}
	if len(r.Email) > 80 || !email.Valid(r.Email) {
		return fmt.Errorf("%w: a carrier invoice needs a bare valid email address of at most 80 bytes",
			ErrRejected)
	}
	if !r.Preference.Known() {
		return fmt.Errorf("%w: %q is not an invoice type this shop offers",
			ErrRejected, r.Preference)
	}
	switch r.Preference {
	case PreferenceCompany:
		if !ValidTaxID(r.TaxID) {
			// i18n-exempt: reached from /admin only, and the back office is the
			// staff of one Taiwanese shop — the category exemption the chrome
			// rule already names.
			return fmt.Errorf("%w: a 公司戶 invoice needs a valid eight-digit 統編, got %q",
				ErrRejected, r.TaxID)
		}
	case PreferenceMobile:
		if !ValidMobileCarrier(r.CarrierCode) {
			// i18n-exempt: back office only, as above.
			return fmt.Errorf("%w: a 手機條碼載具 is a slash and seven characters, got %q",
				ErrRejected, r.CarrierCode)
		}
	case PreferenceMember:
	}
	return nil
}

// Void cancels an issued invoice. A uniform invoice cannot be edited, so a
// wrong one is voided and a correct one issued in its place, and the reason is
// filed with the tax authority rather than left blank.
func (g *Gateway) Void(ctx context.Context, number string, issuedAt time.Time, reason string) error {
	if !g.Enabled() {
		return ErrDisabled
	}
	if number == "" {
		return fmt.Errorf("%w: which invoice?", ErrRejected)
	}
	if issuedAt.IsZero() {
		return fmt.Errorf("%w: which day was it issued?", ErrRejected)
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%w: voiding an invoice needs a reason; it is filed with it", ErrRejected)
	}
	res, err := g.call[invalidResult](ctx, "/B2CInvoice/Invalid", invalidRequest{
		MerchantID: g.merchantID,
		InvoiceNo:  number,
		// Invalid validates this against the invoice's own issue date. A mismatch
		// is 1600003 無發票號碼資料 — indistinguishable from a wrong number. The
		// provider's timestamp is stored as its wall clock labelled UTC, so UTC is
		// load-bearing after a timestamptz round trip in a non-UTC process.
		InvoiceDate: issuedAt.UTC().Format("2006-01-02"),
		Reason:      truncate(reason, 20),
	})
	if err != nil {
		return err
	}
	// A successful Invalid response names the invoice it changed. Settling a
	// different or blank identity would make local tax history disagree with the
	// provider even though the encrypted endpoint call itself succeeded.
	if res.InvoiceNo != number {
		return fmt.Errorf("%w: Invalid returned invoice %q for %q",
			errProviderIdentity, res.InvoiceNo, number)
	}
	return nil
}

// Allowance files a credit note against an issued invoice. A refund does NOT
// void the invoice: the sale happened and the tax was reported, and what
// changed is that some of it came back.
func (g *Gateway) Allowance(ctx context.Context, in AllowanceRequest) (Document, error) {
	if !g.Enabled() {
		return Document{}, ErrDisabled
	}
	if in.InvoiceNumber == "" || in.AmountCents <= 0 || len(in.Lines) == 0 {
		return Document{}, fmt.Errorf("%w: an allowance needs an invoice, an amount and what is being relieved",
			ErrRejected)
	}

	res, err := g.call[allowanceResult](ctx, "/B2CInvoice/Allowance", allowanceRequest{
		MerchantID: g.merchantID,
		InvoiceNo:  in.InvoiceNumber,
		// .UTC() for the reason Invalid's own InvoiceDate carries it: a date
		// shifted by the process's zone returns 1600003, which names the invoice
		// number rather than the date.
		InvoiceDate:     in.InvoiceDate.UTC().Format("2006-01-02"),
		AllowanceNotify: "E", // by email
		CustomerName:    truncate(in.CustomerName, 60),
		NotifyMail:      truncate(in.Email, 80),
		AllowanceAmount: wholeDollars(in.AmountCents),
		Items:           itemsFor(in.Lines),
	})
	if err != nil {
		return Document{}, err
	}
	issuedAt, err := parseECPayTime(res.AllowanceDate)
	if err != nil {
		return Document{}, fmt.Errorf(
			"ECPay Allowance returned an invalid success payload (allowance %q, date %q): %w",
			res.AllowanceNo, res.AllowanceDate, err)
	}
	if !allowanceNumberPattern.MatchString(res.AllowanceNo) ||
		res.InvoiceNo != in.InvoiceNumber {
		return Document{}, fmt.Errorf(
			"ECPay Allowance returned an invalid success payload (allowance %q, invoice %q, date %q)",
			res.AllowanceNo, res.InvoiceNo, res.AllowanceDate)
	}
	return Document{
		Kind:        "allowance",
		Number:      res.AllowanceNo,
		AmountCents: in.AmountCents,
		Status:      "issued",
		IssuedAt:    issuedAt,
		Lines:       in.Lines,
	}, nil
}

// AllowanceRequest is a credit note against one invoice.
type AllowanceRequest struct {
	InvoiceNumber string
	InvoiceDate   time.Time
	CustomerName  string
	Email         string
	AmountCents   int64
	Lines         []Line
}

type issueRequest struct {
	MerchantID         string `json:"MerchantID"`
	RelateNumber       string `json:"RelateNumber"`
	CustomerIdentifier string `json:"CustomerIdentifier,omitempty"`
	CustomerName       string `json:"CustomerName"`
	CustomerEmail      string `json:"CustomerEmail"`
	Print              string `json:"Print"`
	Donation           string `json:"Donation"`
	CarrierT           string `json:"CarrierType"`
	CarrierNum         string `json:"CarrierNum,omitempty"`
	TaxType            string `json:"TaxType"`
	SalesAmount        int64  `json:"SalesAmount"`
	InvType            string `json:"InvType"`
	Vat                string `json:"vat"`
	Items              []item `json:"Items"`
}

type invalidRequest struct {
	MerchantID  string `json:"MerchantID"`
	InvoiceNo   string `json:"InvoiceNo"`
	InvoiceDate string `json:"InvoiceDate"`
	Reason      string `json:"Reason"`
}

type allowanceRequest struct {
	MerchantID      string `json:"MerchantID"`
	InvoiceNo       string `json:"InvoiceNo"`
	InvoiceDate     string `json:"InvoiceDate"`
	AllowanceNotify string `json:"AllowanceNotify"`
	CustomerName    string `json:"CustomerName"`
	NotifyMail      string `json:"NotifyMail"`
	AllowanceAmount int64  `json:"AllowanceAmount"`
	Items           []item `json:"Items"`
}

type invalidResult struct {
	InvoiceNo string `json:"InvoiceNo"`
}

type issueResult struct {
	InvoiceNo    string `json:"InvoiceNo"`
	InvoiceDate  string `json:"InvoiceDate"`
	RandomNumber string `json:"RandomNumber"`
}

type allowanceResult struct {
	AllowanceNo   string `json:"IA_Allow_No"`
	InvoiceNo     string `json:"IA_Invoice_No"`
	AllowanceDate string `json:"IA_Date"`
}

type getIssueRequest struct {
	MerchantID   string `json:"MerchantID"`
	RelateNumber string `json:"RelateNumber"`
}

type getAllowanceListRequest struct {
	MerchantID string `json:"MerchantID"`
	SearchType string `json:"SearchType"`
	InvoiceNo  string `json:"InvoiceNo"`
	Date       string `json:"Date"`
}

// providerDecimal accepts ECPay's inconsistent number-or-decimal-string JSON
// without converting through float64. Money and quantities are accepted only
// when they can later be represented exactly in our integer units.
type providerDecimal string

func (n *providerDecimal) UnmarshalJSON(raw []byte) error {
	var value string
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
	} else {
		value = string(raw)
	}
	if value == "" {
		return errors.New("empty provider number")
	}
	if _, ok := new(big.Rat).SetString(value); !ok {
		return fmt.Errorf("invalid provider number %q", value)
	}
	*n = providerDecimal(value)
	return nil
}

func (n providerDecimal) integer() (int64, error) {
	r, ok := new(big.Rat).SetString(string(n))
	if !ok || !r.IsInt() || !r.Num().IsInt64() {
		return 0, fmt.Errorf("provider value %q is not an exact integer", n)
	}
	return r.Num().Int64(), nil
}

func (n providerDecimal) cents() (int64, error) {
	r, ok := new(big.Rat).SetString(string(n))
	if !ok {
		return 0, fmt.Errorf("provider value %q is not a number", n)
	}
	r.Mul(r, big.NewRat(100, 1))
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, fmt.Errorf("provider money %q is not exact cents", n)
	}
	return r.Num().Int64(), nil
}

type lookupItem struct {
	Name    string          `json:"ItemName"`
	Count   providerDecimal `json:"ItemCount"`
	Price   providerDecimal `json:"ItemPrice"`
	Amount  providerDecimal `json:"ItemAmount"`
	TaxType providerDecimal `json:"ItemTaxType"`
}

func (i lookupItem) line() (Line, error) {
	count, err := i.Count.integer()
	if err != nil || count <= 0 || count > math.MaxInt32 {
		return Line{}, fmt.Errorf("invalid provider item count %q", i.Count)
	}
	price, err := i.Price.cents()
	if err != nil || price < 0 {
		return Line{}, fmt.Errorf("invalid provider item price %q", i.Price)
	}
	amount, err := i.Amount.cents()
	if err != nil || amount < 0 {
		return Line{}, fmt.Errorf("invalid provider item amount %q", i.Amount)
	}
	tax, err := i.TaxType.integer()
	if err != nil || tax != 1 {
		return Line{}, fmt.Errorf("provider item has unsupported tax type %q", i.TaxType)
	}
	if price > math.MaxInt64/count || price*count != amount {
		return Line{}, errors.New("provider item arithmetic is inconsistent")
	}
	return Line{
		Description: i.Name, Quantity: int32(count),
		UnitPriceCents: price, AmountCents: amount,
	}, nil
}

type getIssueResult struct {
	InvoiceNo     string          `json:"IIS_Number"`
	RelateNumber  string          `json:"IIS_Relate_Number"`
	SalesAmount   providerDecimal `json:"IIS_Sales_Amount"`
	CreatedAt     string          `json:"IIS_Create_Date"`
	IssueStatus   providerDecimal `json:"IIS_Issue_Status"`
	InvalidStatus providerDecimal `json:"IIS_Invalid_Status"`
	// IIS_Check_Number is retired and ECPay says to ignore it. Void needs the
	// distinct four-digit random number returned here.
	RandomNumber string       `json:"IIS_Random_Number"`
	Items        []lookupItem `json:"Items"`
}

// IssueLookup is the complete provider truth used to settle or validate one
// frozen Issue operation.
type IssueLookup struct {
	RelateNumber string
	Document     Document
	Issued       bool
	Invalid      bool
}

// FetchIssue looks up an invoice by ECPay's idempotent RelateNumber, through
// their GetIssue operation. RtnCode 2 is the documented authoritative not-found
// verdict and is the only outcome that permits sending the same frozen Issue
// request.
//
// Go names are verbs here, as Void is for their Invalid; ECPay's operation
// names survive where they identify the endpoint — paths, wire structs, errors.
func (g *Gateway) FetchIssue(ctx context.Context, relateNumber string) (IssueLookup, bool, error) {
	if !relateNumberPattern.MatchString(relateNumber) {
		return IssueLookup{}, false, fmt.Errorf("%w: invalid ECPay RelateNumber %q",
			ErrRejected, relateNumber)
	}
	res, err := g.call[getIssueResult](ctx, "/B2CInvoice/GetIssue", getIssueRequest{
		MerchantID: g.merchantID, RelateNumber: relateNumber,
	})
	if err != nil {
		if providerErr, ok := errors.AsType[*providerError](err); ok && providerErr.Code == 2 {
			return IssueLookup{}, false, nil
		}
		return IssueLookup{}, false, err
	}
	lookup, err := issueLookupFrom(res)
	if err != nil {
		return IssueLookup{}, false, err
	}
	return lookup, true, nil
}

func issueLookupFrom(res getIssueResult) (IssueLookup, error) {
	issuedAt, err := parseECPayTime(res.CreatedAt)
	if err != nil || !invoiceNumberPattern.MatchString(res.InvoiceNo) ||
		!randomNumberPattern.MatchString(res.RandomNumber) ||
		!relateNumberPattern.MatchString(res.RelateNumber) || len(res.Items) > MaxIssueItems {
		return IssueLookup{}, errors.New("ECPay GetIssue returned malformed identity")
	}
	amount, err := res.SalesAmount.cents()
	if err != nil || amount <= 0 {
		return IssueLookup{}, errors.New("ECPay GetIssue returned malformed amount")
	}
	issued, err := providerFlag(res.IssueStatus, "issue")
	if err != nil {
		return IssueLookup{}, err
	}
	invalid, err := providerFlag(res.InvalidStatus, "invalid")
	if err != nil {
		return IssueLookup{}, err
	}
	lines := make([]Line, len(res.Items))
	for i := range res.Items {
		lines[i], err = res.Items[i].line()
		if err != nil {
			return IssueLookup{}, fmt.Errorf("ECPay GetIssue item %d: %w", i+1, err)
		}
	}
	return IssueLookup{
		RelateNumber: res.RelateNumber,
		Issued:       issued, Invalid: invalid,
		Document: Document{
			Kind: "invoice", Number: res.InvoiceNo, AmountCents: amount,
			Status: "issued", IssuedAt: issuedAt,
			ProviderRef: res.RandomNumber, Lines: lines,
		},
	}, nil
}

func providerFlag(value providerDecimal, name string) (bool, error) {
	flag, err := value.integer()
	if err != nil || (flag != 0 && flag != 1) {
		return false, fmt.Errorf("ECPay GetIssue returned malformed %s status", name)
	}
	return flag == 1, nil
}

type allowanceLookupResult struct {
	AllowanceNo string          `json:"IA_Allow_No"`
	AllowanceAt string          `json:"IA_Date"`
	InvoiceNo   string          `json:"IA_Invoice_No"`
	Invalid     providerDecimal `json:"IA_Invalid_Status"`
	Total       providerDecimal `json:"IA_Total_Tax_Amount"`
	Items       []lookupItem    `json:"Items"`
}

type getAllowanceListResult struct {
	Allowances []allowanceLookupResult `json:"AllowanceInfo"`
}

// AllowanceLookup is one provider-side credit note returned by the original
// invoice/date query.
type AllowanceLookup struct {
	InvoiceNumber string
	Document      Document
	Invalid       bool
}

// FetchAllowances returns the provider's complete allowance list, through their
// GetAllowanceList operation. RtnCode 7 is the documented authoritative empty
// list. Callers must exclude locally known numbers before considering the one
// unresolved operation.
func (g *Gateway) FetchAllowances(
	ctx context.Context, invoiceNumber string, issuedAt time.Time,
) ([]AllowanceLookup, error) {
	res, err := g.call[getAllowanceListResult](ctx, "/B2CInvoice/GetAllowanceList",
		getAllowanceListRequest{
			MerchantID: g.merchantID, SearchType: "1", InvoiceNo: invoiceNumber,
			Date: issuedAt.UTC().Format("2006-01-02"),
		})
	if err != nil {
		if providerErr, ok := errors.AsType[*providerError](err); ok && providerErr.Code == 7 {
			return []AllowanceLookup{}, nil
		}
		return nil, err
	}
	out := make([]AllowanceLookup, len(res.Allowances))
	for i := range res.Allowances {
		row := &res.Allowances[i]
		issuedAt, parseErr := parseECPayTime(row.AllowanceAt)
		if parseErr != nil || !allowanceNumberPattern.MatchString(row.AllowanceNo) ||
			!invoiceNumberPattern.MatchString(row.InvoiceNo) {
			return nil, fmt.Errorf("ECPay GetAllowanceList returned malformed identity at %d", i+1)
		}
		invalid, parseErr := row.Invalid.integer()
		if parseErr != nil || (invalid != 0 && invalid != 1) {
			return nil, fmt.Errorf("ECPay GetAllowanceList returned malformed status at %d", i+1)
		}
		amount, parseErr := row.Total.cents()
		if parseErr != nil || amount <= 0 {
			return nil, fmt.Errorf("ECPay GetAllowanceList returned malformed amount at %d", i+1)
		}
		lines := make([]Line, len(row.Items))
		for lineAt := range row.Items {
			lines[lineAt], parseErr = row.Items[lineAt].line()
			if parseErr != nil {
				return nil, fmt.Errorf("ECPay allowance %d item %d: %w", i+1, lineAt+1, parseErr)
			}
		}
		out[i] = AllowanceLookup{
			InvoiceNumber: row.InvoiceNo, Invalid: invalid == 1,
			Document: Document{
				Kind: "allowance", Number: row.AllowanceNo,
				AmountCents: amount, Status: "issued", IssuedAt: issuedAt, Lines: lines,
			},
		}
	}
	return out, nil
}

// parseECPayTime reads the exact provider timestamp. A malformed success reply
// is an ambiguous remote success and must not be replaced with the process
// clock: doing so makes a later Invalid request name the wrong issue date.
func parseECPayTime(s string) (time.Time, error) {
	if !ecpayTimePattern.MatchString(s) {
		return time.Time{}, errors.New("invalid ECPay timestamp format")
	}
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse ECPay timestamp: %w", err)
	}
	return t, nil
}
