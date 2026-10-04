//go:build integration

package products_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/i18n"
)

func TestProductFormsAcceptTheirFullBilingualTextLimits(t *testing.T) {
	for _, tt := range []struct {
		name string
		text string
	}{
		{name: "Han", text: "\u6587"},
		{name: "four-byte Unicode", text: "\U0001F331"},
	} {
		for _, update := range []bool{false, true} {
			operation := "create"
			if update {
				operation = "update"
			}
			t.Run(tt.name+"/"+operation, func(t *testing.T) {
				ctx, _ := admintest.StaffContext(t, pool)
				p := admintest.AdminRolePool(t, pool)
				s := products.NewStore(p)
				slug := "long-form-" + uuid.NewString()[:8]
				var category string
				if err := pool.QueryRow(ctx, `SELECT id::text FROM categories ORDER BY id LIMIT 1`).Scan(&category); err != nil {
					t.Fatal(err)
				}
				path := "/admin/products"
				if update {
					created, errs, err := s.Create(ctx, &products.Form{Slug: slug, Name: "Initial product", CategoryID: category, Description: "Original", DescriptionEn: "Original translation"})
					if err != nil || len(errs) > 0 || created != slug {
						t.Fatalf("create update fixture=%q/%v/%v", created, errs, err)
					}
					path += "/" + slug
				}
				form := url.Values{
					"slug": {slug}, "category": {category}, "name": {strings.Repeat(tt.text, 200)}, "name_en": {strings.Repeat(tt.text, 200)},
					"summary": {strings.Repeat(tt.text, 500)}, "summary_en": {strings.Repeat(tt.text, 500)},
					"description": {strings.Repeat(tt.text, 20000)}, "description_en": {strings.Repeat(tt.text, 20000)}, "warranty": {strings.Repeat(tt.text, 300)},
				}
				encoded := form.Encode()
				if len(encoded) <= 65536 {
					t.Fatal("fixture does not exceed the old form cap")
				}
				mux := http.NewServeMux()
				admintest.ProductDesk(p, s).Routes(mux, admintest.BackOffice)
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(encoded))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				res := httptest.NewRecorder()
				mux.ServeHTTP(res, req)
				if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/products/"+slug+"?ok=1" {
					t.Fatalf("legal %s form status/location=%d/%q, want 303 saved", operation, res.Code, res.Header().Get("Location"))
				}
				var got []string
				var name, nameEn, summary, summaryEn, description, descriptionEn, warranty string
				if err := pool.QueryRow(ctx, `SELECT name,coalesce(name_en,''),summary,coalesce(summary_en,''),description,coalesce(description_en,''),coalesce(warranty_note,'') FROM products WHERE slug=$1`, slug).Scan(&name, &nameEn, &summary, &summaryEn, &description, &descriptionEn, &warranty); err != nil {
					t.Fatal(err)
				}
				got = []string{name, nameEn, summary, summaryEn, description, descriptionEn, warranty}
				want := []string{form.Get("name"), form.Get("name_en"), form.Get("summary"), form.Get("summary_en"), form.Get("description"), form.Get("description_en"), form.Get("warranty")}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("saved legal text (-want +got):\n%s", diff)
				}
				for _, locale := range i18n.Locales() {
					form.Set("description", strings.Repeat(tt.text, 20001))
					form.Set("description_en", strings.Repeat(tt.text, 20001))
					req = httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodPost, path, strings.NewReader(form.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					res = httptest.NewRecorder()
					mux.ServeHTTP(res, req)
					if res.Code != http.StatusUnprocessableEntity {
						t.Fatalf("over-limit %s form (%s) status=%d, want 422", operation, locale.Tag(), res.Code)
					}
					assertDescriptionDraft(t, res.Body.String(), form)
					var saved, savedEn string
					if err := pool.QueryRow(ctx, `SELECT description,coalesce(description_en,'') FROM products WHERE slug=$1`, slug).Scan(&saved, &savedEn); err != nil {
						t.Fatal(err)
					}
					if diff := cmp.Diff(want[4:6], []string{saved, savedEn}); diff != "" {
						t.Errorf("refused text changed the saved product (-want +got):\n%s", diff)
					}
				}
			})
		}
	}
}

func assertDescriptionDraft(t *testing.T, body string, want url.Values) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for n := range doc.Descendants() {
		if n.Type != html.ElementNode || n.Data != "textarea" {
			continue
		}
		for _, a := range n.Attr {
			if a.Key == "name" && (a.Val == "description" || a.Val == "description_en") {
				var text strings.Builder
				for child := range n.Descendants() {
					if child.Type == html.TextNode {
						text.WriteString(child.Data)
					}
				}
				got[a.Val] = text.String()
			}
		}
	}
	if diff := cmp.Diff(map[string]string{"description": want.Get("description"), "description_en": want.Get("description_en")}, got); diff != "" {
		t.Errorf("refused descriptions (-want +got):\n%s", diff)
	}
}
