//go:build integration

package products_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

func TestProductReadsDistinguishAbsenceFromInfrastructure(t *testing.T) {
	ctx := t.Context()
	missing := "no-such-product-" + uuid.NewString()
	s := products.NewStore(pool)
	_, err := s.Product(ctx, missing)
	if !errors.Is(err, products.ErrNotFound) || errors.Is(err, products.ErrRefused) {
		t.Fatalf("missing product = %v, want only ErrNotFound", err)
	}

	missingReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/products/"+missing, nil)
	missingReq.SetPathValue("slug", missing)
	missingRes := httptest.NewRecorder()
	admintest.ProductDesk(pool, s).Edit(missingRes, missingReq)
	if missingRes.Code != http.StatusNotFound {
		t.Fatalf("missing product page answered %d, want 404", missingRes.Code)
	}

	var existing string
	if readErr := pool.QueryRow(ctx, `SELECT slug FROM products ORDER BY slug LIMIT 1`).Scan(&existing); readErr != nil {
		t.Fatalf("read existing product: %v", readErr)
	}
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse timeout pool config: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "500"
	timeoutPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open timeout pool: %v", err)
	}
	t.Cleanup(timeoutPool.Close)
	timedStore := products.NewStore(timeoutPool)

	blocker, err := pgx.Connect(ctx, pool.Config().ConnString())
	if err != nil {
		t.Fatalf("open blocker: %v", err)
	}
	t.Cleanup(func() { _ = blocker.Close(context.Background()) }) //nolint:usetesting // cleanup runs after t.Context is canceled
	tx, err := blocker.Begin(ctx)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, lockErr := tx.Exec(ctx, `LOCK TABLE products IN ACCESS EXCLUSIVE MODE`); lockErr != nil {
		t.Fatalf("lock products: %v", lockErr)
	}

	_, err = timedStore.Product(ctx, existing)
	if errors.Is(err, products.ErrNotFound) || errors.Is(err, products.ErrRefused) {
		t.Fatalf("product read timeout acquired a domain category: %v", err)
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.Code != "57014" {
		t.Fatalf("product read failure = %v, want preserved PgError 57014", err)
	}

	timedHandler := admintest.ProductDesk(timeoutPool, timedStore)
	readReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/products/"+existing, nil)
	readReq.SetPathValue("slug", existing)
	readRes := httptest.NewRecorder()
	timedHandler.Edit(readRes, readReq)
	if readRes.Code != http.StatusInternalServerError {
		t.Fatalf("timed-out product page answered %d, want 500", readRes.Code)
	}

	badVariantReq := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/products/"+existing+"/variants", strings.NewReader("sku=&price="))
	badVariantReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badVariantReq.SetPathValue("slug", existing)
	badVariantRes := httptest.NewRecorder()
	timedHandler.AddVariant(badVariantRes, badVariantReq)
	if badVariantRes.Code != http.StatusInternalServerError {
		t.Fatalf("timed-out rejected-form rebuild answered %d, want 500", badVariantRes.Code)
	}
}

func TestProductUpdateDistinguishesAbsenceFromSuccess(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	slug := admintest.DraftProduct(t, ctx, pool, s)
	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("read product fixture: %v", err)
	}
	missing := "no-such-product-" + uuid.NewString()
	form := &products.Form{
		Slug: missing, Name: view.Name, Summary: view.Summary,
		Description: view.Description, NameEn: view.NameEn,
		SummaryEn: view.SummaryEn, DescriptionEn: view.DescriptionEn,
		WarrantyNote: view.WarrantyNote, WarrantyMonths: view.WarrantyMonths,
		BrandID: view.BrandID, CategoryID: view.CategoryID,
	}
	before := admintest.AuditRows(t, pool, audit.ActionUpdateProduct)
	if errs, updateErr := s.Update(ctx, form); !errors.Is(updateErr, products.ErrNotFound) || len(errs) > 0 {
		t.Fatalf("UpdateProduct(absent) = %v, %v; want ErrNotFound and no field errors",
			errs, updateErr)
	}
	if after := admintest.AuditRows(t, pool, audit.ActionUpdateProduct); after != before {
		t.Fatalf("absent product update left %d audit rows, want %d", after, before)
	}

	values := url.Values{
		"name": {form.Name}, "summary": {form.Summary},
		"description": {form.Description}, "name_en": {form.NameEn},
		"summary_en": {form.SummaryEn}, "description_en": {form.DescriptionEn},
		"warranty":        {form.WarrantyNote},
		"warranty_months": {strconv.FormatInt(int64(form.WarrantyMonths), 10)},
		"brand":           {form.BrandID}, "category": {form.CategoryID},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/products/"+missing, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", missing)
	res := httptest.NewRecorder()
	admintest.ProductDesk(pool, s).Update(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("UpdateProduct(absent) HTTP status = %d, want 404", res.Code)
	}
	if location := res.Header().Get("Location"); location != "" {
		t.Errorf("UpdateProduct(absent) redirected to %q; want no success redirect", location)
	}
}

