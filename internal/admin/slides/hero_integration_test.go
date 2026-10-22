//go:build integration

package slides_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/slides"
)

// TestStore_CreateHeroSlideConcurrent: two concurrent hero slides take distinct positions.
//
// It proves the advisory lock is in
// the statement: without it hero_slides_position_key refuses the loser.
func TestStore_CreateHeroSlideConcurrent(t *testing.T) {
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
	form := func(headline string) *slides.HeroForm {
		return &slides.HeroForm{
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

// TestStore_CreateHeroSlideBlankEnglish: a blank hero translation falls back to the Chinese.
//
// It proves the desk and the
// hero_slides *_en_present CHECKs agree: English copy typed as spaces is
// stored as NULL, which localized_name reads as "not translated", rather
// than refused by the database or stored as an empty line.
func TestStore_CreateHeroSlideBlankEnglish(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	const headline = "譯文留白的主視覺"
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is already cancelled in Cleanup
		_, _ = pool.Exec(context.Background(), `DELETE FROM hero_slides WHERE headline = $1`, headline)
	})

	errs, err := newStore(pool).CreateHeroSlide(ctx, &slides.HeroForm{
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
