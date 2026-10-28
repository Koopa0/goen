//go:build integration

package slides_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/slides"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/media/mediatest"
	"github.com/koopa0/goen/internal/ui/uitest"
)

// TestHandler_CreateHeroNoImageStored: a refused hero slide stores no image.
//
// It holds the order of the hero form: its copy
// is checked before its image is decoded, so a slide refused for a missing
// headline or missing alt text writes nothing to media_objects. The last post
// is the control: the same image under complete copy is stored, so the
// absences before it are about the refusal and not about the probe.
func TestHandler_CreateHeroNoImageStored(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	h := handlerOver(newStore(pool))
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
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/slides", &body)
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
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/slides?ok=1" {
		t.Fatalf("a complete slide answered %d to %q, want 303 to ?ok=1",
			res.Code, res.Header().Get("Location"))
	}
	if !stored() {
		t.Fatal("a complete slide's image was not stored; the refusals above prove nothing")
	}
}

// TestHandler_CreateHeroImageRefusal: hero image refusals keep the complete draft.
func TestHandler_CreateHeroImageRefusal(t *testing.T) {
	staffCtx, _ := admintest.StaffContext(t, pool)
	p := admintest.AdminRolePool(t, pool)
	s := newStore(p)
	log := slog.New(slog.DiscardHandler)
	mux := http.NewServeMux()
	slides.NewHandler(s, media.NewHandler(media.NewStore(p), log), log).Routes(mux, admintest.BackOffice)
	baseline, err := s.HeroSlides(staffCtx, "")
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
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/slides", &body)
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
				uitest.AssertRefusedInput(t, res.Body.String(), tt.errorID, "")
				after, err := s.HeroSlides(ctx, "")
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

// TestHandler_CreateHeroBuiltInArtwork: a hero without a selected image still uses the built-in artwork.
func TestHandler_CreateHeroBuiltInArtwork(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	p := admintest.AdminRolePool(t, pool)
	s := newStore(p)
	log := slog.New(slog.DiscardHandler)
	mux := http.NewServeMux()
	slides.NewHandler(s, media.NewHandler(media.NewStore(p), log), log).Routes(mux, admintest.BackOffice)
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
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/slides", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/slides?ok=1" {
		t.Fatalf("optional image = %d to %q, want 303 to ?ok=1", res.Code, res.Header().Get("Location"))
	}
	var imageKey string
	if err := pool.QueryRow(ctx, `SELECT coalesce(media_digest, '') FROM hero_slides WHERE headline = $1`, headline).Scan(&imageKey); err != nil {
		t.Fatal(err)
	}
	if imageKey != "" {
		t.Errorf("optional hero image key = %q, want empty", imageKey)
	}
}

// TestHandler_CreateHeroKeepsSchedule: a refused slide keeps the schedule.
func TestHandler_CreateHeroKeepsSchedule(t *testing.T) {
	p := admintest.Pool(t)
	staffCtx, _ := admintest.StaffContext(t, p)
	heroID := seedSlideSchedule(t, staffCtx, p)
	state := func() []byte {
		t.Helper()
		var rows []byte
		if err := p.QueryRow(staffCtx, `SELECT row_to_json(h) FROM hero_slides h WHERE h.id = $1`, heroID).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	before := state()
	adminPool := admintest.AdminRolePool(t, p)
	log := slog.New(slog.DiscardHandler)
	mux := http.NewServeMux()
	slides.NewHandler(newStore(adminPool), media.NewHandler(media.NewStore(adminPool), log), log).Routes(mux, admintest.BackOffice)
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(staffCtx, locale)
		t.Run(locale.Tag(), func(t *testing.T) {
			get := httptest.NewRecorder()
			mux.ServeHTTP(get, httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/slides", nil))
			if get.Code != http.StatusOK {
				t.Fatalf("slides GET = %d, want 200", get.Code)
			}
			assertSlideSchedule(t, ctx, get.Body.String(), heroID)
			res := httptest.NewRecorder()
			mux.ServeHTTP(res, refusedSlideRequest(t, ctx))
			if res.Code != http.StatusUnprocessableEntity || res.Header().Get("Location") != "" {
				t.Fatalf("refused slide = %d to %q, want 422 without Location", res.Code, res.Header().Get("Location"))
			}
			assertSlideSchedule(t, ctx, res.Body.String(), heroID)
			assertHomeExampleRefusal(t, res.Body.String(), "h-headline")
			draft := uitest.InputElementByID(t, res.Body.String(), "h-plabel")
			if got := uitest.InputAttribute(t, draft, "value"); got != " Draft primary " {
				t.Errorf("refused slide draft = %q, want %q", got, " Draft primary ")
			}
			if diff := cmp.Diff(before, state()); diff != "" {
				t.Errorf("refusal changed the existing slide (-want +got):\n%s", diff)
			}
		})
	}
}

func assertHomeExampleRefusal(t *testing.T, body, id string) {
	t.Helper()
	input := uitest.InputElementByID(t, body, id)
	type inputFacts struct{ Value, Invalid, Descriptions string }
	got := inputFacts{
		Value:        uitest.InputAttribute(t, input, "value"),
		Invalid:      uitest.InputAttribute(t, input, "aria-invalid"),
		Descriptions: uitest.InputAttribute(t, input, "aria-describedby"),
	}
	want := inputFacts{Invalid: "true", Descriptions: id + "-example " + id + "-error"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("refused field (-want +got):\n%s", diff)
	}
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for _, description := range strings.Fields(want.Descriptions) {
		var visible int
		for n := range doc.Descendants() {
			var nodeID, class string
			var hidden bool
			for _, a := range n.Attr {
				switch a.Key {
				case "id":
					nodeID = a.Val
				case "class":
					class = a.Val
				case "hidden":
					hidden = true
				}
			}
			if nodeID != description {
				continue
			}
			var text strings.Builder
			for child := range n.Descendants() {
				if child.Type == html.TextNode {
					text.WriteString(child.Data)
				}
			}
			if n.Data == "p" && !hidden && !strings.Contains(class, "sr-only") && strings.TrimSpace(text.String()) != "" {
				visible++
			}
		}
		if visible != 1 {
			t.Errorf("field %q description %q has %d visible text elements, want one", id, description, visible)
		}
	}
}

// seedSlideSchedule leaves exactly one slide on the schedule.
func seedSlideSchedule(t *testing.T, ctx context.Context, p *pgxpool.Pool) (heroID string) {
	t.Helper()
	heroID = uuid.NewString()
	if _, err := p.Exec(ctx, `DELETE FROM hero_slides`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `
		INSERT INTO hero_slides (id, headline, primary_cta_label, primary_cta_href, position, is_active)
		VALUES ($1, 'Existing slide', 'Browse', '/deals', 0, true)`, heroID); err != nil {
		t.Fatal(err)
	}
	return heroID
}

// assertSlideSchedule holds that the page still lists the seeded slide with
// its toggle, and does not call the schedule empty.
func assertSlideSchedule(t *testing.T, ctx context.Context, body, heroID string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	want := "/admin/slides/" + heroID + "/active"
	found := false
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
		if method == "post" && action == want {
			found = true
		}
	}
	if !found {
		t.Errorf("the existing slide has no toggle form %s", want)
	}
	if strings.Contains(body, i18n.T(ctx, i18n.KeyAdminHomeEmpty)) {
		t.Errorf("nonempty schedule shows %q", i18n.T(ctx, i18n.KeyAdminHomeEmpty))
	}
}

// refusedSlideRequest is a slide form with no headline, multipart as the
// browser sends it.
func refusedSlideRequest(t *testing.T, ctx context.Context) *http.Request {
	t.Helper()
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
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/slides", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	return req
}

// TestHandler_CreateHeroStorageFault: hero uploads keep storage failures separate from refusals.
func TestHandler_CreateHeroStorageFault(t *testing.T) {
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
		var writes mediatest.Writes
		mux := http.NewServeMux()
		slides.NewHandler(newStore(adminPool), media.NewHandler(mediatest.NewStore(t, adminPool, &writes), logger), logger).Routes(mux, admintest.BackOffice)
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
					res = heroUploadWithStorageFault(t, ctx, owner, mux, fields, &writes)
				} else {
					mux.ServeHTTP(res, heroUploadFailureRequest(t, ctx, fields, tt.alt, tt.name == "corrupt"))
					if writes.Calls != 1 || ctx.Err() != nil {
						t.Fatalf("refused input reached PutMedia or lost its request: writes=%d parent=%v", writes.Calls, ctx.Err())
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
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, heroUploadFailureRequest(t, ctx, fields, fields["alt"], false))
		if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/slides?ok=1" {
			t.Errorf("recovered hero upload = %d to %q, want 303 to the editor", res.Code, res.Header().Get("Location"))
		}
		if writes.Calls != 2 || writes.Code != "" || !writes.Live || ctx.Err() != nil {
			t.Fatalf("recovered PutMedia = %+v parent=%v, want the second actual successful write", writes, ctx.Err())
		}
		var alt, altEn, imageKey string
		if err := owner.QueryRow(ctx, `SELECT image_alt, image_alt_en, media_digest FROM hero_slides WHERE headline=$1`,
			strings.TrimSpace(fields["headline"])).Scan(&alt, &altEn, &imageKey); err != nil {
			t.Fatal(err)
		}
		if alt != "Draft picture" || altEn != "English picture" || imageKey == "" || heroUploadAuditCount(t, owner) != beforeAudit+1 {
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

func heroUploadWithStorageFault(t *testing.T, ctx context.Context, owner *pgxpool.Pool, mux *http.ServeMux, fields map[string]string, writes *mediatest.Writes) *httptest.ResponseRecorder {
	t.Helper()
	release := mediatest.HoldWrites(t, owner)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, heroUploadFailureRequest(t, ctx, fields, fields["alt"], false))
	release()
	if writes.Calls != 1 || writes.Code != "55P03" || !writes.Live || ctx.Err() != nil {
		t.Fatalf("PutMedia fault witness = %+v parent=%v, want exactly one real 55P03 with live request", *writes, ctx.Err())
	}
	return res
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
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/slides", &body)
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
		if !strings.Contains(diagnostics, `"level":"ERROR"`) {
			t.Errorf("storage diagnostics = %q, want Error", diagnostics)
		}
		if !strings.Contains(body, i18n.T(ctx, i18n.KeyAdminErrorBody)) || strings.Contains(body, "SQLSTATE") {
			t.Error("storage failure did not show a generic server error")
		}
	case 400:
		if !strings.Contains(body, i18n.T(ctx, i18n.KeyAdminBadForm)) {
			t.Error("malformed multipart text did not show the bad-form response")
		}
	case 422:
		uitest.AssertRefusedInput(t, body, "h-image", "")
		if !strings.Contains(body, i18n.T(ctx, i18n.KeyAdminNoticeNotImage)) || !strings.Contains(diagnostics, `"level":"WARN"`) {
			t.Error("corrupt upload did not retain the image refusal and Warn diagnostic")
		}
		for field, id := range map[string]string{"headline": "h-headline", "headline_en": "h-headline-en", "alt": "h-alt", "alt_en": "h-alt-en", "days": "h-days"} {
			input := uitest.InputElementByID(t, body, id)
			if got := uitest.InputAttribute(t, input, "value"); got != fields[field] {
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
