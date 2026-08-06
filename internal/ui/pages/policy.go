package pages

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

// PolicySection is one heading and its paragraphs.
type PolicySection struct {
	Heading string
	Body    []string
	// Pending marks a section describing what has NOT been decided. It renders
	// differently on purpose: a reader must be able to tell a rule from a gap,
	// and a shop that hides its gaps in the same typeface as its promises is
	// making a promise by accident.
	Pending bool
}

// PolicyDoc is a static policy page.
type PolicyDoc struct {
	Title    string
	Summary  string
	Sections []PolicySection
}

// FAQItem is one question.
type FAQItem struct {
	Question string
	Answer   string
}

// FAQGroup is the questions under one heading.
type FAQGroup struct {
	Category string
	Items    []FAQItem
}

// FAQView is the whole FAQ.
type FAQView struct {
	Groups []FAQGroup
}

// Empty reports whether there is nothing to show.
func (v FAQView) Empty() bool { return len(v.Groups) == 0 }

// ShippingMethod is one delivery option, as the policy page states it.
type ShippingMethod struct {
	Name          string
	Carrier       string
	FeeCents      int64
	FreeOverCents int64
	// Surcharges is where this method costs extra, as DATA. The query used to
	// build the sentence — string_agg with ' 另加 NT$' in the middle — which made
	// it Chinese for every reader and put it somewhere no i18n test looks.
	Surcharges []ZoneSurcharge
}

// ZoneSurcharge is one place that costs more to reach, and how much more.
//
// The NAME is the shop's own word for that zone and stays as typed; the sentence
// around it is chrome and follows the visitor.
type ZoneSurcharge struct {
	Name  string
	Cents int64
}

// HasSurcharges reports whether anywhere costs extra to reach by this method.
func (m ShippingMethod) HasSurcharges() bool { return len(m.Surcharges) > 0 }

// SurchargeText is the surcharges as one phrase in the visitor's language.
//
// It takes a ctx because it is a sentence built from a message plus data, which
// is the pattern CLAUDE.md prescribes for exactly this: the words have to render
// to be assembled, so they cannot be decided by a pure mapping — and they
// certainly cannot be decided in SQL.
func (m ShippingMethod) SurchargeText(ctx context.Context) string {
	parts := make([]string, 0, len(m.Surcharges))
	for _, z := range m.Surcharges {
		parts = append(parts,
			fmt.Sprintf(i18n.T(ctx, i18n.KeyShippingZoneSurcharge), z.Name, twd(z.Cents)))
	}
	return strings.Join(parts, i18n.T(ctx, i18n.KeyListSeparator))
}

// Fee is what it costs.
func (m ShippingMethod) Fee() string { return twd(m.FeeCents) }

// FreeOver is the threshold above which it costs nothing, or empty when there
// is none.
func (m ShippingMethod) FreeOver() string {
	if m.FreeOverCents <= 0 {
		return ""
	}
	return twd(m.FreeOverCents)
}

// ShippingView is the delivery policy.
type ShippingView struct {
	Methods []ShippingMethod
}

// Empty reports whether no method is configured.
func (v ShippingView) Empty() bool { return len(v.Methods) == 0 }

// HoldMinutes is how long checkout reserves stock, stated on the page so the
// number a customer reads is the one the code enforces.
//
// It mirrors cart.HoldTTL. Not imported, because internal/ui must not depend on
// a feature package — but it is one number, and if it drifts the shipping page
// says something the till does not do.
//
// It is 60 because cart.HoldTTL is now PayWindow + StripeSessionFloor rather
// than a flat thirty minutes: the two were equal, which made the hold exactly
// Stripe's session floor and left no session goen could ever open. What a
// customer reads here is the whole reservation, not the half of it they have to
// start paying inside.
const HoldMinutes = 60

// HoldMinutesText is that number, for the template.
func HoldMinutesText() string { return strconv.Itoa(HoldMinutes) }

// AdminFAQEntry is one FAQ row as the back office lists it.
type AdminFAQEntry struct {
	ID       string
	Category string
	Question string
	Answer   string
	// The English entry, empty for what nobody has translated.
	CategoryEn string
	QuestionEn string
	AnswerEn   string
	UpdatedAt  string
}

// Translated reports whether this entry reads in English.
//
// The ANSWER decides. A translated question above a Chinese answer is worse than an
// untranslated pair: it invites a reader in and then does not answer them.
func (e AdminFAQEntry) Translated() bool { return e.AnswerEn != "" }

// AdminFAQView is the FAQ management page.
type AdminFAQView struct {
	Rows   []AdminFAQEntry
	Notice string
	Errors map[string]string
	Draft  AdminFAQEntry
}

// Empty reports whether the shop has published no FAQ at all.
func (v *AdminFAQView) Empty() bool { return len(v.Rows) == 0 }

// HasNotice reports whether to show the banner.
func (v *AdminFAQView) HasNotice() bool { return v.Notice != "" }

// HasErr reports whether a field was refused.
func (v *AdminFAQView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

// Err is why.
func (v *AdminFAQView) Err(f string) string { return v.Errors[f] }

// Untranslated is how many entries an English visitor reads in Chinese. Said as a
// number because /faq groups by category and the gaps are easy to lose in the list.
func (v *AdminFAQView) Untranslated() int {
	n := 0
	for i := range v.Rows {
		if !v.Rows[i].Translated() {
			n++
		}
	}
	return n
}

// untranslatedText is the count as text, for the page's warning line.
func untranslatedText(v *AdminFAQView) string { return strconv.Itoa(v.Untranslated()) }
