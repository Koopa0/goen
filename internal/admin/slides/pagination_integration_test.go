//go:build integration

package slides_test

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
	"github.com/koopa0/goen/internal/admin/slides"
	"github.com/koopa0/goen/internal/home"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/ui/components"
)

func TestEveryScheduledSlideRemainsManageableBeyondExpiredHistory(t *testing.T) {
	p := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, p)
	if _, err := p.Exec(ctx, `DELETE FROM hero_slides`); err != nil {
		t.Fatal(err)
	}
	heroes := make([]string, 0, 43)
	for i := range 43 {
		heroID := fmt.Sprintf("10000000-0000-4000-8000-%012x", i+1)
		heroes = append(heroes, heroID)
		if _, err := p.Exec(ctx, `
			INSERT INTO hero_slides (id, headline, primary_cta_label, primary_cta_href, position, is_active, ends_at)
			VALUES ($1, $2, 'Browse', '/search', $3, $4,
			        CASE WHEN $3 < 20 AND $3 % 2 = 0 THEN now() - interval '1 day' ELSE NULL END)`,
			heroID, fmt.Sprintf("Slide %d", i), i, i < 20 && i%2 == 0 || i >= 20 && i < 25); err != nil {
			t.Fatal(err)
		}
	}
	adminPool := admintest.AdminRolePool(t, p)
	s := newStore(adminPool)
	log := slog.New(slog.DiscardHandler)
	h := slides.NewHandler(s, media.NewHandler(media.NewStore(adminPool), log), log)
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
			first := get(localeCtx, "/admin/slides")
			firstHeroes, heroNext := slideQueue(t, first)
			if len(firstHeroes) != 20 || heroNext == "" {
				t.Fatalf("first page heroes=%d heroNext=%q", len(firstHeroes), heroNext)
			}
			path := "/admin/slides"
			var got []string
			for page := 0; ; page++ {
				if page >= 4 {
					t.Fatal("pagination did not terminate")
				}
				ids, next := slideQueue(t, get(localeCtx, path))
				if len(ids) == 0 || len(ids) > 20 {
					t.Errorf("hero-history page %d has %d entries", page, len(ids))
				}
				got = append(got, ids...)
				if next == "" {
					break
				}
				path = next
			}
			if diff := cmp.Diff(heroes, got); diff != "" {
				t.Errorf("hero-history reachable entries (-want +got):\n%s", diff)
			}
			ids, _ := slideQueue(t, get(localeCtx, "/admin/slides?after=invalid"))
			if diff := cmp.Diff(firstHeroes, ids); diff != "" {
				t.Errorf("an invalid position changes hero-history (-want +got):\n%s", diff)
			}
			second := get(localeCtx, heroNext)
			assertHomepageSlideBadge(t, second, heroes[20], true, i18n.T(localeCtx, i18n.KeyAdminHomeShowing))
			assertHomepageSlideBadge(t, second, heroes[24], false, i18n.T(localeCtx, i18n.KeyAdminHomeShowing))
		})
	}
	_, heroNext := slideQueue(t, get(ctx, "/admin/slides"))
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
	post(second, "/admin/slides/"+heroes[20]+"/active", "false")
	post(second, "/admin/slides/"+heroes[24]+"/promote", "")
	live, err = shop.Carousel(ctx)
	if err != nil || len(live) == 0 || live[0].Source != components.SlideScheduled || live[0].ID != heroes[24] {
		t.Fatalf("later-page controls did not change the real carousel: %+v/%v", live, err)
	}
	for _, slide := range live {
		if slide.ID == heroes[20] {
			t.Error("disabled later-page slide remains on storefront")
		}
	}
	if _, err := p.Exec(ctx, `DELETE FROM hero_slides WHERE id::text = ANY($1);`, heroes[20:]); err != nil {
		t.Fatal(err)
	}
	body := get(ctx, heroNext)
	ids, next := slideQueue(t, body)
	if len(ids) != 0 || next != "" || !strings.Contains(body, i18n.T(ctx, i18n.KeyPageEmpty)) || !strings.Contains(body, `href="/admin/slides#hero-history"`) || strings.Contains(body, i18n.T(ctx, i18n.KeyAdminHomeEmpty)) {
		t.Error("hero-history vanished continuation is not explained with a restart")
	}
}

// slideQueue is the slides one page lists, by the id each toggle form posts
// to, and the page's next link.
func slideQueue(t *testing.T, body string) (ids []string, next string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var side *html.Node
	for n := range doc.Descendants() {
		if homepageAttribute(n, "id") == "hero-history" {
			side = n.Parent
			break
		}
	}
	if side == nil {
		t.Fatal("no queue heading hero-history")
	}
	prefix := "/admin/slides/"
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
		if n.Data != "form" || homepageAttribute(n, "action") != "/admin/slides/"+id+"/active" {
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
