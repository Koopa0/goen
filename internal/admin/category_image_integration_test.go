//go:build integration

package admin_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"image"
	"image/png"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/ui/pages"
)

func categoryImageRequest(t *testing.T, slug, alt string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("image", "department.png")
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(part, image.NewRGBA(image.Rect(0, 0, 15, 10))); err != nil {
		t.Fatal(err)
	}
	if err := form.WriteField("alt", alt); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/categories/"+slug+"/image", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.SetPathValue("slug", slug)
	return req
}

// A sub-category shows its department's tone and photograph: the listing reads
// them up the trail, so a department set once dresses every page under it.
func TestACategoryTakesItsDepartmentsToneAndPhotographUntilItSetsItsOwn(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	h := adminHandlerOver(pool, s)
	root := "dept-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	child := root + "-sub"
	if errs, err := s.CreateCategory(ctx, &admin.TaxonomyForm{Slug: root, Name: "館別", Tone: "sage"}); err != nil || len(errs) > 0 {
		t.Fatalf("create the department: %v %v", errs, err)
	}
	if errs, err := s.CreateCategory(ctx, &admin.TaxonomyForm{Slug: child, Name: "子分類", Parent: root}); err != nil || len(errs) > 0 {
		t.Fatalf("create the sub-category: %v %v", errs, err)
	}
	store := catalog.NewStore(pool)

	view, err := store.Listing(ctx, child, catalog.Filters{})
	if err != nil || view.Theme.ToneAttr() != "sage" || view.Theme.Image().Shown() {
		t.Fatalf("sub-category before any photograph: tone %q photo %+v (err %v), want sage and none", view.Theme.ToneAttr(), view.Theme.Image(), err)
	}

	// Without alt text the photograph is refused at that field and nothing is stored.
	refused := httptest.NewRecorder()
	h.SetCategoryImage(refused, categoryImageRequest(t, root, "  ").WithContext(ctx))
	if refused.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(refused.Body.String(), `aria-describedby="cat-alt-error"`) {
		t.Fatalf("no alt answered %d, want 422 with the alt field flagged", refused.Code)
	}

	ok := httptest.NewRecorder()
	h.SetCategoryImage(ok, categoryImageRequest(t, root, "館別照片").WithContext(ctx))
	if ok.Code != http.StatusSeeOther || ok.Header().Get("Location") != "/admin/categories/"+root+"?ok=1" {
		t.Fatalf("upload answered %d to %q, want 303 to ?ok=1", ok.Code, ok.Header().Get("Location"))
	}
	view, err = store.Listing(ctx, child, catalog.Filters{})
	if err != nil || !view.Theme.Image().Shown() || !strings.HasPrefix(view.Theme.Image().URL, "/media/") || view.Theme.Image().Alt != "館別照片" {
		t.Fatalf("sub-category photograph = %+v (err %v), want the department's", view.Theme.Image(), err)
	}
	var audited int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = 'category.image.set' AND after->>'category' = $1`, root).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("audit rows = %d (err %v), want 1", audited, err)
	}

	// The sub-category's own tone wins over the department's.
	if err = s.Rename(ctx, "category", child, "子分類", "", "", "ink", false); err != nil {
		t.Fatalf("set the sub-category's tone: %v", err)
	}
	view, err = store.Listing(ctx, child, catalog.Filters{})
	if err != nil || view.Theme.ToneAttr() != "ink" {
		t.Fatalf("sub-category tone = %q (err %v), want its own ink", view.Theme.ToneAttr(), err)
	}
	if err = s.Rename(ctx, "category", child, "子分類", "", "", "neon", false); err == nil {
		t.Error("a tone outside the set was accepted")
	}

	remove := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/categories/"+root+"/image/remove", http.NoBody)
	remove.SetPathValue("slug", root)
	remove.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	gone := httptest.NewRecorder()
	h.RemoveCategoryImage(gone, remove)
	if gone.Code != http.StatusSeeOther {
		t.Fatalf("remove answered %d, want 303", gone.Code)
	}
	view, err = store.Listing(ctx, child, catalog.Filters{})
	if err != nil || view.Theme.Image().Shown() {
		t.Fatalf("after removal the photograph is %+v (err %v)", view.Theme.Image(), err)
	}
}

// A root with no tone of its own is stone, and a campaign takes any tone of the
// closed set: the Go set and the schema's CHECK name the same six.
func TestEveryToneOfTheClosedSetIsStorable(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug := "tone-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	if errs, err := s.CreateCategory(ctx, &admin.TaxonomyForm{Slug: slug, Name: "無色調"}); err != nil || len(errs) > 0 {
		t.Fatalf("create: %v %v", errs, err)
	}
	view, err := catalog.NewStore(pool).Listing(ctx, slug, catalog.Filters{})
	if err != nil || view.Theme.ToneAttr() != "stone" {
		t.Fatalf("a root with no tone = %q (err %v), want stone", view.Theme.ToneAttr(), err)
	}

	camp := "tone-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	errs, err := s.CreateCampaign(ctx, &admin.CampaignForm{Slug: camp, Title: "色調測試", Days: 7})
	if err != nil || len(errs) > 0 {
		t.Fatalf("create the campaign: %v %v", errs, err)
	}
	for _, tone := range pages.Tones() {
		if err = s.SetCampaignTone(ctx, camp, string(tone)); err != nil {
			t.Errorf("SetCampaignTone(%q): %v", tone, err)
		}
		if err = s.Rename(ctx, "category", slug, "無色調", "", "", string(tone), false); err != nil {
			t.Errorf("Rename with tone %q: %v", tone, err)
		}
	}
	if err = s.SetCampaignTone(ctx, camp, "neon"); err == nil {
		t.Error("a campaign accepted a tone outside the set")
	}
	cv, err := catalog.NewStore(pool).Campaign(ctx, camp)
	if err != nil || cv.Tone != pages.ToneInk {
		t.Fatalf("campaign tone = %q (err %v), want the last one set, ink", cv.Tone, err)
	}
}

// The health page counts uploads nothing references; a category's photograph
// is a reference.
func TestAUploadHeldByACategoryIsNotCountedUnreferenced(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	messages := outbox.NewStore(pool, slog.New(slog.DiscardHandler))
	count := func() int64 {
		t.Helper()
		health, err := s.WorkerHealth(ctx, messages)
		if err != nil {
			t.Fatalf("read health: %v", err)
		}
		return health.UnreferencedMedia
	}

	before := count()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	digest := hex.EncodeToString(raw)
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_objects (digest, content_type, byte_size, width, height, bytes, created_at)
		VALUES ($1, 'image/png', 1, 1, 1, '\x00', now() - interval '48 hours')`, digest); err != nil {
		t.Fatalf("store an old upload: %v", err)
	}
	if got := count(); got != before+1 {
		t.Fatalf("an unreferenced upload counts %d, want %d", got, before+1)
	}

	slug := "held-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	if errs, err := s.CreateCategory(ctx, &admin.TaxonomyForm{Slug: slug, Name: "持有照片"}); err != nil || len(errs) > 0 {
		t.Fatalf("create: %v %v", errs, err)
	}
	if err := s.SetCategoryImage(ctx, slug, digest, "照片", ""); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if got := count(); got != before {
		t.Errorf("a category's photograph counts %d unreferenced, want %d", got, before)
	}
}
