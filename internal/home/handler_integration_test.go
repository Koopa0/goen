//go:build integration

package home_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"errors"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/home"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/hero"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p

	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		slog.Error("read seed", "error", err)
		os.Exit(1)
	}
	if _, err := pool.Exec(context.Background(), string(seed)); err != nil {
		slog.Error("load seed", "error", err)
		os.Exit(1)
	}

	code := m.Run()
	stop()
	os.Exit(code)
}

func load(t *testing.T, ctx context.Context, store *home.Store, choice home.Choice) home.View {
	t.Helper()
	view, err := store.Load(ctx, choice)
	if err != nil {
		t.Fatalf("Load(%+v): %v", choice, err)
	}
	return view
}

// TestHandler_IndexSections holds that Index draws the page's five rhythms
// from the seeded shop: the hero under its heading, the largest department's
// shelf with its sub-categories, its stage, the rail and the other
// departments, then the rules.
func TestHandler_IndexSections(t *testing.T) {
	h := home.NewHandler(home.NewStore(pool), slog.New(slog.DiscardHandler), catalog.CompareToggle)
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	res := httptest.NewRecorder()
	h.Index(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	body := res.Body.String()
	at := -1
	for _, want := range []string{
		`<h1 id="hero-heading" class="goen-bento__headline">` + i18n.T(ctx, i18n.KeyHeroHeadline) + `</h1>`,
		`id="shelf"`, `class="goen-stage"`, `id="under"`, `id="more"`, `class="goen-home__band goen-home__rules"`,
	} {
		i := strings.Index(body, want)
		if i < 0 {
			t.Errorf("the page lacks %s", want)
			continue
		}
		if i < at {
			t.Errorf("%s is drawn out of order", want)
		}
		at = i
	}
	if !strings.Contains(body, `href="/?shelf=phones" hx-get="/?shelf=phones"`) {
		t.Error("a sub-category pill is not a link htmx fetches from its own address")
	}
	if !strings.Contains(body, `src="/static/media/products/`) || !strings.Contains(body, ` 1600w"`) {
		t.Error("a product photograph is not mapped to its embedded media with its renditions")
	}
	if !strings.Contains(body, `src="/static/media/hero/hero-tech-flagship.webp?v=`) {
		t.Error("the dark tile does not stand on the bundled photograph")
	}
	// The featured shelf is technology's, which compares: each of its eight
	// cards carries the catalogue's toggle, and no other card on the page does.
	shelf, stage := strings.Index(body, `id="shelf"`), strings.Index(body, `class="goen-stage"`)
	if shelf >= 0 && stage > shelf {
		const toggle = `<form class="goen-comparetoggle" method="post" action="/compare/items"`
		if got := strings.Count(body[shelf:stage], toggle); got != 8 {
			t.Errorf("the featured shelf carries %d compare toggles, want one on each of its 8 cards", got)
		}
		if got := strings.Count(body, toggle); got != 8 {
			t.Errorf("the page carries %d compare toggles, want the featured shelf's 8 alone", got)
		}
	}
}

// TestStore_LoadShelfCompares holds that the featured shelf compares where its
// department does: a sub-category chosen takes the department's answer, and a
// department that stops comparing takes the toggle off its shelf.
func TestStore_LoadShelfCompares(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, choice := range []home.Choice{{}, {Shelf: "phones"}} {
		if view := load(t, ctx, home.NewStore(pool), choice); view.Shelf == nil || !view.Shelf.Compares {
			t.Errorf("shelf %q of technology does not compare", choice.Shelf)
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `UPDATE categories SET comparable = false WHERE slug = 'tech'`); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if view := load(t, ctx, home.NewStore(tx), home.Choice{Shelf: "phones"}); view.Shelf == nil || view.Shelf.Compares {
		t.Error("the phones shelf still compares once technology does not")
	}
}

// TestStore_LoadFeaturedShelf holds that the shelf is the department holding
// the most products, each sub-category counted across its own subtree, and
// that a chosen sub-category narrows it while a name it does not hold is the
// whole department.
func TestStore_LoadFeaturedShelf(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	store := home.NewStore(pool)

	view := load(t, ctx, store, home.Choice{})
	if view.Featured == nil || view.Featured.Slug != "tech" || view.Featured.Products != 20 {
		t.Fatalf("Featured = %+v, want tech with its 20 products", view.Featured)
	}
	if view.Products != 40 {
		t.Errorf("Products = %d, want the seed's 40", view.Products)
	}
	counts := map[string]int64{}
	for _, c := range view.Shelf.Filters {
		counts[c.Slug] = c.Products
	}
	want := map[string]int64{"phones": 3, "laptops": 3, "tablets": 2, "audio": 3, "wearables": 3, "accessories": 6}
	for slug, n := range want {
		if counts[slug] != n {
			t.Errorf("the %s pill counts %d, want %d", slug, counts[slug], n)
		}
	}
	if len(view.Shelf.Tiles) != 8 || view.Shelf.Chosen != "" {
		t.Errorf("the whole shelf holds %d tiles chosen %q, want 8 of all of it", len(view.Shelf.Tiles), view.Shelf.Chosen)
	}

	phones := load(t, ctx, store, home.Choice{Shelf: "phones"})
	if phones.Shelf.Chosen != "phones" || phones.Choice.Shelf != "phones" || len(phones.Shelf.Tiles) != 3 {
		t.Errorf("the phones shelf is %q with %d tiles, want phones with 3", phones.Shelf.Chosen, len(phones.Shelf.Tiles))
	}
	for _, unknown := range []string{"kitchen", "nothing-here"} {
		other := load(t, ctx, store, home.Choice{Shelf: unknown})
		if other.Shelf.Chosen != "" || other.Choice.Shelf != "" || len(other.Shelf.Tiles) != 8 {
			t.Errorf("Choice{Shelf: %q} drew %q with %d tiles, want the whole department", unknown, other.Shelf.Chosen, len(other.Shelf.Tiles))
		}
	}
}

// TestStore_LoadStage holds that the stage is the featured department's two
// highest-priced products that can be bought, dearest first.
func TestStore_LoadStage(t *testing.T) {
	view := load(t, t.Context(), home.NewStore(pool), home.Choice{})
	if len(view.Stage) != 2 {
		t.Fatalf("the stage holds %d products, want 2", len(view.Stage))
	}
	var dearest int64
	if err := pool.QueryRow(t.Context(), `
		WITH RECURSIVE d AS (
		    SELECT id FROM categories WHERE slug = 'tech'
		    UNION ALL SELECT c.id FROM categories c JOIN d ON c.parent_id = d.id
		)
		SELECT max(mv.price) FROM products p
		JOIN LATERAL (
		    SELECT price_cents AS price FROM product_variants
		    WHERE product_id = p.id AND is_active AND stock_quantity > safety_stock
		    ORDER BY price_cents LIMIT 1
		) mv ON true
		WHERE p.status = 'active' AND p.category_id IN (SELECT id FROM d)`).Scan(&dearest); err != nil {
		t.Fatalf("read the dearest product: %v", err)
	}
	if view.Stage[0].PriceCents != dearest || view.Stage[1].PriceCents > view.Stage[0].PriceCents {
		t.Errorf("the stage is %d then %d, want the dearest %d first", view.Stage[0].PriceCents, view.Stage[1].PriceCents, dearest)
	}
	for _, s := range view.Stage {
		if !s.InStock {
			t.Errorf("%s is on the stage and cannot be bought", s.Slug)
		}
	}
}

// TestStore_LoadRail holds that the rail is what costs less than its ceiling
// and can be bought, cheapest first, across every department.
func TestStore_LoadRail(t *testing.T) {
	view := load(t, t.Context(), home.NewStore(pool), home.Choice{})
	if len(view.Rail.Tiles) == 0 {
		t.Fatal("the seed sells products under NT$1,000 and the rail is empty")
	}
	if view.Rail.BelowCents != 100000 {
		t.Errorf("the rail's ceiling is %d cents, want 100000", view.Rail.BelowCents)
	}
	for i, r := range view.Rail.Tiles {
		if r.PriceCents >= view.Rail.BelowCents || !r.InStock {
			t.Errorf("%s at %d cents, in stock %t, is on the rail", r.Slug, r.PriceCents, r.InStock)
		}
		if i > 0 && view.Rail.Tiles[i-1].PriceCents > r.PriceCents {
			t.Errorf("%s at %d comes after a dearer product", r.Slug, r.PriceCents)
		}
	}
}

// TestStore_LoadMore holds that the showcase offers every other department
// that holds products, the first unless the visitor chose another.
func TestStore_LoadMore(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	store := home.NewStore(pool)
	view := load(t, ctx, store, home.Choice{})
	tabs := make([]string, 0, len(view.More.Tabs))
	for _, c := range view.More.Tabs {
		tabs = append(tabs, c.Slug)
	}
	if want := []string{"books-stationery", "home-living", "beauty", "fashion", "food-drink"}; !slices.Equal(tabs, want) {
		t.Errorf("the tabs are %v, want %v", tabs, want)
	}
	if view.More.Chosen.Slug != "books-stationery" || len(view.More.Tiles) != 4 {
		t.Errorf("the showcase opens on %s with %d tiles, want books-stationery with 4", view.More.Chosen.Slug, len(view.More.Tiles))
	}
	fashion := load(t, ctx, store, home.Choice{More: "fashion"})
	if fashion.More.Chosen.Slug != "fashion" || fashion.Choice.More != "fashion" {
		t.Errorf("Choice{More: fashion} opens on %s", fashion.More.Chosen.Slug)
	}
	if tech := load(t, ctx, store, home.Choice{More: "tech"}); tech.More.Chosen.Slug != "books-stationery" {
		t.Errorf("the featured department opened in the showcase: %s", tech.More.Chosen.Slug)
	}
}

// TestStore_LoadTileFacts holds that a card carries what its tile says beyond
// the row: the quick add only for a product's one variant that can be bought,
// the first three specification values, the stated warranty, and each
// colour's photograph beside its name.
func TestStore_LoadTileFacts(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer db.Rollback(ctx, tx)
	store := home.NewStore(tx)
	all := load(t, ctx, store, home.Choice{Shelf: "audio"})
	byslug := map[string]int{}
	for i, s := range all.Shelf.Tiles {
		byslug[s.Slug] = i
	}
	i, ok := byslug["nimbus-buds-pro"]
	if !ok {
		t.Fatal("the audio shelf lacks nimbus-buds-pro")
	}
	buds := all.Shelf.Tiles[i]
	if buds.QuickAdd != "" {
		t.Errorf("nimbus-buds-pro has three variants and offers a quick add of %s", buds.QuickAdd)
	}
	if buds.WarrantyMonths != 12 || len(buds.Highlights) != 3 {
		t.Errorf("nimbus-buds-pro states %d months and %d specifications, want 12 and 3", buds.WarrantyMonths, len(buds.Highlights))
	}
	if len(buds.ColourNames) != len(buds.Colours) || len(buds.ColourPhotos) != len(buds.Colours) {
		t.Fatalf("nimbus-buds-pro has %d colours, %d names and %d photographs", len(buds.Colours), len(buds.ColourNames), len(buds.ColourPhotos))
	}
	// Each colour beside its own photograph, as the seed tags them.
	own := map[string]string{"雲白": "nimbus-buds-pro-01.webp", "曜石黑": "nimbus-buds-pro-02.webp", "薄荷綠": "nimbus-buds-pro-03.webp"}
	for j, p := range buds.ColourPhotos {
		if want := "/static/media/products/" + own[buds.ColourNames[j]]; !strings.HasPrefix(p.URL, want+"?") {
			t.Errorf("colour %s (%s) shows %q, want its own photograph %s", buds.ColourNames[j], buds.Colours[j], p.URL, want)
		}
	}

	rail := load(t, ctx, store, home.Choice{})
	var pens bool
	for _, r := range rail.Rail.Tiles {
		switch r.Slug {
		case "paperwick-gel-pen-set":
			pens = true
			var want string
			if err = tx.QueryRow(ctx, `
				SELECT v.id::text FROM product_variants v JOIN products p ON p.id = v.product_id
				WHERE p.slug = 'paperwick-gel-pen-set' AND v.is_active`).Scan(&want); err != nil {
				t.Fatalf("read the pen set's variant: %v", err)
			}
			if r.QuickAdd != want {
				t.Errorf("the pen set's quick add is %q, want its one variant %s", r.QuickAdd, want)
			}
		case "paperwick-dot-notebook-a5":
			if r.QuickAdd != "" {
				t.Errorf("the notebook has three variants and offers a quick add of %s", r.QuickAdd)
			}
		}
	}
	if !pens {
		t.Error("the rail lacks the pen set, the cheapest product the seed sells")
	}

	// Everything but the pen set sold out: the rail, which offers what can be
	// bought, holds the pen set alone, however few cards it then fills.
	if _, err = tx.Exec(ctx, `
		UPDATE product_variants SET stock_quantity = safety_stock
		WHERE product_id IN (SELECT id FROM products WHERE slug <> 'paperwick-gel-pen-set')`); err != nil {
		t.Fatalf("sell out the rest: %v", err)
	}
	alone := load(t, ctx, store, home.Choice{})
	slugs := make([]string, 0, len(alone.Rail.Tiles))
	for _, r := range alone.Rail.Tiles {
		slugs = append(slugs, r.Slug)
	}
	if want := []string{"paperwick-gel-pen-set"}; !slices.Equal(slugs, want) {
		t.Errorf("with one product in stock the rail is %v, want %v", slugs, want)
	}
}

// TestStore_LoadEmptyHeroTable holds that with no slide scheduled and no
// campaign running, the lifestyle tile leads to the department its bundled
// photograph shows.
func TestStore_LoadEmptyHeroTable(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	emptyHeroSlides(t)
	stopCampaigns(t)
	slide := firstSlide(t, ctx)
	if slide.Source != hero.SourceDepartment || slide.CTA.Href != "/c/home-living" || slide.Title != "居家生活" {
		t.Errorf("the lifestyle tile is %+v, want the home-living department", slide)
	}
	if slide.Photo.Shown() {
		t.Error("a department's tile brought a photograph of its own; it stands on the bundled one")
	}
	carousel, err := home.NewStore(pool).Carousel(ctx)
	if err != nil {
		t.Fatalf("Carousel(): %v", err)
	}
	if len(carousel) != 1 || carousel[0].CTA.Href != slide.CTA.Href {
		t.Errorf("Carousel() = %+v, want the one tile the page shows", carousel)
	}
}

func slidesOf(t *testing.T, ctx context.Context) []hero.Slide {
	t.Helper()
	view := load(t, ctx, home.NewStore(pool), home.Choice{})
	if view.Lifestyle == nil {
		return nil
	}
	return []hero.Slide{*view.Lifestyle}
}

func firstSlide(t *testing.T, ctx context.Context) hero.Slide {
	t.Helper()
	slides := slidesOf(t, ctx)
	if len(slides) == 0 {
		t.Fatal("the lifestyle tile is empty")
	}
	return slides[0]
}

// TestStore_LoadScheduledSlide holds that Load shows only the scheduled slide
// whose window is open. The window is judged by the database's clock, which
// wrote starts_at.
func TestStore_LoadScheduledSlide(t *testing.T) {
	ctx := t.Context()
	stopCampaigns(t)

	tests := []struct {
		name  string
		setup string
		want  string
	}{
		{"active and unbounded", `INSERT INTO hero_slides
			(headline, primary_cta_label, primary_cta_href, position)
			VALUES ('現在', '看', '/deals', 0)`, "現在"},
		{"ended", `INSERT INTO hero_slides
			(headline, primary_cta_label, primary_cta_href, position, starts_at, ends_at)
			VALUES ('過期', '看', '/deals', 0, now() - interval '30 days', now() - interval '1 day')`, ""},
		{"not started", `INSERT INTO hero_slides
			(headline, primary_cta_label, primary_cta_href, position, starts_at)
			VALUES ('未來', '看', '/deals', 0, now() + interval '1 day')`, ""},
		{"switched off", `INSERT INTO hero_slides
			(headline, primary_cta_label, primary_cta_href, position, is_active)
			VALUES ('停用', '看', '/deals', 0, false)`, ""},
		{"starts exactly now", `INSERT INTO hero_slides
			(headline, primary_cta_label, primary_cta_href, position, starts_at)
			VALUES ('剛好', '看', '/deals', 0, now())`, "剛好"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `DELETE FROM hero_slides`); err != nil {
				t.Fatalf("clear: %v", err)
			}
			if _, err := pool.Exec(ctx, tt.setup); err != nil {
				t.Fatalf("setup: %v", err)
			}
			slide := firstSlide(t, ctx)
			if tt.want == "" {
				if slide.Source != hero.SourceDepartment {
					t.Errorf("the tile is %q from %s, want a department", slide.Title, slide.Source)
				}
				return
			}
			if slide.Title != tt.want || slide.Source != hero.SourceScheduled {
				t.Errorf("the tile is %q from %s, want the scheduled %q", slide.Title, slide.Source, tt.want)
			}
		})
	}
}

