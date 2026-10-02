//go:build integration

package admin_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/i18n"
)

func TestACampaignsDatesAndStateAreEditedOnItsOwnPage(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug := testCampaignSlug(t)
	if _, err := s.CreateCampaign(ctx, &admin.CampaignForm{Slug: slug, Title: "秋季精選", Days: 7}); err != nil {
		t.Fatal(err)
	}

	errs, err := s.SetCampaignWindow(ctx, slug, "2026-11-01T09:00", "2026-11-30T23:59")
	if err != nil || len(errs) > 0 {
		t.Fatalf("SetCampaignWindow: %v %v", err, errs)
	}
	got, err := s.CampaignDetail(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "秋季精選" || got.StartsAtInput != "2026-11-01T09:00" || got.EndsAtInput != "2026-11-30T23:59" {
		t.Errorf("the page reads %+v after the edit", got)
	}

	for name, window := range map[string][2]string{
		"an empty window":    {"2026-12-01T09:00", "2026-12-01T09:00"},
		"a window too long":  {"2026-12-01T09:00", "2027-03-02T09:01"},
		"a malformed minute": {"2026-12-01", "2026-12-02"},
	} {
		errs, err = s.SetCampaignWindow(ctx, slug, window[0], window[1])
		if err != nil || errs["window"] != i18n.T(ctx, i18n.KeyFormCampaignWindow) {
			t.Errorf("%s = %v %v, want the window refusal", name, err, errs)
		}
	}
	if after, _ := s.CampaignDetail(ctx, slug); after.StartsAtInput != "2026-11-01T09:00" {
		t.Errorf("a refused edit moved the dates to %s", after.StartsAtInput)
	}
	if _, err = s.SetCampaignWindow(ctx, "no-such-"+uuid.NewString()[:8], "2026-11-01T09:00", "2026-11-02T09:00"); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("unknown campaign = %v, want ErrNotFound", err)
	}
}

func TestEditingACampaignsDatesRecordsTheOldAndTheNewOnes(t *testing.T) {
	ctx, actor := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug := testCampaignSlug(t)
	if _, err := s.CreateCampaign(ctx, &admin.CampaignForm{Slug: slug, Title: "稽核活動", Days: 7}); err != nil {
		t.Fatal(err)
	}
	for _, window := range [][2]string{{"2026-11-01T09:00", "2026-11-10T09:00"}, {"2026-11-02T10:00", "2026-11-12T10:00"}} {
		if errs, err := s.SetCampaignWindow(ctx, slug, window[0], window[1]); err != nil || len(errs) > 0 {
			t.Fatalf("SetCampaignWindow: %v %v", err, errs)
		}
	}

	var ok bool
	if err := pool.QueryRow(ctx, `
		SELECT (before->>'ends_at')::timestamptz = $1::timestamptz
		   AND (after->>'starts_at')::timestamptz = $2::timestamptz
		   AND after->>'slug' = $3
		FROM audit_events
		WHERE action = $4 AND actor_id_snapshot = $5
		ORDER BY occurred_at DESC, id DESC LIMIT 1`,
		time.Date(2026, 11, 10, 1, 0, 0, 0, time.UTC), time.Date(2026, 11, 2, 2, 0, 0, 0, time.UTC),
		slug, string(admin.ActionSetCampaignWindow), actor).Scan(&ok); err != nil {
		t.Fatalf("read the audit row: %v", err)
	}
	if !ok {
		t.Error("the audit row does not hold the previous end and the new start as UTC instants")
	}
}

func TestTheCampaignProductSearchOffersWhatIsNotFeaturedYet(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug := testCampaignSlug(t)
	if _, err := s.CreateCampaign(ctx, &admin.CampaignForm{Slug: slug, Title: "測試活動", Days: 7}); err != nil {
		t.Fatal(err)
	}
	featured := discountedProductSlug(t)
	if err := s.FeatureProduct(ctx, slug, featured); err != nil {
		t.Fatal(err)
	}
	fresh := draftProduct(t, ctx, s)

	got, err := s.SearchCampaignProducts(ctx, slug, fresh)
	if err != nil || len(got) != 1 || got[0].Slug != fresh {
		t.Fatalf("search by slug = %v %v, want exactly %s", got, err, fresh)
	}
	if byName, searchErr := s.SearchCampaignProducts(ctx, slug, "規格表測試 "+fresh); searchErr != nil || len(byName) != 1 {
		t.Errorf("search by name = %v %v, want the one product", byName, searchErr)
	}
	// A slug is a substring of its siblings' (pixelight-9-pro, pixelight-9-pro-case),
	// so the search may offer those; only the featured product itself is refused.
	again, searchErr := s.SearchCampaignProducts(ctx, slug, featured)
	if searchErr != nil {
		t.Fatal(searchErr)
	}
	for _, offered := range again {
		if offered.Slug == featured {
			t.Errorf("the featured product %s is offered again: %v", featured, again)
		}
	}
	if _, execErr := pool.Exec(ctx, `UPDATE products SET status = 'archived' WHERE slug = $1`, fresh); execErr != nil {
		t.Fatal(execErr)
	}
	if archived, searchErr := s.SearchCampaignProducts(ctx, slug, fresh); searchErr != nil || len(archived) != 0 {
		t.Errorf("an archived product is offered: %v %v", archived, searchErr)
	}
	if none, searchErr := s.SearchCampaignProducts(ctx, slug, "   "); searchErr != nil || len(none) != 0 {
		t.Errorf("a blank search = %v %v, want nothing", none, searchErr)
	}
}
