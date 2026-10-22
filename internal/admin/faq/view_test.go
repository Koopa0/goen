package faq

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/uitest"
)

// From copy_test.go: TestEveryExampleHintReadsInFull, which keeps the product half.
func TestEveryFAQExampleHintReadsInFull(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale   i18n.Locale
		sentence func(example string) string
	}{
		{i18n.ZhHant, func(e string) string { return "<p class=\"goen-admin__hint\">例如 " + e + "。</p>" }},
		{i18n.En, func(e string) string { return "<p class=\"goen-admin__hint\">For example, " + e + ".</p>" }},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			body := renderComponent(t, ctx, FAQ(frame.Page{Title: "FAQ"}, &FAQView{}))
			if want := tt.sentence("Orders"); !strings.Contains(body, want) {
				t.Errorf("faq page lacks the hint %s", want)
			}
		})
	}
}

// From TestDeskRefusalsNameTheirFormAndLinkToTheFirstInvalidControl, which keeps
// the product half.
func TestARefusedFAQEditNamesItsFormAndLinksToTheFirstInvalidControl(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			bad := i18n.T(ctx, i18n.KeyAdminNoticeRefused)
			body := renderComponent(t, ctx, FAQ(frame.Page{}, &FAQView{
				Rows:       []Entry{{ID: "faq1", Question: "stored", Answer: "stored"}},
				Edit:       Entry{ID: "faq1", Question: "", Answer: " kept ", QuestionEn: "bad"},
				EditErrors: map[string]string{"question": bad, "question_en": bad},
			}))
			uitest.AssertRefusalSummary(t, body, uitest.RefusedForm{
				Action: "/admin/faq/faq1#faq-faq1", Target: "faq-faq1", First: "q-faq1",
				Title: i18n.T(ctx, i18n.KeyAdminRefusalFAQ), Count: 2,
			})
		})
	}
}