// TestStore_LoadSlideQueueOrder holds that the tile shows the first scheduled
// slide in queue order and leaves an inactive one out.
func TestStore_LoadSlideQueueOrder(t *testing.T) {
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `DELETE FROM hero_slides`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	for _, row := range []struct {
		headline string
		position int
		active   bool
	}{
		{"第三", 2, true},
		{"第一但停用", 0, false},
		{"第二", 1, true},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO hero_slides
			(headline, primary_cta_label, primary_cta_href, position, is_active)
			VALUES ($1, '看', '/deals', $2, $3)`,
			row.headline, row.position, row.active); err != nil {
			t.Fatalf("insert %s: %v", row.headline, err)
		}
	}
	if slide := firstSlide(t, ctx); slide.Title != "第二" {
		t.Errorf("the tile shows %q, want 第二: a disabled slide is skipped and the rest keep the order the editor set", slide.Title)
	}
}

// TestStore_LoadHeroImageWidth holds that Load gives the slide's photograph
// the real width of its uploaded media, which its srcset states.
func TestStore_LoadHeroImageWidth(t *testing.T) {
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `DELETE FROM hero_slides`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	const digest = "1111111111111111111111111111111111111111111111111111111111111111"
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_objects (digest, content_type, bytes, width, height, byte_size)
		VALUES ($1, 'image/png', '\x89504e47'::bytea, 1440, 900, 4)
		ON CONFLICT DO NOTHING`, digest); err != nil {
		t.Fatalf("seed media: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO hero_slides
		(headline, primary_cta_label, primary_cta_href, position, media_digest, image_alt)
		VALUES ('有圖', '看', '/deals', 0, $1, '主視覺')`, digest); err != nil {
		t.Fatalf("insert: %v", err)
	}

	slide := firstSlide(t, ctx)
	if !slide.Photo.Shown() {
		t.Fatal("a slide with an image drew no photograph")
	}
	if !strings.HasSuffix(slide.Photo.Srcset, " 1440w") {
		t.Errorf("the srcset is %q, want it to end at the media's own 1440w", slide.Photo.Srcset)
	}
}

// TestStore_LoadOffSiteHeroCTA holds that Load drops an off-site link when the
// slide is read, and keeps the slide. The admin form validates these paths
// too, but direct SQL and old rows still reach the storefront reader.
func TestStore_LoadOffSiteHeroCTA(t *testing.T) {
	ctx := t.Context()
	emptyHeroSlides(t)
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO hero_slides
		    (headline, primary_cta_label, primary_cta_href, position)
		VALUES ('仍是這張投影片', '假的按鈕', '///evil.example/x', -100)
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("insert poisoned primary CTA: %v", err)
	}
	cleanupHeroSlide(t, ctx, id)

	slide := firstSlide(t, ctx)
	if slide.Title != "仍是這張投影片" {
		t.Errorf("a bad link discarded the slide copy: %q", slide.Title)
	}
	if slide.CTA.Shown() {
		t.Errorf("a poisoned primary CTA survived as %+v", slide.CTA)
	}
}

// stopCampaigns switches every campaign off for the test: a running one takes
// the lifestyle tile, and the development seed runs two.
func stopCampaigns(t *testing.T) {
	t.Helper()
	rows, err := pool.Query(t.Context(), `UPDATE sale_campaigns SET is_active = false WHERE is_active RETURNING id`)
	if err != nil {
		t.Fatalf("stop campaigns: %v", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatalf("stop campaigns: %v", err)
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(clean, `UPDATE sale_campaigns SET is_active = true WHERE id = ANY($1)`, ids); err != nil {
			t.Errorf("restart campaigns: %v", err)
		}
	})
}

// TestStore_LoadCampaignSlide holds that with no slide scheduled the tile is
// the running campaign that ends first, in the visitor's language.
func TestStore_LoadCampaignSlide(t *testing.T) {
	ctx := t.Context()
	emptyHeroSlides(t)
	stopCampaigns(t)

	for _, c := range []struct{ slug, title, titleEn, ends string }{
		{"hero-later", "較晚結束", "Ends later", "2 hours"},
		{"hero-sooner", "較早結束", "Ends sooner", "1 hour"},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO sale_campaigns (slug, title, title_en, ends_at)
			VALUES ($1, $2, $3, now() + $4::interval)`, c.slug, c.title, c.titleEn, c.ends); err != nil {
			t.Fatalf("insert campaign %s: %v", c.slug, err)
		}
	}
	// A campaign is listed only while it features a published product in stock.
	for _, slug := range []string{"hero-sooner", "hero-later"} {
		featureProduct(t, slug, 5)
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(clean, `DELETE FROM sale_campaigns WHERE slug LIKE 'hero-%'`); err != nil {
			t.Errorf("clean up campaigns: %v", err)
		}
	})

	for locale, want := range map[i18n.Locale]string{i18n.ZhHant: "較早結束", i18n.En: "Ends sooner"} {
		slide := firstSlide(t, i18n.WithLocale(ctx, locale))
		if slide.Title != want || slide.Source != hero.SourceCampaign || slide.CTA.Href != "/s/hero-sooner" {
			t.Errorf("%s tile = %q from %s to %s, want %q to /s/hero-sooner", locale, slide.Title, slide.Source, slide.CTA.Href, want)
		}
		if slide.Ends == nil {
			t.Errorf("%s campaign tile does not say when it ends", locale)
		}
	}
}

