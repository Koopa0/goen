//go:build integration

package home_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"errors"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/home"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/ui/pages"
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

func TestHomeShowsCategoriesAndProducts(t *testing.T) {
	// Departments fill the carousel only when no slide or campaign does.
	emptyHeroSlides(t)
	stopCampaigns(t)

	h := home.NewHandler(home.NewStore(pool), slog.New(slog.DiscardHandler), false)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	res := httptest.NewRecorder()
	h.Index(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	body := res.Body.String()

	for _, cat := range []string{"書籍文具", "居家生活", "美妝保養", "服飾配件", "美食飲品", "3C 數位"} {
		if !strings.Contains(body, cat) {
			t.Errorf("category tile %q is missing", cat)
		}
	}
	if strings.Count(body, `class="goen-tile__cell"`) < 4 {
		t.Error("the product row is missing its tiles")
	}
	if !strings.Contains(body, "NT$") {
		t.Error("no price is formatted; the tiles have no price")
	}
	// By shape, not by naming one file: artwork arrives a few products at a time.
	if !strings.Contains(body, `src="/static/media/products/`) {
		t.Error("no product storage key was mapped to an embedded media URL")
	}
	if !strings.Contains(body, `srcset="/static/media/products/`) ||
		!strings.Contains(body, ` 400w, `) ||
		!strings.Contains(body, ` 800w, `) ||
		!strings.Contains(body, ` 1600w"`) {
		t.Error("product image is missing its responsive srcset candidates")
	}
	if !strings.Contains(body, `width="1600" height="1200" loading="lazy"`) {
		t.Error("product image is missing its intrinsic dimensions or lazy-loading hint")
	}
	// A product with no artwork yet falls back to the tile placeholder.
	if !strings.Contains(body, "goen-tile__ph") && strings.Count(body, "/static/media/products/") < 8 {
		t.Error("a tile without embedded artwork rendered neither an image nor the placeholder")
	}
	if !strings.Contains(body, `class="goen-hero__slide goen-hero__slide--split"`) {
		t.Error("with no scheduled slide and no campaign the carousel shows no department")
	}
	if !strings.Contains(body, `src="/static/media/products/department-`) {
		t.Error("a department photograph was not mapped to its embedded media URL")
	}
	if !strings.Contains(body, "<html") {
		t.Error("the home response is not a full document")
	}
}

func TestStoreLoadAggregatesTiles(t *testing.T) {
	v, err := home.NewStore(pool).Load(t.Context())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(v.Categories) != 6 {
		t.Errorf("got %d category tiles; want the 6 root categories", len(v.Categories))
	}
	if len(v.Row.Tiles) != 4 {
		t.Fatalf("got %d product-row tiles; want 4", len(v.Row.Tiles))
	}

	for _, tile := range v.Row.Tiles {
		if tile.PriceCents <= 0 {
			t.Errorf("tile %q has no price (min active variant)", tile.Slug)
		}
		if tile.Name == "" || tile.Brand == "" {
			t.Errorf("tile %q is missing name or brand", tile.Slug)
		}
		if tile.HasImage() && (tile.ImageWidth != 1600 || tile.ImageHeight != 1200) {
			t.Errorf("tile %q image is %dx%d; want 1600x1200", tile.Slug, tile.ImageWidth, tile.ImageHeight)
		}
		if tile.OnSale() && tile.CompareCents <= tile.PriceCents {
			t.Errorf("tile %q claims a sale but the compare price is not higher", tile.Slug)
		}
	}
}

func TestADepartmentWithNoListedProductIsNotInTheDirectory(t *testing.T) {
	ctx := t.Context()
	before, err := home.NewStore(pool).Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(before.Categories) == 0 {
		t.Fatal("the seed has no departments")
	}
	hidden := before.Categories[0].Slug

	var ids []uuid.UUID
	rows, err := pool.Query(ctx, `
		WITH RECURSIVE tree AS (
		    SELECT id FROM categories WHERE slug = $1
		    UNION ALL
		    SELECT k.id FROM categories k JOIN tree t ON k.parent_id = t.id
		)
		UPDATE products SET status = 'draft'
		WHERE status = 'active' AND category_id IN (SELECT id FROM tree)
		RETURNING id`, hidden)
	if err != nil {
		t.Fatalf("unlist the department's products: %v", err)
	}
	ids, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatalf("read the unlisted products: %v", err)
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, relistErr := pool.Exec(clean, `UPDATE products SET status = 'active' WHERE id = ANY($1)`, ids); relistErr != nil {
			t.Errorf("list the products again: %v", relistErr)
		}
	})

	after, err := home.NewStore(pool).Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(after.Categories) != len(before.Categories)-1 {
		t.Errorf("%d departments listed, want %d", len(after.Categories), len(before.Categories)-1)
	}
	for i := range after.Categories {
		if after.Categories[i].Slug == hidden {
			t.Errorf("%s has no listed product and is still in the directory", hidden)
		}
	}
}

