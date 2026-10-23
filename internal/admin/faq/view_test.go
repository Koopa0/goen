package faq

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/uitest"
)

// From copy_test.go: TestProductFormExampleHints, which keeps the product half.
// TestFAQExampleHints: every FAQ example hint reads in full.
func TestFAQExampleHints(t *testing.T) {
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
			body := renderComponent(t, ctx, FAQ(frame.Page{Title: "FAQ"}, &View{}))
			if want := tt.sentence("Orders"); !strings.Contains(body, want) {
				t.Errorf("faq page lacks the hint %s", want)
			}
		})
	}
}

// From TestProductFormRefusalSummary, which keeps
// the product half.
// TestFAQRefusalSummary: a refused FAQ edit names its form and links to the first invalid control.
func TestFAQRefusalSummary(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			bad := i18n.T(ctx, i18n.KeyAdminNoticeRefused)
			body := renderComponent(t, ctx, FAQ(frame.Page{}, &View{
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
