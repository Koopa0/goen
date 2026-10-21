package policy

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

type Section struct {
	Heading   string
	Body      []string
	HeadingEn string
	BodyEn    []string
	Pending   bool
	// Windows is a catalogue message that states the checkout windows; For
	// formats it after Body, with the windows the database states.
	Windows i18n.Key
}

type Doc struct {
	Title     string
	TitleEn   string
	Summary   string
	SummaryEn string
	Sections  []Section
}

// For is the document in one language, with every Windows message formatted from w.
func (d Doc) For(l i18n.Locale, w CheckoutWindows) Doc {
	out := Doc{Title: d.Title, Summary: d.Summary}
	if l == i18n.En {
		out.Title, out.Summary = d.TitleEn, d.SummaryEn
	}
	ctx := i18n.WithLocale(context.Background(), l)
	for _, s := range d.Sections {
		section := Section{Heading: s.Heading, Body: s.Body, Pending: s.Pending}
		if l == i18n.En {
			section.Heading, section.Body = s.HeadingEn, s.BodyEn
		}
		if s.Windows != "" {
			section.Body = append(slices.Clip(section.Body), w.State(ctx, s.Windows))
		}
		out.Sections = append(out.Sections, section)
	}
	return out
}

// statesWindows reports whether any section states the checkout windows, the
// one thing For needs the database for.
func (d Doc) statesWindows() bool {
	return slices.ContainsFunc(d.Sections, func(s Section) bool { return s.Windows != "" })
}

// CheckoutWindows is how long placing an order holds the stock and how long the
// customer then has to start paying, as the database states them.
type CheckoutWindows struct {
	HoldMinutes     int64
	PayStartMinutes int64
}

// State formats a message that takes the hold as its first argument and the
// start window as its second.
func (w CheckoutWindows) State(ctx context.Context, k i18n.Key) string {
	return fmt.Sprintf(i18n.T(ctx, k),
		strconv.FormatInt(w.HoldMinutes, 10), strconv.FormatInt(w.PayStartMinutes, 10))
}

// SectionID names a section by position, since a policy section has no identifier of its own (the documents are prose in
// internal/policy); the contents list uses the same numbers.
func (d Doc) SectionID(i int) string {
	return "doc-section-" + strconv.Itoa(i+1)
}

func (d Doc) SectionHref(i int) string { return "#" + d.SectionID(i) }

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
	Windows CheckoutWindows
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

// checkout.DraftTTL.
const checkoutDraftHours = 1

// backinstock.NotifiedRetain.
const stockAlertRetainDays = 30

// outbox.Retain, for delivered and for undelivered mail alike.
const mailRetainDays = 30

// auth.SessionTTL.
const sessionDays = 14

// auth.DeviceTTL.
const knownDeviceDays = 90

// auth.DevicesPerAccount.
const knownDevicesPerAccount = 10

func NewsletterConfirmHoursText() string { return strconv.Itoa(newsletterConfirmHours) }
func NewsletterRetainDaysText() string   { return strconv.Itoa(newsletterRetainDays) }
func GuestCartRetainDaysText() string    { return strconv.Itoa(guestCartRetainDays) }
func StockAlertRetainDaysText() string   { return strconv.Itoa(stockAlertRetainDays) }
func CheckoutDraftHoursText() string     { return strconv.Itoa(checkoutDraftHours) }
func MailRetainDaysText() string         { return strconv.Itoa(mailRetainDays) }
func SessionDaysText() string            { return strconv.Itoa(sessionDays) }
func KnownDeviceDaysText() string        { return strconv.Itoa(knownDeviceDays) }
func KnownDevicesPerAccountText() string { return strconv.Itoa(knownDevicesPerAccount) }