func TestAnEmptyHeroTableIsAWorkingHomePage(t *testing.T) {
	ctx := t.Context()
	emptyHeroSlides(t)
	stopCampaigns(t)

	slides := slidesOf(t, ctx)
	if len(slides) == 0 || len(slides) > 3 {
		t.Fatalf("an empty table produced %d slides, want 1 to 3 departments", len(slides))
	}
	for _, slide := range slides {
		if slide.Layout != pages.SlideSplit || !slide.Photo.Shown() || !slide.CTA.Shown() {
			t.Errorf("a department slide is %+v, want a photograph and a button", slide)
		}
		if want := i18n.T(ctx, i18n.KeyHeroCampaignCTA); slide.CTA.Label != want {
			t.Errorf("a department slide's button reads %q, want %q", slide.CTA.Label, want)
		}
		if len(slide.Stats) != 2 {
			t.Errorf("a department slide states %d figures, want items and categories", len(slide.Stats))
		}
	}
}

// slidesOf is the carousel the home page would draw.
func slidesOf(t *testing.T, ctx context.Context) []pages.HeroSlide {
	t.Helper()
	view, err := home.NewStore(pool).Load(ctx)
	if err != nil {
		t.Fatalf("load home: %v", err)
	}
	return view.Slides
}

func firstSlide(t *testing.T, ctx context.Context) pages.HeroSlide {
	t.Helper()
	slides := slidesOf(t, ctx)
	if len(slides) == 0 {
		t.Fatal("the carousel is empty")
	}
	return slides[0]
}

// The window is judged by the database's clock, which wrote starts_at.
func TestTheScheduledSlideIsTheOneShown(t *testing.T) {
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
				if slide.Layout != pages.SlideSplit {
					t.Errorf("the first slide is %q, want a department", slide.Title)
				}
				return
			}
			if slide.Title != tt.want {
				t.Errorf("headline is %q, want %q", slide.Title, tt.want)
			}
		})
	}
}

func TestScheduledSlidesKeepTheirQueueOrder(t *testing.T) {
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

	slides := slidesOf(t, ctx)
	if len(slides) < 2 || slides[0].Title != "第二" || slides[1].Title != "第三" {
		t.Errorf("the queue reads %+v, want 第二 then 第三 — a disabled slide is skipped "+
			"and the rest keep the order the editor set", slides)
	}
}

