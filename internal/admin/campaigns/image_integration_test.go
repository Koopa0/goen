//go:build integration

package campaigns_test

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

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/campaigns"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
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
