//go:build integration

package campaigns_test

import (
	"bytes"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/campaigns"
	"github.com/koopa0/goen/internal/catalog"
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
