//go:build integration

package banners_test

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
	"github.com/koopa0/goen/internal/admin/banners"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/site"
)

func TestEveryStripRemainsManageableBeyondExpiredHistory(t *testing.T) {
	p := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, p)
	if _, err := p.Exec(ctx, `DELETE FROM promo_banners`); err != nil {
		t.Fatal(err)
	}
	strips := make([]string, 0, 43)
	for i := range 43 {
		bannerID := fmt.Sprintf("20000000-0000-4000-8000-%012x", i+1)
		strips = append(strips, bannerID)
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
		bannerOrder = append(bannerOrder, strips[i])
	}
	bannerOrder = append(bannerOrder, strips[20])
	for i := 42; i >= 21; i-- {
		bannerOrder = append(bannerOrder, strips[i])
	}
	adminPool := admintest.AdminRolePool(t, p)
	h := banners.NewHandler(banners.NewStore(adminPool), slog.New(slog.DiscardHandler))
	mux := http.NewServeMux()
	h.Routes(mux, admintest.BackOffice)
	chrome := site.NewStore(adminPool)
	strip, err := chrome.Banner(ctx, "")
	if err != nil || strip.Message != "Strip 20" {
		t.Fatalf("fixture banner=%+v/%v", strip, err)
	}

	get := func(ctx context.Context, path string) string {
		t.Helper()
		path, _, _ = strings.Cut(path, "#")
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil))
		if res.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d: %s", path, res.Code, res.Body.String())
		}
		return res.Body.String()
	}
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			localeCtx := i18n.WithLocale(ctx, locale)
			first := get(localeCtx, "/admin/banners")
			firstBanners, bannerNext := bannerQueue(t, first)
			if len(firstBanners) != 20 || bannerNext == "" {
				t.Fatalf("first page banners=%d bannerNext=%q", len(firstBanners), bannerNext)
			}
			path := "/admin/banners"
			var got []string
			for page := 0; ; page++ {
				if page >= 4 {
					t.Fatal("pagination did not terminate")
				}
				ids, next := bannerQueue(t, get(localeCtx, path))
				if len(ids) == 0 || len(ids) > 20 {
					t.Errorf("banner-history page %d has %d entries", page, len(ids))
				}
				got = append(got, ids...)
				if next == "" {
					break
				}
				path = next
			}
			if diff := cmp.Diff(bannerOrder, got); diff != "" {
				t.Errorf("banner-history reachable entries (-want +got):\n%s", diff)
			}
			ids, _ := bannerQueue(t, get(localeCtx, "/admin/banners?after=invalid"))
			if diff := cmp.Diff(firstBanners, ids); diff != "" {
				t.Errorf("an invalid position changes banner-history (-want +got):\n%s", diff)
			}
		})
	}
	_, bannerNext := bannerQueue(t, get(ctx, "/admin/banners"))
	later := get(ctx, bannerNext)
	action := "/admin/banners/" + strips[20] + "/active"
	if !strings.Contains(later, `action="`+action+`"`) {
		t.Fatalf("later management page has no action %s", action)
	}
	res := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, action, strings.NewReader(url.Values{"active": {"0"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("POST %s status=%d: %s", action, res.Code, res.Body.String())
	}
	strip, err = chrome.Banner(ctx, "")
	if err != nil || strip.Message != "" {
		t.Fatalf("later-page banner control did not clear the storefront: %+v/%v", strip, err)
	}
	if _, err := p.Exec(ctx, `DELETE FROM promo_banners WHERE id::text = ANY($1);`, strips[20:]); err != nil {
		t.Fatal(err)
	}
	body := get(ctx, bannerNext)
	ids, next := bannerQueue(t, body)
	if len(ids) != 0 || next != "" || !strings.Contains(body, i18n.T(ctx, i18n.KeyPageEmpty)) || !strings.Contains(body, `href="/admin/banners#banner-history"`) || strings.Contains(body, i18n.T(ctx, i18n.KeyAdminHomeBannerEmpty)) {
		t.Error("banner-history vanished continuation is not explained with a restart")
	}
}

// bannerQueue is the strips one page lists, by the id each toggle form posts
// to, and the page's next link.
func bannerQueue(t *testing.T, body string) (ids []string, next string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var side *html.Node
	for n := range doc.Descendants() {
		if bannerAttribute(n, "id") == "banner-history" {
			side = n.Parent
			break
		}
	}
	if side == nil {
		t.Fatal("no queue heading banner-history")
	}
	for n := range side.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		if n.Data == "a" && bannerAttribute(n, "rel") == "next" {
			next = bannerAttribute(n, "href")
		}
		if n.Data == "form" {
			action := bannerAttribute(n, "action")
			if id, ok := strings.CutPrefix(action, "/admin/banners/"); ok && strings.HasSuffix(id, "/active") {
				ids = append(ids, strings.TrimSuffix(id, "/active"))
			}
		}
	}
	return ids, next
}

func bannerAttribute(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
