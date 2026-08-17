package invoice

import (
	"context"
	"fmt"
	"strings"
	"time"
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
	case "company":
		// A business-tax-number invoice STILL needs a carrier or a printed copy
		// (ECPay RtnCode 5000028). The number says who it is FOR; the carrier
		// says where it is held.
		req.CustomerIdentifier = in.TaxID
		req.CarrierT = CarrierMember
	case "mobile_carrier":
		req.CarrierT = CarrierMobile
		req.CarrierNum = in.CarrierCode
	default:
		// Member carrier: ECPay holds it against the customer's email.
		req.CarrierT = CarrierMember
	}

	res, err := g.call(ctx, "/B2CInvoice/Issue", req)
	if err != nil {
		return Document{}, err
	}
	return Document{
		Kind:        "invoice",
		Number:      res.InvoiceNo,
		AmountCents: in.AmountCents,
		Status:      "issued",
		IssuedAt:    parseECPayTime(res.InvoiceDate),
		ProviderRef: res.RandomNumber,
	}, nil
}

// IssueRequest is one order, as an invoice.
type IssueRequest struct {
	OrderNumber  string
	CustomerName string
	Email        string
	// Preference is invoice_preferences.invoice_type.
	Preference  string
	CarrierCode string
	TaxID       string
	// AmountCents is the order's total, tax included: what the customer was
	// charged, which is what the invoice records.
	AmountCents int64
	Lines       []Line
}

// validate refuses what ECPay would refuse, in words rather than a code.
func (r IssueRequest) validate() error {
	if r.OrderNumber == "" {
		return fmt.Errorf("%w: an invoice needs the order it is for", ErrRejected)
	}
	if r.AmountCents <= 0 {
		return fmt.Errorf("%w: an invoice for %d cannot be issued", ErrRejected, r.AmountCents)
	}
	if len(r.Lines) == 0 {
		return fmt.Errorf("%w: an invoice with no items says nothing about what was sold", ErrRejected)
	}
	if r.Email == "" {
		return fmt.Errorf("%w: a carrier invoice is held against an email address", ErrRejected)
	}
	switch r.Preference {
	case "company":
		if len(r.TaxID) != 8 {
			// i18n-exempt: reached from /admin only, and the back office is the
			// staff of one Taiwanese shop — the category exemption the chrome
			// rule already names.
			return fmt.Errorf("%w: a 公司戶 invoice needs an eight-digit 統編, got %q",
				ErrRejected, r.TaxID)
		}
	case "mobile_carrier":
		// A mobile barcode is a slash and seven of A-Z, 0-9, +, - and dot.
		if len(r.CarrierCode) != 8 || !strings.HasPrefix(r.CarrierCode, "/") {
			// i18n-exempt: back office only, as above.
			return fmt.Errorf("%w: a 手機條碼載具 is a slash and seven characters, got %q",
				ErrRejected, r.CarrierCode)
		}
	case "member_carrier":
	default:
		return fmt.Errorf("%w: %q is not an invoice type this shop offers",
			ErrRejected, r.Preference)
	}
	return nil
}

// Void cancels an issued invoice. A uniform invoice cannot be edited, so a
// wrong one is voided and a correct one issued in its place, and the reason is
// filed with the tax authority rather than left blank.
func (g *Gateway) Void(ctx context.Context, number, reason string) error {
	if !g.Enabled() {
		return ErrDisabled
	}
	if number == "" {
		return fmt.Errorf("%w: which invoice?", ErrRejected)
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%w: voiding an invoice needs a reason; it is filed with it", ErrRejected)
	}
	_, err := g.call(ctx, "/B2CInvoice/Invalid", invalidRequest{
		MerchantID: g.merchantID,
		InvoiceNo:  number,
		// ECPay want the invoice's own date and accept today's for one issued
		// today, which is the only case this reaches.
		InvoiceDate: time.Now().Format("2006-01-02"),
		Reason:      truncate(reason, 20),
	})
	return err
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

	res, err := g.call(ctx, "/B2CInvoice/Allowance", allowanceRequest{
		MerchantID:      g.merchantID,
		InvoiceNo:       in.InvoiceNumber,
		InvoiceDate:     in.InvoiceDate.Format("2006-01-02"),
		AllowanceNotify: "E", // by email
		CustomerName:    truncate(in.CustomerName, 60),
		NotifyMail:      truncate(in.Email, 80),
		AllowanceAmount: wholeDollars(in.AmountCents),
		Items:           itemsFor(in.Lines),
	})
	if err != nil {
		return Document{}, err
	}
	return Document{
		Kind:        "allowance",
		Number:      res.AllowanceNo,
		AmountCents: in.AmountCents,
		Status:      "issued",
		IssuedAt:    time.Now(),
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

// parseECPayTime reads the timestamp ECPay returns, falling back to goen's
// clock: a value that will not parse is no reason to fail an invoice that has
// already been filed.
func parseECPayTime(s string) time.Time {
	if t, err := time.Parse("2006-01-02 15:04:05", strings.TrimSpace(s)); err == nil {
		return t
	}
	return time.Now()
}
