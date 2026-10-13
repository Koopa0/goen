//go:build integration

package orders_test

import (
	"bytes"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/content"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/admin/returns"
	"github.com/koopa0/goen/internal/admin/shipping"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/admin/taxonomy"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
)

// Actual plain POST refusals, across the desks that share the navigation
// contract. The raw drafts include blanks, whitespace and unreadable numbers.
func TestDeskRefusalPOSTsKeepDraftsAndLocateTheirForm(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	log := slog.New(slog.DiscardHandler)
	images := media.NewHandler(media.NewStore(pool), log)
	mux := http.NewServeMux()
	ac := access.New(log, func(*http.Request) (bool, error) { return true, nil })
	productStore := products.NewStore(pool)
	admintest.ProductDesk(pool, productStore).Routes(mux, ac)
	stock.NewHandler(stock.NewStore(pool), log).Routes(mux, ac)
	contentStore := content.NewStore(pool)
	content.NewHandler(contentStore, images, newsletter.NewStore(pool), log).Routes(mux, ac)
	taxonomy.NewHandler(taxonomy.NewStore(pool), images, log).Routes(mux, ac)
	shipping.NewHandler(shipping.NewStore(pool), false, log).Routes(mux, ac)
	returns.NewHandler(admintest.ReturnDesk(pool, admintest.Refunder{}), log).Routes(mux, ac)

	slug := admintest.DraftProduct(t, ctx, pool, productStore)
	var sku, category string
	if err := pool.QueryRow(ctx, `SELECT sku FROM product_variants WHERE is_active LIMIT 1`).Scan(&sku); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM categories LIMIT 1`).Scan(&category); err != nil {
		t.Fatal(err)
	}
	faqCategory := "refusal-" + uuid.NewString()
	if errs, err := contentStore.CreateFAQEntry(ctx, &content.FAQForm{Category: faqCategory[:30], Question: "original question", Answer: "original answer"}); err != nil || len(errs) > 0 {
		t.Fatalf("FAQ fixture: %v %v", err, errs)
	}
	faq, err := contentStore.FAQ(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var faqID string
	for _, row := range faq.Rows {
		if row.Category == faqCategory[:30] {
			faqID = row.ID
		}
	}
	if faqID == "" {
		t.Fatal("FAQ fixture is not listed")
	}
	returnID := admintest.PreapprovedReturn(t, pool).String()
	var lineID string
	if err := pool.QueryRow(ctx, `SELECT order_line_id::text FROM return_request_lines WHERE return_request_id=$1`, returnID).Scan(&lineID); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, path, anchor, first string
		form                      url.Values
		kept                      map[string]string
		multipart                 bool
	}{
		{"stock", "/admin/stock/adjust", "row-" + sku, "adj-" + sku, url.Values{"sku": {sku}, "delta": {" +oops "}, "idempotency": {uuid.NewString()}, "return": {"/admin/stock?q=" + sku}}, map[string]string{"adj-" + sku: " +oops "}, false},
		{"details", "/admin/products/" + slug, "sec-details", "p-name", url.Values{"name": {"   "}, "summary": {" kept summary "}, "description": {"kept description"}, "category": {category}}, map[string]string{"p-name": "   ", "p-summary": " kept summary "}, false},
		{"variant", "/admin/products/" + slug + "/variants", "sec-variants", "v-sku", url.Values{"sku": {"bad sku"}, "price": {" +oops "}, "compare": {""}, "safety": {"0"}, "parcel_longest": {"0"}, "parcel_sum": {"0"}, "parcel_weight": {"0"}}, map[string]string{"v-sku": "bad sku", "v-price": " +oops "}, false},
		{"spec", "/admin/products/" + slug + "/specs", "sec-specs", "spec-label", url.Values{"label": {"   "}, "value": {" kept value "}, "label_en": {"kept label"}}, map[string]string{"spec-label": "   ", "spec-value": " kept value "}, false},
		{"brand", "/admin/taxonomy/brands", "new-brands", "brands-name", url.Values{"name": {"   "}, "slug": {"brand-" + uuid.NewString()}}, map[string]string{"brands-name": "   "}, false},
		{"category", "/admin/taxonomy/categories", "new-categories", "categories-name", url.Values{"name": {"   "}, "slug": {"category-" + uuid.NewString()}, "icon_key": {"unknown-icon"}, "tone": {"invalid-tone"}}, map[string]string{"categories-name": "   ", "categories-icon": "unknown-icon", "categories-tone": "invalid-tone"}, false},
		{"faq", "/admin/faq/" + faqID, "faq-" + faqID, "q-" + faqID, url.Values{"action": {"save"}, "category": {faqCategory[:30]}, "question": {"   "}, "answer": {" kept answer "}}, map[string]string{"q-" + faqID: "   ", "a-" + faqID: " kept answer "}, false},
		{"hero", "/admin/home", "new-hero", "h-headline", url.Values{"headline": {"   "}, "body": {" kept body "}, "primary_label": {"Go"}, "primary_href": {"/about"}, "days": {"0"}}, map[string]string{"h-headline": "   ", "h-body": " kept body "}, true},
		{"banner", "/admin/home/banner", "new-banner", "b-message", url.Values{"message": {"   "}, "short": {" kept short "}, "days": {"0"}}, map[string]string{"b-message": "   ", "b-short": " kept short "}, false},
		{"method", "/admin/shipping/method", "new-method", "m-code", url.Values{"code": {"bad code"}, "destination": {"address"}, "name": {" kept name "}, "fee": {" +oops "}, "free_over": {""}, "max_parcel_longest": {"0"}, "max_parcel_sum": {"0"}, "max_parcel_weight": {"0"}}, map[string]string{"m-code": "bad code", "m-fee": " +oops ", "m-name": " kept name "}, false},
		{"zone", "/admin/shipping/zone", "new-zone", "z-code", url.Values{"code": {"bad code"}, "name": {" kept name "}, "prefixes": {"bogus"}}, map[string]string{"z-code": "bad code", "z-name": " kept name ", "z-prefixes": "bogus"}, false},
		{"inspection", "/admin/returns/" + returnID + "/inspect", "inspect-" + returnID, "recv-" + returnID + "-" + lineID, url.Values{"received_" + lineID: {" +oops "}, "note_" + lineID: {" kept note "}}, map[string]string{"recv-" + returnID + "-" + lineID: " +oops ", "note-" + returnID + "-" + lineID: " kept note "}, false},
		{"blank inspection", "/admin/returns/" + returnID + "/inspect", "inspect-" + returnID, "recv-" + returnID + "-" + lineID, url.Values{"received_" + lineID: {""}}, map[string]string{"recv-" + returnID + "-" + lineID: ""}, false},
	}
	for _, locale := range i18n.Locales() {
		for _, tc := range cases {
			t.Run(locale.Tag()+"/"+tc.name, func(t *testing.T) {
				req := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodPost, tc.path, strings.NewReader(tc.form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				if tc.multipart {
					var body bytes.Buffer
					writer := multipart.NewWriter(&body)
					for k, vs := range tc.form {
						for _, v := range vs {
							if writeErr := writer.WriteField(k, v); writeErr != nil {
								t.Fatal(writeErr)
							}
						}
					}
					if closeErr := writer.Close(); closeErr != nil {
						t.Fatal(closeErr)
					}
					req = httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodPost, tc.path, &body)
					req.Header.Set("Content-Type", writer.FormDataContentType())
				}
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				if rec.Code != http.StatusUnprocessableEntity {
					t.Fatalf("status=%d, want 422", rec.Code)
				}
				doc, parseErr := html.Parse(strings.NewReader(rec.Body.String()))
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				ids := map[string]*html.Node{}
				anchored, alert := false, false
				for n := range doc.Descendants() {
					if n.Type != html.ElementNode {
						continue
					}
					if id := deskRefusalAttr(n, "id"); id != "" {
						ids[id] = n
					}
					if n.Data == "form" && deskRefusalAttr(n, "action") == tc.path+"#"+tc.anchor {
						anchored = true
					}
					if deskRefusalAttr(n, "role") == "alert" {
						for child := range n.Descendants() {
							if child.Data == "a" && deskRefusalAttr(child, "href") == "#"+tc.first {
								alert = true
							}
						}
					}
				}
				if !anchored || ids[tc.anchor] == nil || !alert {
					t.Errorf("refusal navigation: anchored=%t, target=%t, summary=%t", anchored, ids[tc.anchor] != nil, alert)
				}
				for id, want := range tc.kept {
					n := ids[id]
					if n == nil {
						t.Errorf("missing submitted control %s", id)
						continue
					}
					got := deskRefusalAttr(n, "value")
					if n.Data == "textarea" {
						var b strings.Builder
						for child := range n.Descendants() {
							if child.Type == html.TextNode {
								b.WriteString(child.Data)
							}
						}
						got = b.String()
					}
					if n.Data == "select" {
						for child := range n.Descendants() {
							if child.Data == "option" {
								for _, a := range child.Attr {
									if a.Key == "selected" {
										got = deskRefusalAttr(child, "value")
									}
								}
							}
						}
					}
					if got != want {
						t.Errorf("%s draft=%q, want %q", id, got, want)
					}
					if got != "" && strings.Contains(got, "oops") && deskRefusalAttr(n, "type") == "number" {
						t.Errorf("%s will discard the raw number in the browser", id)
					}
				}
			})
		}
	}
}

func deskRefusalAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