// TestStore_LoadCampaignNothingToBuy: A campaign whose featured product is sold
// out does not take the tile; a sellable product brings it back.
func TestStore_LoadCampaignNothingToBuy(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	emptyHeroSlides(t)
	stopCampaigns(t)
	if _, err := pool.Exec(ctx, `INSERT INTO sale_campaigns (slug, title, ends_at) VALUES ('hero-empty', '空活動', now() + interval '1 hour')`); err != nil {
		t.Fatalf("insert campaign: %v", err)
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(clean, `DELETE FROM sale_campaigns WHERE slug = 'hero-empty'`); err != nil {
			t.Errorf("clean up campaign: %v", err)
		}
	})

	featureProduct(t, "hero-empty", 0)
	if slide := firstSlide(t, ctx); slide.CTA.Href == "/s/hero-empty" {
		t.Fatal("a campaign with only a sold-out product took the lifestyle tile")
	}
	featureProduct(t, "hero-empty", 5)
	if slide := firstSlide(t, ctx); slide.CTA.Href != "/s/hero-empty" {
		t.Fatalf("a campaign with a sellable product is not the lifestyle tile: %+v", slide)
	}
}

func featureProduct(t *testing.T, campaignSlug string, stock int) {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(), `
		WITH p AS (
		    INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		    SELECT (SELECT id FROM brands LIMIT 1),
		           (SELECT id FROM categories WHERE parent_id IS NULL LIMIT 1),
		           'hero-' || gen_random_uuid(), '活動商品', 'active', now()
		    RETURNING id, slug
		), v AS (
		    INSERT INTO product_variants
		        (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, position)
		    SELECT p.id, 'HERO-' || upper(replace(gen_random_uuid()::text, '-', '')), 1000, 2000, $1, 0, 0 FROM p
		)
		SELECT slug FROM p`, stock).Scan(&slug); err != nil {
		t.Fatalf("create product: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO sale_campaign_products (campaign_id, product_id, position)
		SELECT c.id, p.id,
		       (SELECT coalesce(max(position) + 1, 0) FROM sale_campaign_products WHERE campaign_id = c.id)
		FROM sale_campaigns c, products p
		WHERE c.slug = $1 AND p.slug = $2`, campaignSlug, slug); err != nil {
		t.Fatalf("feature product on %s: %v", campaignSlug, err)
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		for _, stmt := range []string{
			`DELETE FROM sale_campaign_products WHERE product_id = (SELECT id FROM products WHERE slug = $1)`,
			`UPDATE products SET status = 'draft' WHERE slug = $1`,
			`DELETE FROM product_variants WHERE product_id = (SELECT id FROM products WHERE slug = $1)`,
			`DELETE FROM products WHERE slug = $1`,
		} {
			if _, err := pool.Exec(clean, stmt, slug); err != nil {
				t.Errorf("remove product %s: %v", slug, err)
				return
			}
		}
	})
}

func emptyHeroSlides(t *testing.T) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `DELETE FROM hero_slides`); err != nil {
		t.Fatalf("clear hero slides: %v", err)
	}
}

func cleanupHeroSlide(t *testing.T, ctx context.Context, id uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(clean, `DELETE FROM hero_slides WHERE id = $1`, id); err != nil {
			t.Errorf("clean up the slide: %v", err)
		}
	})
}

// TestStore_LoadHeroLocale holds that Load reads the slide in the visitor's
// language.
func TestStore_LoadHeroLocale(t *testing.T) {
	ctx := t.Context()

	// Its own slide, at the front of the queue: another test's slide would
	// otherwise decide what this one reads.
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO hero_slides (eyebrow, headline, body, primary_cta_label,
		                         primary_cta_href, eyebrow_en, headline_en, body_en,
		                         primary_cta_label_en, position)
		VALUES ('小標', '挑一台好的', '說明文字', '看商品', '/c/phones',
		        'Eyebrow', 'Choose one good thing', 'Body copy', 'Shop', -1)
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("queue a slide: %v", err)
	}
	cleanupHeroSlide(t, ctx, id)

	for _, tt := range []struct {
		name     string
		locale   i18n.Locale
		eyebrow  string
		headline string
		body     string
		cta      string
	}{
		{name: "Chinese", locale: i18n.ZhHant, eyebrow: "小標", headline: "挑一台好的", body: "說明文字", cta: "看商品"},
		{name: "English", locale: i18n.En, eyebrow: "Eyebrow", headline: "Choose one good thing", body: "Body copy", cta: "Shop"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			slide := firstSlide(t, i18n.WithLocale(ctx, tt.locale))
			got := [4]string{slide.Eyebrow, slide.Title, slide.Fact, slide.CTA.Label}
			if want := [4]string{tt.eyebrow, tt.headline, tt.body, tt.cta}; got != want {
				t.Errorf("the tile reads %q, want %q", got, want)
			}
			if slide.CTA.Href != "/c/phones" {
				t.Errorf("the tile points at %q, want /c/phones", slide.CTA.Href)
			}
		})
	}
}

// TestStore_LoadSlideNoEyebrow holds that Load reads a slide that has no
// eyebrow. localized_name(NULL, NULL, ...) is NULL while sqlc types the result
// as non-null, so an uncoalesced wrap fails to scan a slide with no eyebrow.
func TestStore_LoadSlideNoEyebrow(t *testing.T) {
	ctx := t.Context()

	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO hero_slides (headline, primary_cta_label, primary_cta_href, position)
		VALUES ('只有標題', '看看', '/deals', -2)
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("queue a bare slide: %v", err)
	}
	cleanupHeroSlide(t, ctx, id)

	slide := firstSlide(t, i18n.WithLocale(ctx, i18n.En))
	if slide.Title != "只有標題" {
		t.Errorf("the headline reads %q", slide.Title)
	}
	if slide.Fact != "" || slide.Eyebrow != "" {
		t.Errorf("absent copy came back as %q and %q", slide.Eyebrow, slide.Fact)
	}
}

// TestStore_LoadFreeDeliveryStrip holds that Load makes the free-delivery strip
// state what the till charges. free_over_cents lives in
// shipping_method_versions, which a shop edits at /admin/shipping, so a page
// stating the figure can drift from the till.
func TestStore_LoadFreeDeliveryStrip(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	// A new version, because shipping_method_versions is append-only.
	if _, err := pool.Exec(ctx, `
		INSERT INTO shipping_method_versions
		    (method_id, name, carrier, fee_cents, free_over_cents, effective_at)
		SELECT v.method_id, v.name, v.carrier, v.fee_cents, 555500, now()
		FROM shipping_method_versions v
		JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.is_active
		ORDER BY v.effective_at DESC LIMIT 1`); err != nil {
		t.Fatalf("publish a new threshold: %v", err)
	}

	view := load(t, ctx, home.NewStore(pool), home.Choice{})
	// The other active method still carries the seeded 300000; only the higher
	// threshold is free for both.
	if got := view.Rules.FreeDeliveryCents; got != 555500 {
		t.Errorf("the shop rules state a threshold of %d cents, want 555500 — the highest threshold, "+
			"the one every active method honours", got)
	}
	if got, want := view.IndexHead(ctx), "快速索引 · 滿 NT$5,555 免運"; got != want {
		t.Errorf("IndexHead() = %q, want %q", got, want)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO shipping_method_versions
		    (method_id, name, carrier, fee_cents, free_over_cents, effective_at)
		SELECT DISTINCT ON (v.method_id) v.method_id, v.name, v.carrier, v.fee_cents, NULL, now()
		FROM shipping_method_versions v
		JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.is_active
		ORDER BY v.method_id, v.effective_at DESC`); err != nil {
		t.Fatalf("withdraw free delivery: %v", err)
	}
	bare := load(t, ctx, home.NewStore(pool), home.Choice{})
	if got := bare.Rules.FreeDeliveryCents; got != 0 {
		t.Errorf("a shop that charges for every parcel advertises free delivery over %d cents", got)
	}
	if got, want := bare.IndexHead(ctx), "快速索引"; got != want {
		t.Errorf("IndexHead() = %q with no threshold, want %q", got, want)
	}
}

