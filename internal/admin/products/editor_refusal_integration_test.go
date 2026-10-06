//go:build integration

package products_test

import (
	"bytes"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/i18n"
)

func TestProductEditorRefusalsKeepTheirDraftAndImageControls(t *testing.T) {
	staffCtx, _ := admintest.StaffContext(t, pool)
	p := admintest.AdminRolePool(t, pool)
	s := products.NewStore(p)
	slug := admintest.DraftProduct(t, staffCtx, pool, s)
	attached, library := storeMedia(t), storeMedia(t)
	if err := s.AttachImage(staffCtx, slug, attached, "Editor photograph", "Editor photograph", "", 800, 600); err != nil {
		t.Fatal(err)
	}
	if errs, err := s.AddSpec(staffCtx, slug, products.SpecDraft{Label: "Capacity", Value: "Original value", LabelEn: "Original label", ValueEn: "Original English value"}); err != nil || len(errs) > 0 {
		t.Fatalf("create duplicate spec fixture=%v/%v", errs, err)
	}
	view, err := s.Product(staffCtx, slug)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	admintest.ProductDesk(p, s).Routes(mux, admintest.BackOffice)
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(staffCtx, locale)
		baseline, err := s.Product(ctx, slug)
		if err != nil {
			t.Fatal(err)
		}
		page := httptest.NewRecorder()
		mux.ServeHTTP(page, httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/products/"+slug, nil))
		if page.Code != http.StatusOK {
			t.Fatalf("initial editor status=%d", page.Code)
		}
		assertEditorImageControls(t, page.Body.String(), slug, attached, library)
		for _, tt := range []struct {
			name, path string
			form       url.Values
		}{
			{name: "detail", path: "", form: url.Values{"name": {view.Name}, "category": {view.CategoryID}, "summary": {strings.Repeat("s", 501)}, "description": {strings.Repeat("d", 20001)}, "description_en": {view.DescriptionEn}}},
			{name: "variant", path: "/variants", form: url.Values{"sku": {"refused variant"}, "price": {"unreadable"}}},
			{name: "option", path: "/options", form: url.Values{"name": {""}, "name_en": {"Option sentinel"}}},
			{name: "specification", path: "/specs", form: url.Values{"label": {" Capacity "}, "value": {" New value "}, "label_en": {" Authored English label "}, "value_en": {" Authored English value "}}},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/products/"+slug+tt.path, strings.NewReader(tt.form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				res := httptest.NewRecorder()
				mux.ServeHTTP(res, req)
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatalf("refused editor status=%d, want 422", res.Code)
				}
				body := res.Body.String()
				assertEditorImageControls(t, body, slug, attached, library)
				switch tt.name {
				case "specification":
					admintest.AssertRefusedInput(t, body, "spec-label", tt.form.Get("label"))
					for id, field := range map[string]string{"spec-value": "value", "spec-label-en": "label_en", "spec-value-en": "value_en"} {
						input := admintest.InputElementByID(t, body, id)
						if got := admintest.InputAttribute(t, input, "value"); got != tt.form.Get(field) {
							t.Errorf("specification field %q=%q, want raw %q", field, got, tt.form.Get(field))
						}
						if admintest.InputAttribute(t, input, "aria-invalid") != "" {
							t.Errorf("unrefused spec field %q inherited an error", field)
						}
					}
					if !strings.Contains(body, i18n.T(ctx, i18n.KeyFormSpecLabelDuplicate)) {
						t.Error("duplicate specification explanation is absent")
					}
				case "detail":
					admintest.AssertRefusedInput(t, body, "p-summary", tt.form.Get("summary"))
					doc, err := html.Parse(strings.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for n := range doc.Descendants() {
						if n.Type != html.ElementNode || n.Data != "textarea" {
							continue
						}
						attrs := map[string]string{}
						for _, a := range n.Attr {
							attrs[a.Key] = a.Val
						}
						if attrs["id"] != "p-description" {
							continue
						}
						found = true
						text := ""
						if n.FirstChild != nil {
							text = n.FirstChild.Data
						}
						if diff := cmp.Diff([]string{tt.form.Get("description"), "true", "p-description-error"}, []string{text, attrs["aria-invalid"], attrs["aria-describedby"]}); diff != "" {
							t.Errorf("refused primary description (-want +got):\n%s", diff)
						}
					}
					if !found || !strings.Contains(body, i18n.T(ctx, i18n.KeyFormProductDescriptionLong)) {
						t.Error("primary description control or explanation is absent")
					}
				}
				persisted, err := s.Product(ctx, slug)
				if err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(baseline, persisted); diff != "" {
					t.Errorf("refusal changed persisted editor state (-want +got):\n%s", diff)
				}
			})
		}
	}
}