func TestAnAuditRowNamesItsActorAndRequest(t *testing.T) {
	ctx, actor := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)

	if err := s.SetStatus(ctx, admintest.AnyProductSlug(t, pool), "draft"); err != nil {
		t.Fatalf("publish: %v", err)
	}

	var gotActor uuid.UUID
	var requestID, action string
	if err := pool.QueryRow(ctx, `
		SELECT actor_user_id, coalesce(request_id, ''), action FROM audit_events
		WHERE actor_user_id = $1 ORDER BY occurred_at DESC LIMIT 1`, actor).
		Scan(&gotActor, &requestID, &action); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if gotActor != actor {
		t.Errorf("actor is %s, want %s", gotActor, actor)
	}
	if requestID == "" {
		t.Error("no request id; the row cannot be put beside the log lines from " +
			"the same request")
	}
	if action != string(audit.ActionPublishProduct) {
		t.Errorf("action is %q", action)
	}
}

func TestProductUpdateAndAuditCommitTogether(t *testing.T) {
	ctx, actor := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	slug := admintest.DraftProduct(t, ctx, pool, s)
	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("read product fixture: %v", err)
	}
	form := &products.Form{
		Slug: slug, Name: "已稽核商品 " + uuid.NewString()[:8], Summary: view.Summary,
		Description: view.Description, NameEn: view.NameEn,
		SummaryEn: view.SummaryEn, DescriptionEn: view.DescriptionEn,
		WarrantyNote: view.WarrantyNote, WarrantyMonths: view.WarrantyMonths,
		BrandID: view.BrandID, CategoryID: view.CategoryID,
	}
	before := admintest.AuditRows(t, pool, audit.ActionUpdateProduct)
	if errs, updateErr := s.Update(ctx, form); updateErr != nil || len(errs) > 0 {
		t.Fatalf("UpdateProduct: %v %v", updateErr, errs)
	}

	var gotActor uuid.UUID
	var requestID, auditedSlug, auditedName string
	if err := pool.QueryRow(ctx, `
		SELECT actor_user_id, coalesce(request_id, ''),
		       coalesce(after->>'slug', ''), coalesce(after->>'name', '')
		FROM audit_events
		WHERE action = $1 AND after->>'slug' = $2
		ORDER BY occurred_at DESC, id DESC LIMIT 1`,
		string(audit.ActionUpdateProduct), slug).
		Scan(&gotActor, &requestID, &auditedSlug, &auditedName); err != nil {
		t.Fatalf("read product update audit row: %v", err)
	}
	if gotActor != actor || requestID == "" || auditedSlug != slug || auditedName != form.Name {
		t.Errorf("product update audit = actor %s request %q slug %q name %q; "+
			"want %s/nonempty/%q/%q", gotActor, requestID, auditedSlug, auditedName,
			actor, slug, form.Name)
	}
	if after := admintest.AuditRows(t, pool, audit.ActionUpdateProduct); after != before+1 {
		t.Fatalf("successful update left %d audit rows, want %d", after, before+1)
	}

	// A syntactically valid but nonexistent actor reaches record_audit_event and
	// fails its users foreign key. The product write ran first in the same
	// transaction, so observing the old name proves the audit failure rolled it
	// back instead of leaving an unattributed customer-visible change.
	missingActor := uuid.New()
	failingCtx := web.WithRequestID(user.NewContext(t.Context(), user.User{
		ID: missingActor.String(), Role: user.RoleAdmin,
	}), "req-missing-actor")
	failing := *form
	failing.Name = "不得落地 " + uuid.NewString()[:8]
	errs, updateErr := s.Update(failingCtx, &failing)
	if updateErr == nil || len(errs) > 0 {
		t.Fatalf("UpdateProduct with an unrecordable actor = %v, %v; want audit error",
			errs, updateErr)
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](updateErr)
	if !ok || pgErr.ConstraintName != "audit_events_actor_user_id_fkey" {
		t.Fatalf("product audit insertion failure = %v, want audit actor FK", updateErr)
	}
	var persisted string
	if err := pool.QueryRow(ctx, `SELECT name FROM products WHERE slug = $1`, slug).Scan(&persisted); err != nil {
		t.Fatalf("read product after audit failure: %v", err)
	}
	if persisted != form.Name {
		t.Errorf("product name after audit failure = %q, want rolled back to %q",
			persisted, form.Name)
	}
	if after := admintest.AuditRows(t, pool, audit.ActionUpdateProduct); after != before+1 {
		t.Errorf("failed audit changed product-update trail from %d to %d", before+1, after)
	}
}