// TestHandler_IndexLowestFeeNote holds that Index makes the free-delivery note
// state the lowest current fee. fee_cents lives in shipping_method_versions,
// which a shop edits at /admin/shipping, so a page stating the figure can drift
// from the till.
func TestHandler_IndexLowestFeeNote(t *testing.T) {
	ctx := t.Context()

	// A new version, because shipping_method_versions is append-only. Every
	// active method is raised so the lowest honest figure is the fixture, not
	// a leftover seed 6000. free_over_cents stays positive so the card renders.
	if _, err := pool.Exec(ctx, `
		INSERT INTO shipping_method_versions
		    (method_id, name, carrier, fee_cents, free_over_cents, effective_at)
		SELECT DISTINCT ON (v.method_id) v.method_id, v.name, v.carrier, 8000,
		       coalesce(v.free_over_cents, 300000), now()
		FROM shipping_method_versions v
		JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.is_active
		ORDER BY v.method_id, v.effective_at DESC`); err != nil {
		t.Fatalf("publish a new fee: %v", err)
	}

	h := home.NewHandler(home.NewStore(pool), slog.New(slog.DiscardHandler), catalog.CompareToggle)
	req := httptest.NewRequestWithContext(i18n.WithLocale(ctx, i18n.ZhHant), http.MethodGet, "/", http.NoBody)
	res := httptest.NewRecorder()
	h.Index(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	body := res.Body.String()

	if !strings.Contains(body, "未達門檻運費 NT$80 起") {
		t.Error("the free-delivery note does not state the current lowest fee")
	}
	if !strings.Contains(body, "運費 NT$80 起") {
		t.Error("the quick index does not state the current lowest fee")
	}
	if strings.Contains(body, "NT$60 起") {
		t.Error("the page still states the old NT$60 floor")
	}
}

// TestStore_LoadHeaderTilesOrder: The header and the home page's showcase read
// one list, so their order is the roots' positions, and two roots sharing a
// position is refused at the schema: categories_position_key is NULLS NOT
// DISTINCT precisely because the ROOT categories are the header, and a plain
// unique index would treat their NULL parents as distinct and allow the pair.
func TestStore_LoadHeaderTilesOrder(t *testing.T) {
	ctx := t.Context()

	// Two roots at one position is what CreateCategory's max(position) + 1 can
	// hand two staff members at once.
	_, err := pool.Exec(ctx, `
		UPDATE categories SET position = (
		    SELECT min(position) FROM categories WHERE parent_id IS NULL
		) WHERE slug = (
		    SELECT slug FROM categories WHERE parent_id IS NULL
		    ORDER BY position DESC LIMIT 1
		)`)
	if err == nil {
		t.Fatal("two root categories were allowed to share a position; " +
			"the header's order is then whatever the planner returned")
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		t.Fatalf("refused by something other than a constraint: %v", err)
	}
	if pgErr.ConstraintName != "categories_position_key" {
		t.Errorf("refused by %q, want categories_position_key — bound to the "+
			"constraint, because a statement meant to prove one rule often trips "+
			"another first", pgErr.ConstraintName)
	}
}

// TestHandler_IndexNoteWithoutPickup holds that Index makes the free-delivery
// note describe only the methods checkout offers. Checkout drops store pickup
// where the store map is not configured, so the note's floor and wording must
// describe the home delivery it still offers.
func TestHandler_IndexNoteWithoutPickup(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	// New versions, because shipping_method_versions is append-only and a
	// sibling test leaves every method at one fee. The fixture names its own.
	if _, err := pool.Exec(ctx, `
		INSERT INTO shipping_method_versions
		    (method_id, name, carrier, fee_cents, free_over_cents, effective_at)
		SELECT DISTINCT ON (v.method_id) v.method_id, v.name, v.carrier,
		       CASE sm.destination_kind WHEN 'pickup_point' THEN 6000 ELSE 8000 END,
		       300000, now()
		FROM shipping_method_versions v
		JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.is_active
		ORDER BY v.method_id, v.effective_at DESC`); err != nil {
		t.Fatalf("publish the fees: %v", err)
	}
	var pickups int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM shipping_methods
		WHERE is_active AND destination_kind = 'pickup_point'`).Scan(&pickups); err != nil || pickups == 0 {
		t.Fatalf("fixture: no active pickup method (%d): %v", pickups, err)
	}

	render := func(s *home.Store) string {
		h := home.NewHandler(s, slog.New(slog.DiscardHandler), catalog.CompareToggle)
		res := httptest.NewRecorder()
		h.Index(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody))
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200", res.Code)
		}
		return res.Body.String()
	}

	with := render(home.NewStore(pool))
	if !strings.Contains(with, "未達門檻運費 NT$60 起") {
		t.Error("a shop that offers pickup does not state its NT$60 floor")
	}
	if !strings.Contains(with, "宅配與超商取貨") {
		t.Error("a shop that offers pickup does not say so")
	}
	without := render(home.NewStore(pool).WithoutPickup())
	if strings.Contains(without, "宅配與超商取貨") {
		t.Error("the free-delivery note promises pickup where checkout does not offer it")
	}
	if want := "未達門檻運費 NT$80 起"; !strings.Contains(without, want) {
		t.Errorf("the note's floor is not the home delivery fee; want %q", want)
	}
}

// TestStore_LoadLowestFeeAlwaysFree: A method that is always free charges
// nothing below any threshold, so the floor the strip states is the cheapest
// fee that is actually charged.
func TestStore_LoadLowestFeeAlwaysFree(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer db.Rollback(ctx, tx)

	if _, err = tx.Exec(ctx, `
		INSERT INTO shipping_method_versions
		    (method_id, name, carrier, fee_cents, free_over_cents, effective_at)
		SELECT DISTINCT ON (v.method_id) v.method_id, v.name, v.carrier,
		       CASE sm.destination_kind WHEN 'pickup_point' THEN 0 ELSE 8000 END,
		       300000, now()
		FROM shipping_method_versions v
		JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.is_active
		ORDER BY v.method_id, v.effective_at DESC`); err != nil {
		t.Fatalf("publish the fees: %v", err)
	}

	view := load(t, ctx, home.NewStore(tx), home.Choice{})
	if view.Rules.LowestFeeCents != 8000 {
		t.Errorf("Rules.LowestFeeCents = %d, want 8000: an always-free pickup method is not the fee below the threshold",
			view.Rules.LowestFeeCents)
	}
}
