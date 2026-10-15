package site

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

type PolicySection struct {
	Heading   string
	Body      []string
	HeadingEn string
	BodyEn    []string
	Pending   bool
}

type PolicyDoc struct {
	Title     string
	TitleEn   string
	Summary   string
	SummaryEn string
	Sections  []PolicySection
}

func (d PolicyDoc) For(l i18n.Locale) PolicyDoc {
	if l != i18n.En {
		return d
	}
	out := PolicyDoc{Title: d.TitleEn, Summary: d.SummaryEn}
	for _, s := range d.Sections {
		out.Sections = append(out.Sections, PolicySection{
			Heading: s.HeadingEn,
			Body:    s.BodyEn,
			Pending: s.Pending,
		})
	}
	return out
}

type FAQItem struct {
	Question string
	Answer   string
}

// SectionID names a section by position, since a policy section has no identifier of its own (the documents are prose in
// internal/site); the contents list uses the same numbers.
func (d PolicyDoc) SectionID(i int) string {
	return "doc-section-" + strconv.Itoa(i+1)
}

func (d PolicyDoc) SectionHref(i int) string { return "#" + d.SectionID(i) }

// faqGroupID names a category the same way, for the same reason.
func faqGroupID(i int) string { return "faq-group-" + strconv.Itoa(i+1) }

func faqGroupHref(i int) string { return "#" + faqGroupID(i) }

type FAQGroup struct {
	Category string
	Items    []FAQItem
}

type FAQView struct {
	Groups []FAQGroup
}

func (v FAQView) Empty() bool { return len(v.Groups) == 0 }

type ShippingMethod struct {
	Name          string
	Carrier       string
	FeeCents      int64
	FreeOverCents int64
	Surcharges    []ZoneSurcharge
}

type ZoneSurcharge struct {
	Name  string
	Cents int64
}

func (m ShippingMethod) HasSurcharges() bool { return len(m.Surcharges) > 0 }

func (m ShippingMethod) SurchargeText(ctx context.Context) string {
	parts := make([]string, 0, len(m.Surcharges))
	for _, z := range m.Surcharges {
		parts = append(parts,
			fmt.Sprintf(i18n.T(ctx, i18n.KeyShippingZoneSurcharge), z.Name, twd(z.Cents)))
	}
	return strings.Join(parts, i18n.T(ctx, i18n.KeyListSeparator))
}

func (m ShippingMethod) Fee() string { return twd(m.FeeCents) }

func (m ShippingMethod) FreeOver() string {
	if m.FreeOverCents <= 0 {
		return ""
	}
	return twd(m.FreeOverCents)
}

type ShippingView struct {
	Methods []ShippingMethod
}

func (v ShippingView) Empty() bool { return len(v.Methods) == 0 }

// The privacy policy states how long each kind of personal data is kept. Each
// number below is the one a sweep enforces in another package, which this one
// cannot import; a test in that package binds it, named beside the constant.

// newsletter.ConfirmTTL.
const newsletterConfirmHours = 48

// newsletter.ConfirmationRetain.
const newsletterRetainDays = 14

// cart.GuestCartRetain.
const guestCartRetainDays = 30

// cart.DraftTTL.
const checkoutDraftHours = 1

// restock.NotifiedRetain.
const stockAlertRetainDays = 30

// outbox.Retain, for delivered and for undelivered mail alike.
const mailRetainDays = 30

// account.SessionTTL.
const sessionDays = 14

func NewsletterConfirmHoursText() string { return strconv.Itoa(newsletterConfirmHours) }
func NewsletterRetainDaysText() string   { return strconv.Itoa(newsletterRetainDays) }
func GuestCartRetainDaysText() string    { return strconv.Itoa(guestCartRetainDays) }
func StockAlertRetainDaysText() string   { return strconv.Itoa(stockAlertRetainDays) }
func CheckoutDraftHoursText() string     { return strconv.Itoa(checkoutDraftHours) }
func MailRetainDaysText() string         { return strconv.Itoa(mailRetainDays) }
func SessionDaysText() string            { return strconv.Itoa(sessionDays) }
