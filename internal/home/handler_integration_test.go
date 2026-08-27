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

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"errors"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/home"
	"github.com/koopa0/goen/internal/i18n"
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
	// The assertions below are about the default hero, which shows only when no
	// slide qualifies.
	emptyHeroSlides(t)

	h := home.NewHandler(home.NewStore(pool), slog.New(slog.DiscardHandler), false)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	res := httptest.NewRecorder()
	h.Home(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	body := res.Body.String()

	for _, cat := range []string{"手機", "筆電", "平板", "耳機與音響", "穿戴裝置", "周邊配件"} {
		if !strings.Contains(body, cat) {
			t.Errorf("category tile %q is missing", cat)
		}
	}
	if !strings.Contains(body, "Meridian Book 14") {
		t.Error("recommended products are missing")
	}
	if !strings.Contains(body, "NT$") {
		t.Error("no price is formatted; the tiles have no price")
	}
	// By shape, not by naming one file: a tile renders an <img> only when the
	// artwork is embedded, and they arrive a few at a time.
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
	if !strings.Contains(body, `src="/static/media/hero/home-hero-01.webp`) {
		t.Error("home hero does not use the required embedded media URL")
	}
	if !strings.Contains(body, `srcset="/static/media/hero/home-hero-01-720.webp`) ||
		!strings.Contains(body, ` 720w, `) ||
		!strings.Contains(body, ` 1440w"`) {
		t.Error("home hero is missing its responsive srcset candidates")
	}
	if !strings.Contains(body, `width="1440" height="900" decoding="async" fetchpriority="high"`) {
		t.Error("home hero is missing its intrinsic dimensions or priority hint")
	}
	if !strings.Contains(body, "<html") {
		t.Error("the home response is not a full document")
	}
}

func TestStoreLoadAggregatesTiles(t *testing.T) {
	v, err := home.NewStore(pool).Load(t.Context(), 8)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(v.Categories) != 6 {
		t.Errorf("got %d category tiles; want the 6 root categories", len(v.Categories))
	}
	if len(v.Recommended) != 8 {
		t.Fatalf("got %d recommended tiles; want 8", len(v.Recommended))
	}

	for _, tile := range v.Recommended {
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

func TestAnEmptyHeroTableIsAWorkingHomePage(t *testing.T) {
	ctx := t.Context()
	emptyHeroSlides(t)

	hero, err := home.NewStore(pool).Hero(ctx)
	if err != nil {
		t.Fatalf("hero: %v", err)
	}
	if hero.Headline == "" {
		t.Fatal("an empty table produced a hero with no headline")
	}
	if hero != pages.DefaultHero(t.Context()) {
		t.Errorf("an empty table produced %+v, want the built-in copy", hero)
	}
	if !hero.PrimaryCTA.Shown() {
		t.Error("the fallback hero has no button; the home page would have no call to action")
	}
}

// The window is judged by the database's clock, which wrote starts_at.
func TestTheScheduledSlideIsTheOneShown(t *testing.T) {
	ctx := t.Context()
	s := home.NewStore(pool)

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
			hero, err := s.Hero(ctx)
			if err != nil {
				t.Fatalf("hero: %v", err)
			}
			if tt.want == "" {
				if hero != pages.DefaultHero(t.Context()) {
					t.Errorf("headline is %q, want the built-in fallback", hero.Headline)
				}
				return
			}
			if hero.Headline != tt.want {
				t.Errorf("headline is %q, want %q", hero.Headline, tt.want)
			}
		})
	}
}

