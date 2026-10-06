//go:build integration

package products_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/content"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
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
					req := productImageUploadRequest(t, ctx, slug, map[string]string{"alt": tt.alt, "alt_en": tt.altEn, "option_value": options[1]}, tt.picture)
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
					if chosen := selectedProductImageOption(t, res.Body.String()); chosen != options[1] {
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

func productImageUploadRequest(t *testing.T, ctx context.Context, slug string, fields map[string]string, picture []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := form.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := form.CreateFormFile("image", "product.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(picture); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/products/"+slug+"/images", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	return req
}

func selectedProductImageOption(t *testing.T, body string) string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for n := range doc.Descendants() {
		if n.Type != html.ElementNode || n.Data != "select" || productImageAttributes(n)["id"] != "p-image-option" {
			continue
		}
		var chosen string
		for option := range n.Descendants() {
			attrs := productImageAttributes(option)
			if _, selected := attrs["selected"]; selected {
				chosen = attrs["value"]
			}
		}
		return chosen
	}
	return ""
}

func productImageAttributes(n *html.Node) map[string]string {
	attrs := make(map[string]string, len(n.Attr))
	for _, a := range n.Attr {
		attrs[a.Key] = a.Val
	}
	return attrs
}

func TestALosslessWebPUploadIsRefusedWithItsOwnNotice(t *testing.T) {
	staffCtx, _ := admintest.StaffContext(t, pool)
	p := admintest.AdminRolePool(t, pool)
	s := products.NewStore(p)
	slug := admintest.DraftProduct(t, staffCtx, pool, s)
	heroes := content.NewStore(p)
	beforeImages, err := s.Images(staffCtx, slug)
	if err != nil {
		t.Fatal(err)
	}
	beforeHeroes, err := heroes.HeroSlides(staffCtx)
	if err != nil {
		t.Fatal(err)
	}

	// An upload that reaches storage would show uploadfailed, not the lossless notice.
	idle, err := pgxpool.New(t.Context(), "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(idle.Close)
	log := slog.New(slog.DiscardHandler)
	images := media.NewHandler(media.NewStore(idle), log)
	mux := http.NewServeMux()
	products.NewHandler(s, images, log).Routes(mux, admintest.BackOffice)
	content.NewHandler(heroes, images, newsletter.NewStore(p), log).Routes(mux, admintest.BackOffice)
	lossless := []byte{
		'R', 'I', 'F', 'F', 0x14, 0x00, 0x00, 0x00, 'W', 'E', 'B', 'P',
		'V', 'P', '8', 'L', 0x08, 0x00, 0x00, 0x00,
		0x2f, 0x3f, 0xc0, 0x0b, 0x00, 0x88, 0x88, 0x08,
	}
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(staffCtx, locale)
		for _, tt := range []struct {
			name, path, inputID, errorID string
			fields                       map[string]string
		}{
			{
				name: "product", path: "/admin/products/" + slug + "/images",
				inputID: "p-image", errorID: "p-image-error",
				fields: map[string]string{"alt": " 正面 ", "alt_en": " Front "},
			},
			{
				name: "hero", path: "/admin/home",
				inputID: "h-image", errorID: "h-image-error",
				fields: map[string]string{
					"headline": " 秋季新品 ", "primary_label": " 去看看 ", "primary_href": "/deals",
					"alt": " 秋季新品主視覺 ", "alt_en": " Autumn collection ",
				},
			},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				var body bytes.Buffer
				form := multipart.NewWriter(&body)
				part, err := form.CreateFormFile("image", "photo.webp")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = part.Write(lossless); err != nil {
					t.Fatal(err)
				}
				for name, value := range tt.fields {
					if err = form.WriteField(name, value); err != nil {
						t.Fatal(err)
					}
				}
				if err = form.Close(); err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, tt.path, &body)
				req.Header.Set("Content-Type", form.FormDataContentType())
				res := httptest.NewRecorder()
				mux.ServeHTTP(res, req)
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatalf("lossless upload = %d, want 422", res.Code)
				}
				admintest.AssertRefusedInput(t, res.Body.String(), tt.inputID, "")
				doc, err := html.Parse(strings.NewReader(res.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				var reason strings.Builder
				for n := range doc.Descendants() {
					if n.Type != html.ElementNode {
						continue
					}
					for _, a := range n.Attr {
						if a.Key != "id" || a.Val != tt.errorID {
							continue
						}
						for child := range n.Descendants() {
							if child.Type == html.TextNode {
								reason.WriteString(child.Data)
							}
						}
					}
				}
				if got, want := reason.String(), i18n.T(ctx, i18n.KeyAdminNoticeLosslessWebP); got != want {
					t.Errorf("lossless file error = %q, want %q", got, want)
				}
				afterImages, err := s.Images(ctx, slug)
				if err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(beforeImages, afterImages); diff != "" {
					t.Errorf("lossless refusal changed product images (-want +got):\n%s", diff)
				}
				afterHeroes, err := heroes.HeroSlides(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(beforeHeroes.Rows, afterHeroes.Rows); diff != "" {
					t.Errorf("lossless refusal changed hero slides (-want +got):\n%s", diff)
				}
			})
		}
	}
}
