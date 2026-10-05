package admin

import (
	"os"
	"path/filepath"
	"regexp"
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
	for _, want := range []string{"<dt>Status</dt>", "<dd>Awaiting payment → Picking</dd>"} {
		if !strings.Contains(html, want) {
			t.Errorf("audit row is missing %s", want)
		}
	}
	if strings.Contains(html, `{"status"`) || strings.Contains(html, "{&#34;status") {
		t.Error("the audit row prints raw JSON")
	}
}

func TestAuditChangesReadAsWordsAndWholeDollars(t *testing.T) {
	t.Parallel()
	const customerID = "0b6c3a4e-1111-4222-8333-444455556666"
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	html := renderComponent(t, ctx, Audit(layouts.Page{}, AuditView{Rows: []AuditEntry{
		{
			Action: "credit.grant", Entity: "store_credit_entries", Actor: "staff", At: "2026-10-02 10:00",
			Changes: []AuditChange{{Field: "amount_cents", After: "9999900"}},
		},
		{
			Action: "order.ship", Entity: "orders", Actor: "staff", At: "2026-10-02 10:00",
			Changes: []AuditChange{{Field: "carrier", After: "black_cat"}},
		},
		{
			Action: "coupon.create", Entity: "coupons", Actor: "staff", At: "2026-10-02 10:00",
			Changes: []AuditChange{{Field: "kind", After: "amount"}, {Field: "value", After: "50"}},
		},
		{
			Action: "customer.view", Entity: "users", Actor: "staff", At: "2026-10-02 10:00", UserName: "王小明",
			Changes: []AuditChange{{Field: "user_id", After: customerID}},
		},
		{
			Action: "order.advance", Entity: "orders", Actor: "staff", At: "2026-10-02 10:00",
			Changes: []AuditChange{{Field: "odd_key", After: "odd"}},
		},
	}}))
	for _, want := range []string{
		"NT$99,999", "T-CAT (Black Cat)", "NT$50", "Fixed amount",
		`<a href="/admin/customers/` + customerID + `">王小明</a>`,
		"<dt>odd_key</dt>", "<dd>odd</dd>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("audit page is missing %q", want)
		}
	}
	for _, unwanted := range []string{"_cents", "9999900", "black_cat"} {
		if strings.Contains(html, unwanted) {
			t.Errorf("audit page still prints %q", unwanted)
		}
	}
}

func TestAMoneyRowCarriesAVisibleTag(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderComponent(t, ctx, Audit(layouts.Page{}, AuditView{Rows: []AuditEntry{
		{Action: "credit.grant", Entity: "store_credit_entries", Actor: "staff"},
	}}))
	if !strings.Contains(html, ">金額</span>") {
		t.Error("a money row has no visible money tag")
	}
}

var (
	auditedTable = regexp.MustCompile(`Table:\s*"([a-z_]+)"`)
	// An entity named in the schema's own audit writes: record_audit_event's
	// third argument, or the entity_table column of an INSERT INTO audit_events.
	auditedSQLTable = regexp.MustCompile(`(?:record_audit_event\(\s*\w+,\s*[\w']+,|'invoice\.[^']*',)\s*'([a-z_]+)'`)
)

func TestEveryAuditedTableHasAnEntityLabel(t *testing.T) {
	t.Parallel()
	tables := map[string]string{}
	err := filepath.WalkDir("../../../admin", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		for _, m := range auditedTable.FindAllSubmatch(src, -1) {
			tables[string(m[1])] = path
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile("../../../../migrations/001_initial_schema.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range auditedSQLTable.FindAllSubmatch(schema, -1) {
		tables[string(m[1])] = "migrations/001_initial_schema.up.sql"
	}
	if len(tables) < 20 {
		t.Fatalf("found %d audited tables, want at least 20: the search no longer matches the code", len(tables))
	}
	for table, where := range tables {
		if _, ok := entityLabels[table]; !ok {
			t.Errorf("%s audits table %q, which has no entry in entityLabels", where, table)
		}
	}
}
