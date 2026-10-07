//go:build integration

package products_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/content"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/pgtx"
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
	_, foreignOptions := productWithColours(t, staffCtx, s, "Red", "White")
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
			field, option    string
			key              i18n.Key
			picture          []byte
			reuse            bool
		}{
			{name: "corrupt upload", field: "image", key: i18n.KeyAdminNoticeNotImage, alt: " 原始中文說明 ", altEn: " Raw English description ", picture: []byte("corrupt PNG")},
			{name: "invalid upload description", field: "alt", key: i18n.KeyFormHeroAlt, alt: strings.Repeat("界", 201), altEn: " Raw English description ", picture: valid.Bytes()},
			{name: "invalid upload English description", field: "alt_en", key: i18n.KeyFormCampaignAltEnLong, alt: " 原始中文說明 ", altEn: strings.Repeat("e", 201), picture: valid.Bytes()},
			{name: "invalid reused description", field: "alt", key: i18n.KeyFormHeroAlt, alt: strings.Repeat("界", 201), altEn: " Raw English description ", reuse: true},
			{name: "invalid reused English description", field: "alt_en", key: i18n.KeyFormCampaignAltEnLong, alt: " 原始中文說明 ", altEn: strings.Repeat("e", 201), reuse: true},
			{name: "wrong product display option", field: "image_option", key: i18n.KeyAdminNoticeBadOption, alt: " Raw primary ", altEn: " Raw English ", option: foreignOptions[0], picture: valid.Bytes()},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				chosen := options[1]
				if tt.option != "" {
					chosen = tt.option
				}
				res := httptest.NewRecorder()
				if tt.reuse {
					form := url.Values{"digest": {library}, "alt": {tt.alt}, "alt_en": {tt.altEn}}
					req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/products/"+slug+"/images/reuse", strings.NewReader(form.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					mux.ServeHTTP(res, req)
				} else {
					req := productImageUploadRequest(t, ctx, slug, map[string]string{"alt": tt.alt, "alt_en": tt.altEn, "option_value": chosen}, tt.picture)
					mux.ServeHTTP(res, req)
				}
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatalf("refused product image = %d, want 422", res.Code)
				}
				altID, altEnID := "p-alt", "p-alt-en"
				errorID := map[string]string{"image": "p-image", "alt": "p-alt", "alt_en": "p-alt-en", "image_option": "p-image-option"}[tt.field]
				if tt.reuse {
					altID, altEnID = "reuse-alt-"+library, "reuse-alt-en-"+library
					errorID = "reuse-" + strings.ReplaceAll(tt.field, "_", "-") + "-" + library
				}
				for id, want := range map[string]string{altID: tt.alt, altEnID: tt.altEn} {
					input := admintest.InputElementByID(t, res.Body.String(), id)
					if got := admintest.InputAttribute(t, input, "value"); got != want {
						t.Errorf("image draft %q = %q, want %q", id, got, want)
					}
				}
				errorValue := map[string]string{"image": "", "alt": tt.alt, "alt_en": tt.altEn, "image_option": chosen}[tt.field]
				assertRefusedProductImageControl(t, res.Body.String(), errorID, errorValue, i18n.T(ctx, tt.key))
				if !tt.reuse {
					if got := selectedProductImageOption(t, res.Body.String()); got != chosen {
						t.Errorf("image option = %q, want %q", got, chosen)
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

func assertRefusedProductImageControl(t *testing.T, body, id, value, reason string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	found, explained := false, false
	for n := range doc.Descendants() {
		attrs := productImageAttributes(n)
		if attrs["aria-invalid"] == "true" && attrs["id"] != id {
			t.Errorf("unrefused image control %q inherited an error", attrs["id"])
		}
		if attrs["id"] == id {
			found = true
			got := attrs["value"]
			if n.Data == "select" {
				got = selectedProductImageOption(t, body)
			}
			if got != value || attrs["aria-invalid"] != "true" || !strings.Contains(" "+attrs["aria-describedby"]+" ", " "+id+"-error ") {
				t.Errorf("refused image control %q = value %q, invalid %q, describedby %q; want raw %q with its error", id, got, attrs["aria-invalid"], attrs["aria-describedby"], value)
			}
		}
		if attrs["id"] == id+"-error" && attrs["role"] == "alert" && n.FirstChild != nil {
			explained = n.FirstChild.Data == reason
		}
	}
	if !found || !explained {
		t.Errorf("refused image control %q or exact localized explanation is absent", id)
	}
}

func TestProductImageAttachmentFailuresAnswer500AndRecover(t *testing.T) {
	p := admintest.AdminRolePool(t, pool)
	healthy := products.NewStore(p)
	images := media.NewStore(p)
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewNRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	for _, locale := range i18n.Locales() {
		for _, reuse := range []bool{false, true} {
			name := "upload"
			if reuse {
				name = "reuse"
			}
			t.Run(locale.Tag()+"/"+name, func(t *testing.T) {
				staffCtx, actor := admintest.StaffContext(t, pool)
				ctx := i18n.WithLocale(staffCtx, locale)
				slug := admintest.DraftProduct(t, ctx, pool, healthy)
				obj, err := images.Put(ctx, bytes.NewReader(picture.Bytes()))
				if err != nil {
					t.Fatal(err)
				}
				trace := &catalogueLockTrace{slug: slug}
				cfg := p.Config().Copy()
				cfg.ConnConfig.RuntimeParams["lock_timeout"] = "200"
				cfg.ConnConfig.Tracer = trace
				faults, err := pgxpool.NewWithConfig(ctx, cfg)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(faults.Close)
				var role string
				if err = faults.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil || role != "admin" {
					t.Fatalf("attachment pool role = %q, want admin: %v", role, err)
				}
				log := slog.New(slog.DiscardHandler)
				mux := http.NewServeMux()
				products.NewHandler(products.NewStore(faults), media.NewHandler(images, log), log).Routes(mux, admintest.BackOffice)
				post := func() *httptest.ResponseRecorder {
					req := productImageUploadRequest(t, ctx, slug, map[string]string{"alt": " Primary ", "alt_en": " English "}, picture.Bytes())
					if reuse {
						form := url.Values{"digest": {obj.Digest}, "alt": {" Primary "}, "alt_en": {" English "}}
						req = httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/products/"+slug+"/images/reuse", strings.NewReader(form.Encode()))
						req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					}
					res := httptest.NewRecorder()
					mux.ServeHTTP(res, req)
					return res
				}
				auditRows := func() int {
					var n int
					if countErr := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE actor_user_id = $1 AND action = $2`, actor, audit.ActionAttachImage).Scan(&n); countErr != nil {
						t.Fatal(countErr)
					}
					return n
				}
				before, err := healthy.Images(ctx, slug)
				if err != nil {
					t.Fatal(err)
				}
				beforeAudits := auditRows()
				holder, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer pgtx.Rollback(ctx, holder)
				if _, err = holder.Exec(ctx, `SELECT 1 FROM products WHERE slug = $1 FOR UPDATE`, slug); err != nil {
					t.Fatal(err)
				}
				res := post()
				trace.mu.Lock()
				lockErrors := append([]error(nil), trace.errs...)
				trace.mu.Unlock()
				if len(lockErrors) != 1 {
					t.Fatalf("catalogue lock errors = %v; want one lock attempt", lockErrors)
				}
				pgErr, ok := errors.AsType[*pgconn.PgError](lockErrors[0])
				if !ok || pgErr.Code != "55P03" || ctx.Err() != nil {
					t.Fatalf("catalogue lock errors = %v, parent = %v; want one live-request 55P03", lockErrors, ctx.Err())
				}
				if res.Code != http.StatusInternalServerError || res.Header().Get("Location") != "" {
					t.Errorf("attachment failure status = %d, Location = %q; want 500 without redirect", res.Code, res.Header().Get("Location"))
				}
				after, err := healthy.Images(ctx, slug)
				if err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(before, after); diff != "" {
					t.Errorf("failed attachment changed product images (-want +got):\n%s", diff)
				}
				if got := auditRows(); got != beforeAudits {
					t.Errorf("failed attachment audit count = %d, want %d", got, beforeAudits)
				}
				stored, err := images.Object(ctx, obj.Digest)
				if err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(obj, stored); diff != "" {
					t.Errorf("healthy media changed (-want +got):\n%s", diff)
				}
				if err = holder.Rollback(ctx); err != nil {
					t.Fatal(err)
				}
				recovered := post()
				if recovered.Code != http.StatusSeeOther {
					t.Fatalf("unlocked attachment status = %d, want 303", recovered.Code)
				}
				attached, err := healthy.Images(ctx, slug)
				if err != nil {
					t.Fatal(err)
				}
				if len(attached) != 1 || attached[0].Key != obj.Digest || auditRows() != beforeAudits+1 {
					t.Errorf("unlocked attachment = %+v, audits = %d; want one image and one audit", attached, auditRows())
				}
			})
		}
	}
}

type catalogueLockTrace struct {
	slug string
	mu   sync.Mutex
	errs []error
}

type catalogueLockTraceKey struct{}

func (tr *catalogueLockTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "-- name: LockProductCatalogue :one\n") && len(data.Args) == 1 && data.Args[0] == tr.slug {
		return context.WithValue(ctx, catalogueLockTraceKey{}, true)
	}
	return ctx
}

func (tr *catalogueLockTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if marked, _ := ctx.Value(catalogueLockTraceKey{}).(bool); marked {
		tr.mu.Lock()
		tr.errs = append(tr.errs, data.Err)
		tr.mu.Unlock()
	}
}

func TestImageUploadsKeepStorageFailuresSeparateFromRefusals(t *testing.T) {
	owner := admintest.Pool(t)
	staff, _ := admintest.StaffContext(t, owner)
	adminPool := admintest.AdminRolePool(t, owner)
	s := products.NewStore(adminPool)
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(staff, locale)
		slug := admintest.DraftProduct(t, ctx, owner, s)
		var diagnostics bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&diagnostics, nil))
		witness := &uploadWriteTrace{}
		mediaPool := uploadWritePool(t, adminPool, witness)
		h := products.NewHandler(s, media.NewHandler(media.NewStore(mediaPool), logger), logger)
		mux := http.NewServeMux()
		h.Routes(mux, admintest.BackOffice)
		path := "/admin/products/" + slug + "/images"
		for _, tt := range []struct {
			name   string
			alt    string
			status int
		}{
			{name: "storage", alt: " Draft description ", status: 500},
			{name: "invalid utf8", alt: string([]byte{0xff}), status: 400},
			{name: "nul text", alt: string([]byte{'a', 0, 'b'}), status: 400},
			{name: "corrupt", alt: " Draft description ", status: 422},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				before := uploadSavedRows(t, owner)
				diagnostics.Reset()
				var release func()
				if tt.name == "storage" {
					release = holdUploadWrite(t, owner)
				}
				res := httptest.NewRecorder()
				mux.ServeHTTP(res, uploadFailureRequest(t, ctx, path, tt.alt, tt.name == "corrupt"))
				if release != nil {
					release()
				}
				if tt.name == "storage" {
					assertUploadStorageWitness(t, ctx, witness)
				}
				if res.Code != tt.status || res.Header().Get("Location") != "" {
					t.Errorf("image upload %s = %d to %q, want %d with no redirect", tt.name, res.Code, res.Header().Get("Location"), tt.status)
				}
				if got := uploadSavedRows(t, owner); got != before {
					t.Errorf("refused upload changed saved media/images/audit (-want +got):\n%s\n%s", before, got)
				}
				assertUploadFailure(t, ctx, res.Body.String(), diagnostics.String(), tt.status, tt.alt)
			})
		}
		beforeAudit := uploadAuditCount(t, owner)
		diagnostics.Reset()
		witness.code = ""
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, uploadFailureRequest(t, ctx, path, " Recovered description ", false))
		if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/products/"+slug+"?ok=1" {
			t.Errorf("recovered image upload = %d to %q, want 303 to the editor", res.Code, res.Header().Get("Location"))
		}
		if witness.seen != 2 || witness.code != "" || !witness.live || ctx.Err() != nil {
			t.Errorf("recovered PutMedia = %d/%q/live=%t, want the second actual successful write", witness.seen, witness.code, witness.live)
		}
		var alt, altEn string
		if err := owner.QueryRow(ctx, `SELECT alt_text, alt_text_en FROM product_images WHERE product_id=(SELECT id FROM products WHERE slug=$1)`, slug).Scan(&alt, &altEn); err != nil {
			t.Fatal(err)
		}
		if alt != "Recovered description" || altEn != "English description" || uploadAuditCount(t, owner) != beforeAudit+1 {
			t.Errorf("recovered image = %q/%q, want saved descriptions and exactly one new audit", alt, altEn)
		}
	}
}

type uploadTraceKey struct{}

type uploadWriteTrace struct {
	seen int
	code string
	live bool
}

func (w *uploadWriteTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "-- name: PutMedia :exec\n") {
		w.seen++
		return context.WithValue(ctx, uploadTraceKey{}, true)
	}
	return ctx
}

func (w *uploadWriteTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if target, ok := ctx.Value(uploadTraceKey{}).(bool); !ok || !target {
		return
	}
	w.live = ctx.Err() == nil
	if cause, ok := errors.AsType[*pgconn.PgError](data.Err); ok {
		w.code = cause.Code
	}
}

func uploadWritePool(t *testing.T, adminPool *pgxpool.Pool, witness *uploadWriteTrace) *pgxpool.Pool {
	t.Helper()
	cfg := adminPool.Config().Copy()
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "75ms"
	cfg.ConnConfig.Tracer = witness
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	var role string
	if err := p.QueryRow(t.Context(), `SELECT current_user`).Scan(&role); err != nil || role != "admin" {
		t.Fatalf("media current_user = %q/%v, want admin", role, err)
	}
	return p
}

func holdUploadWrite(t *testing.T, owner *pgxpool.Pool) func() {
	t.Helper()
	ctx := t.Context()
	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pgtx.Rollback(ctx, tx) })
	if _, err := tx.Exec(ctx, `LOCK TABLE media_objects IN SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func uploadFailureRequest(t *testing.T, ctx context.Context, path, alt string, corrupt bool) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("image", "upload.png")
	if err != nil {
		t.Fatal(err)
	}
	writeUploadImage(t, part, corrupt)
	for name, value := range map[string]string{"alt": alt, "alt_en": " English description "} {
		if err := form.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	return req
}

func assertUploadFailure(t *testing.T, ctx context.Context, body, diagnostics string, status int, alt string) {
	t.Helper()
	switch status {
	case 500:
		if !strings.Contains(diagnostics, `"level":"ERROR"`) || !strings.Contains(diagnostics, "SQLSTATE 55P03") || !strings.Contains(diagnostics, "store image") {
			t.Errorf("storage diagnostics = %q, want Error with actual PutMedia cause", diagnostics)
		}
		if !strings.Contains(body, i18n.T(ctx, i18n.KeyAdminErrorBody)) || strings.Contains(body, "SQLSTATE") {
			t.Error("storage failure did not show a generic server error")
		}
	case 400:
		if !strings.Contains(body, i18n.T(ctx, i18n.KeyAdminBadForm)) {
			t.Error("malformed multipart text did not show the bad-form response")
		}
	case 422:
		input := admintest.InputElementByID(t, body, "p-image")
		if admintest.InputAttribute(t, input, "aria-invalid") != "true" || admintest.InputAttribute(t, input, "aria-describedby") != "p-image-error" {
			t.Error("corrupt file must flag only its image control with the linked refusal")
		}
		if !strings.Contains(body, i18n.T(ctx, i18n.KeyAdminNoticeNotImage)) {
			t.Error("corrupt upload did not retain the image refusal")
		}
		for id, want := range map[string]string{"p-alt": alt, "p-alt-en": " English description "} {
			input := admintest.InputElementByID(t, body, id)
			if got := admintest.InputAttribute(t, input, "value"); got != want {
				t.Errorf("refused image draft %s = %q, want raw %q", id, got, want)
			}
		}
	}
	if status != 422 && strings.Contains(body, i18n.T(ctx, i18n.KeyAdminNoticeUploadFailed)) {
		t.Error("a non-image failure was blamed on the image")
	}
}

func uploadAuditCount(t *testing.T, owner *pgxpool.Pool) int {
	t.Helper()
	var count int
	if err := owner.QueryRow(t.Context(), `SELECT count(*) FROM audit_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func uploadSavedRows(t *testing.T, owner *pgxpool.Pool) string {
	t.Helper()
	var saved string
	if err := owner.QueryRow(t.Context(), `SELECT jsonb_build_object(
		'media', (SELECT coalesce(jsonb_agg(to_jsonb(m) ORDER BY m.digest), '[]') FROM media_objects m),
		'categories', (SELECT coalesce(jsonb_agg(to_jsonb(c) ORDER BY c.id), '[]') FROM categories c),
		'campaigns', (SELECT coalesce(jsonb_agg(to_jsonb(c) ORDER BY c.id), '[]') FROM sale_campaigns c),
		'images', (SELECT coalesce(jsonb_agg(to_jsonb(i) ORDER BY i.id), '[]') FROM product_images i),
		'audit', (SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY a.id), '[]') FROM audit_events a)
	)::text`).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	return saved
}

func assertUploadStorageWitness(t *testing.T, ctx context.Context, witness *uploadWriteTrace) {
	t.Helper()
	if witness.seen != 1 || witness.code != "55P03" || !witness.live || ctx.Err() != nil {
		t.Fatalf("PutMedia fault witness = %d/%q/live=%t parent=%v, want exactly one real 55P03 with live request", witness.seen, witness.code, witness.live, ctx.Err())
	}
}

func writeUploadImage(t *testing.T, part io.Writer, corrupt bool) {
	t.Helper()
	if corrupt {
		if _, err := part.Write([]byte("corrupt photograph")); err != nil {
			t.Fatal(err)
		}
		return
	}
	img := image.NewRGBA(image.Rect(0, 0, 17, 7))
	img.Set(0, 0, color.RGBA{R: 83, G: 29, B: 143, A: 255})
	if err := png.Encode(part, img); err != nil {
		t.Fatal(err)
	}
}