func TestTheFirstQualifyingSlideWins(t *testing.T) {
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

	hero, err := home.NewStore(pool).Hero(ctx)
	if err != nil {
		t.Fatalf("hero: %v", err)
	}
	if hero.Headline != "第二" {
		t.Errorf("headline is %q, want 第二 — the queue must skip a disabled slide "+
			"and stop at the first that qualifies", hero.Headline)
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

	hero, err := home.NewStore(pool).Hero(ctx)
	if err != nil {
		t.Fatalf("hero: %v", err)
	}
	if hero.ImageWidth != 1440 {
		t.Errorf("ImageWidth is %d, want 1440 — the srcset would state a width "+
			"the image does not have", hero.ImageWidth)
	}
	if !hero.Custom() {
		t.Error("a slide with an image reported itself as the fallback")
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

// TestAnOffSiteHeroCTAIsReplacedAtReadTime. The admin form validates these
// paths too, but direct SQL and old rows still reach the storefront reader.
func TestAnOffSiteHeroCTAIsReplacedAtReadTime(t *testing.T) {
	ctx := t.Context()
	s := home.NewStore(pool)

	t.Run("primary uses the built-in CTA pair", func(t *testing.T) {
		emptyHeroSlides(t)
		if _, err := pool.Exec(ctx, `
			INSERT INTO hero_slides
			    (headline, primary_cta_label, primary_cta_href,
			     secondary_cta_label, secondary_cta_href, position)
			VALUES ('仍是這張投影片', '假的按鈕', '///evil.example/x',
			        '原本安全的次按鈕', '/custom-safe', -100)`); err != nil {
			t.Fatalf("insert poisoned primary CTA: %v", err)
		}

		hero, err := s.Hero(ctx)
		if err != nil {
			t.Fatalf("hero: %v", err)
		}
		if hero.Headline != "仍是這張投影片" {
			t.Errorf("a bad link discarded the slide copy: %q", hero.Headline)
		}
		defaults := pages.DefaultHero(ctx)
		if hero.PrimaryCTA != defaults.PrimaryCTA || hero.SecondaryCTA != defaults.SecondaryCTA {
			t.Errorf("poisoned primary CTA left pairs %+v / %+v, want built-ins %+v / %+v",
				hero.PrimaryCTA, hero.SecondaryCTA, defaults.PrimaryCTA, defaults.SecondaryCTA)
		}
	})

	t.Run("secondary is dropped while primary survives", func(t *testing.T) {
		emptyHeroSlides(t)
		if _, err := pool.Exec(ctx, `
			INSERT INTO hero_slides
			    (headline, primary_cta_label, primary_cta_href,
			     secondary_cta_label, secondary_cta_href, position)
			VALUES ('安全的主按鈕', '看優惠', '/deals',
			        '假的次按鈕', '///evil.example/x', -100)`); err != nil {
			t.Fatalf("insert poisoned secondary CTA: %v", err)
		}

		hero, err := s.Hero(ctx)
		if err != nil {
			t.Fatalf("hero: %v", err)
		}
		if hero.PrimaryCTA != (pages.CTA{Label: "看優惠", Href: "/deals"}) {
			t.Errorf("safe primary CTA changed: %+v", hero.PrimaryCTA)
		}
		if hero.SecondaryCTA != (pages.CTA{}) {
			t.Errorf("poisoned secondary CTA survived: %+v", hero.SecondaryCTA)
		}
	})
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

func emptyHeroSlides(t *testing.T) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `DELETE FROM hero_slides`); err != nil {
		t.Fatalf("clear hero slides: %v", err)
	}
}

func TestTheHeroSpeaksTheVisitorsLanguage(t *testing.T) {
	ctx := t.Context()
	s := home.NewStore(pool)

	// Its own slide, at the front of the queue: the hero is a queue, so another
	// test's slide would decide what this one reads.
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
		cta      string
	}{
		{name: "Chinese", locale: i18n.ZhHant, headline: "挑一台好的", cta: "看商品"},
		{name: "English", locale: i18n.En, headline: "Choose one good thing", cta: "Shop"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			hero, err := s.Hero(i18n.WithLocale(ctx, tt.locale))
			if err != nil {
				t.Fatalf("Hero: %v", err)
			}
			if hero.Headline != tt.headline {
				t.Errorf("the headline reads %q, want %q", hero.Headline, tt.headline)
			}
			if hero.PrimaryCTA.Label != tt.cta {
				t.Errorf("the button reads %q, want %q", hero.PrimaryCTA.Label, tt.cta)
			}
			if hero.PrimaryCTA.Href != "/c/phones" {
				t.Errorf("the button points at %q, want /c/phones", hero.PrimaryCTA.Href)
			}
		})
	}
}

// localized_name(NULL, NULL, ...) is NULL while sqlc types the result as
// non-null, so an uncoalesced wrap fails to scan a slide with no eyebrow.
func TestASlideWithNoEyebrowStillRenders(t *testing.T) {
	ctx := t.Context()
	s := home.NewStore(pool)

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

	hero, err := s.Hero(i18n.WithLocale(ctx, i18n.En))
	if err != nil {
		t.Fatalf("Hero: %v", err)
	}
	if hero.Headline != "只有標題" {
		t.Errorf("the headline reads %q", hero.Headline)
	}
	if hero.Eyebrow != "" || hero.Body != "" {
		t.Errorf("absent copy came back as %q / %q", hero.Eyebrow, hero.Body)
	}
}

// free_over_cents lives in shipping_method_versions and a shop edits it at
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

	view, err := home.NewStore(pool).Load(i18n.WithLocale(ctx, i18n.ZhHant), 4)
	if err != nil {
		t.Fatalf("load home: %v", err)
	}

	// The other active method still carries the seeded 300000, which is the
	// lowest and therefore the honest figure.
	if got := view.FreeDelivery(); got != "NT$3,000" {
		t.Errorf("the strip states %q, want NT$3,000 — the lowest threshold any "+
			"active method honours", got)
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
	bare, err := home.NewStore(pool).Load(i18n.WithLocale(ctx, i18n.ZhHant), 4)
	if err != nil {
		t.Fatalf("load home: %v", err)
	}
	if got := bare.FreeDelivery(); got != "" {
		t.Errorf("a shop that charges for every parcel advertises free delivery over %q", got)
	}
}

// TestTheHeaderAndTheTilesAgreeOnOrder holds one question that had two answers.
//
// NavCategories ordered by (position, name) and HomeCategories by (position)
// alone — the same six rows, read by the header and by the tiles directly below
// it on the same page. Measured before the fix, by colliding two positions: the
// header read accessories > phones > laptops and the tiles read phones >
// accessories > laptops, on one render.
//
// It is one query now, so the two cannot disagree however the rows are ordered.
// And the collision that made them disagree is refused at the schema:
// categories_position_key is NULLS NOT DISTINCT precisely because the ROOT
// categories are the header, and a plain unique index would treat their NULL
// parents as distinct and allow the pair.
//
// The staged collision this test used to build is therefore a state the
// application can no longer reach, so it asserts the refusal by NAME instead —
// a fixture for an impossible state proves nothing about the code that runs.
func TestTheHeaderAndTheTilesAgreeOnOrder(t *testing.T) {
	ctx := t.Context()

	// Two roots at one position is what CreateCategory's max(position) + 1 could
	// hand out to two staff members at once, and what nothing used to refuse.
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

	// And both surfaces read one query, so there is no second order to drift.
	locale := i18n.WithLocale(ctx, i18n.ZhHant)
	store := home.NewStore(pool)

	nav, err := store.Nav(locale)
	if err != nil {
		t.Fatalf("read the header: %v", err)
	}
	view, err := store.Load(locale, 4)
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
