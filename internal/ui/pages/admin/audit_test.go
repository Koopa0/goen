package admin

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestStaffAuditActionsRenderAPhrase(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, action := range []string{"staff.grant", "staff.revoke", "staff.factor.remove"} {
		if got := (AuditEntry{Action: action}).Label(ctx); got == action {
			t.Errorf("Label(%q) = %q; the staff action has no staff-facing phrase", action, got)
		}
	}
}

func TestProductUpdateAuditLabelIsLocalized(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		want   string
	}{
		{name: "Traditional Chinese", locale: i18n.ZhHant, want: "修改商品"},
		{name: "English", locale: i18n.En, want: "Edit product"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			if got := (AuditEntry{Action: "product.update"}).Label(ctx); got != tt.want {
				t.Errorf("product.update label = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestASystemIssueIsNotReadAsAnErasedAccount: a system row has no user, which
// is also what an erased staff account leaves, and the two must read apart.
func TestASystemIssueIsNotReadAsAnErasedAccount(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		entry  AuditEntry
		want   string
	}{
		{name: "system", locale: i18n.ZhHant, entry: AuditEntry{System: true}, want: "系統自動開立"},
		{name: "system in English", locale: i18n.En, entry: AuditEntry{System: true}, want: "Issued automatically"},
		{name: "erased staff", locale: i18n.ZhHant, entry: AuditEntry{}, want: "已刪除的帳號"},
		{name: "staff", locale: i18n.ZhHant, entry: AuditEntry{Actor: "王店長"}, want: "王店長"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			if got := tt.entry.ActorText(ctx); got != tt.want {
				t.Errorf("ActorText = %q, want %q", got, tt.want)
			}
		})
	}
}
