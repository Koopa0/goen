package feedback_test

import (
	"testing"

	"github.com/koopa0/goen/internal/admin/feedback"
	"github.com/koopa0/goen/internal/contactsubject"
	"github.com/koopa0/goen/internal/i18n"
)

func TestSubjectLabelUsesTheReadersLanguage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		subject contactsubject.Subject
		zh      string
		en      string
	}{
		{"訂單問題", "訂單問題", "An order"},
		{"退換貨", "退貨", "Returns"},
		{"保固維修", "保固維修", "Warranty or repair"},
		{"商品諮詢", "商品諮詢", "A product question"},
		{"合作提案", "合作提案", "A partnership"},
	} {
		t.Run(string(tc.subject), func(t *testing.T) {
			t.Parallel()
			for _, lang := range []struct {
				locale i18n.Locale
				want   string
			}{{i18n.ZhHant, tc.zh}, {i18n.En, tc.en}} {
				t.Run(lang.locale.Tag(), func(t *testing.T) {
					t.Parallel()
					got, err := feedback.SubjectLabel(i18n.WithLocale(t.Context(), lang.locale), tc.subject)
					if err != nil || got != lang.want {
						t.Errorf("SubjectLabel(%q) = %q, %v, want %q, nil", tc.subject, got, err, lang.want)
					}
				})
			}
		})
	}
}

func TestSubjectLabelRefusesAnUnknownCategory(t *testing.T) {
	t.Parallel()
	got, err := feedback.SubjectLabel(t.Context(), "An order")
	if got != "" || err == nil {
		t.Errorf("SubjectLabel(unknown) = %q, %v, want empty label and error", got, err)
	}
}
