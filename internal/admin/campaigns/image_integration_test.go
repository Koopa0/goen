//go:build integration

package campaigns_test

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
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/campaigns"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/pgtx"
)

func campaignImageRequest(t *testing.T, slug, alt string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("image", "banner.png")
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(part, image.NewRGBA(image.Rect(0, 0, 16, 6))); err != nil {
		t.Fatal(err)
	}
	if err := form.WriteField("alt", alt); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/campaigns/"+slug+"/image", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.SetPathValue("slug", slug)
	return req
}

func TestACampaignHeaderIsUploadedShownAndRemoved(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := campaigns.NewStore(pool)
	h := handlerOver(s)
	slug := "header-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	if errs, err := s.Create(ctx, &campaigns.Form{Slug: slug, Title: "頁首測試", Days: 7}); err != nil || len(errs) > 0 {
		t.Fatalf("create the campaign: %v %v", errs, err)
	}

	// Without alt text the picture is refused at that field and nothing is stored.
	refused := httptest.NewRecorder()
	h.SetImage(refused, campaignImageRequest(t, slug, "  ").WithContext(ctx))
	if refused.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(refused.Body.String(), `aria-describedby="c-alt-error"`) {
		t.Fatalf("no alt answered %d, want 422 with the alt field flagged", refused.Code)
	}
	view, err := catalog.NewStore(pool).Campaign(ctx, slug)
	if err != nil || view.Image.Shown() {
		t.Fatalf("a refused upload left a header: %+v (err %v)", view.Image, err)
	}

	ok := httptest.NewRecorder()
	h.SetImage(ok, campaignImageRequest(t, slug, "限時優惠商品").WithContext(ctx))
	if ok.Code != http.StatusSeeOther || ok.Header().Get("Location") != "/admin/campaigns/"+slug+"?ok=1" {
		t.Fatalf("upload answered %d to %q, want 303 to ?ok=1", ok.Code, ok.Header().Get("Location"))
	}
	view, err = catalog.NewStore(pool).Campaign(ctx, slug)
	if err != nil || !view.Image.Shown() || !strings.HasPrefix(view.Image.URL, "/media/") || view.Image.Alt != "限時優惠商品" {
		t.Fatalf("the campaign page header = %+v (err %v)", view.Image, err)
	}
	var audited int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = 'campaign.image.set' AND after->>'campaign' = $1`, slug).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("audit rows = %d (err %v), want 1", audited, err)
	}

	remove := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/campaigns/"+slug+"/image/remove", http.NoBody)
	remove.SetPathValue("slug", slug)
	remove.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	gone := httptest.NewRecorder()
	h.RemoveImage(gone, remove)
	if gone.Code != http.StatusSeeOther {
		t.Fatalf("remove answered %d, want 303", gone.Code)
	}
	view, err = catalog.NewStore(pool).Campaign(ctx, slug)
	if err != nil || view.Image.Shown() {
		t.Fatalf("after removal the header is %+v (err %v)", view.Image, err)
	}
}

func TestHeaderImageRefusalsKeepBothDescriptions(t *testing.T) {
	staffCtx, _ := admintest.StaffContext(t, pool)
	p := admintest.AdminRolePool(t, pool)
	s := campaigns.NewStore(p)
	log := slog.New(slog.DiscardHandler)
	mux := http.NewServeMux()
	campaigns.NewHandler(s, media.NewHandler(media.NewStore(p), log), log).Routes(mux, admintest.BackOffice)
	slug := "recover-campaign-" + uuid.NewString()[:8]
	ctx := staffCtx
	if errs, err := s.Create(ctx, &campaigns.Form{Slug: slug, Title: "Recovery campaign", Days: 7}); err != nil || len(errs) > 0 {
		t.Fatalf("create header fixture: %v %v", errs, err)
	}
	var valid bytes.Buffer
	if err := png.Encode(&valid, image.NewRGBA(image.Rect(0, 0, 16, 6))); err != nil {
		t.Fatal(err)
	}
	post := func(ctx context.Context, fields map[string]string, picture []byte) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		for name, value := range fields {
			if err := form.WriteField(name, value); err != nil {
				t.Fatal(err)
			}
		}
		part, err := form.CreateFormFile("image", "header.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(picture); err != nil {
			t.Fatal(err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/campaigns/"+slug+"/image", &body)
		req.Header.Set("Content-Type", form.FormDataContentType())
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		return res
	}
	if res := post(ctx, map[string]string{"alt": "Saved header", "alt_en": "Saved English header"}, valid.Bytes()); res.Code != http.StatusSeeOther {
		t.Fatalf("save control = %d, want 303", res.Code)
	}
	baseline, baselineTone, err := s.Image(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(staffCtx, locale)
		for _, tt := range []struct {
			name, alt, altEn, field string
			picture                 []byte
		}{
			{name: "corrupt image", alt: " 原始中文說明 ", altEn: " Raw English description ", field: "c-image", picture: []byte("corrupt PNG")},
			{name: "invalid primary description", alt: strings.Repeat("界", 201), altEn: " Raw English description ", field: "c-alt", picture: valid.Bytes()},
			{name: "invalid English description", alt: " 原始中文說明 ", altEn: strings.Repeat("e", 201), field: "c-alt-en", picture: valid.Bytes()},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				res := post(ctx, map[string]string{"alt": tt.alt, "alt_en": tt.altEn}, tt.picture)
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatalf("refused header = %d, want 422", res.Code)
				}
				for id, want := range map[string]string{"c-alt": tt.alt, "c-alt-en": tt.altEn} {
					input := admintest.InputElementByID(t, res.Body.String(), id)
					if got := admintest.InputAttribute(t, input, "value"); got != want {
						t.Errorf("draft %q = %q, want %q", id, got, want)
					}
				}
				admintest.AssertRefusedInput(t, res.Body.String(), tt.field, map[string]string{"c-alt": tt.alt, "c-alt-en": tt.altEn}[tt.field])
				after, tone, err := s.Image(ctx, slug)
				if err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(baseline, after); diff != "" {
					t.Errorf("refusal changed saved header (-want +got):\n%s", diff)
				}

				if tone != baselineTone {
					t.Errorf("refusal changed saved tone = %q, want %q", tone, baselineTone)
				}
			})
		}
	}
}

func TestImageUploadsKeepStorageFailuresSeparateFromRefusals(t *testing.T) {
	owner := admintest.Pool(t)
	staff, _ := admintest.StaffContext(t, owner)
	adminPool := admintest.AdminRolePool(t, owner)
	s := campaigns.NewStore(adminPool)
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(staff, locale)
		slug := "upload-" + uuid.NewString()[:8]
		if fields, err := s.Create(ctx, &campaigns.Form{Slug: slug, Title: "Upload campaign", Days: 7}); err != nil || len(fields) != 0 {
			t.Fatalf("campaign fixture = %v/%v", fields, err)
		}
		var diagnostics bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&diagnostics, nil))
		witness := &uploadWriteTrace{}
		mediaPool := uploadWritePool(t, adminPool, witness)
		h := campaigns.NewHandler(s, media.NewHandler(media.NewStore(mediaPool), logger), logger)
		mux := http.NewServeMux()
		h.Routes(mux, admintest.BackOffice)
		path := "/admin/campaigns/" + slug + "/image"
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
		if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/campaigns/"+slug+"?ok=1" {
			t.Errorf("recovered image upload = %d to %q, want 303 to the editor", res.Code, res.Header().Get("Location"))
		}
		if witness.seen != 2 || witness.code != "" || !witness.live || ctx.Err() != nil {
			t.Errorf("recovered PutMedia = %d/%q/live=%t, want the second actual successful write", witness.seen, witness.code, witness.live)
		}
		var alt, altEn string
		if err := owner.QueryRow(ctx, `SELECT image_alt, image_alt_en FROM sale_campaigns WHERE slug=$1`, slug).Scan(&alt, &altEn); err != nil {
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
		input := admintest.InputElementByID(t, body, "c-image")
		if admintest.InputAttribute(t, input, "aria-invalid") != "true" || admintest.InputAttribute(t, input, "aria-describedby") != "c-image-error" {
			t.Error("corrupt file must flag only its image control with the linked refusal")
		}
		if !strings.Contains(body, i18n.T(ctx, i18n.KeyAdminNoticeNotImage)) {
			t.Error("corrupt upload did not retain the image refusal")
		}
		for id, want := range map[string]string{"c-alt": alt, "c-alt-en": " English description "} {
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
