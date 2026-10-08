//go:build integration

package content_test

import (
	"bytes"
	"context"
	"errors"
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
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/content"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/pgtx"
)

// TestARefusedHeroSlideStoresNoImage holds the order of the hero form: its copy
// is checked before its image is decoded, so a slide refused for a missing
// headline or missing alt text writes nothing to media_objects. The last post
// is the control: the same image under complete copy is stored, so the
// absences before it are about the refusal and not about the probe.
func TestARefusedHeroSlideStoresNoImage(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	h := handlerOver(content.NewStore(pool))
	headline := "主視覺" + uuid.NewString()[:8]
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is already cancelled in Cleanup
		_, _ = pool.Exec(context.Background(), `DELETE FROM hero_slides WHERE headline = $1`, headline)
	})

	// Pixels no other test uploads, so the digest names this upload alone.
	picture := image.NewRGBA(image.Rect(0, 0, 5, 4))
	marker := uuid.New()
	for i := range picture.Pix {
		picture.Pix[i] = marker[i%len(marker)] | 0x01
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatalf("encode the picture: %v", err)
	}
	obj, _, err := media.Normalise(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatalf("normalise the picture: %v", err)
	}
	stored := func() bool {
		t.Helper()
		var found bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM media_objects WHERE digest = $1)`, obj.Digest,
		).Scan(&found); err != nil {
			t.Fatalf("look for the image: %v", err)
		}
		return found
	}

	post := func(fields map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		part, err := form.CreateFormFile("image", "hero.png")
		if err != nil {
			t.Fatalf("create the file part: %v", err)
		}
		if _, err := part.Write(encoded.Bytes()); err != nil {
			t.Fatalf("write the file part: %v", err)
		}
		for name, value := range fields {
			if err := form.WriteField(name, value); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		if err := form.Close(); err != nil {
			t.Fatalf("close the form: %v", err)
		}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/home", &body)
		req.Header.Set("Content-Type", form.FormDataContentType())
		res := httptest.NewRecorder()
		h.CreateHero(res, req)
		return res
	}

	for _, tt := range []struct {
		name   string
		fields map[string]string
	}{
		{name: "no headline", fields: map[string]string{
			"primary_label": "去看看", "primary_href": "/deals", "alt": "秋季新品主視覺",
		}},
		{name: "no alt text for the image", fields: map[string]string{
			"headline": headline, "primary_label": "去看看", "primary_href": "/deals",
		}},
	} {
		res := post(tt.fields)
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: answered %d, want 422", tt.name, res.Code)
		}
		if stored() {
			t.Fatalf("%s: the refused slide's image was stored; it is an orphan only the "+
				"sweeper reclaims, and a form refused again and again writes one each time", tt.name)
		}
	}

	res := post(map[string]string{
		"headline": headline, "primary_label": "去看看", "primary_href": "/deals",
		"alt": "秋季新品主視覺",
	})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/home?ok=1" {
		t.Fatalf("a complete slide answered %d to %q, want 303 to ?ok=1",
			res.Code, res.Header().Get("Location"))
	}
	if !stored() {
		t.Fatal("a complete slide's image was not stored; the refusals above prove nothing")
	}
}

func TestHeroImageRefusalsKeepTheCompleteDraft(t *testing.T) {
	staffCtx, _ := admintest.StaffContext(t, pool)
	p := admintest.AdminRolePool(t, pool)
	s := content.NewStore(p)
	log := slog.New(slog.DiscardHandler)
	mux := http.NewServeMux()
	content.NewHandler(s, media.NewHandler(media.NewStore(p), log), newsletter.NewStore(p), log).Routes(mux, admintest.BackOffice)
	baseline, err := s.HeroSlides(staffCtx)
	if err != nil {
		t.Fatal(err)
	}
	var valid bytes.Buffer
	if err := png.Encode(&valid, image.NewRGBA(image.Rect(0, 0, 16, 9))); err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{"eyebrow": " 原始副標 ", "headline": " 原始標題 ", "body": " 原始內文 ", "primary_label": " 主按鈕 ", "primary_href": "/deals?q=1&sort=price", "second_label": " 次按鈕 ", "second_href": "/about", "alt": " 原始圖片 ", "days": "007", "eyebrow_en": " Raw kicker ", "headline_en": " Raw title ", "body_en": " Raw body ", "primary_label_en": " Primary ", "second_label_en": " Secondary ", "alt_en": " Raw picture "}
	ids := map[string]string{"eyebrow": "h-eyebrow", "headline": "h-headline", "body": "h-body", "primary_label": "h-plabel", "primary_href": "h-phref", "second_label": "h-slabel", "second_href": "h-shref", "alt": "h-alt", "days": "h-days", "eyebrow_en": "h-eyebrow-en", "headline_en": "h-headline-en", "body_en": "h-body-en", "primary_label_en": "h-plabel-en", "second_label_en": "h-slabel-en", "alt_en": "h-alt-en"}
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(staffCtx, locale)
		for _, tt := range []struct {
			name, errorID string
			picture       []byte
			missingAlt    bool
		}{
			{name: "corrupt image", errorID: "h-image", picture: []byte("corrupt PNG")},
			{name: "invalid image description", errorID: "h-alt", picture: valid.Bytes(), missingAlt: true},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				var body bytes.Buffer
				form := multipart.NewWriter(&body)
				want := map[string]string{}
				for name, value := range fields {
					if tt.missingAlt && name == "alt" {
						value = ""
					}
					want[ids[name]] = value
					if err := form.WriteField(name, value); err != nil {
						t.Fatal(err)
					}
				}
				part, err := form.CreateFormFile("image", "hero.png")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = part.Write(tt.picture); err != nil {
					t.Fatal(err)
				}
				if err = form.Close(); err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/home", &body)
				req.Header.Set("Content-Type", form.FormDataContentType())
				res := httptest.NewRecorder()
				mux.ServeHTTP(res, req)
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatalf("refused hero image = %d, want 422", res.Code)
				}
				doc, err := html.Parse(strings.NewReader(res.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				got := map[string]string{}
				for n := range doc.Descendants() {
					if n.Type != html.ElementNode {
						continue
					}
					attrs := map[string]string{}
					for _, a := range n.Attr {
						attrs[a.Key] = a.Val
					}
					id := attrs["id"]
					if _, ok := want[id]; !ok {
						continue
					}
					value := attrs["value"]
					if n.Data == "textarea" && n.FirstChild != nil {
						value = n.FirstChild.Data
					}
					got[id] = value
				}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("hero upload draft (-want +got):\n%s", diff)
				}
				admintest.AssertRefusedInput(t, res.Body.String(), tt.errorID, "")
				after, err := s.HeroSlides(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(baseline.Rows, after.Rows); diff != "" {
					t.Errorf("refusal changed saved hero slides (-want +got):\n%s", diff)
				}
			})
		}
	}
}

func TestHeroWithoutASelectedImageStillUsesBuiltInArtwork(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	p := admintest.AdminRolePool(t, pool)
	s := content.NewStore(p)
	log := slog.New(slog.DiscardHandler)
	mux := http.NewServeMux()
	content.NewHandler(s, media.NewHandler(media.NewStore(p), log), newsletter.NewStore(p), log).Routes(mux, admintest.BackOffice)
	headline := "optional-image-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		if _, err := pool.Exec(context.WithoutCancel(t.Context()), `DELETE FROM hero_slides WHERE headline = $1`, headline); err != nil {
			t.Errorf("remove optional-image slide: %v", err)
		}
	})
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for name, value := range map[string]string{"headline": headline, "primary_label": "Browse", "primary_href": "/deals"} {
		if err := form.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/home", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/home?ok=1" {
		t.Fatalf("optional image = %d to %q, want 303 to ?ok=1", res.Code, res.Header().Get("Location"))
	}
	var imageKey string
	if err := pool.QueryRow(ctx, `SELECT coalesce(image_key, '') FROM hero_slides WHERE headline = $1`, headline).Scan(&imageKey); err != nil {
		t.Fatal(err)
	}
	if imageKey != "" {
		t.Errorf("optional hero image key = %q, want empty", imageKey)
	}
}

func TestRefusedHomeFormsKeepBothEditorQueues(t *testing.T) {
	p := admintest.Pool(t)
	staffCtx, _ := admintest.StaffContext(t, p)
	heroID, bannerID := seedHomeEditorQueues(t, staffCtx, p)
	state := func() []byte {
		t.Helper()
		var rows []byte
		if err := p.QueryRow(staffCtx, `
			SELECT jsonb_build_object('hero', row_to_json(h), 'banner', row_to_json(b))
			FROM hero_slides h CROSS JOIN promo_banners b WHERE h.id = $1 AND b.id = $2`,
			heroID, bannerID).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	before := state()
	adminPool := admintest.AdminRolePool(t, p)
	log := slog.New(slog.DiscardHandler)
	mux := http.NewServeMux()
	content.NewHandler(content.NewStore(adminPool), media.NewHandler(media.NewStore(adminPool), log),
		newsletter.NewStore(adminPool), log).Routes(mux, admintest.BackOffice)
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(staffCtx, locale)
		t.Run(locale.Tag(), func(t *testing.T) {
			get := httptest.NewRecorder()
			mux.ServeHTTP(get, httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/home", nil))
			if get.Code != http.StatusOK {
				t.Fatalf("home GET = %d, want 200", get.Code)
			}
			assertHomeEditorQueues(t, ctx, get.Body.String(), heroID, bannerID)
			for _, tt := range []struct {
				name, path, errorID, draftID, draft string
			}{
				{name: "hero", path: "/admin/home", errorID: "h-headline", draftID: "h-plabel", draft: " Draft primary "},
				{name: "banner", path: "/admin/home/banner", errorID: "b-message", draftID: "b-short", draft: "Draft strip"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					res := httptest.NewRecorder()
					mux.ServeHTTP(res, refusedHomeFormRequest(t, ctx, tt.path))
					if res.Code != http.StatusUnprocessableEntity || res.Header().Get("Location") != "" {
						t.Fatalf("refused %s = %d to %q, want 422 without Location", tt.name, res.Code, res.Header().Get("Location"))
					}
					assertHomeEditorQueues(t, ctx, res.Body.String(), heroID, bannerID)
					admintest.AssertRefusedInput(t, res.Body.String(), tt.errorID, "")
					draft := admintest.InputElementByID(t, res.Body.String(), tt.draftID)
					if got := admintest.InputAttribute(t, draft, "value"); got != tt.draft {
						t.Errorf("refused %s draft = %q, want %q", tt.name, got, tt.draft)
					}
					if diff := cmp.Diff(before, state()); diff != "" {
						t.Errorf("refusal changed existing editor rows (-want +got):\n%s", diff)
					}
				})
			}
		})
	}
}

func seedHomeEditorQueues(t *testing.T, ctx context.Context, p *pgxpool.Pool) (heroID, bannerID string) {
	t.Helper()
	heroID, bannerID = uuid.NewString(), uuid.NewString()
	if _, err := p.Exec(ctx, `DELETE FROM hero_slides; DELETE FROM promo_banners`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `
		INSERT INTO hero_slides (id, headline, primary_cta_label, primary_cta_href, position, is_active)
		VALUES ($1, 'Existing slide', 'Browse', '/deals', 0, true)`, heroID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `
		INSERT INTO promo_banners (id, message, message_en, is_active)
		VALUES ($1, 'Existing strip', 'Existing strip', true)`, bannerID); err != nil {
		t.Fatal(err)
	}
	return heroID, bannerID
}

func assertHomeEditorQueues(t *testing.T, ctx context.Context, body, heroID, bannerID string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"/admin/home/" + heroID + "/active":          true,
		"/admin/home/banner/" + bannerID + "/active": true,
	}
	got := map[string]bool{}
	for node := range doc.Descendants() {
		if node.Type != html.ElementNode || node.Data != "form" {
			continue
		}
		var method, action string
		for _, attr := range node.Attr {
			switch attr.Key {
			case "method":
				method = attr.Val
			case "action":
				action = attr.Val
			}
		}
		if method == "post" && want[action] {
			got[action] = true
		}
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("existing home toggle forms (-want +got):\n%s", diff)
	}
	for _, key := range []i18n.Key{i18n.KeyAdminHomeEmpty, i18n.KeyAdminHomeBannerEmpty} {
		if strings.Contains(body, i18n.T(ctx, key)) {
			t.Errorf("nonempty home editor shows %q", i18n.T(ctx, key))
		}
	}
}

func refusedHomeFormRequest(t *testing.T, ctx context.Context, path string) *http.Request {
	t.Helper()
	if path == "/admin/home/banner" {
		fields := url.Values{"message": {""}, "short": {"Draft strip"}}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(fields.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return req
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for key, value := range map[string]string{"headline": "", "primary_label": " Draft primary ", "primary_href": "/deals"} {
		if err := form.WriteField(key, value); err != nil {
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

func TestHeroUploadsKeepStorageFailuresSeparateFromRefusals(t *testing.T) {
	owner := admintest.Pool(t)
	staff, _ := admintest.StaffContext(t, owner)
	adminPool := admintest.AdminRolePool(t, owner)
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(staff, locale)
		fields := map[string]string{
			"headline": " Draft hero " + uuid.NewString()[:8], "headline_en": " Raw English title ",
			"primary_label": " Browse ", "primary_href": "/deals", "days": "007",
			"alt": " Draft picture ", "alt_en": " English picture ",
		}
		var diagnostics bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&diagnostics, nil))
		witness := &heroUploadWriteTrace{}
		mediaPool := heroUploadWritePool(t, adminPool, witness)
		mux := http.NewServeMux()
		content.NewHandler(content.NewStore(adminPool), media.NewHandler(media.NewStore(mediaPool), logger),
			newsletter.NewStore(adminPool), logger).Routes(mux, admintest.BackOffice)
		for _, tt := range []struct {
			name   string
			alt    string
			status int
		}{
			{name: "storage", alt: fields["alt"], status: 500},
			{name: "invalid utf8", alt: string([]byte{0xff}), status: 400},
			{name: "nul text", alt: string([]byte{'a', 0, 'b'}), status: 400},
			{name: "corrupt", alt: fields["alt"], status: 422},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				before := heroUploadSavedRows(t, owner)
				diagnostics.Reset()
				res := httptest.NewRecorder()
				if tt.name == "storage" {
					res = heroUploadWithStorageFault(t, ctx, owner, mux, fields, witness)
				} else {
					mux.ServeHTTP(res, heroUploadFailureRequest(t, ctx, fields, tt.alt, tt.name == "corrupt"))
					if witness.seen != 1 || ctx.Err() != nil {
						t.Fatalf("refused input reached PutMedia or lost its request: writes=%d parent=%v", witness.seen, ctx.Err())
					}
				}
				assertHeroUploadFailure(t, ctx, res, diagnostics.String(), tt.status, fields)
				if diff := cmp.Diff(before, heroUploadSavedRows(t, owner)); diff != "" {
					t.Errorf("refused hero upload changed saved hero/media/audit (-want +got):\n%s", diff)
				}
			})
		}
		beforeAudit := heroUploadAuditCount(t, owner)
		diagnostics.Reset()
		witness.code = ""
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, heroUploadFailureRequest(t, ctx, fields, fields["alt"], false))
		if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/home?ok=1" {
			t.Errorf("recovered hero upload = %d to %q, want 303 to the editor", res.Code, res.Header().Get("Location"))
		}
		if witness.seen != 2 || witness.code != "" || !witness.live || ctx.Err() != nil {
			t.Fatalf("recovered PutMedia = %d/%q/live=%t parent=%v, want the second actual successful write", witness.seen, witness.code, witness.live, ctx.Err())
		}
		var alt, altEn, imageKey string
		if err := owner.QueryRow(ctx, `SELECT image_alt, image_alt_en, image_key FROM hero_slides WHERE headline=$1`,
			strings.TrimSpace(fields["headline"])).Scan(&alt, &altEn, &imageKey); err != nil {
			t.Fatal(err)
		}
		if alt != "Draft picture" || altEn != fields["alt_en"] || imageKey == "" || heroUploadAuditCount(t, owner) != beforeAudit+1 {
			t.Errorf("recovered hero = %q/%q/image=%q, want saved descriptions, image and exactly one new audit", alt, altEn, imageKey)
		}
		var imageExists bool
		if err := owner.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM media_objects WHERE digest=$1)`, imageKey).Scan(&imageExists); err != nil {
			t.Fatal(err)
		}
		if !imageExists {
			t.Error("recovered hero refers to an image that was not stored")
		}
	}
}

