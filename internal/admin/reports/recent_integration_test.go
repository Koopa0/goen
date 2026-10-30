//go:build integration

package reports_test

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/reports"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/sales"
)

// TestStore_RecentDaysAreWhole: Days lays out the last 14 shop days with every
// day whole but today, so an order in the hours between the previous period's
// cut and the first day of the current one is on its day.
//
// Seven shop days up to Wednesday 2019-03-13 10:00 start on 03-07 and the seven
// before them on 02-28; the previous period stops at 03-06 10:00. The order at
// 03-06 23:00 is in neither, and is the day before 03-07 all the same.
func TestStore_RecentDaysAreWhole(t *testing.T) {
	ctx := t.Context()
	s := reports.NewStore(pool, stock.NewStore(pool))
	now := shopMoment(t, "2019-03-13 10:00:00")

	for _, order := range []struct {
		placed string
		cents  int64
	}{
		{"2019-03-13 09:00:00", 1_00},  // today, before now
		{"2019-03-13 11:00:00", 2_00},  // after now
		{"2019-03-06 23:00:00", 4_00},  // between the two periods
		{"2019-03-06 23:59:59", 8_00},  // the same day, its last second
		{"2019-03-07 00:00:00", 16_00}, // the current period's first instant
		{"2019-02-28 00:00:00", 32_00}, // the first row's first instant
		{"2019-02-27 23:59:59", 64_00}, // the shop day before the first row
	} {
		placed := shopMoment(t, order.placed)
		reportOrderAt(t, order.cents, true, &placed)
	}

	recent, err := s.Recent(ctx, 7, now)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if got := len(recent.Days); got != 14 {
		t.Fatalf("%d days, want 14", got)
	}
	want := map[string]struct {
		orders, cents int64
	}{
		"2019-02-28": {1, 32_00},
		"2019-03-06": {2, 12_00},
		"2019-03-07": {1, 16_00},
		"2019-03-13": {1, 1_00},
	}
	var sum int64
	for i, day := range recent.Days {
		key := day.Day.Format(time.DateOnly)
		if wantDay := time.Date(2019, 2, 28+i, 0, 0, 0, 0, time.UTC).Format(time.DateOnly); key != wantDay {
			t.Errorf("row %d is %s, want %s: a row for every shop day, in order", i, key, wantDay)
		}
		w := want[key]
		if day.Orders != w.orders || day.RevenueCents != w.cents {
			t.Errorf("%s: %d orders and %d cents, want %d and %d", key, day.Orders, day.RevenueCents, w.orders, w.cents)
		}
		sum += day.RevenueCents
	}
	if sum != 61_00 {
		t.Errorf("the days add up to %d cents, want 6100: everything from 02-28 up to now", sum)
	}
	if last := recent.Days[13].Day.Format(time.DateOnly); last != "2019-03-13" {
		t.Errorf("the last row is %s, want today", last)
	}
	// The two periods keep their own cut, which is why they cannot make the days.
	if got, want := recent.Previous.RevenueCents, int64(32_00); got != want {
		t.Errorf("the previous period holds %d cents, want %d", got, want)
	}
	if got, want := recent.Current.RevenueCents, int64(16_00+1_00); got != want {
		t.Errorf("the current period holds %d cents, want %d", got, want)
	}
}

// TestStore_RecentBestSellersAreTheCurrentWeek: BestSellers are the current period's
// best sellers, at most sales.RecentBestSellers of them, and a product sold only in
// the period before is not among them.
func TestStore_RecentBestSellersAreTheCurrentWeek(t *testing.T) {
	ctx := t.Context()
	s := reports.NewStore(pool, stock.NewStore(pool))
	now := shopMoment(t, "2018-06-20 15:00:00")
	thisWeek := shopMoment(t, "2018-06-17 10:00:00")
	lastWeek := shopMoment(t, "2018-06-09 10:00:00")

	slugs := make([]string, 0, sales.RecentBestSellers+1)
	for units := 1; units <= sales.RecentBestSellers+1; units++ {
		variantID, sku := newVariant(t, 20, 0)
		orderOnVariant(t, variantID, sku, units, &thisWeek)
		slugs = append(slugs, productSlug(t, sku))
	}
	variantID, sku := newVariant(t, 20, 0)
	orderOnVariant(t, variantID, sku, 9, &lastWeek)
	previous := productSlug(t, sku)

	recent, err := s.Recent(ctx, 7, now)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if got := len(recent.BestSellers); got != sales.RecentBestSellers {
		t.Fatalf("%d sellers, want %d", got, sales.RecentBestSellers)
	}
	for i, seller := range recent.BestSellers {
		if want := slugs[len(slugs)-1-i]; seller.Slug != want {
			t.Errorf("seller %d is %s, want %s: most units first", i, seller.Slug, want)
		}
		if seller.Slug == previous {
			t.Errorf("%s was sold only the week before and is listed", previous)
		}
	}
	if got, want := recent.BestSellers[0].Units, int64(sales.RecentBestSellers+1); got != want {
		t.Errorf("the best seller sold %d units, want %d", got, want)
	}
}

// TestStore_BestSellersTieBreak: products tied on units and revenue come back
// by name, then slug, so the dashboard's five are the first five of the
// report's list and the fifth place does not change between reads.
//
// Seven products sell the same units at the same price. They are made, and
// their slugs run, against the order they must come in, so neither the slug nor
// the age of a row can stand in for the name. Two share a name, so the slug
// decides who is fifth.
func TestStore_BestSellersTieBreak(t *testing.T) {
	ctx := t.Context()
	s := reports.NewStore(pool, stock.NewStore(pool))
	// A week of its own, so no other test's sale joins the tie.
	now := shopMoment(t, "2022-03-16 15:00:00")
	placed := shopMoment(t, "2022-03-14 10:00:00")

	unique := "tie-" + uuid.NewString()[:8] + "-"
	order := []struct{ name, slug string }{
		{"TieA", unique + "g"},
		{"TieB", unique + "f"},
		{"TieC", unique + "e"},
		{"TieD", unique + "d"},
		{"TieE", unique + "b"},
		{"TieE", unique + "c"},
		{"TieF", unique + "a"},
	}
	for _, seller := range slices.Backward(order) {
		variantID, sku := newVariant(t, 20, 0)
		orderOnVariant(t, variantID, sku, 2, &placed)
		// After the order: a line's name must be the name its product had.
		if _, err := pool.Exec(ctx, `
			UPDATE products SET name = $2, slug = $3
			WHERE id = (SELECT product_id FROM product_variants WHERE sku = $1)`,
			sku, seller.name, seller.slug); err != nil {
			t.Fatalf("name %s: %v", sku, err)
		}
	}
	wantSlugs := make([]string, 0, len(order))
	for _, seller := range order {
		wantSlugs = append(wantSlugs, seller.slug)
	}
	slugsOf := func(sellers []sales.BestSeller) []string {
		slugs := make([]string, 0, len(sellers))
		for _, seller := range sellers {
			slugs = append(slugs, seller.Slug)
		}
		return slugs
	}

	recent, err := s.Recent(ctx, 7, now)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if got, want := slugsOf(recent.BestSellers), wantSlugs[:sales.RecentBestSellers]; !slices.Equal(got, want) {
		t.Errorf("Recent best sellers = %v, want %v", got, want)
	}
	report, err := s.ReportAt(ctx, 7, now)
	if err != nil {
		t.Fatalf("ReportAt: %v", err)
	}
	if got := slugsOf(report.BestSellers); !slices.Equal(got, wantSlugs) {
		t.Errorf("ReportAt best sellers = %v, want %v", got, wantSlugs)
	}
}
