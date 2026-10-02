//go:build integration

package admin_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/product"
)

func TestTheShopCanPublishAnUnbrandedProduct(t *testing.T) {
	p := isolatedAdminSeedPool(t)
	ctx, actor := staffContextOn(t, p)
	store := admin.NewStore(p, fakeRefunder{}, nil, nil)
	var categoryID, categorySlug string
	if readErr := p.QueryRow(ctx, `SELECT id::text, slug FROM categories WHERE parent_id IS NOT NULL LIMIT 1`).Scan(&categoryID, &categorySlug); readErr != nil {
		t.Fatal(readErr)
	}
	form := &admin.ProductForm{Slug: "generic-" + uuid.NewString()[:8], Name: "Generic item", Description: "Unbranded fixture", CategoryID: categoryID}
	slug, errs, err := store.CreateProduct(ctx, form)
	if err != nil || len(errs) != 0 {
		t.Fatalf("create unbranded product = %v, %v", err, errs)
	}
	sku := "GENERIC-" + uuid.NewString()[:8]
	if variantErrs, variantErr := store.AddVariant(ctx, slug, &admin.VariantForm{SKU: sku, PriceCents: 10000}); variantErr != nil || len(variantErrs) != 0 {
		t.Fatalf("add variant = %v, %v", variantErr, variantErrs)
	}
	if publishErr := store.SetProductStatus(ctx, slug, "active"); publishErr != nil {
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
	search, err := catalog.NewStore(p).Search(ctx, form.Name, catalog.SortRelevance, 1)
	if err != nil || len(search.Products) != 1 || search.Products[0].Slug != slug || search.Products[0].Brand != "" {
		t.Errorf("unbranded search = %+v, %v", search.Products, err)
	}
	pdp, err := product.NewStore(p).Load(ctx, slug, nil)
	if err != nil || pdp.Brand != "" || pdp.Slug != slug {
		t.Errorf("unbranded PDP = slug %q brand %q, %v", pdp.Slug, pdp.Brand, err)
	}
	if stockErr := store.AdjustStock(ctx, sku, 5, actor.String(), "generic-stock-"+uuid.NewString()); stockErr != nil {
		t.Fatal(stockErr)
	}
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
	p := isolatedAdminSeedPool(t)
	ctx, _ := staffContextOn(t, p)
	store := admin.NewStore(p, fakeRefunder{}, nil, nil)
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
	adminHandlerOver(p, store).UpdateProduct(res, req)
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
	if updateErrs, updateErr := store.UpdateProduct(ctx, &admin.ProductForm{Slug: slug, Name: view.Name, Description: view.Description, CategoryID: view.CategoryID}); updateErr != nil || len(updateErrs) != 0 {
		t.Fatalf("clear brand = %v, %v", updateErr, updateErrs)
	}
	var cleared, audited bool
	if readErr := p.QueryRow(ctx, `
		SELECT p.brand_id IS NULL, EXISTS (
			SELECT 1 FROM audit_events WHERE action = $2 AND after->>'slug' = p.slug
			AND after ? 'brand_id' AND after->'brand_id' = 'null'::jsonb)
		FROM products p WHERE slug = $1`, slug, string(admin.ActionUpdateProduct)).Scan(&cleared, &audited); readErr != nil {
		t.Fatal(readErr)
	}
	if !cleared || !audited {
		t.Errorf("cleared brand absent=%t audited=%t, want both true", cleared, audited)
	}
}
