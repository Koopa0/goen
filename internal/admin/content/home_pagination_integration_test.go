//go:build integration

package content_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/content"
	"github.com/koopa0/goen/internal/home"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestEveryHomepageEntryRemainsManageableBeyondExpiredHistory(t *testing.T) {
	p := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, p)
	if _, err := p.Exec(ctx, `DELETE FROM hero_slides; DELETE FROM promo_banners`); err != nil {
		t.Fatal(err)
	}
	heroes, banners := make([]string, 0, 43), make([]string, 0, 43)
	for i := range 43 {
		heroID := fmt.Sprintf("10000000-0000-4000-8000-%012x", i+1)
		bannerID := fmt.Sprintf("20000000-0000-4000-8000-%012x", i+1)
		heroes = append(heroes, heroID)
		banners = append(banners, bannerID)
		if _, err := p.Exec(ctx, `
			INSERT INTO hero_slides (id, headline, primary_cta_label, primary_cta_href, position, is_active, ends_at)
			VALUES ($1, $2, 'Browse', '/search', $3, $4,
			        CASE WHEN $3 < 20 AND $3 % 2 = 0 THEN now() - interval '1 day' ELSE NULL END)`,
			heroID, fmt.Sprintf("Slide %d", i), i, i < 20 && i%2 == 0 || i >= 20 && i < 25); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Exec(ctx, `
			INSERT INTO promo_banners (id, message, message_en, is_active, created_at, ends_at)
			VALUES ($1, $2, $2, $3,
			        CASE WHEN $4 = 20 THEN '2019-01-01'::timestamptz ELSE '2020-01-01'::timestamptz END,
			        CASE WHEN $4 < 20 THEN now() - interval '1 day' ELSE NULL END)`,
			bannerID, fmt.Sprintf("Strip %d", i), i <= 20, i); err != nil {
			t.Fatal(err)
		}
	}
	bannerOrder := make([]string, 0, 43)
	for i := 19; i >= 0; i-- {
		bannerOrder = append(bannerOrder, banners[i])
	}
	bannerOrder = append(bannerOrder, banners[20])
	for i := 42; i >= 21; i-- {
		bannerOrder = append(bannerOrder, banners[i])
	}
	adminPool := admintest.AdminRolePool(t, p)
	s := content.NewStore(adminPool)
	log := slog.New(slog.DiscardHandler)
	h := content.NewHandler(s, media.NewHandler(media.NewStore(adminPool), log), newsletter.NewStore(adminPool), log)
	mux := http.NewServeMux()
	h.Routes(mux, admintest.BackOffice)
	shop := home.NewStore(adminPool)
	live, err := shop.Carousel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 3 || live[0].ID != heroes[20] || live[2].ID != heroes[22] {
		t.Fatalf("fixture carousel=%+v", live)
	}
	strip, err := shop.Banner(ctx, "")
	if err != nil || strip.Message != "Strip 20" {
		t.Fatalf("fixture banner=%+v/%v", strip, err)
	}

	get := func(ctx context.Context, path string) string {
		t.Helper()
		target, err := url.Parse(path)
		if err != nil {
			t.Fatal(err)
		}
		// A browser keeps the arrival fragment; the HTTP request excludes it.
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequestWithContext(ctx, http.MethodGet, target.RequestURI(), nil))
		if res.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d: %s", path, res.Code, res.Body.String())
		}
		return res.Body.String()
	}
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			localeCtx := i18n.WithLocale(ctx, locale)
			first := get(localeCtx, "/admin/home")
			firstHeroes, heroNext := homepageQueue(t, first, "hero-history")
			firstBanners, bannerNext := homepageQueue(t, first, "banner-history")
			if len(firstHeroes) != 20 || len(firstBanners) != 20 || heroNext == "" || bannerNext == "" {
				t.Fatalf("first page heroes=%d banners=%d heroNext=%q bannerNext=%q", len(firstHeroes), len(firstBanners), heroNext, bannerNext)
			}
			for _, queue := range []struct {
				heading, other   string
				want, otherFirst []string
			}{
				{"hero-history", "banner-history", heroes, firstBanners},
				{"banner-history", "hero-history", bannerOrder, firstHeroes},
			} {
				path := "/admin/home"
				var got []string
				for page := 0; ; page++ {
					if page >= 4 {
						t.Fatal("pagination did not terminate")
					}
					body := get(localeCtx, path)
					ids, next := homepageQueue(t, body, queue.heading)
					if len(ids) == 0 || len(ids) > 20 {
						t.Errorf("%s page %d has %d entries", queue.heading, page, len(ids))
					}
					other, _ := homepageQueue(t, body, queue.other)
					if diff := cmp.Diff(queue.otherFirst, other); diff != "" {
						t.Errorf("paging %s moved the other queue (-want +got):\n%s", queue.heading, diff)
					}
					got = append(got, ids...)
					if next == "" {
						break
					}
					path = next
				}
				if diff := cmp.Diff(queue.want, got); diff != "" {
					t.Errorf("%s reachable entries (-want +got):\n%s", queue.heading, diff)
				}
			}
			for _, tt := range []struct {
				path, heading string
				want          []string
			}{
				{strings.Replace(heroNext, "queue=slides", "queue=banners", 1), "banner-history", firstBanners},
				{strings.Replace(bannerNext, "queue=banners", "queue=slides", 1), "hero-history", firstHeroes},
				{"/admin/home?queue=slides&after=invalid", "hero-history", firstHeroes},
				{"/admin/home?queue=banners&after=invalid", "banner-history", firstBanners},
			} {
				got, _ := homepageQueue(t, get(localeCtx, tt.path), tt.heading)
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("invalid or foreign position changes %s (-want +got):\n%s", tt.heading, diff)
				}
			}
			second := get(localeCtx, heroNext)
			assertHomepageSlideBadge(t, second, heroes[20], true, i18n.T(localeCtx, i18n.KeyAdminHomeShowing))
			assertHomepageSlideBadge(t, second, heroes[24], false, i18n.T(localeCtx, i18n.KeyAdminHomeShowing))
		})
	}
	first := get(ctx, "/admin/home")
	_, heroNext := homepageQueue(t, first, "hero-history")
	_, bannerNext := homepageQueue(t, first, "banner-history")
	post := func(body, action, active string) {
		t.Helper()
		if !strings.Contains(body, `action="`+action+`"`) {
			t.Fatalf("later management page has no action %s", action)
		}
		res := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, action, strings.NewReader(url.Values{"active": {active}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusSeeOther {
			t.Fatalf("POST %s status=%d: %s", action, res.Code, res.Body.String())
		}
	}
	second := get(ctx, heroNext)
	post(second, "/admin/home/"+heroes[20]+"/active", "false")
	post(second, "/admin/home/"+heroes[24]+"/promote", "")
	live, err = shop.Carousel(ctx)
	if err != nil || len(live) == 0 || live[0].Source != pages.SlideScheduled || live[0].ID != heroes[24] {
		t.Fatalf("later-page controls did not change the real carousel: %+v/%v", live, err)
	}
	for _, slide := range live {
		if slide.ID == heroes[20] {
			t.Error("disabled later-page slide remains on storefront")
		}
	}
	post(get(ctx, bannerNext), "/admin/home/banner/"+banners[20]+"/active", "0")
	strip, err = shop.Banner(ctx, "")
	if err != nil || strip.Message != "" {
		t.Fatalf("later-page banner control did not clear the storefront: %+v/%v", strip, err)
	}
	if _, err := p.Exec(ctx, `DELETE FROM hero_slides WHERE id::text = ANY($1);`, heroes[20:]); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `DELETE FROM promo_banners WHERE id::text = ANY($1);`, banners[20:]); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		path, heading, first string
		empty                i18n.Key
	}{
		{heroNext, "hero-history", "/admin/home?queue=slides#hero-history", i18n.KeyAdminHomeEmpty},
		{bannerNext, "banner-history", "/admin/home?queue=banners#banner-history", i18n.KeyAdminHomeBannerEmpty},
	} {
		body := get(ctx, tt.path)
		ids, next := homepageQueue(t, body, tt.heading)
		if len(ids) != 0 || next != "" || !strings.Contains(body, i18n.T(ctx, i18n.KeyPageEmpty)) || !strings.Contains(body, `href="`+tt.first+`"`) || strings.Contains(body, i18n.T(ctx, tt.empty)) {
			t.Errorf("%s vanished continuation is not explained with a restart", tt.heading)
		}
	}
}

