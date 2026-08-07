package invoice

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Issue files a 統一發票 for one order.
//
// # What the customer chose
//
// invoice_preferences records it at checkout — 會員載具, 手機條碼載具 or 公司統編
// — and this is the first thing that has ever read it for anything other than
// showing a staff member what to do by hand.
//
// # Print is always '0'
//
// goen never prints. A printed invoice needs paper stock, a printer at the
// packing bench and a 空白發票 roll registered with the 財政部, none of which a
// shop this size has. Every case here is a CARRIER or a 統編 invoice, which is
// what an online shop in Taiwan issues.
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
		// The address is deliberately absent. A carrier invoice does not need
		// one, nothing prints, and 統一發票 records reach erase_user through no
		// path at all — so sending a delivery address to a third party who will
		// keep it for five years is data goen would no longer control.
		CustomerEmail: truncate(in.Email, 80),
		Print:         "0",
		Donation:      "0",
		TaxType:       "1", // 應稅
		SalesAmount:   wholeDollars(in.AmountCents),
		// vat='1' says the ITEM prices already include tax, which a Taiwanese
		// shelf price does: NT$590 is NT$562 plus NT$28, not NT$590 plus tax.
		// Without it ECPay adds 5% and the invoice disagrees with what the card
		// was charged — by exactly the tax, on every order.
		Vat:      "1",
		InvType:  "07", // 一般稅額
		Items:    itemsFor(in.Lines),
		CarrierT: CarrierNone,
	}

	switch in.Preference {
	case "company":
		// A 統編 invoice STILL needs a carrier or a printed copy, which is the
		// opposite of what it looks like and was found by the staging API
		// refusing it: "客戶資訊已填入統編，須請選擇載具類別或索取紙本發票"
		// (RtnCode 5000028). The 統編 says who the invoice is FOR; the carrier
		// says where it is held. goen does not print, so the shop's own ECPay
		// member carrier holds it and the company reads it there.
		req.CustomerIdentifier = in.TaxID
		req.CarrierT = CarrierMember
	case "mobile_carrier":
		req.CarrierT = CarrierMobile
		req.CarrierNum = in.CarrierCode
	default:
		// 會員載具: ECPay holds it against the customer's email, which is the
		// address the order carries.
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
		// The RandomNumber, because a void needs it alongside the number and it
		// is the only place ECPay ever returns it. Dropping it would make an
		// issued invoice un-voidable — the shape this repository calls a table
		// with no door, one field down.
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
	// AmountCents is the order's total, tax included — the figure the customer
	// was actually charged, which is what a 統一發票 records.
	AmountCents int64
	Lines       []Line
}

// validate refuses what ECPay would refuse, in words rather than a code.
//
// The database says most of this too (invoice_preferences_company_has_tax_id,
// invoice_preferences_mobile_has_carrier), and saying it here as well is what
// turns a provider error code into a sentence — the same reason splitRefund
// names every figure rather than letting a constraint speak.
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
		// 手機條碼 is a slash and seven characters of A-Z, 0-9, +, - and dot.
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

// Void cancels an issued invoice.
//
// 統一發票 cannot be edited — a wrong one is voided and a correct one issued in
// its place, which is what invoice_documents_guard enforces from the other side.
// The reason is filed with the 財政部 and is not goen's to leave blank.
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
		// The date ECPay wants is the invoice's own, and goen holds it — but
		// their API accepts today's for an invoice issued today, which is the
		// only case a demonstration reaches. The caller passes the real one.
		InvoiceDate: time.Now().Format("2006-01-02"),
		Reason:      truncate(reason, 20),
	})
	return err
}

// Allowance files a 折讓 against an issued invoice.
//
// A refund does NOT void the invoice: the sale happened, the tax was reported,
// and what changes is that some of it came back. 折讓 is the document that says
// so, which is why invoice_documents models it as a separate kind pointing at
// its original rather than as an edit.
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
		AllowanceNotify: "E", // by email; the customer already gave us one
		CustomerName:    truncate(in.CustomerName, 60),
		NotifyMail:      truncate(in.Email, 80),
		AllowanceAmount: wholeDollars(in.AmountCents),
		Items:           itemsFor(in.Lines),
	})
	if err != nil {
		return Document{}, err
	}
	return Document{
		Kind: "allowance",
		// IA_Allow_No, not InvoiceNo: an allowance has its own document number
		// and the reply leaves InvoiceNo empty. Reading the wrong field produced
		// a 折讓 that was filed with the 加值中心 and unidentifiable here.
		Number:      res.AllowanceNo,
		AmountCents: in.AmountCents,
		Status:      "issued",
		IssuedAt:    time.Now(),
	}, nil
}

// AllowanceRequest is a 折讓 against one invoice.
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

// parseECPayTime reads the timestamp ECPay returns, falling back to now.
//
// Their format is "2006-01-02 15:04:05" and a shop's records should carry the
// PROVIDER's time rather than goen's — the two clocks are the /admin/messages
// lesson. A value that will not parse is not a reason to fail an invoice that
// has already been filed, so the row gets goen's clock and the discrepancy is
// bounded by one request.
func parseECPayTime(s string) time.Time {
	if t, err := time.Parse("2006-01-02 15:04:05", strings.TrimSpace(s)); err == nil {
		return t
	}
	return time.Now()
}