func heroUploadWithStorageFault(t *testing.T, ctx context.Context, owner *pgxpool.Pool, mux *http.ServeMux, fields map[string]string, witness *heroUploadWriteTrace) *httptest.ResponseRecorder {
	t.Helper()
	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pgtx.Rollback(ctx, tx)
	if _, err := tx.Exec(ctx, `LOCK TABLE media_objects IN SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, heroUploadFailureRequest(t, ctx, fields, fields["alt"], false))
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if witness.seen != 1 || witness.code != "55P03" || !witness.live || ctx.Err() != nil {
		t.Fatalf("PutMedia fault witness = %d/%q/live=%t parent=%v, want exactly one real 55P03 with live request", witness.seen, witness.code, witness.live, ctx.Err())
	}
	return res
}

type heroUploadTraceKey struct{}

type heroUploadWriteTrace struct {
	seen int
	code string
	live bool
}

func (w *heroUploadWriteTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "-- name: PutMedia :exec\n") {
		w.seen++
		return context.WithValue(ctx, heroUploadTraceKey{}, true)
	}
	return ctx
}

func (w *heroUploadWriteTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if target, ok := ctx.Value(heroUploadTraceKey{}).(bool); !ok || !target {
		return
	}
	w.live = ctx.Err() == nil
	if cause, ok := errors.AsType[*pgconn.PgError](data.Err); ok {
		w.code = cause.Code
	}
}

func heroUploadWritePool(t *testing.T, adminPool *pgxpool.Pool, witness *heroUploadWriteTrace) *pgxpool.Pool {
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

func heroUploadFailureRequest(t *testing.T, ctx context.Context, fields map[string]string, alt string, corrupt bool) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for name, value := range fields {
		if name == "alt" {
			value = alt
		}
		if err := form.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := form.CreateFormFile("image", "hero.png")
	if err != nil {
		t.Fatal(err)
	}
	if corrupt {
		if _, err := part.Write([]byte("corrupt photograph")); err != nil {
			t.Fatal(err)
		}
	} else if err := png.Encode(part, image.NewRGBA(image.Rect(0, 0, 17, 7))); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/home", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	return req
}

func assertHeroUploadFailure(t *testing.T, ctx context.Context, res *httptest.ResponseRecorder, diagnostics string, status int, fields map[string]string) {
	t.Helper()
	if res.Code != status || res.Header().Get("Location") != "" {
		t.Errorf("hero image upload = %d to %q, want %d with no redirect", res.Code, res.Header().Get("Location"), status)
	}
	body := res.Body.String()
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
		admintest.AssertRefusedInput(t, body, "h-image", "")
		if !strings.Contains(body, i18n.T(ctx, i18n.KeyAdminNoticeNotImage)) || !strings.Contains(diagnostics, `"level":"WARN"`) {
			t.Error("corrupt upload did not retain the image refusal and Warn diagnostic")
		}
		for field, id := range map[string]string{"headline": "h-headline", "headline_en": "h-headline-en", "alt": "h-alt", "alt_en": "h-alt-en", "days": "h-days"} {
			input := admintest.InputElementByID(t, body, id)
			if got := admintest.InputAttribute(t, input, "value"); got != fields[field] {
				t.Errorf("refused image draft %s = %q, want raw %q", id, got, fields[field])
			}
		}
	}
	if status != 422 && (strings.Contains(body, i18n.T(ctx, i18n.KeyAdminNoticeUploadFailed)) || strings.Contains(body, `id="h-image-error"`)) {
		t.Error("a non-image failure was blamed on the image")
	}
}

func heroUploadAuditCount(t *testing.T, owner *pgxpool.Pool) int {
	t.Helper()
	var count int
	if err := owner.QueryRow(t.Context(), `SELECT count(*) FROM audit_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func heroUploadSavedRows(t *testing.T, owner *pgxpool.Pool) string {
	t.Helper()
	var saved string
	if err := owner.QueryRow(t.Context(), `SELECT jsonb_build_object(
		'hero', (SELECT coalesce(jsonb_agg(to_jsonb(h) ORDER BY h.id), '[]') FROM hero_slides h),
		'media', (SELECT coalesce(jsonb_agg(to_jsonb(m) ORDER BY m.digest), '[]') FROM media_objects m),
		'audit', (SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY a.id), '[]') FROM audit_events a)
	)::text`).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	return saved
}