func TestTheHeroImageCarriesItsRealWidth(t *testing.T) {
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
		(headline, primary_cta_label, primary_cta_href, position, image_key, image_alt)
		VALUES ('有圖', '看', '/deals', 0, $1, '主視覺')`, digest); err != nil {
		t.Fatalf("insert: %v", err)
	}

	slide := firstSlide(t, ctx)
	if slide.PhotoWidth != 1440 {
		t.Errorf("PhotoWidth is %d, want 1440 — the srcset would state a width "+
			"the image does not have", slide.PhotoWidth)
	}
	if slide.PhotoHeight != 900 {
		t.Errorf("PhotoHeight is %d, want 900 — without it the hero <img> carries "+
			"no intrinsic ratio and the copy under it moves when the artwork lands",
			slide.PhotoHeight)
	}
	if !slide.Photo.Shown() {
		t.Error("a slide with an image drew no photograph")
	}
}

func TestDismissingOneBannerDoesNotSilenceTheNext(t *testing.T) {
	ctx := t.Context()
	s := home.NewStore(pool)

	if _, err := pool.Exec(ctx, `DELETE FROM promo_banners`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	first := seedBanner(t, "第一檔")

	shown, err := s.Banner(ctx, "")
	if err != nil {
		t.Fatalf("banner: %v", err)
	}
	if !shown.Shown() || shown.Message != "第一檔" {
		t.Fatalf("the running banner did not show: %+v", shown)
	}

	dismissed := home.DismissDigest(first)
	hidden, hiddenErr := s.Banner(ctx, dismissed)
	if hiddenErr != nil {
		t.Fatalf("banner: %v", hiddenErr)
	}
	if hidden.Shown() {
		t.Error("a dismissed banner is still showing")
	}

	if _, err := pool.Exec(ctx, `UPDATE promo_banners SET is_active = false`); err != nil {
		t.Fatalf("retire: %v", err)
	}
	seedBanner(t, "第二檔")

	next, nextErr := s.Banner(ctx, dismissed)
	if nextErr != nil {
		t.Fatalf("banner: %v", nextErr)
	}
	if !next.Shown() {
		t.Error("the NEXT promotion is hidden by a dismissal of the previous one")
	}
	if next.Message != "第二檔" {
		t.Errorf("showing %q, want 第二檔", next.Message)
	}
}

func TestTheCookieCarriesADigestNotTheID(t *testing.T) {
	const id = "019fa72c-dd04-7383-acc5-4c32f889c790"
	digest := home.DismissDigest(id)

	if strings.Contains(digest, id) || digest == id {
		t.Errorf("the cookie value %q contains the banner id", digest)
	}
	if len(digest) > 32 {
		t.Errorf("the cookie value is %d characters; it travels on every request",
			len(digest))
	}
	if again := home.DismissDigest(id); again != digest {
		t.Error("the digest is not stable")
	}
	if other := home.DismissDigest("019fa72c-dd04-7383-acc5-4c32f889c791"); other == digest {
		t.Error("two banners share a digest")
	}
}

func TestABannerOutsideItsWindowDoesNotShow(t *testing.T) {
	ctx := t.Context()
	s := home.NewStore(pool)

	tests := []struct {
		name  string
		setup string
		want  bool
	}{
		{"running", `UPDATE promo_banners SET is_active = true, starts_at = NULL, ends_at = NULL`, true},
		{"switched off", `UPDATE promo_banners SET is_active = false`, false},
		{"ended", `UPDATE promo_banners SET is_active = true,
			starts_at = now() - interval '30 days', ends_at = now() - interval '1 day'`, false},
		{"not started", `UPDATE promo_banners SET is_active = true,
			starts_at = now() + interval '1 day', ends_at = NULL`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `DELETE FROM promo_banners`); err != nil {
				t.Fatalf("clear: %v", err)
			}
			seedBanner(t, "檔期測試")
			if _, err := pool.Exec(ctx, tt.setup); err != nil {
				t.Fatalf("setup: %v", err)
			}
			banner, err := s.Banner(ctx, "")
			if err != nil {
				t.Fatalf("banner: %v", err)
			}
			if banner.Shown() != tt.want {
				t.Errorf("shown=%v, want %v", banner.Shown(), tt.want)
			}
		})
	}
}

func TestAnOffSiteCTAIsDroppedNotRendered(t *testing.T) {
	ctx := t.Context()
	s := home.NewStore(pool)

	for _, href := range []string{
		"https://evil.example", "//evil.example/x", "///evil.example/x",
		"javascript:alert(1)", `/\evil.example`,
	} {
		if _, err := pool.Exec(ctx, `DELETE FROM promo_banners`); err != nil {
			t.Fatalf("clear: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO promo_banners (message, cta_label, cta_href)
			VALUES ('測試', '點我', $1)`, href); err != nil {
			t.Fatalf("insert %q: %v", href, err)
		}
		banner, err := s.Banner(ctx, "")
		if err != nil {
			t.Fatalf("banner: %v", err)
		}
		if banner.HasCTA() {
			t.Errorf("an off-site CTA %q was rendered as %q", href, banner.CTAHref)
		}
		if !banner.Shown() {
			t.Errorf("a bad CTA hid the whole banner")
		}
	}

	if _, err := pool.Exec(ctx, `DELETE FROM promo_banners`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO promo_banners (message, cta_label, cta_href)
		VALUES ('測試', '看優惠', '/deals')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	banner, err := s.Banner(ctx, "")
	if err != nil {
		t.Fatalf("banner: %v", err)
	}
	if !banner.HasCTA() || banner.CTAHref != "/deals" {
		t.Errorf("a same-site CTA came back as %q", banner.CTAHref)
	}
}

