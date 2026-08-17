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
	// HeadingEn and BodyEn are the English the same section renders in.
	HeadingEn string
	BodyEn    []string
	// Pending marks a section describing what has not been decided; it renders
	// as a visible gap so a reader can tell a rule from one.
	Pending bool
}

// PolicyDoc is a static policy page.
type PolicyDoc struct {
	Title     string
	TitleEn   string
	Summary   string
	SummaryEn string
	Sections  []PolicySection
}

// For resolves the document into one locale's strings. Consumer Protection Act
// §18 I 3 makes stating the rescission information the trader's obligation, and
// a page stating it in a language the customer cannot read discharges nothing.
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
	// Surcharges is where this method costs extra, as data: the query hands over
	// rows and never the sentence, which is chrome and follows the visitor.
	Surcharges []ZoneSurcharge
}

// ZoneSurcharge is one place that costs more to reach, and how much more.
type ZoneSurcharge struct {
	Name  string
	Cents int64
}

// HasSurcharges reports whether anywhere costs extra to reach by this method.
func (m ShippingMethod) HasSurcharges() bool { return len(m.Surcharges) > 0 }

// SurchargeText is the surcharges as one phrase in the visitor's language.
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

// HoldMinutes is how long checkout reserves stock. It mirrors cart.HoldTTL,
// which internal/ui may not import, so the page and the till share one number
// only as long as this one is kept equal to it.
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

// Translated reports whether this entry reads in English. The answer decides: a
// translated question above a Chinese answer invites a reader in and then does
// not answer them.
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

// Untranslated is how many entries an English visitor reads in Chinese.
func (v *AdminFAQView) Untranslated() int {
	n := 0
	for i := range v.Rows {
		if !v.Rows[i].Translated() {
			n++
		}
	}
	return n
}

func untranslatedText(v *AdminFAQView) string { return strconv.Itoa(v.Untranslated()) }