func homepageQueue(t *testing.T, body, heading string) (ids []string, next string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var side *html.Node
	for n := range doc.Descendants() {
		if homepageAttribute(n, "id") == heading {
			side = n.Parent
			break
		}
	}
	if side == nil {
		t.Fatalf("no queue heading %q", heading)
	}
	prefix := "/admin/home/"
	if heading == "banner-history" {
		prefix += "banner/"
	}
	for n := range side.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		if n.Data == "a" && homepageAttribute(n, "rel") == "next" {
			next = homepageAttribute(n, "href")
		}
		if n.Data == "form" {
			action := homepageAttribute(n, "action")
			if id, ok := strings.CutPrefix(action, prefix); ok && strings.HasSuffix(id, "/active") {
				ids = append(ids, strings.TrimSuffix(id, "/active"))
			}
		}
	}
	return ids, next
}

func assertHomepageSlideBadge(t *testing.T, body, id string, showing bool, label string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for n := range doc.Descendants() {
		if n.Data != "form" || homepageAttribute(n, "action") != "/admin/home/"+id+"/active" {
			continue
		}
		var text strings.Builder
		for child := range n.Parent.Parent.Descendants() {
			if child.Type == html.TextNode {
				text.WriteString(child.Data)
			}
		}
		if got := strings.Contains(text.String(), label); got != showing {
			t.Errorf("slide %s showing badge=%v, want %v", id, got, showing)
		}
		return
	}
	t.Errorf("later page has no slide control for %s", id)
}

func homepageAttribute(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