// The admin form validates these paths too, but direct SQL and old rows still
// reach the storefront reader.
func TestAnOffSiteHeroCTAIsDroppedAtReadTime(t *testing.T) {
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

func seedBanner(t *testing.T, message string) string {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO promo_banners (message, message_short)
		VALUES ($1, $1) RETURNING id`, message).Scan(&id); err != nil {
		t.Fatalf("seed banner: %v", err)
	}
	return id.String()
}

// stopCampaigns switches every campaign off for the test: a running one takes a
// carousel slot and the product row, and the development seed runs two.
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

func TestRunningCampaignsFollowTheScheduledSlidesSoonestFirst(t *testing.T) {
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

	for locale, want := range map[i18n.Locale][2]string{
		i18n.ZhHant: {"較早結束", "較晚結束"},
		i18n.En:     {"Ends sooner", "Ends later"},
	} {
		slides := slidesOf(t, i18n.WithLocale(ctx, locale))
		if len(slides) < 2 || slides[0].Title != want[0] || slides[1].Title != want[1] {
			t.Fatalf("%s slides = %+v, want %q then %q first", locale, slides, want[0], want[1])
		}
		if slides[0].CTA.Href != "/s/hero-sooner" {
			t.Errorf("%s campaign button goes to %q, want /s/hero-sooner", locale, slides[0].CTA.Href)
		}
		if len(slides) != 3 {
			t.Errorf("%s carousel has %d slides, want one department filling it to 3", locale, len(slides))
		}
	}

	view, err := home.NewStore(pool).Load(ctx)
	if err != nil {
		t.Fatalf("load home: %v", err)
	}
	if view.Row.Href != "/s/hero-sooner" {
		t.Errorf("the product row is %q, want the campaign ending soonest", view.Row.Href)
	}
}

// A campaign whose featured product is sold out takes no carousel slide and no
// product row; a sellable product brings both back.
func TestACampaignWithNothingToBuyTakesNoCarouselSlide(t *testing.T) {
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

	shown := func() (slide, row bool) {
		for _, s := range slidesOf(t, ctx) {
			slide = slide || s.CTA.Href == "/s/hero-empty"
		}
		view, err := home.NewStore(pool).Load(ctx)
		if err != nil {
			t.Fatalf("load home: %v", err)
		}
		return slide, view.Row.Href == "/s/hero-empty"
	}
	featureProduct(t, "hero-empty", 0)
	if slide, row := shown(); slide || row {
		t.Fatalf("a campaign with only a sold-out product is on the home page (slide %v, row %v)", slide, row)
	}
	featureProduct(t, "hero-empty", 5)
	if slide, row := shown(); !slide || !row {
		t.Fatalf("a campaign with a sellable product is missing from the home page (slide %v, row %v)", slide, row)
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

func TestTheHeroSpeaksTheVisitorsLanguage(t *testing.T) {
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
	t.Cleanup(func() {
		// Its own context: t.Context() is cancelled before cleanups run.
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(clean, `DELETE FROM hero_slides WHERE id = $1`, id); err != nil {
			t.Errorf("clean up the slide: %v", err)
		}
	})

	for _, tt := range []struct {
		name     string
		locale   i18n.Locale
		headline string
		body     string
		cta      string
	}{
		{name: "Chinese", locale: i18n.ZhHant, headline: "挑一台好的", body: "說明文字", cta: "看商品"},
		{name: "English", locale: i18n.En, headline: "Choose one good thing", body: "Body copy", cta: "Shop"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			slide := firstSlide(t, i18n.WithLocale(ctx, tt.locale))
			if slide.Title != tt.headline {
				t.Errorf("the headline reads %q, want %q", slide.Title, tt.headline)
			}
			if slide.Fact != tt.body {
				t.Errorf("the line reads %q, want %q", slide.Fact, tt.body)
			}
			if slide.CTA.Label != tt.cta {
				t.Errorf("the button reads %q, want %q", slide.CTA.Label, tt.cta)
			}
			if slide.CTA.Href != "/c/phones" {
				t.Errorf("the button points at %q, want /c/phones", slide.CTA.Href)
			}
		})
	}
}

// localized_name(NULL, NULL, ...) is NULL while sqlc types the result as
// non-null, so an uncoalesced wrap fails to scan a slide with no eyebrow.
func TestASlideWithNoEyebrowStillRenders(t *testing.T) {
	ctx := t.Context()

	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO hero_slides (headline, primary_cta_label, primary_cta_href, position)
		VALUES ('只有標題', '看看', '/deals', -2)
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("queue a bare slide: %v", err)
	}
	t.Cleanup(func() {
		// Its own context: t.Context() is cancelled before cleanups run.
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(clean, `DELETE FROM hero_slides WHERE id = $1`, id); err != nil {
			t.Errorf("clean up the slide: %v", err)
		}
	})

	slide := firstSlide(t, i18n.WithLocale(ctx, i18n.En))
	if slide.Title != "只有標題" {
		t.Errorf("the headline reads %q", slide.Title)
	}
	if slide.Fact != "" {
		t.Errorf("absent copy came back as %q", slide.Fact)
	}
}

// free_over_cents lives in shipping_method_versions, which a shop edits at
// /admin/shipping, so a page stating the figure can drift from the till.
func TestTheFreeDeliveryStripStatesWhatTheTillCharges(t *testing.T) {
	ctx := t.Context()

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

	view, err := home.NewStore(pool).Load(i18n.WithLocale(ctx, i18n.ZhHant))
	if err != nil {
		t.Fatalf("load home: %v", err)
	}

	// The other active method still carries the seeded 300000; only the higher
	// threshold is free for both.
	if got := view.Rules.FreeDeliveryCents; got != 555500 {
		t.Errorf("the shop rules state a threshold of %d cents, want 555500 — the highest threshold, "+
			"the one every active method honours", got)
	}

	if _, bareErr := pool.Exec(ctx, `
		INSERT INTO shipping_method_versions
		    (method_id, name, carrier, fee_cents, free_over_cents, effective_at)
		SELECT DISTINCT ON (v.method_id) v.method_id, v.name, v.carrier, v.fee_cents, NULL, now()
		FROM shipping_method_versions v
		JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.is_active
		ORDER BY v.method_id, v.effective_at DESC`); bareErr != nil {
		t.Fatalf("withdraw free delivery: %v", bareErr)
	}
	bare, err := home.NewStore(pool).Load(i18n.WithLocale(ctx, i18n.ZhHant))
	if err != nil {
		t.Fatalf("load home: %v", err)
	}
	if got := bare.Rules.FreeDeliveryCents; got != 0 {
		t.Errorf("a shop that charges for every parcel advertises free delivery over %d cents", got)
	}
}

// fee_cents lives in shipping_method_versions, which a shop edits at
// /admin/shipping, so a page stating the figure can drift from the till.
func TestTheFreeDeliveryNoteStatesTheLowestCurrentFee(t *testing.T) {
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

	h := home.NewHandler(home.NewStore(pool), slog.New(slog.DiscardHandler), false)
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
	if strings.Contains(body, "NT$60") {
		t.Error("the free-delivery note still states the old NT$60 floor")
	}
}

// The header and the home tiles read one query, so their order cannot disagree,
// and two roots sharing a position is refused at the schema:
// categories_position_key is NULLS NOT DISTINCT precisely because the ROOT
// categories are the header, and a plain unique index would treat their NULL
// parents as distinct and allow the pair.
func TestTheHeaderAndTheTilesAgreeOnOrder(t *testing.T) {
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

	locale := i18n.WithLocale(ctx, i18n.ZhHant)
	store := home.NewStore(pool)

	nav, err := store.Nav(locale)
	if err != nil {
		t.Fatalf("read the header: %v", err)
	}
	view, err := store.Load(locale)
	if err != nil {
		t.Fatalf("read the home page: %v", err)
	}
	if len(nav) == 0 || len(view.Categories) == 0 {
		t.Fatal("one of the two read nothing, so this compared nothing")
	}

	header := make([]string, 0, len(nav))
	for i := range nav {
		header = append(header, nav[i].Slug)
	}
	tiles := make([]string, 0, len(view.Categories))
	for i := range view.Categories {
		tiles = append(tiles, view.Categories[i].Slug)
	}
	if strings.Join(header, ">") != strings.Join(tiles, ">") {
		t.Errorf("the header says %v and the tiles say %v, on one page", header, tiles)
	}
}

// Checkout drops store pickup where the store map is not configured, so the
// note's floor and wording must describe the home delivery it still offers.
func TestTheFreeDeliveryNoteDescribesOnlyTheMethodsCheckoutOffers(t *testing.T) {
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
		h := home.NewHandler(s, slog.New(slog.DiscardHandler), false)
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

// A method that is always free charges nothing below any threshold, so the
// floor the strip states is the cheapest fee that is actually charged.
func TestTheLowestFeeSkipsAMethodThatIsAlwaysFree(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer pgtx.Rollback(ctx, tx)

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

	view, err := home.NewStore(tx).Load(ctx)
	if err != nil {
		t.Fatalf("load home: %v", err)
	}
	if view.Rules.LowestFeeCents != 8000 {
		t.Errorf("Rules.LowestFeeCents = %d, want 8000: an always-free pickup method is not the fee below the threshold",
			view.Rules.LowestFeeCents)
	}
}

// A department's header panel shows at most three of its own products, newest
// first, and only ones that can be bought: the header is on every page, and a
// sold-out product there is a click that ends at a disabled button. The newest
// product of the department is sold out for the length of the test, so the rule
// is exercised by a row it actually removes.
func TestADepartmentPanelShowsItsNewestBuyableProducts(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	const dept = "tech"
	var newest string
	if err := pool.QueryRow(ctx, `
		WITH RECURSIVE tree AS (
		    SELECT id FROM categories WHERE slug = $1
		    UNION ALL
		    SELECT k.id FROM categories k JOIN tree t ON k.parent_id = t.id
		)
		SELECT p.slug FROM products p
		WHERE p.category_id IN (SELECT id FROM tree) AND p.status = 'active'
		ORDER BY p.published_at DESC, p.id DESC LIMIT 1`, dept).Scan(&newest); err != nil {
		t.Fatalf("fixture: the newest %s product: %v", dept, err)
	}
	// Remembered in Go rather than a temporary table: a pool hands each
	// statement whichever connection is free, and a temp table lives on one.
	var ids []uuid.UUID
	var stock []int32
	if err := pool.QueryRow(ctx, `
		SELECT array_agg(v.id), array_agg(v.stock_quantity) FROM product_variants v
		JOIN products p ON p.id = v.product_id WHERE p.slug = $1`, newest).Scan(&ids, &stock); err != nil {
		t.Fatalf("fixture: remember the stock: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.WithoutCancel(ctx), `
			UPDATE product_variants v SET stock_quantity = s.stock
			FROM unnest($1::uuid[], $2::integer[]) AS s(id, stock) WHERE s.id = v.id`, ids, stock); err != nil {
			t.Errorf("restore the stock: %v", err)
		}
	})
	if _, err := pool.Exec(ctx, `
		UPDATE product_variants SET stock_quantity = safety_stock WHERE id = ANY($1::uuid[])`, ids); err != nil {
		t.Fatalf("fixture: sell out %s: %v", newest, err)
	}

	nav, err := home.NewStore(pool).Nav(ctx)
	if err != nil {
		t.Fatalf("read the header: %v", err)
	}
	shown := 0
	for i := range nav {
		d := &nav[i]
		rows, err := pool.Query(ctx, `
			WITH RECURSIVE tree AS (
			    SELECT id FROM categories WHERE slug = $1
			    UNION ALL
			    SELECT k.id FROM categories k JOIN tree t ON k.parent_id = t.id
			)
			SELECT p.slug FROM products p
			WHERE p.category_id IN (SELECT id FROM tree) AND p.status = 'active'
			  AND EXISTS (
			      SELECT 1 FROM product_variants v
			      WHERE v.product_id = p.id AND v.is_active AND v.stock_quantity > v.safety_stock)
			ORDER BY p.published_at DESC, p.id DESC LIMIT 3`, d.Slug)
		if err != nil {
			t.Fatalf("read %s's products: %v", d.Slug, err)
		}
		want, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("read %s's products: %v", d.Slug, err)
		}
		got := make([]string, 0, len(d.Picks))
		for _, p := range d.Picks {
			got = append(got, p.Slug)
			if p.Slug == newest {
				t.Errorf("%s's panel offers %s, which is sold out", d.Slug, newest)
			}
			if !strings.HasPrefix(p.Price, "NT$") {
				t.Errorf("%s's panel prices %s as %q", d.Slug, p.Slug, p.Price)
			}
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s's panel shows %v, want %v", d.Slug, got, want)
		}
		shown += len(got)
	}
	if shown == 0 {
		t.Fatal("no department showed a product, so this compared nothing")
	}
}

func TestTheNavCountsEachDepartmentsActiveProducts(t *testing.T) {
	ctx := t.Context()
	var big, small, sub uuid.UUID
	for _, c := range []struct {
		id     *uuid.UUID
		slug   string
		parent *uuid.UUID
	}{
		{&big, "navcount-big", nil},
		{&small, "navcount-small", nil},
		{&sub, "navcount-big-sub", &big},
	} {
		var parent any
		if c.parent != nil {
			parent = *c.parent
		}
		if err := pool.QueryRow(ctx, `
			INSERT INTO categories (slug, name, parent_id, position)
			VALUES ($1, $1, $2, (SELECT coalesce(max(position), -1) + 1 FROM categories WHERE parent_id IS NOT DISTINCT FROM $2))
			RETURNING id`, c.slug, parent).Scan(c.id); err != nil {
			t.Fatalf("insert category %s: %v", c.slug, err)
		}
	}
	slugs := []string{"navcount-a", "navcount-b", "navcount-c", "navcount-d"}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		// Archived first: an active product may not lose its last variant.
		for _, q := range []string{
			`UPDATE products SET status = 'archived' WHERE slug = ANY($1)`,
			`DELETE FROM product_variants WHERE product_id IN (SELECT id FROM products WHERE slug = ANY($1))`,
			`DELETE FROM products WHERE slug = ANY($1)`,
		} {
			if _, err := pool.Exec(clean, q, slugs); err != nil {
				t.Errorf("clean up: %v", err)
			}
		}
		if _, err := pool.Exec(clean, `DELETE FROM categories WHERE id IN ($1, $2, $3)`, sub, big, small); err != nil {
			t.Errorf("clean up categories: %v", err)
		}
	})
	for _, p := range []struct {
		slug, status string
		category     uuid.UUID
	}{
		{slugs[0], "active", big},
		{slugs[1], "active", sub},
		{slugs[2], "draft", big},
		{slugs[3], "active", small},
	} {
		if _, err := pool.Exec(ctx, `
			WITH p AS (
			    INSERT INTO products (category_id, slug, name, status, published_at)
			    VALUES ($1, $2, $2, $3, now())
			    RETURNING id
			)
			INSERT INTO product_variants (product_id, sku, price_cents, stock_quantity, safety_stock, position)
			SELECT p.id, upper($2), 1000, 5, 0, 0 FROM p`,
			p.category, p.slug, p.status); err != nil {
			t.Fatalf("insert product %s: %v", p.slug, err)
		}
	}

	items, err := home.NewStore(pool).Nav(i18n.WithLocale(ctx, i18n.ZhHant))
	if err != nil {
		t.Fatalf("nav: %v", err)
	}
	got := map[string]int{}
	for _, n := range items {
		got[n.Slug] = n.ProductCount
	}
	if got["navcount-big"] != 2 || got["navcount-small"] != 1 {
		t.Errorf("department counts = big %d, small %d; want 2 (one in a sub-category, the draft left out) and 1",
			got["navcount-big"], got["navcount-small"])
	}
}

// The lead tile is the campaign's first product, so its first photograph's
// width decides; a photograph whose width was never stored counts as too narrow.
func TestTheLeadTileFollowsTheFirstPhotographsWidth(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	emptyHeroSlides(t)
	stopCampaigns(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer pgtx.Rollback(ctx, tx)
	if _, err = tx.Exec(ctx, `INSERT INTO sale_campaigns (slug, title, ends_at) VALUES ('lead-width', '主圖寬度', now() + interval '3 days')`); err != nil {
		t.Fatalf("insert campaign: %v", err)
	}
	var campaignID uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM sale_campaigns WHERE slug = 'lead-width'`).Scan(&campaignID); err != nil {
		t.Fatalf("read campaign: %v", err)
	}
	productIDs := make([]uuid.UUID, 0, 4)
	for range 4 {
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			WITH p AS (
			    INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
			    SELECT (SELECT id FROM brands LIMIT 1),
			           (SELECT id FROM categories WHERE parent_id IS NULL LIMIT 1),
			           'lead-' || gen_random_uuid(), '主圖商品', 'active', now()
			    RETURNING id
			), v AS (
			    INSERT INTO product_variants (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, position)
			    SELECT p.id, 'LEAD-' || upper(replace(gen_random_uuid()::text, '-', '')), 1000, 2000, 5, 0, 0 FROM p
			)
			SELECT id FROM p`).Scan(&id); err != nil {
			t.Fatalf("create product: %v", err)
		}
		productIDs = append(productIDs, id)
	}
	for i, id := range productIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO sale_campaign_products (campaign_id, product_id, position) VALUES ($1, $2, $3)`, campaignID, id, i); err != nil {
			t.Fatalf("feature product: %v", err)
		}
	}
	for _, tt := range []struct {
		name  string
		width any
		lead  bool
	}{
		{"wide", 1600, true},
		{"just under", 1199, false},
		{"width never stored", nil, false},
	} {
		if _, err := tx.Exec(ctx, `DELETE FROM product_images WHERE product_id = $1`, productIDs[0]); err != nil {
			t.Fatalf("clear photograph: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO product_images (product_id, storage_key, alt_text, width, height, position)
			VALUES ($1, 'lead.webp', '主圖', $2, 1200, 0)`, productIDs[0], tt.width); err != nil {
			t.Fatalf("%s: insert photograph: %v", tt.name, err)
		}
		view, err := home.NewStore(tx).Load(ctx)
		if err != nil {
			t.Fatalf("%s: load: %v", tt.name, err)
		}
		if view.Row.Href != "/s/lead-width" {
			t.Fatalf("%s: the row is %q, want the campaign's", tt.name, view.Row.Href)
		}
		if got := view.Row.HasLead(); got != tt.lead {
			t.Errorf("%s: lead tile = %t, want %t (first photograph %v wide)", tt.name, got, tt.lead, tt.width)
		}
	}
}

// The home page's band is a shelf of four products from a department that
// holds at least four; it needs no photograph.
func TestTheDepartmentBandHoldsFourProductsOfAFullDepartment(t *testing.T) {
	view, err := home.NewStore(pool).Load(t.Context())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if view.Band == nil {
		t.Fatal("the seed has a department with four products and the home page drew no band")
	}
	if got := len(view.Band.Tiles); got != 4 {
		t.Errorf("the band holds %d products, want 4", got)
	}
	if view.Band.Items < 4 {
		t.Errorf("the band's department holds %d products, want at least 4", view.Band.Items)
	}
}

// The shelf never repeats a product of the row above it, whichever department
// the day selects.
func TestTheDepartmentBandNeverRepeatsTheRowsProducts(t *testing.T) {
	store := home.NewStore(pool)
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for day := range 14 {
		view, err := store.AtTime(start.AddDate(0, 0, day)).Load(t.Context())
		if err != nil {
			t.Fatalf("load on day %d: %v", day, err)
		}
		if view.Band == nil {
			continue
		}
		for _, b := range view.Band.Tiles {
			for _, r := range view.Row.Tiles {
				if b.Slug == r.Slug {
					t.Errorf("day %d: %q is on the row and on the %s shelf", day, b.Slug, view.Band.Name)
				}
			}
		}
	}
}