func TestAnActionWithNoActorIsRefused(t *testing.T) {
	s := products.NewStore(pool)
	slug := admintest.AnyProductSlug(t, pool)

	// Read before and compared after: anyProductSlug can hand back a product already in the target status.
	var before string
	if scanErr := pool.QueryRow(t.Context(),
		`SELECT status FROM products WHERE slug = $1`, slug).Scan(&before); scanErr != nil {
		t.Fatalf("read product: %v", scanErr)
	}
	target := "draft"
	if before == "draft" {
		target = "archived"
	}

	err := s.SetStatus(t.Context(), slug, target)
	if !errors.Is(err, audit.ErrNoActor) {
		t.Fatalf("a back-office write with no actor gave %v, want ErrNoActor", err)
	}

	var after string
	if scanErr := pool.QueryRow(t.Context(),
		`SELECT status FROM products WHERE slug = $1`, slug).Scan(&after); scanErr != nil {
		t.Fatalf("read product: %v", scanErr)
	}
	if after != before {
		t.Errorf("the write landed anyway (%s → %s); the audit failure must roll it back",
			before, after)
	}
}

func TestAFailedWriteLeavesNoAuditRow(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)

	before := admintest.AuditRows(t, pool, audit.ActionPublishProduct)
	if err := s.SetStatus(ctx, admintest.AnyProductSlug(t, pool), "nonsense"); err == nil {
		t.Fatal("an invalid status was accepted")
	}
	if err := s.SetStatus(ctx, "no-such-product-"+uuid.NewString(), "active"); !errors.Is(err, products.ErrNotFound) {
		t.Errorf("publishing an absent product gave %v, want ErrNotFound", err)
	}
	if after := admintest.AuditRows(t, pool, audit.ActionPublishProduct); after != before {
		t.Errorf("%d audit rows after a refused write, want %d", after, before)
	}
}

func TestOneUploadCanBeAttachedToTwoProducts(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)

	const digest = "aa11bb22cc33dd44ee55ff6677889900aa11bb22cc33dd44ee55ff6677889900"
	if _, err := pool.Exec(ctx, `
		-- byte_size must equal length(bytes): media_objects_size_matches refuses
		-- a row that claims a size its own bytes do not have.
		INSERT INTO media_objects (digest, content_type, byte_size, width, height, bytes)
		VALUES ($1, 'image/png', 1, 800, 600, '\x00'::bytea)
		ON CONFLICT (digest) DO NOTHING`, digest); err != nil {
		t.Fatalf("store the image: %v", err)
	}

	first, second := twoProducts(t)
	if err := s.AttachImage(ctx, first, digest, "第一個商品", "First product", "", 800, 600); err != nil {
		t.Fatalf("attach to the first: %v", err)
	}
	if err := s.AttachImage(ctx, second, digest, "第二個商品", "", "", 800, 600); err != nil {
		t.Fatalf("attach the SAME image to the second: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM product_images WHERE storage_key = $1`, digest).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("the image is attached to %d products, want 2", n)
	}
	if err := s.AttachImage(ctx, first, digest, "再一次", "", "", 800, 600); err == nil {
		t.Error("the same image was attached to one product twice")
	}
}

