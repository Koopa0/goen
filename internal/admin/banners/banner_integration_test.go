//go:build integration

package banners_test

import (
	"testing"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/banners"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/site"
)

// TestStore_BannerPromotion: the shop can run a promotion.
func TestStore_BannerPromotion(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := banners.NewStore(pool)

	if errs, err := s.CreateBanner(ctx, &banners.Form{
		Message: "全站滿 NT$3,000 免運", Short: "滿 3,000 免運",
		MessageEn: "Free delivery over NT$3,000", ShortEn: "Free over 3,000",
		CTALabel: "看看", CTAHref: "/deals", CTALabelEn: "Shop", Days: 7,
	}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateBanner: %v %v", err, errs)
	}

	list, err := s.Banners(ctx, "")
	if err != nil {
		t.Fatalf("Banners: %v", err)
	}
	// The list puts live strips first and the newest first among them, so the
	// first match is this run's: an earlier run's copy is switched off.
	var made *banners.Banner
	for i := range list.Rows {
		if list.Rows[i].Message == "全站滿 NT$3,000 免運" {
			made = &list.Rows[i]
			break
		}
	}
	if made == nil {
		t.Fatal("the promotion is not in the back office's list")
	}
	if !made.Active || !made.Translated() || !made.HasCTA() || !made.Scheduled() {
		t.Errorf("the promotion reads as %+v", *made)
	}

	shop := site.NewStore(pool)
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		want   string
	}{
		{name: "Chinese", locale: i18n.ZhHant, want: "全站滿 NT$3,000 免運"},
		{name: "English", locale: i18n.En, want: "Free delivery over NT$3,000"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			banner, bannerErr := shop.Banner(i18n.WithLocale(ctx, tt.locale), "")
			if bannerErr != nil {
				t.Fatalf("Banner: %v", bannerErr)
			}
			if banner.Message != tt.want {
				t.Errorf("the strip reads %q, want %q", banner.Message, tt.want)
			}
		})
	}

	if err := s.SetBannerActive(ctx, made.ID, false); err != nil {
		t.Fatalf("SetBannerActive: %v", err)
	}
	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM promo_banners WHERE id = $1 AND NOT is_active`,
		made.ID).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Error("switching a promotion off deleted it")
	}
}

// TestStore_CreateBannerLocalLink: a promotion's button must stay on this site.
func TestStore_CreateBannerLocalLink(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := banners.NewStore(pool)

	for _, href := range []string{
		"https://evil.example/deals",
		"//evil.example/deals",
		"javascript:alert(1)",
	} {
		errs, err := s.CreateBanner(ctx, &banners.Form{
			Message: "測試", CTALabel: "看看", CTAHref: href,
		})
		if err != nil {
			t.Fatalf("CreateBanner(%q): %v", href, err)
		}
		if errs["cta"] == "" {
			t.Errorf("CreateBanner accepted the href %q: %v", href, errs)
		}
	}

	errs, err := s.CreateBanner(ctx, &banners.Form{Message: "測試", CTALabel: "看看"})
	if err != nil {
		t.Fatalf("CreateBanner: %v", err)
	}
	if errs["cta"] == "" {
		t.Errorf("a label with no href was accepted: %v", errs)
	}
}