func assertEditorImageControls(t *testing.T, body, slug, attached, library string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	controls := map[string][]string{}
	for form := range doc.Descendants() {
		if form.Type != html.ElementNode || form.Data != "form" {
			continue
		}
		var action string
		for _, a := range form.Attr {
			if a.Key == "action" {
				action = a.Val
			}
		}
		for input := range form.Descendants() {
			if input.Type != html.ElementNode || input.Data != "input" {
				continue
			}
			var name, value string
			for _, a := range input.Attr {
				if a.Key == "name" {
					name = a.Val
				}
				if a.Key == "value" {
					value = a.Val
				}
			}
			if name == "digest" {
				controls[action] = append(controls[action], value)
			}
		}
	}
	for action, digest := range map[string]string{"/admin/products/" + slug + "/images/remove": attached, "/admin/products/" + slug + "/images/reuse": library} {
		found := false
		for _, got := range controls[action] {
			if got == digest {
				found = true
			}
		}
		if !found {
			t.Errorf("image control %q omitted fixture digest %q; got %v", action, digest, controls[action])
		}
	}
}

func TestProductImageRefusalsKeepDescriptionsAndSelection(t *testing.T) {
	staffCtx, _ := admintest.StaffContext(t, pool)
	p := admintest.AdminRolePool(t, pool)
	s := products.NewStore(p)
	mux := http.NewServeMux()
	admintest.ProductDesk(p, s).Routes(mux, admintest.BackOffice)
	slug, options := productWithColours(t, staffCtx, s, "Blue", "Black")
	attached, library := storeMedia(t), storeMedia(t)
	if err := s.AttachImage(staffCtx, slug, attached, "Saved picture", "Saved English picture", "", 800, 600); err != nil {
		t.Fatal(err)
	}
	baseline, err := s.Images(staffCtx, slug)
	if err != nil {
		t.Fatal(err)
	}
	var valid bytes.Buffer
	if err := png.Encode(&valid, image.NewRGBA(image.Rect(0, 0, 12, 8))); err != nil {
		t.Fatal(err)
	}
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(staffCtx, locale)
		for _, tt := range []struct {
			name, alt, altEn string
			picture          []byte
			reuse            bool
		}{
			{name: "corrupt upload", alt: " 原始中文說明 ", altEn: " Raw English description ", picture: []byte("corrupt PNG")},
			{name: "invalid upload description", alt: strings.Repeat("界", 201), altEn: " Raw English description ", picture: valid.Bytes()},
			{name: "invalid upload English description", alt: " 原始中文說明 ", altEn: strings.Repeat("e", 201), picture: valid.Bytes()},
			{name: "invalid reused description", alt: strings.Repeat("界", 201), altEn: " Raw English description ", reuse: true},
			{name: "invalid reused English description", alt: " 原始中文說明 ", altEn: strings.Repeat("e", 201), reuse: true},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				res := httptest.NewRecorder()
				if tt.reuse {
					form := url.Values{"digest": {library}, "alt": {tt.alt}, "alt_en": {tt.altEn}}
					req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/products/"+slug+"/images/reuse", strings.NewReader(form.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					mux.ServeHTTP(res, req)
				} else {
					var body bytes.Buffer
					form := multipart.NewWriter(&body)
					for name, value := range map[string]string{"alt": tt.alt, "alt_en": tt.altEn, "option_value": options[1]} {
						if err := form.WriteField(name, value); err != nil {
							t.Fatal(err)
						}
					}
					part, err := form.CreateFormFile("image", "product.png")
					if err != nil {
						t.Fatal(err)
					}
					if _, err := part.Write(tt.picture); err != nil {
						t.Fatal(err)
					}
					if err := form.Close(); err != nil {
						t.Fatal(err)
					}
					req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/products/"+slug+"/images", &body)
					req.Header.Set("Content-Type", form.FormDataContentType())
					mux.ServeHTTP(res, req)
				}
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatalf("refused product image = %d, want 422", res.Code)
				}
				altID, altEnID, errorID := "p-alt", "p-alt-en", "p-image"
				if tt.reuse {
					altID, altEnID, errorID = "reuse-alt-"+library, "reuse-alt-en-"+library, "reuse-alt-"+library
				}
				for id, want := range map[string]string{altID: tt.alt, altEnID: tt.altEn} {
					input := admintest.InputElementByID(t, res.Body.String(), id)
					if got := admintest.InputAttribute(t, input, "value"); got != want {
						t.Errorf("image draft %q = %q, want %q", id, got, want)
					}
				}
				errorValue := ""
				if tt.reuse {
					errorValue = tt.alt
				}
				admintest.AssertRefusedInput(t, res.Body.String(), errorID, errorValue)
				if !tt.reuse {
					doc, err := html.Parse(strings.NewReader(res.Body.String()))
					if err != nil {
						t.Fatal(err)
					}
					chosen := ""
					for n := range doc.Descendants() {
						if n.Type != html.ElementNode || n.Data != "select" {
							continue
						}
						id := ""
						for _, a := range n.Attr {
							if a.Key == "id" {
								id = a.Val
							}
						}
						if id != "p-image-option" {
							continue
						}
						for option := range n.Descendants() {
							selected, value := false, ""
							for _, a := range option.Attr {
								if a.Key == "selected" {
									selected = true
								}
								if a.Key == "value" {
									value = a.Val
								}
							}
							if selected {
								chosen = value
							}
						}
					}
					if chosen != options[1] {
						t.Errorf("image option = %q, want %q", chosen, options[1])
					}
				}
				assertEditorImageControls(t, res.Body.String(), slug, attached, library)
				after, err := s.Images(ctx, slug)
				if err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(baseline, after); diff != "" {
					t.Errorf("refusal changed attached images (-want +got):\n%s", diff)
				}
			})
		}
	}
}