func twoProducts(t *testing.T) (first, second string) {
	t.Helper()
	slugs := make([]string, 0, 2)
	for i := range 2 {
		slug := "picker-" + uuid.NewString()[:8] + "-" + strconv.Itoa(i)
		if _, err := pool.Exec(t.Context(), `
			INSERT INTO products (brand_id, category_id, slug, name)
			SELECT b.id, c.id, $1, $1 FROM brands b, categories c
			ORDER BY b.id, c.id LIMIT 1`, slug); err != nil {
			t.Fatalf("create product: %v", err)
		}
		slugs = append(slugs, slug)
	}
	return slugs[0], slugs[1]
}

func TestTheShopCanGiveAProductASpecTable(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	slug := admintest.DraftProduct(t, ctx, pool, s)

	if errs, err := s.AddSpec(ctx, slug, products.SpecDraft{
		Label: "螢幕", Value: "6.3 吋 OLED", LabelEn: "Screen",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("AddSpec: %v %v", err, errs)
	}
	if errs, err := s.AddSpec(ctx, slug, products.SpecDraft{
		Label: "重量", Value: "187 公克", LabelEn: "Weight", ValueEn: "187 g",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("AddSpec: %v %v", err, errs)
	}

	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if !view.HasSpecs() {
		t.Fatal("the product states no specs after two were added")
	}
	want := []string{"螢幕", "重量"}
	got := make([]string, 0, len(view.Specs))
	for _, sp := range view.Specs {
		got = append(got, sp.Label)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("spec labels (-want +got):\n%s", diff)
	}

	if rmErr := s.RemoveSpec(ctx, slug, view.Specs[0].ID); rmErr != nil {
		t.Fatalf("RemoveSpec: %v", rmErr)
	}
	after, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product after removal: %v", err)
	}
	if len(after.Specs) != 1 || after.Specs[0].Label != "重量" {
		t.Errorf("after removing 螢幕 the table is %+v", after.Specs)
	}

	other := admintest.DraftProduct(t, ctx, pool, s)
	if rmErr := s.RemoveSpec(ctx, other, after.Specs[0].ID); !errors.Is(rmErr, products.ErrNotFound) {
		t.Errorf("removing another product's spec returned %v, want ErrNotFound", rmErr)
	}
}

func TestASpecIsRefusedRatherThanTruncated(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	slug := admintest.DraftProduct(t, ctx, pool, s)

	tests := []struct {
		name  string
		label string
		value string
		field string
	}{
		{name: "no label", label: "  ", value: "6.3 吋", field: "spec_label"},
		{name: "no value", label: "螢幕", value: "\t", field: "spec_value"},
		{
			name:  "a label that is a sentence",
			label: strings.Repeat("螢", products.SpecLabelRunes+1),
			value: "6.3 吋",
			field: "spec_label",
		},
		{
			name:  "a value past the bound",
			label: "螢幕",
			value: strings.Repeat("吋", products.SpecValueRunes+1),
			field: "spec_value",
		},
	}
	if errs, _ := s.AddSpec(ctx, slug, products.SpecDraft{
		Label: "螢幕", Value: "6.3 吋",
		LabelEn: strings.Repeat("S", products.SpecLabelRunes+1),
	}); errs["spec_label_en"] == "" {
		t.Errorf("an over-long English label was accepted: %v", errs)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs, err := s.AddSpec(ctx, slug, products.SpecDraft{
				Label: tt.label, Value: tt.value,
			})
			if err != nil {
				t.Fatalf("AddSpec: %v", err)
			}
			if _, ok := errs[tt.field]; !ok {
				t.Errorf("AddSpec(%q, %q) refused %v, want a %s error",
					tt.label, tt.value, errs, tt.field)
			}
		})
	}

	if errs, err := s.AddSpec(ctx, slug, products.SpecDraft{
		Label: strings.Repeat("螢", products.SpecLabelRunes), Value: "剛好",
	}); err != nil || len(errs) > 0 {
		t.Errorf("a label exactly at the bound was refused: %v %v", err, errs)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO product_specs (product_id, label, value, position)
		SELECT id, repeat('螢', 41), '繞過表單', 99 FROM products WHERE slug = $1`, slug)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok ||
		pgErr.ConstraintName != "product_specs_label_bounded" {
		t.Errorf("a 41-character label written directly returned %v, want "+
			"product_specs_label_bounded", err)
	}
}

func TestAProductCannotHaveTwoSpecsWithTheSameIdentity(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	slug := admintest.DraftProduct(t, ctx, pool, s)

	if errs, err := s.AddSpec(ctx, slug, products.SpecDraft{
		Label: "連接埠", Value: "USB-C",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("first AddSpec: %v %v", err, errs)
	}
	errs, err := s.AddSpec(ctx, slug, products.SpecDraft{
		Label: "連接埠", Value: "HDMI",
	})
	if err != nil {
		t.Fatalf("duplicate AddSpec returned an infrastructure error: %v", err)
	}
	if errs["spec_label"] == "" {
		t.Fatalf("duplicate AddSpec errors = %v, want spec_label", errs)
	}

	var rows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM product_specs s
		JOIN products p ON p.id = s.product_id
		WHERE p.slug = $1 AND s.label = '連接埠'`, slug).Scan(&rows); err != nil {
		t.Fatalf("count duplicate specs: %v", err)
	}
	if rows != 1 {
		t.Errorf("product has %d specs named 連接埠, want 1; Compare would overwrite a cell", rows)
	}
}

