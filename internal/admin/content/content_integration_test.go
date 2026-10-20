//go:build integration

package content_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/content"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/site"
)

func TestTheShopCanRunAPromotion(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := newStore(pool)

	if errs, err := s.CreateBanner(ctx, &content.BannerForm{
		Message: "全站滿 NT$3,000 免運", Short: "滿 3,000 免運",
		MessageEn: "Free delivery over NT$3,000", ShortEn: "Free over 3,000",
		CTALabel: "看看", CTAHref: "/deals", CTALabelEn: "Shop", Days: 7,
	}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateBanner: %v %v", err, errs)
	}

	banners, err := s.Banners(ctx)
	if err != nil {
		t.Fatalf("Banners: %v", err)
	}
	var made *content.Banner
	for i := range banners.Rows {
		if banners.Rows[i].Message == "全站滿 NT$3,000 免運" {
			made = &banners.Rows[i]
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

func TestAPromotionsButtonMustStayOnThisSite(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := newStore(pool)

	for _, href := range []string{
		"https://evil.example/deals",
		"//evil.example/deals",
		"javascript:alert(1)",
	} {
		errs, err := s.CreateBanner(ctx, &content.BannerForm{
			Message: "測試", CTALabel: "看看", CTAHref: href,
		})
		if err != nil {
			t.Fatalf("CreateBanner(%q): %v", href, err)
		}
		if errs["banner_cta"] == "" {
			t.Errorf("CreateBanner accepted the href %q: %v", href, errs)
		}
	}

	errs, err := s.CreateBanner(ctx, &content.BannerForm{Message: "測試", CTALabel: "看看"})
	if err != nil {
		t.Fatalf("CreateBanner: %v", err)
	}
	if errs["banner_cta"] == "" {
		t.Errorf("a label with no href was accepted: %v", errs)
	}
}

// TestTwoConcurrentHeroSlidesTakeDistinctPositions proves the advisory lock is in
// the statement: without it hero_slides_position_key refuses the loser.
func TestTwoConcurrentHeroSlidesTakeDistinctPositions(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is already cancelled in Cleanup
		_, _ = pool.Exec(context.Background(), `
			DELETE FROM hero_slides WHERE headline IN ('主視覺甲', '主視覺乙')`)
	})
	poolA := admintest.NamedPool(t, pool, "hero-append-a-"+uuid.NewString()[:8])
	poolB := admintest.NamedPool(t, pool, "hero-append-b-"+uuid.NewString()[:8])
	storeA := newStore(poolA)
	storeB := newStore(poolB)
	form := func(headline string) *content.HeroForm {
		return &content.HeroForm{
			Headline: headline, PrimaryLabel: "立即選購", PrimaryHref: "/deals",
		}
	}

	start := make(chan struct{})
	type result struct {
		errs map[string]string
		err  error
	}
	done := make(chan result, 2)
	go func() {
		<-start
		errs, err := storeA.CreateHeroSlide(ctx, form("主視覺甲"))
		done <- result{errs: errs, err: err}
	}()
	go func() {
		<-start
		errs, err := storeB.CreateHeroSlide(ctx, form("主視覺乙"))
		done <- result{errs: errs, err: err}
	}()
	close(start)

	for range 2 {
		got := <-done
		if got.err != nil || len(got.errs) > 0 {
			t.Fatalf("CreateHeroSlide: %v %v", got.err, got.errs)
		}
	}

	var positions []int32
	rows, err := pool.Query(ctx, `
		SELECT position FROM hero_slides
		WHERE headline IN ('主視覺甲', '主視覺乙')
		ORDER BY position`)
	if err != nil {
		t.Fatalf("read positions: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p int32
		if scanErr := rows.Scan(&p); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		positions = append(positions, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate hero positions: %v", err)
	}
	if len(positions) != 2 || positions[0] == positions[1] {
		t.Errorf("positions = %v, want two distinct values — the lock is not in "+
			"the statement, so two concurrent inserts collided on max(position)+1",
			positions)
	}
}

// TestABlankHeroTranslationFallsBackToTheChinese proves the desk and the
// hero_slides *_en_present CHECKs agree: English copy typed as spaces is
// stored as NULL, which localized_name reads as "not translated", rather
// than refused by the database or stored as an empty line.
func TestABlankHeroTranslationFallsBackToTheChinese(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	const headline = "譯文留白的主視覺"
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is already cancelled in Cleanup
		_, _ = pool.Exec(context.Background(), `DELETE FROM hero_slides WHERE headline = $1`, headline)
	})

	errs, err := newStore(pool).CreateHeroSlide(ctx, &content.HeroForm{
		Eyebrow: "夏季", Headline: headline, Body: "內文",
		PrimaryLabel: "立即選購", PrimaryHref: "/deals",
		SecondLabel: "關於", SecondHref: "/about",
		EyebrowEn: " ", HeadlineEn: "\t", BodyEn: "  ",
		PrimaryLabelEn: " ", SecondLabelEn: "　", ImageAltEn: " ",
	})
	if err != nil || len(errs) > 0 {
		t.Fatalf("CreateHeroSlide with blank English copy: %v %v", err, errs)
	}

	var translated int
	if err := pool.QueryRow(ctx, `
		SELECT num_nonnulls(eyebrow_en, headline_en, body_en, primary_cta_label_en,
		                    secondary_cta_label_en, image_alt_en)
		FROM hero_slides WHERE headline = $1`, headline).Scan(&translated); err != nil {
		t.Fatalf("read the slide: %v", err)
	}
	if translated != 0 {
		t.Errorf("%d blank English fields were stored as text, want all 6 NULL so the Chinese shows", translated)
	}
}
