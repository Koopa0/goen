package audit

import (
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestEntry_LabelProductUpdate: product update audit label is localized.
func TestEntry_LabelProductUpdate(t *testing.T) {
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
			if got := (Entry{Action: "product.update"}).Label(ctx); got != tt.want {
				t.Errorf("product.update label = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestEntry_ActorTextSystem: a system row has no user, which
// is also what an erased staff account leaves, and the two must read apart.
func TestEntry_ActorTextSystem(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		entry  Entry
		want   string
	}{
		{name: "system", locale: i18n.ZhHant, entry: Entry{System: true}, want: "系統"},
		{name: "system in English", locale: i18n.En, entry: Entry{System: true}, want: "System"},
		{name: "erased staff", locale: i18n.ZhHant, entry: Entry{}, want: "已刪除的帳號"},
		{name: "staff", locale: i18n.ZhHant, entry: Entry{Actor: "王店長"}, want: "王店長"},
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

// TestAuditChangesAsRows: the audit trail shows changes as rows not JSON.
func TestAuditChangesAsRows(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	markup := renderComponent(t, ctx, Audit(frame.Page{}, View{Rows: []Entry{{
		Action: "order.advance", Entity: "orders", Actor: "staff", At: "2026-10-02 10:00",
		Changes: []Change{{Field: "status", Before: "pending", After: "picking"}},
	}}}))
	for _, want := range []string{"<dt>Status</dt>", "<dd>Awaiting payment → Packing</dd>"} {
		if !strings.Contains(markup, want) {
			t.Errorf("audit row is missing %s", want)
		}
	}
	if strings.Contains(markup, `{"status"`) || strings.Contains(markup, "{&#34;status") {
		t.Error("the audit row prints raw JSON")
	}
}

func TestAuditChangesReadAsWordsAndWholeDollars(t *testing.T) {
	t.Parallel()
	const customerID = "0b6c3a4e-1111-4222-8333-444455556666"
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	markup := renderComponent(t, ctx, Audit(frame.Page{}, View{Rows: []Entry{
		{
			Action: "credit.grant", Entity: "store_credit_entries", Actor: "staff", At: "2026-10-02 10:00",
			Changes: []Change{{Field: "amount_cents", After: "9999900"}},
		},
		{
			Action: "order.ship", Entity: "orders", Actor: "staff", At: "2026-10-02 10:00",
			Changes: []Change{{Field: "carrier", After: "black_cat"}},
		},
		{
			Action: "coupon.create", Entity: "coupons", Actor: "staff", At: "2026-10-02 10:00",
			Changes: []Change{{Field: "kind", After: "amount"}, {Field: "value", After: "50"}},
		},
		{
			Action: "customer.view", Entity: "users", Actor: "staff", At: "2026-10-02 10:00", CustomerEmail: "wang@example.test", CustomerID: customerID,
			Changes: []Change{{Field: "user_id", After: customerID}},
		},
		{
			Action: "order.advance", Entity: "orders", Actor: "staff", At: "2026-10-02 10:00",
			Changes: []Change{{Field: "odd_key", After: "odd"}},
		},
	}}))
	for _, want := range []string{
		"NT$99,999", "T-CAT (Black Cat)", "NT$50", "Fixed amount",
		`<a href="/admin/customers/` + customerID + `">wang@example.test</a>`,
		"<dt>This record includes the field “odd_key”.</dt>", "<dd>odd</dd>",
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("audit page is missing %q", want)
		}
	}
	for _, unwanted := range []string{"_cents", "9999900", "black_cat"} {
		if strings.Contains(markup, unwanted) {
			t.Errorf("audit page still prints %q", unwanted)
		}
	}
}

// TestAuditMoneyTag: a money row carries a visible tag.
func TestAuditMoneyTag(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	markup := renderComponent(t, ctx, Audit(frame.Page{}, View{Rows: []Entry{
		{Action: "credit.grant", Entity: "store_credit_entries", Actor: "staff", Changes: []Change{{Field: "amount_cents", After: "100"}}},
	}}))
	if !strings.Contains(markup, ">金額</span>") {
		t.Error("a money row has no visible money tag")
	}
}

func TestEntryReadsEachRecordedKindAsWords(t *testing.T) {
	t.Parallel()
	const customerID = "0b6c3a4e-1111-4222-8333-444455556666"
	for _, tt := range []struct {
		name    string
		locale  i18n.Locale
		entry   Entry
		want    string
		wantNot string
		// wantKey, when set, is the message want is read from, so the case
		// follows the wording rather than a copy of it.
		wantKey i18n.Key
	}{
		{
			name: "return decision", locale: i18n.En,
			entry: Entry{Entity: "return_requests", Changes: []Change{{Field: "decision", After: "approved"}}},
			want:  "<dd>Approved</dd>", wantNot: "<dd>approved</dd>",
		},
		{
			name: "statutory entitlement", locale: i18n.ZhHant,
			entry: Entry{Entity: "return_requests", Changes: []Change{{Field: "entitlement", After: "statutory"}}},
			want:  "<dd>七日猶豫期</dd>", wantNot: "statutory",
		},
		{
			name: "goodwill entitlement", locale: i18n.ZhHant,
			entry: Entry{Entity: "return_requests", Changes: []Change{{Field: "entitlement", After: "goodwill"}}},
			want:  "<dd>店家優惠</dd>", wantNot: "goodwill",
		},
		{
			name: "exception entitlement", locale: i18n.ZhHant,
			entry: Entry{Entity: "return_requests", Changes: []Change{{Field: "entitlement", After: "exception"}}},
			want:  "<dd>人工例外</dd>", wantNot: "exception",
		},
		{
			name: "policy window", locale: i18n.ZhHant,
			entry:   Entry{Entity: "return_requests", Changes: []Change{{Field: "policy_window", After: "goodwill"}}},
			wantKey: i18n.KeyAdminReturnWindowGoodwill, wantNot: "<dd>goodwill</dd>",
		},
		{
			name: "an unsent refund's reason code is a word", locale: i18n.ZhHant,
			entry: Entry{Action: "return.refund_unsent", Entity: "return_requests", Changes: []Change{{Field: "reason", After: "supplier_failed"}}},
			want:  "<dd>供應商無法供貨</dd>", wantNot: "supplier_failed",
		},
		{
			name: "a customer's reason stays as they wrote it", locale: i18n.ZhHant,
			entry: Entry{Action: "return.request", Entity: "return_requests", Changes: []Change{{Field: "reason", After: "lost"}}},
			want:  "<dd>lost</dd>", wantNot: "商品遺失",
		},
		{
			name: "percent coupon", locale: i18n.En,
			entry: Entry{Entity: "coupons", Changes: []Change{{Field: "kind", After: "percent"}, {Field: "value", After: "20"}}},
			want:  "<dd>20%</dd>", wantNot: "NT$",
		},
		{
			name: "order status is a word", locale: i18n.En,
			entry: Entry{Entity: "orders", Changes: []Change{{Field: "status", After: "shipped"}}},
			want:  "<dd>Shipped</dd>", wantNot: "<dd>shipped</dd>",
		},
		{
			name: "product status stays as recorded", locale: i18n.En,
			entry: Entry{Entity: "products", Changes: []Change{{Field: "status", After: "delivered"}}},
			want:  "<dd>delivered</dd>", wantNot: "<dd>Delivered</dd>",
		},
		{
			name: "refund status stays as recorded", locale: i18n.En,
			entry: Entry{Entity: "refunds", Changes: []Change{{Field: "status", After: "delivered"}}},
			want:  "<dd>delivered</dd>", wantNot: "<dd>Delivered</dd>",
		},
		{
			name: "credit returned by a cancellation is whole dollars", locale: i18n.En,
			entry: Entry{Entity: "orders", Changes: []Change{{Field: "credit_returned_cents", After: "500000"}}},
			want:  "<dd>NT$5,000</dd>", wantNot: "credit_returned_cents",
		},
		{
			name: "an erased customer prints the recorded id", locale: i18n.En,
			entry: Entry{Entity: "users", Changes: []Change{{Field: "user_id", After: customerID}}},
			want:  "<dd>" + customerID + "</dd>", wantNot: "/admin/customers/",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			want := tt.want
			if tt.wantKey != "" {
				want = i18n.T(ctx, tt.wantKey)
			}
			markup := renderComponent(t, ctx, Audit(frame.Page{}, View{Rows: []Entry{tt.entry}}))
			if !strings.Contains(markup, want) {
				t.Errorf("audit row is missing %q", want)
			}
			if strings.Contains(markup, tt.wantNot) {
				t.Errorf("audit row still carries %q", tt.wantNot)
			}
		})
	}
}

// TestEntry_MoneyAmountsOnly: only a row that records an amount says money.
func TestEntry_MoneyAmountsOnly(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		entry Entry
		want  bool
	}{
		{"refund before shipment", Entry{Action: "return.refund_before_shipment", Changes: []Change{{Field: "card_refund_cents", After: "100"}}}, true},
		{"amount coupon value", Entry{Entity: "coupons", Action: "coupon.create", Changes: []Change{{Field: "kind", After: "amount"}, {Field: "value", After: "50"}}}, true},
		{"percent coupon value", Entry{Entity: "coupons", Action: "coupon.create", Changes: []Change{{Field: "kind", After: "percent"}, {Field: "value", After: "20"}}}, false},
		{"stock quantity", Entry{Action: "stock.adjust", Changes: []Change{{Field: "delta", After: "3"}}}, false},
		{"declined return", Entry{Action: "return.decide", Changes: []Change{{Field: "decision", After: "rejected"}}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.entry.Money(); got != tt.want {
				t.Errorf("Money() = %v, want %v", got, tt.want)
			}
			ctx := i18n.WithLocale(t.Context(), i18n.En)
			markup := renderComponent(t, ctx, Audit(frame.Page{}, View{Rows: []Entry{tt.entry}}))
			if got := strings.Contains(markup, ">Money<"); got != tt.want {
				t.Errorf("rendered Money tag = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuditUnknownFieldsRetainDetails(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		field  string
		before string
		after  string
		zh     string
		en     string
		text   string
	}{
		{name: "future field", field: "future_key", before: "old", after: "new", zh: "這筆記錄包含「future_key」欄位。", en: "This record includes the field “future_key”.", text: "old → new"},
		{name: "hostile field and value", field: "<script>alert(1)</script>", before: "<b>old</b>", after: "<img src=x onerror=alert(1)>", zh: "這筆記錄包含「<script>alert(1)</script>」欄位。", en: "This record includes the field “<script>alert(1)</script>”.", text: "<b>old</b> → <img src=x onerror=alert(1)>"},
		{name: "historical non-object", after: `["old",null]`, zh: "記錄內容", en: "Recorded details", text: `["old",null]`},
		{name: "null value", field: "future_null", after: "—", zh: "這筆記錄包含「future_null」欄位。", en: "This record includes the field “future_null”.", text: "—"},
		{name: "removed value", field: "future_removed", before: "original", zh: "這筆記錄包含「future_removed」欄位。", en: "This record includes the field “future_removed”.", text: "original"},
	} {
		for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			t.Run(tt.name+"/"+string(locale), func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				label := tt.zh
				if locale == i18n.En {
					label = tt.en
				}
				entry := Entry{Entity: "future_table", Changes: []Change{{Field: tt.field, Before: tt.before, After: tt.after}}}
				changes := entry.ReadableChanges(ctx)
				if len(changes) != 1 {
					t.Fatalf("got %d changes, want one", len(changes))
				}
				if changes[0].Label != label || changes[0].Text != tt.text || changes[0].Href != "" {
					t.Errorf("unknown field = %#v, want label %q and text %q without a link", changes[0], label, tt.text)
				}
				rendered := renderComponent(t, ctx, Audit(frame.Page{}, View{Rows: []Entry{entry}}))
				for _, want := range []string{"<dt>" + html.EscapeString(label) + "</dt>", "<dd>" + html.EscapeString(tt.text) + "</dd>"} {
					if !strings.Contains(rendered, want) {
						t.Errorf("unknown field is missing %q", want)
					}
				}
			})
		}
	}
}

// TestAuditSQLProducerFieldsAreInTheLedger derives every (table, key) pair SQL
// writes into audit_events.before/after and asserts auditLedger lists it. It
// reads migrations/*.up.sql and every .sql file under internal/, seed/ and
// scripts/. Go writers are not read: their State is keyed by Field, which only
// this package can make, and TestFieldKeysInLedger holds each Field to
// the ledger.
func TestAuditSQLProducerFieldsAreInTheLedger(t *testing.T) {
	t.Parallel()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(root, "go.mod")); statErr == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("cannot find the repository's go.mod")
		}
		root = parent
	}
	fields := make(map[[2]string]string)
	auditSQLProducerFields(t, root, func(entity string, keys []string, source string) {
		for _, key := range keys {
			fields[[2]string{entity, key}] = source
		}
	})
	ledger := make(map[[2]string]bool, len(auditLedger))
	for _, row := range auditLedger {
		ledger[[2]string{row.entity, row.field}] = true
	}
	for pair, source := range fields {
		if !ledger[pair] {
			t.Errorf("recorded SQL producer field %s.%s from %s is not in the audit ledger: add it to auditLedger with its label (audit_events keeps it forever)", pair[0], pair[1], source)
		}
	}
	// Measured on 6ba69813: 38 pairs. A walk that reads nothing must not pass.
	if len(fields) < 35 {
		t.Errorf("SQL producer discovery derived %d pairs, want at least 35: the walk stopped reading sources", len(fields))
	}
}

func auditSQLProducerFields(t *testing.T, root string, add func(string, []string, string)) {
	t.Helper()
	readPayload := func(tokens []string) []string {
		if len(tokens) == 1 && strings.EqualFold(tokens[0], "null") {
			return nil
		}
		if len(tokens) > 1 && strings.EqualFold(tokens[0], "jsonb_build_object") {
			args, _ := auditSQLArguments(t, tokens, 1)
			if len(args)%2 != 0 {
				t.Fatal("odd audit JSON object arguments")
			}
			var keys []string
			for index := 0; index < len(args); index += 2 {
				if len(args[index]) != 1 || !strings.HasPrefix(args[index][0], "'") {
					t.Fatal("dynamic SQL audit key needs explicit discovery")
				}
				keys = append(keys, strings.ReplaceAll(strings.Trim(args[index][0], "'"), "''", "'"))
			}
			return keys
		}
		t.Fatalf("unsupported SQL audit payload %v; extend producer discovery", tokens)
		return nil
	}
	readCall := func(name string, args [][]string, invoiceEntity, path string) {
		t.Helper()
		if auditSQLQueryParameters(path, args) {
			return
		}
		switch name {
		case "record_audit_event":
			if (len(args) != 6 && len(args) != 7) || len(args[2]) != 1 || !strings.HasPrefix(args[2][0], "'") {
				t.Fatalf("unsupported audit SQL call in %s", path)
			}
			entity := strings.Trim(args[2][0], "'")
			add(entity, readPayload(args[4]), path)
			add(entity, readPayload(args[5]), path)
		case "record_invoice_operation_audit":
			if len(args) != 3 {
				t.Fatalf("unsupported invoice audit SQL call in %s", path)
			}
			if invoiceEntity == "" {
				t.Fatalf("%s: cannot resolve invoice audit wrapper entity", path)
			}
			add(invoiceEntity, readPayload(args[2]), path)
		}
	}
	readInsert := func(function string, signature [][]string, tokens []string, index int, path string) {
		t.Helper()
		columns, values := auditSQLInsertValues(t, tokens, index)
		entity := auditSQLInsertEntity(columns, values)
		if entity == "" {
			if !auditSQLGenericForwarder(function, signature, columns, values) {
				t.Fatalf("%s: unsupported dynamic audit INSERT entity in %s", path, function)
			}
			return
		}
		invoiceForwarder := auditSQLInvoiceForwarder(function, signature, columns, values)
		for column, names := range columns {
			if names[0] != "before" && names[0] != "after" {
				continue
			}
			if names[0] == "after" && invoiceForwarder {
				continue // Only this exact wrapper forwards caller-supplied JSON.
			}
			add(entity, readPayload(values[column]), path)
		}
	}
	sourceFS := os.DirFS(root)
	paths, err := filepath.Glob(filepath.Join(root, "migrations", "*.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"internal", "seed", "scripts"} {
		if err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && strings.HasSuffix(path, ".sql") {
				paths = append(paths, path)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range paths {
		relative, pathErr := filepath.Rel(root, path)
		if pathErr != nil {
			t.Fatal(pathErr)
		}
		data, readErr := fs.ReadFile(sourceFS, filepath.ToSlash(relative))
		if readErr != nil {
			t.Fatal(readErr)
		}
		tokens := auditSQLTokens(t, string(data))
		// Infer the forwarding wrapper's table from its actual INSERT.
		invoiceEntity := ""
		function := ""
		var signature [][]string
		for index, word := range tokens {
			if strings.EqualFold(word, "create") && index+2 < len(tokens) && strings.EqualFold(tokens[index+1], "function") {
				function, signature = auditSQLFunctionDeclaration(t, tokens, index)
			}
			if word == "$audit_body_end$" {
				function, signature = "", nil
			}
			if function != "record_invoice_operation_audit" || !strings.EqualFold(word, "insert") || index+3 >= len(tokens) || !strings.EqualFold(tokens[index+2], "audit_events") {
				continue
			}
			columns, end := auditSQLArguments(t, tokens, index+3)
			if end+1 >= len(tokens) || !strings.EqualFold(tokens[end], "values") {
				t.Fatal("invoice audit wrapper has no explicit VALUES row")
			}
			values, _ := auditSQLArguments(t, tokens, end+1)
			if len(columns) != len(values) {
				t.Fatal("invoice audit wrapper columns and values differ")
			}
			if !auditSQLInvoiceForwarder(function, signature, columns, values) {
				t.Fatal("unfamiliar invoice audit forwarding function")
			}
			for column, names := range columns {
				if names[0] != "entity_table" {
					continue
				}
				if len(values[column]) != 1 || !strings.HasPrefix(values[column][0], "'") {
					t.Fatal("invoice audit wrapper has a dynamic entity")
				}
				invoiceEntity = strings.Trim(values[column][0], "'")
			}
		}
		function, signature = "", nil
		for index, word := range tokens {
			name := strings.ToLower(word)
			if name == "create" && index+2 < len(tokens) && strings.EqualFold(tokens[index+1], "function") {
				function, signature = auditSQLFunctionDeclaration(t, tokens, index)
			}
			if word == "$audit_body_end$" {
				function, signature = "", nil
			}
			if (name == "record_audit_event" || name == "record_invoice_operation_audit") && index+1 < len(tokens) && tokens[index+1] == "(" && (index == 0 || !strings.EqualFold(tokens[index-1], "function")) {
				args, _ := auditSQLArguments(t, tokens, index+1)
				readCall(name, args, invoiceEntity, path)
			}
			if name == "insert" && index+3 < len(tokens) && strings.EqualFold(tokens[index+1], "into") && strings.EqualFold(tokens[index+2], "audit_events") {
				readInsert(function, signature, tokens, index, path)
			}
		}
	}
}

func auditSQLQueryParameters(path string, args [][]string) bool {
	if !strings.Contains(path, string(filepath.Separator)+"internal"+string(filepath.Separator)) {
		return false
	}
	// A parameter-only query wrapper stores what its Go caller passes, and the
	// caller marshals a State.
	if len(args) == 7 && len(args[4]) > 0 && len(args[5]) > 0 && args[4][0] == "@" && args[5][0] == "@" {
		return true
	}
	return len(args) == 7 && len(args[5]) > 0 && args[5][0] == "@" && strings.EqualFold(args[4][0], "null")
}

func auditSQLInsertValues(t *testing.T, tokens []string, index int) (columns, values [][]string) {
	t.Helper()
	columns, end := auditSQLArguments(t, tokens, index+3)
	if end+1 >= len(tokens) || !strings.EqualFold(tokens[end], "values") {
		t.Fatal("audit INSERT is not an explicit VALUES row")
	}
	values, _ = auditSQLArguments(t, tokens, end+1)
	if len(columns) != len(values) {
		t.Fatal("audit INSERT columns and values differ")
	}
	return columns, values
}

func auditSQLInsertEntity(columns, values [][]string) string {
	entity := ""
	for column, names := range columns {
		if names[0] == "entity_table" && len(values[column]) == 1 && strings.HasPrefix(values[column][0], "'") {
			entity = strings.Trim(values[column][0], "'")
		}
	}
	return entity
}

func auditSQLArguments(t *testing.T, tokens []string, start int) (args [][]string, next int) {
	t.Helper()
	if start >= len(tokens) || tokens[start] != "(" {
		t.Fatalf("expected SQL argument list at token %d in %v", start, tokens[max(0, start-3):min(len(tokens), start+3)])
	}
	var current []string
	depth := 1
	for index := start + 1; index < len(tokens); index++ {
		word := tokens[index]
		switch word {
		case "(", "[":
			depth++
		case ")", "]":
			depth--
		}
		if depth == 0 {
			return append(args, current), index + 1
		}
		if word == "," && depth == 1 {
			args = append(args, current)
			current = nil
		} else {
			current = append(current, word)
		}
	}
	t.Fatal("unterminated SQL argument list")
	return nil, 0
}

func auditSQLTokens(t *testing.T, source string) []string {
	t.Helper()
	var tokens []string
	for index := 0; index < len(source); {
		if strings.HasPrefix(source[index:], "--") {
			end := strings.IndexByte(source[index:], '\n')
			if end < 0 {
				break
			}
			index += end + 1
			continue
		}
		if strings.HasPrefix(source[index:], "/*") {
			end := strings.Index(source[index+2:], "*/")
			if end < 0 {
				t.Fatal("unterminated SQL comment")
			}
			index += end + 4
			continue
		}
		character := source[index]
		if character == '$' {
			quoted, next := auditSQLDollarTokens(t, source, index, tokens)
			if next != index {
				tokens = append(tokens, quoted...)
				index = next
				continue
			}
		}
		switch {
		case character == '\'':
			start := index
			index++
			for index < len(source) {
				if source[index] == '\'' {
					index++
					if index < len(source) && source[index] == '\'' {
						index++
						continue
					}
					break
				}
				index++
			}
			tokens = append(tokens, source[start:index])
		case unicode.IsSpace(rune(character)):
			index++
		case character == '_' || unicode.IsLetter(rune(character)):
			start := index
			for index < len(source) && (source[index] == '_' || unicode.IsLetter(rune(source[index])) || unicode.IsDigit(rune(source[index]))) {
				index++
			}
			tokens = append(tokens, source[start:index])
		default:
			tokens = append(tokens, string(character))
			index++
		}
	}
	return tokens
}

func auditSQLDollarTokens(t *testing.T, source string, start int, preceding []string) (tokens []string, next int) {
	t.Helper()
	end := start + 1
	for end < len(source) && (source[end] == '_' || unicode.IsLetter(rune(source[end])) || unicode.IsDigit(rune(source[end]))) {
		end++
	}
	if end >= len(source) || source[end] != '$' {
		return nil, start
	}
	marker := source[start : end+1]
	closeAt := strings.Index(source[end+1:], marker)
	if closeAt < 0 {
		t.Fatal("unterminated SQL dollar quote")
	}
	bodyEnd := end + 1 + closeAt
	next = bodyEnd + len(marker)
	if len(preceding) == 0 || (!strings.EqualFold(preceding[len(preceding)-1], "as") && !strings.EqualFold(preceding[len(preceding)-1], "do")) {
		return []string{source[start:next]}, next
	}
	tokens = append(tokens, "$audit_body_begin$")
	tokens = append(tokens, auditSQLTokens(t, source[end+1:bodyEnd])...)
	tokens = append(tokens, "$audit_body_end$")
	return tokens, next
}

func auditSQLSignatureMatches(signature [][]string, namesAndTypes []string) bool {
	if len(signature)*2 != len(namesAndTypes) {
		return false
	}
	for index, parameter := range signature {
		if len(parameter) < 2 || parameter[0] != namesAndTypes[index*2] || parameter[1] != namesAndTypes[index*2+1] {
			return false
		}
	}
	return true
}

func auditSQLColumnsMatch(columns, values [][]string, expected map[string]string) bool {
	if len(columns) != len(values) {
		return false
	}
	for name, value := range expected {
		found := false
		for index, column := range columns {
			if len(column) == 1 && column[0] == name && len(values[index]) == 1 && values[index][0] == value {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func auditSQLGenericForwarder(function string, signature, columns, values [][]string) bool {
	return function == "record_audit_event" &&
		auditSQLSignatureMatches(signature, []string{"p_actor", "uuid", "p_action", "text", "p_entity_table", "text", "p_entity_id", "uuid", "p_before", "jsonb", "p_after", "jsonb", "p_request_id", "text"}) &&
		auditSQLColumnsMatch(columns, values, map[string]string{"entity_table": "p_entity_table", "before": "p_before", "after": "p_after"})
}

func auditSQLInvoiceForwarder(function string, signature, columns, values [][]string) bool {
	return function == "record_invoice_operation_audit" &&
		auditSQLSignatureMatches(signature, []string{"p_operation_id", "uuid", "p_entity_id", "uuid", "p_after", "jsonb"}) &&
		auditSQLColumnsMatch(columns, values, map[string]string{"entity_id": "p_entity_id", "before": "NULL", "after": "p_after"})
}

// TestAuditSQLForwardingExactFunction: audit SQL forwarding requires its exact function.
func TestAuditSQLForwardingExactFunction(t *testing.T) {
	t.Parallel()
	columns := [][]string{{"entity_id"}, {"entity_table"}, {"before"}, {"after"}}
	generic := [][]string{{"p_actor", "uuid"}, {"p_action", "text"}, {"p_entity_table", "text"}, {"p_entity_id", "uuid"}, {"p_before", "jsonb"}, {"p_after", "jsonb"}, {"p_request_id", "text"}}
	invoice := [][]string{{"p_operation_id", "uuid"}, {"p_entity_id", "uuid"}, {"p_after", "jsonb"}}
	genericValues := [][]string{{"p_entity_id"}, {"p_entity_table"}, {"p_before"}, {"p_after"}}
	invoiceValues := [][]string{{"p_entity_id"}, {"'invoice_documents'"}, {"NULL"}, {"p_after"}}
	if !auditSQLGenericForwarder("record_audit_event", generic, columns, genericValues) {
		t.Error("the exact generic forwarding function was not recognized")
	}
	if !auditSQLInvoiceForwarder("record_invoice_operation_audit", invoice, columns, invoiceValues) {
		t.Error("the exact invoice forwarding function was not recognized")
	}
	for _, function := range []string{"", "new_writer", "record_invoice_operation_audit"} {
		if auditSQLGenericForwarder(function, generic, columns, genericValues) {
			t.Errorf("unrecognized %q may silently omit a dynamic entity", function)
		}
	}
	for _, function := range []string{"", "new_writer", "record_audit_event"} {
		if auditSQLInvoiceForwarder(function, invoice, columns, invoiceValues) {
			t.Errorf("unrecognized %q may silently omit p_after", function)
		}
	}
	if auditSQLGenericForwarder("record_audit_event", invoice, columns, genericValues) || auditSQLInvoiceForwarder("record_invoice_operation_audit", generic, columns, invoiceValues) {
		t.Error("a forwarding name with a different signature was accepted")
	}
	wrongValues := [][]string{{"p_entity_id"}, {"p_entity_table"}, {"p_before"}, {"new_payload"}}
	if auditSQLGenericForwarder("record_audit_event", generic, columns, wrongValues) {
		t.Error("a forwarding function with a different payload was accepted")
	}
}

func auditSQLFunctionDeclaration(t *testing.T, tokens []string, start int) (name string, signature [][]string) {
	t.Helper()
	var declaration strings.Builder
	declaration.WriteString(tokens[start+2])
	index := start + 3
	for index+1 < len(tokens) && tokens[index] == "." {
		declaration.WriteByte('.')
		declaration.WriteString(tokens[index+1])
		index += 2
	}
	signature, _ = auditSQLArguments(t, tokens, index)
	return declaration.String(), signature
}