func TestTheShopCanGiveAProductAVariantPicker(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	slug := admintest.DraftProduct(t, ctx, pool, s)

	if errs, err := s.AddOption(ctx, slug, products.OptionDraft{
		Name: "顏色", NameEn: "Colour",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("AddOption: %v %v", err, errs)
	}
	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if !view.HasOptions() || view.Options[0].Name != "顏色" {
		t.Fatalf("the product's options are %+v", view.Options)
	}
	if view.Options[0].HasValues() {
		t.Error("a new option already has values")
	}

	optionID := view.Options[0].ID
	for _, v := range []struct{ value, label string }{
		{"星霧藍", "Mist Blue"},
		{"曜石黑", ""},
	} {
		if errs, addErr := s.AddOptionValue(ctx, slug, products.OptionDraft{
			OptionID: optionID, Name: v.value, NameEn: v.label,
		}); addErr != nil || len(errs) > 0 {
			t.Fatalf("AddOptionValue(%s): %v %v", v.value, addErr, errs)
		}
	}

	view, err = s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if len(view.Options[0].Values) != 2 {
		t.Fatalf("the axis has %d values, want 2", len(view.Options[0].Values))
	}
	if view.Options[0].Values[0].Value != "星霧藍" ||
		view.Options[0].Values[0].Label != "Mist Blue" {
		t.Errorf("the first value is %+v", view.Options[0].Values[0])
	}
	if view.Options[0].Values[1].Label != "" {
		t.Errorf("an untranslated value reports label %q",
			view.Options[0].Values[1].Label)
	}

	if errs, addErr := s.AddVariant(ctx, slug, &products.VariantForm{
		SKU: "PICKER-NONE", PriceCents: 100000,
	}); addErr != nil {
		t.Fatalf("AddVariant: %v", addErr)
	} else if errs["options"] == "" {
		t.Errorf("a variant naming no option value was accepted: %v", errs)
	}

	if errs, addErr := s.AddVariant(ctx, slug, &products.VariantForm{
		SKU: "PICKER-BLUE", PriceCents: 100000,
		OptionValues: []string{view.Options[0].Values[0].ID},
	}); addErr != nil || len(errs) > 0 {
		t.Fatalf("AddVariant: %v %v", addErr, errs)
	}

	after, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	var found bool
	for _, sv := range after.Variants {
		if sv.SKU != "PICKER-BLUE" {
			continue
		}
		found = true
		if sv.OptionText() != "星霧藍" {
			t.Errorf("the variant list shows %q for its options", sv.OptionText())
		}
	}
	if !found {
		t.Error("the new variant is not in the product's variant list")
	}
}

// A colour is a shape, not a word, and the shop finds that out at the field
// rather than from a refused write. The column's CHECK is the last word; this
// is the first one, and the two have to agree or the page 500s on a typo.
func TestAMistypedColourComesBackBesideTheField(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	h := admintest.ProductDesk(pool, s)
	slug := admintest.DraftProduct(t, ctx, pool, s)

	if errs, err := s.AddOption(ctx, slug, products.OptionDraft{
		Name: "顏色", NameEn: "Colour",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("AddOption: %v %v", err, errs)
	}
	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	optionID := view.Options[0].ID

	refused := []struct {
		name string
		raw  string
	}{
		{name: "no hash", raw: "1c1c1e"},
		{name: "three digits", raw: "#abc"},
		{name: "not hexadecimal", raw: "#1c1c1g"},
		{name: "a colour name", raw: "black"},
		{name: "too long", raw: "#1c1c1e0"},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			res := postOptionValueForm(t, h, ctx, slug, url.Values{
				"option":     {optionID},
				"value":      {"色碼測試 " + uuid.NewString()[:8]},
				"value_en":   {""},
				"swatch_hex": {tt.raw},
			})
			if res.Code != http.StatusUnprocessableEntity {
				t.Fatalf("AddOptionValue(swatch_hex=%q) status = %d, want 422", tt.raw, res.Code)
			}
			body := res.Body.String()
			input := admintest.InputElementByID(t, body, "optval-swatch")
			if got := admintest.InputAttribute(t, input, "aria-invalid"); got != "true" {
				t.Errorf("colour field aria-invalid = %q, want true", got)
			}
			if got := admintest.InputAttribute(t, input, "aria-describedby"); got != "optval-swatch-error" {
				t.Errorf("colour field aria-describedby = %q, want the error's id", got)
			}
			if !regexp.MustCompile(`<p[^>]*id="optval-swatch-error"[^>]*>[^<]+</p>`).MatchString(body) {
				t.Error("the refusal names no reason beside the colour field")
			}
			if strings.Contains(admintest.InputElementByID(t, body, "optval-value"), `aria-invalid`) {
				t.Error("a bad colour marked the value field invalid too")
			}
		})
	}

	// The spelling is not the shape. A shop that types the other case is
	// storing the same colour, so this one is accepted and lower-cased.
	value := "曜石黑 " + uuid.NewString()[:8]
	res := postOptionValueForm(t, h, ctx, slug, url.Values{
		"option":     {optionID},
		"value":      {value},
		"value_en":   {""},
		"swatch_hex": {"#1C1C1E"},
	})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("AddOptionValue(swatch_hex=%q) status = %d, want 303", "#1C1C1E", res.Code)
	}
	var stored string
	if err := pool.QueryRow(ctx,
		`SELECT swatch_hex FROM product_option_values WHERE option_id = $1::uuid AND value = $2`,
		optionID, value).Scan(&stored); err != nil {
		t.Fatalf("read the stored colour: %v", err)
	}
	if stored != "#1c1c1e" {
		t.Errorf("stored colour = %q, want the lower-cased spelling", stored)
	}
}

