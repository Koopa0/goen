//go:build integration

package products_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/product"
)

func TestTheShopCanPublishAnUnbrandedProduct(t *testing.T) {
	p := admintest.Pool(t)
	ctx, actor := admintest.StaffContext(t, p)
	store := products.NewStore(p)
	var categoryID, categorySlug string
	if readErr := p.QueryRow(ctx, `SELECT id::text, slug FROM categories WHERE parent_id IS NOT NULL LIMIT 1`).Scan(&categoryID, &categorySlug); readErr != nil {
		t.Fatal(readErr)
	}
	form := &products.Form{Slug: "generic-" + uuid.NewString()[:8], Name: "Generic item", Description: "Unbranded fixture", CategoryID: categoryID}
	slug, errs, err := store.Create(ctx, form)
	if err != nil || len(errs) != 0 {
		t.Fatalf("create unbranded product = %v, %v", err, errs)
	}
	variant := &products.VariantForm{SKU: "GENERIC-" + uuid.NewString()[:8], PriceCents: 10000, CompareCents: 20000}
	if variantErrs, variantErr := store.AddVariant(ctx, slug, variant); variantErr != nil || len(variantErrs) != 0 {
		t.Fatalf("add variant = %v, %v", variantErr, variantErrs)
	}
	sku := variant.SKU
	if publishErr := store.SetStatus(ctx, slug, "active"); publishErr != nil {
		t.Fatalf("publish unbranded product: %v", publishErr)
	}
	var absent, audited bool
	if stateErr := p.QueryRow(ctx, `
		SELECT p.brand_id IS NULL, EXISTS (
			SELECT 1 FROM audit_events WHERE action = $2 AND after->>'slug' = p.slug
			AND after ? 'brand_id' AND after->'brand_id' = 'null'::jsonb)
		FROM products p WHERE slug = $1`, slug, "product.create").Scan(&absent, &audited); stateErr != nil {
		t.Fatal(stateErr)
	}
	if !absent || !audited {
		t.Errorf("unbranded product absent=%t audited=%t, want both true", absent, audited)
	}
	view, err := store.Product(ctx, slug)
	if err != nil || view.BrandID != "" {
		t.Fatalf("admin unbranded read = brand %q, %v", view.BrandID, err)
	}
	listing, err := catalog.NewStore(p).Listing(ctx, categorySlug, catalog.Filters{Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tile := range listing.Products {
		if tile.Slug == slug {
			found = true
			if tile.Brand != "" {
				t.Errorf("listing invented brand %q", tile.Brand)
			}
		}
	}
	if !found {
		t.Error("category listing dropped the unbranded product")
	}
	search, err := catalog.NewStore(p).Search(ctx, catalog.SearchPattern(form.Name), catalog.SortRelevance, 1)
	if err != nil || search.Total != 1 || len(search.Products) != 1 || search.Products[0].Slug != slug || search.Products[0].Brand != "" {
		t.Errorf("unbranded search = %+v, %v", search.Products, err)
	}
	pdp, err := product.NewStore(p).Load(ctx, slug, nil)
	if err != nil || pdp.Brand != "" || pdp.Slug != slug {
		t.Errorf("unbranded PDP = slug %q brand %q, %v", pdp.Slug, pdp.Brand, err)
	}
	if stockErr := stock.NewStore(p).Adjust(ctx, sku, 5, actor.String(), "generic-stock-"+uuid.NewString()); stockErr != nil {
		t.Fatal(stockErr)
	}
	assertUnbrandedCatalogueReads(t, ctx, p, slug, sku, uuid.MustParse(categoryID))
	var variantID uuid.UUID
	if variantErr := p.QueryRow(ctx, `SELECT id FROM product_variants WHERE sku = $1`, sku).Scan(&variantID); variantErr != nil {
		t.Fatal(variantErr)
	}
	carts := cart.NewStore(p)
	cartID, err := carts.Create(ctx, uuid.NewString(), uuid.NullUUID{UUID: actor, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	if addErr := carts.Add(ctx, cartID, variantID, 1); addErr != nil {
		t.Fatal(addErr)
	}
	cartView, err := carts.View(ctx, cartID)
	if err != nil || len(cartView.Lines) != 1 || cartView.Lines[0].Slug != slug || cartView.Lines[0].Brand != "" {
		t.Errorf("unbranded cart = %+v, %v", cartView.Lines, err)
	}
	accounts := account.NewStore(p)
	if wishlistErr := accounts.SaveToWishlist(ctx, actor.String(), slug); wishlistErr != nil {
		t.Fatal(wishlistErr)
	}
	wishlist, err := accounts.Wishlist(ctx, actor.String())
	if err != nil || len(wishlist) != 1 || wishlist[0].Slug != slug || wishlist[0].Brand != "" {
		t.Errorf("unbranded wishlist = %+v, %v", wishlist, err)
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var orderID uuid.UUID
	var number string
	if readErr := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id, shipping_method_code, shipping_method_name)
		SELECT next_order_number(), $1, v.id, sm.code, v.name
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1 RETURNING id, order_number`, actor).Scan(&orderID, &number); readErr != nil {
		t.Fatal(readErr)
	}
	if _, writeErr := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, $2, $3, $4, 10000, 1)`, orderID, variantID, sku, form.Name); writeErr != nil {
		t.Fatal(writeErr)
	}
	if _, writeErr := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
		VALUES ($1, 'generic@example.com', 'Fixture', '0912345678', '110', '台北市', '信義區', 'Fixture street')`, orderID); writeErr != nil {
		t.Fatal(writeErr)
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		t.Fatal(commitErr)
	}
	order, err := carts.Order(ctx, number)
	if err != nil || len(order.Lines) != 1 || order.Lines[0].SKU != sku {
		t.Errorf("unbranded order = %+v, %v", order.Lines, err)
	}
	history, err := accounts.Overview(ctx, account.User{ID: actor.String()})
	if err != nil || len(history.Orders) != 1 || history.Orders[0].Number != number {
		t.Errorf("unbranded order history = %+v, %v", history.Orders, err)
	}
	comparison, err := catalog.NewStore(p).Compare(ctx, []string{slug})
	if err != nil || len(comparison.Products) != 1 || comparison.Products[0].Brand != "" {
		t.Errorf("unbranded comparison = %+v, %v", comparison.Products, err)
	}
}

func TestClearingAProductBrandSurvivesARefusedEdit(t *testing.T) {
	p := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, p)
	store := products.NewStore(p)
	var slug, brandID string
	if readErr := p.QueryRow(ctx, `SELECT slug, brand_id::text FROM products WHERE brand_id IS NOT NULL LIMIT 1`).Scan(&slug, &brandID); readErr != nil {
		t.Fatal(readErr)
	}
	view, err := store.Product(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{"name": {view.Name}, "description": {view.Description}, "category": {view.CategoryID}, "brand": {""}, "warranty_months": {"mistyped"}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/products/"+slug, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	res := httptest.NewRecorder()
	admintest.ProductDesk(p, store).Update(res, req)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("refused edit status = %d, want 422", res.Code)
	}
	doc, err := html.Parse(strings.NewReader(res.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	var selected []string
	var firstChoice string
	hasOption := false
	var visit func(*html.Node, bool)
	visit = func(n *html.Node, brandSelect bool) {
		attrs := map[string]string{}
		for _, attr := range n.Attr {
			attrs[attr.Key] = attr.Val
		}
		if n.Data == "select" {
			brandSelect = attrs["id"] == "p-brand"
		}
		if n.Data == "option" && brandSelect {
			if !hasOption {
				firstChoice, hasOption = attrs["value"], true
			}
			if _, ok := attrs["selected"]; ok {
				selected = append(selected, attrs["value"])
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child, brandSelect)
		}
	}
	visit(doc, false)
	choice := firstChoice
	if len(selected) > 0 {
		choice = selected[len(selected)-1]
	}
	if !hasOption || choice != "" {
		t.Errorf("422 effective brand choice = %q, want the submitted no-brand choice", choice)
	}
	var persisted string
	if readErr := p.QueryRow(ctx, `SELECT brand_id::text FROM products WHERE slug = $1`, slug).Scan(&persisted); readErr != nil {
		t.Fatal(readErr)
	}
	if persisted != brandID {
		t.Error("refused form changed the persisted brand")
	}
	if updateErrs, updateErr := store.Update(ctx, &products.Form{Slug: slug, Name: view.Name, Description: view.Description, CategoryID: view.CategoryID}); updateErr != nil || len(updateErrs) != 0 {
		t.Fatalf("clear brand = %v, %v", updateErr, updateErrs)
	}
	var cleared, audited bool
	if readErr := p.QueryRow(ctx, `
		SELECT p.brand_id IS NULL, EXISTS (
			SELECT 1 FROM audit_events WHERE action = $2 AND after->>'slug' = p.slug
			AND after ? 'brand_id' AND after->'brand_id' = 'null'::jsonb)
		FROM products p WHERE slug = $1`, slug, string(audit.ActionUpdateProduct)).Scan(&cleared, &audited); readErr != nil {
		t.Fatal(readErr)
	}
	if !cleared || !audited {
		t.Errorf("cleared brand absent=%t audited=%t, want both true", cleared, audited)
	}
}

func assertUnbrandedCatalogueReads(t *testing.T, ctx context.Context, p *pgxpool.Pool, slug, sku string, categoryID uuid.UUID) {
	t.Helper()
	var productID, anchorID, campaignID uuid.UUID
	anchorSlug := "related-" + uuid.NewString()[:8]
	if err := p.QueryRow(ctx, `SELECT id FROM products WHERE slug = $1`, slug).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `INSERT INTO products (brand_id, category_id, slug, name, status)
		SELECT id, $1, $2, 'Related fixture', 'draft' FROM brands LIMIT 1 RETURNING id`, categoryID, anchorSlug).Scan(&anchorID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `INSERT INTO product_variants (product_id, sku, price_cents)
		VALUES ($1, $2, 10000)`, anchorID, "RELATED-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	if err := products.NewStore(p).SetStatus(ctx, anchorSlug, "active"); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `INSERT INTO sale_campaigns (slug, title, ends_at)
		VALUES ($1, 'Campaign fixture', now() + interval '1 day') RETURNING id`, "generic-"+uuid.NewString()[:8]).Scan(&campaignID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `INSERT INTO sale_campaign_products (campaign_id, product_id) VALUES ($1, $2)`, campaignID, productID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `INSERT INTO product_copurchases (product_id, other_product_id, orders) VALUES ($1, $2, $3)`, anchorID, productID, product.MinCoPurchases); err != nil {
		t.Fatal(err)
	}
	var rowLimit int32
	if err := p.QueryRow(ctx, `SELECT (count(*) + 1)::integer FROM products`).Scan(&rowLimit); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	observe := func(read, candidateSlug, brand string) {
		if candidateSlug == slug {
			found[read] = true
			if brand != "" {
				t.Errorf("%s invented brand %q for the unbranded product", read, brand)
			}
		}
	}
	check := func(read string, err error) {
		if err != nil {
			t.Fatalf("%s: %v", read, err)
		}
	}
	q := db.New(p)
	newest, err := q.NewestProducts(ctx, db.NewestProductsParams{Locale: "en", PageSize: rowLimit})
	check("NewestProducts", err)
	for i := range newest {
		row := &newest[i]
		observe("NewestProducts", row.Slug, row.Brand)
	}
	deals, err := q.DealProducts(ctx, db.DealProductsParams{Locale: "en", PageSize: rowLimit})
	check("DealProducts", err)
	for i := range deals {
		row := &deals[i]
		observe("DealProducts", row.Slug, row.Brand)
	}
	campaign, err := q.CampaignProducts(ctx, db.CampaignProductsParams{Locale: "en", CampaignID: campaignID})
	check("CampaignProducts", err)
	for i := range campaign {
		row := &campaign[i]
		observe("CampaignProducts", row.Slug, row.Brand)
	}
	suggestions, err := q.CompareSuggestions(ctx, db.CompareSuggestionsParams{Locale: "en", ProductSlug: anchorSlug, ExcludeSlugs: []string{anchorSlug}, AnchorCents: 10000, RowLimit: rowLimit})
	check("CompareSuggestions", err)
	for _, row := range suggestions {
		observe("CompareSuggestions", row.Slug, row.Brand)
	}
	related, err := q.RelatedProducts(ctx, db.RelatedProductsParams{Locale: "en", CategoryID: categoryID, ExcludeID: anchorID, RowLimit: rowLimit})
	check("RelatedProducts", err)
	for i := range related {
		row := &related[i]
		observe("RelatedProducts", row.Slug, row.Brand)
	}
	also, err := q.BoughtTogether(ctx, db.BoughtTogetherParams{Locale: "en", ProductID: anchorID, MinOrders: product.MinCoPurchases, LimitTo: rowLimit})
	check("BoughtTogether", err)
	for i := range also {
		row := &also[i]
		observe("BoughtTogether", row.Slug, row.Brand)
	}
	home, err := q.HomeTiles(ctx, db.HomeTilesParams{Locale: "en", MaxTiles: rowLimit})
	check("HomeTiles", err)
	for i := range home {
		row := &home[i]
		observe("HomeTiles", row.Slug, row.Brand)
	}
	adminProducts, err := q.AdminProducts(ctx, db.AdminProductsParams{RowLimit: rowLimit})
	check("AdminProducts", err)
	for i := range adminProducts {
		row := &adminProducts[i]
		observe("AdminProducts", row.Slug, row.Brand)
	}
	variants, err := stock.NewStore(p).Variants(ctx, false, sku)
	check("AdminVariants", err)
	for i := range variants.Variants {
		row := &variants.Variants[i]
		observe("AdminVariants", row.Slug, row.Brand)
	}
	for _, read := range []string{"NewestProducts", "DealProducts", "CampaignProducts", "CompareSuggestions", "RelatedProducts", "BoughtTogether", "HomeTiles", "AdminProducts", "AdminVariants"} {
		if !found[read] {
			t.Errorf("%s dropped the unbranded product", read)
		}
	}
}
