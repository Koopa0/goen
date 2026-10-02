//go:build integration

package content_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/content"
	"github.com/koopa0/goen/internal/ui/pages"
)

// The back office shows the carousel the storefront builds, so a campaign that
// feeds it appears there with its source named.
func TestTheHomeQueuePageCarriesTheCampaignSlidesTheStorefrontShows(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := content.NewStore(pool)
	// Queued slides come first in the carousel and there is room for three, so
	// what other tests left behind would push the campaign out.
	if _, err := pool.Exec(ctx, `DELETE FROM hero_slides`); err != nil {
		t.Fatalf("clear hero slides: %v", err)
	}
	title := "輪播活動 " + uuid.NewString()[:8]
	if _, err := pool.Exec(ctx, `
		INSERT INTO sale_campaigns (slug, title, ends_at)
		VALUES ($1, $2, now() + interval '1 hour')`, "live-"+uuid.NewString()[:8], title); err != nil {
		t.Fatalf("insert campaign: %v", err)
	}
	view, err := s.HeroSlides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, slide := range view.Carousel {
		if slide.Title == title {
			if slide.Source != pages.SlideCampaign {
				t.Errorf("source = %q, want %q", slide.Source, pages.SlideCampaign)
			}
			return
		}
	}
	t.Errorf("the running campaign %q is not among the %d carousel slides", title, len(view.Carousel))
}