func postOptionValueForm(
	t *testing.T, h *products.Handler, ctx context.Context, slug string, form url.Values,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/products/"+slug+"/options/values", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	res := httptest.NewRecorder()
	h.AddOptionValue(res, req)
	return res
}

func TestAVariantCannotBorrowAnotherProductsOptionValue(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)

	first := admintest.DraftProduct(t, ctx, pool, s)
	if errs, err := s.AddOption(ctx, first, products.OptionDraft{Name: "顏色"}); err != nil ||
		len(errs) > 0 {
		t.Fatalf("AddOption: %v %v", err, errs)
	}
	firstView, err := s.Product(ctx, first)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if errs, addErr := s.AddOptionValue(ctx, first, products.OptionDraft{
		OptionID: firstView.Options[0].ID, Name: "星霧藍",
	}); addErr != nil || len(errs) > 0 {
		t.Fatalf("AddOptionValue: %v %v", addErr, errs)
	}
	firstView, err = s.Product(ctx, first)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	borrowed := firstView.Options[0].Values[0].ID

	second := admintest.DraftProduct(t, ctx, pool, s)
	if errs, addErr := s.AddOption(ctx, second, products.OptionDraft{Name: "顏色"}); addErr != nil ||
		len(errs) > 0 {
		t.Fatalf("AddOption: %v %v", addErr, errs)
	}

	errs, addErr := s.AddVariant(ctx, second, &products.VariantForm{
		SKU: "BORROW-1", PriceCents: 100000, OptionValues: []string{borrowed},
	})
	if addErr != nil {
		t.Fatalf("AddVariant: %v", addErr)
	}
	if errs["options"] == "" {
		t.Errorf("a variant borrowing another product's value was accepted: %v", errs)
	}
	var variants int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM product_variants WHERE sku = 'BORROW-1'`).Scan(&variants); err != nil {
		t.Fatalf("count variants: %v", err)
	}
	if variants != 0 {
		t.Errorf("%d variants named BORROW-1 survived the refusal", variants)
	}
}

func TestTheOptionValueIsAddedToTheRightProduct(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)

	first := admintest.DraftProduct(t, ctx, pool, s)
	if errs, err := s.AddOption(ctx, first, products.OptionDraft{Name: "顏色"}); err != nil ||
		len(errs) > 0 {
		t.Fatalf("AddOption: %v %v", err, errs)
	}
	view, err := s.Product(ctx, first)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}

	second := admintest.DraftProduct(t, ctx, pool, s)
	errs, err := s.AddOptionValue(ctx, second, products.OptionDraft{
		OptionID: view.Options[0].ID, Name: "星霧藍",
	})
	if err != nil {
		t.Fatalf("AddOptionValue: %v", err)
	}
	if errs["value"] == "" {
		t.Errorf("a value was added to another product's axis: %v", errs)
	}
}

func TestAltTextFollowsThePagesLanguage(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	slug := admintest.DraftProduct(t, ctx, pool, s)
	digest := storeMedia(t)

	if err := s.AttachImage(ctx, slug, digest, "銀色筆電,螢幕開啟",
		"Silver laptop, screen open", "", 800, 600); err != nil {
		t.Fatalf("AttachImage: %v", err)
	}

	for _, tt := range []struct {
		name   string
		locale string
		want   string
	}{
		{name: "a Chinese page", locale: "zh-Hant", want: "銀色筆電,螢幕開啟"},
		{name: "an English page", locale: "en", want: "Silver laptop, screen open"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			if err := pool.QueryRow(ctx, `
				SELECT localized_name(i.alt_text, i.alt_text_en, $2)
				FROM product_images i JOIN products p ON p.id = i.product_id
				WHERE p.slug = $1`, slug, tt.locale).Scan(&got); err != nil {
				t.Fatalf("read the alt text: %v", err)
			}
			if got != tt.want {
				t.Errorf("the %s announces %q, want %q", tt.name, got, tt.want)
			}
		})
	}

	second := admintest.DraftProduct(t, ctx, pool, s)
	if err := s.AttachImage(ctx, second, digest, "沒有英文說明", "", "", 800, 600); err != nil {
		t.Fatalf("AttachImage without English: %v", err)
	}
	var fallback string
	if err := pool.QueryRow(ctx, `
		SELECT localized_name(i.alt_text, i.alt_text_en, 'en')
		FROM product_images i JOIN products p ON p.id = i.product_id
		WHERE p.slug = $1`, second).Scan(&fallback); err != nil {
		t.Fatalf("read the alt text: %v", err)
	}
	if fallback != "沒有英文說明" {
		t.Errorf("an untranslated image announces %q, want the Chinese text", fallback)
	}
}

func storeMedia(t *testing.T) string {
	t.Helper()
	digest := fmt.Sprintf("%064x", uuid.New().ID())
	if _, err := pool.Exec(t.Context(), `
		-- byte_size must equal length(bytes): media_objects_size_matches refuses a
		-- row that claims a size its own bytes do not have.
		INSERT INTO media_objects (digest, content_type, byte_size, width, height, bytes)
		VALUES ($1, 'image/png', 1, 800, 600, '\x00'::bytea)`, digest); err != nil {
		t.Fatalf("store the image: %v", err)
	}
	return digest
}

func TestAMistypedWarrantyTermIsRefusedNotDropped(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	slug := admintest.DraftProduct(t, ctx, pool, s)
	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if errs, updateErr := s.Update(ctx, &products.Form{
		Slug: slug, Name: view.Name, Summary: view.Summary, Description: view.Description,
		BrandID: view.BrandID, CategoryID: view.CategoryID, WarrantyMonths: 24,
	}); updateErr != nil || len(errs) > 0 {
		t.Fatalf("seed warranty term: %v %v", updateErr, errs)
	}

	form := url.Values{
		"name":            {view.Name},
		"summary":         {view.Summary},
		"description":     {view.Description},
		"brand":           {view.BrandID},
		"category":        {view.CategoryID},
		"warranty_months": {"12o"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/products/"+slug, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	res := httptest.NewRecorder()
	admintest.ProductDesk(pool, s).Update(res, req)
	if res.Code != http.StatusUnprocessableEntity {
		t.Errorf("UpdateProduct(warranty_months=%q) status = %d, want 422", "12o", res.Code)
	} else {
		body := res.Body.String()
		admintest.AssertRefusedInput(t, body, "p-warranty-months", "12o")
		admintest.AssertTextNumberControl(t, body, "p-warranty-months")
	}

	var months *int32
	if err := pool.QueryRow(ctx,
		`SELECT warranty_months FROM products WHERE slug = $1`, slug).Scan(&months); err != nil {
		t.Fatalf("read warranty after refusal: %v", err)
	}
	if months == nil || *months != 24 {
		got := "NULL"
		if months != nil {
			got = strconv.FormatInt(int64(*months), 10)
		}
		t.Errorf("warranty_months after mistyped edit = %s, want 24", got)
	}
}

func TestAParcelSumCannotBeShorterThanItsLongestSide(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	slug := admintest.DraftProduct(t, ctx, pool, s)
	sku := "SHORT-SUM-" + strings.ToUpper(uuid.NewString()[:8])
	form := url.Values{
		"sku": {sku}, "price": {"1000"}, "safety": {"0"},
		"parcel_longest": {"500"}, "parcel_sum": {"499"}, "parcel_weight": {"1000"},
	}
	res := admintest.PostVariantForm(t, admintest.ProductDesk(pool, s), ctx, slug, form)
	if res.Code != http.StatusUnprocessableEntity {
		t.Errorf("AddVariant(parcel_sum < parcel_longest) status = %d, want 422", res.Code)
	} else {
		admintest.AssertRefusedInput(t, res.Body.String(), "v-sum", "499")
	}
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM product_variants WHERE sku = $1`, sku).Scan(&count); err != nil {
		t.Fatalf("count short-sum variant: %v", err)
	}
	if count != 0 {
		t.Errorf("AddVariant(parcel_sum < parcel_longest) inserted %d rows", count)
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO product_variants
			(product_id, sku, price_cents, safety_stock, parcel_longest_mm, parcel_sum_mm)
		SELECT id, $2, 100000, 0, 500, 499 FROM products WHERE slug = $1`,
		slug, "DIRECT-SHORT-"+strings.ToUpper(uuid.NewString()[:8]))
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok ||
		pgErr.ConstraintName != "product_variants_parcel_sum_covers_longest" {
		t.Errorf("direct short parcel sum error = %v, want constraint %q", err,
			"product_variants_parcel_sum_covers_longest")
	}
}

func TestTheShopSetsEachProductsWarrantyTerm(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	slug := admintest.DraftProduct(t, ctx, pool, s)

	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if view.WarrantyMonths != 0 || view.WarrantyMonthsText() != "" {
		t.Errorf("a new product reports %d months (%q)",
			view.WarrantyMonths, view.WarrantyMonthsText())
	}

	form := &products.Form{
		Slug: slug, Name: view.Name, Summary: view.Summary,
		Description: view.Description, BrandID: view.BrandID,
		CategoryID: view.CategoryID, WarrantyMonths: 24,
	}
	if errs, updErr := s.Update(ctx, form); updErr != nil || len(errs) > 0 {
		t.Fatalf("UpdateProduct: %v %v", updErr, errs)
	}

	after, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if after.WarrantyMonths != 24 {
		t.Errorf("the product states %d months, want 24", after.WarrantyMonths)
	}

	form.WarrantyMonths = 0
	if errs, updErr := s.Update(ctx, form); updErr != nil || len(errs) > 0 {
		t.Fatalf("UpdateProduct: %v %v", updErr, errs)
	}
	var months *int32
	if scanErr := pool.QueryRow(ctx,
		`SELECT warranty_months FROM products WHERE slug = $1`, slug).Scan(&months); scanErr != nil {
		t.Fatalf("read the term: %v", scanErr)
	}
	if months != nil {
		t.Errorf("clearing the term left %d", *months)
	}

	form.WarrantyMonths = products.MaxWarrantyMonths + 1
	errs, err := s.Update(ctx, form)
	if err != nil {
		t.Fatalf("UpdateProduct: %v", err)
	}
	if errs["warranty_months"] == "" {
		t.Errorf("a %d-month term was accepted: %v", form.WarrantyMonths, errs)
	}
}
