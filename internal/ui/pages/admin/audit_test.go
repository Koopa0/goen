package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
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
		{name: "system", locale: i18n.ZhHant, entry: AuditEntry{System: true}, want: "系統"},
		{name: "system in English", locale: i18n.En, entry: AuditEntry{System: true}, want: "System"},
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

func TestTheAuditTrailShowsChangesAsRowsNotJSON(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	html := renderComponent(t, ctx, Audit(layouts.Page{}, AuditView{Rows: []AuditEntry{{
		Action: "order.advance", Entity: "orders", Actor: "staff", At: "2026-10-02 10:00",
		Changes: []AuditChange{{Field: "status", Before: "pending", After: "picking"}},
	}}}))
	for _, want := range []string{"<dt>status</dt>", "<dd>pending → picking</dd>"} {
		if !strings.Contains(html, want) {
			t.Errorf("audit row is missing %s", want)
		}
	}
	if strings.Contains(html, `{"status"`) || strings.Contains(html, "{&#34;status") {
		t.Error("the audit row prints raw JSON")
	}
}
