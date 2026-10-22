package campaign_test

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/ui/widget"
)

func renderCampaign(t *testing.T, startsAt, endsAt time.Time) string {
	t.Helper()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	return renderComponent(t, ctx, campaign.Page(frame.Page{Title: "c"}, campaign.View{
		Slug: "c", Title: "秋日選物", Products: []tile.Tile{{Slug: "p", Name: "茶壺", PriceCents: 80000, InStock: true}},
		Schedule: campaign.NewSchedule(ctx, "秋日選物", 1, startsAt, endsAt, scheduleNow),
	}))
}

// TestPageFactsAndDays: a campaign page states its facts and draws its days.
func TestPageFactsAndDays(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		startsAt     time.Time
		endsAt       time.Time
		want, absent []string
		filled       int
	}{
		{"running", day(1), day(11), []string{"<dt>商品</dt>", "<dt>結束</dt>", "<dt>剩餘</dt>", "8\u00a0<small>天</small>"}, []string{"開始", "已結束", "限時"}, 1},
		{"one day left", day(1), day(4), []string{"明天結束"}, []string{"<dt>剩餘</dt>", "已結束"}, 1},
		{"last day", day(1), day(3), []string{"今天結束"}, []string{"<dt>剩餘</dt>", "已結束"}, 1},
		{"not started", day(5), day(9), []string{"<dt>開始</dt>", "<dt>結束</dt>", "<dt>商品</dt>", "10\u00a0月 5\u00a0日", "還沒開始"}, []string{"已結束", "<dt>剩餘</dt>", `data-cell`}, 0},
		{"ended", day(-8), day(2), []string{"已結束", "<dt>結束</dt>", "已結束。"}, []string{"<dt>開始</dt>", "<dt>剩餘</dt>", `data-cell="today"`}, 10},
	}
	for _, tt := range tests {
		got := renderCampaign(t, tt.startsAt, tt.endsAt)
		for _, want := range tt.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s: page lacks %q", tt.name, want)
			}
		}
		for _, absent := range tt.absent {
			if strings.Contains(got, absent) {
				t.Errorf("%s: page holds %q", tt.name, absent)
			}
		}
		if !strings.Contains(got, `class="ui-period"`) {
			t.Errorf("%s: page draws no day grid", tt.name)
		}
		if n := strings.Count(got, `data-cell="past"`); tt.name == "ended" && n != tt.filled {
			t.Errorf("ended: %d filled cells, want %d", n, tt.filled)
		}
	}
}

// TestPageEmptyIllustration: the empty campaign shows its illustration as
// decoration. The other pages' rows of this test are catalog's
// TestListingAndSearchEmptyArt.
func TestPageEmptyIllustration(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	page := renderComponent(t, ctx, campaign.Page(frame.Page{}, campaign.View{Title: "x"}))
	want := `<img class="goen-empty__art" src="` + assets.URL(assets.EmptyCampaignImage) + `" alt="" width="128" height="96"`
	if !strings.Contains(page, want) {
		t.Errorf("campaign empty state omits %s", want)
	}
}

// TestMetaLinkPreview: the campaign page sends its own link-preview tags. From
// TestMetaLinkPreviewTags, which keeps the other pages'
// rows.
func TestMetaLinkPreview(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(frame.WithRequestPath(t.Context(), "/s/autumn"), i18n.ZhHant)
	p := campaign.Meta(ctx, "秋日選物", widget.Photo{URL: "/static/media/campaigns/autumn.webp?v=1", Alt: "秋日"})
	head := renderComponent(t, frame.WithSiteOrigin(ctx, "https://goen.test"), frame.Base(p))
	for _, want := range []string{
		`og:title" content="秋日選物 · goen"`,
		`og:description" content="秋日選物 — goen 優惠"`,
		`og:image" content="https://goen.test/static/media/campaigns/autumn.webp?v=1"`,
		`og:type" content="website"`,
		`og:site_name" content="goen"`,
		`og:url" content="https://goen.test/s/autumn"`,
		`name="twitter:card" content="summary_large_image"`,
	} {
		if !strings.Contains(head, want) {
			t.Errorf("campaign head omits %s", want)
		}
	}
}

// TestMetaEnglishWording: a campaign is called a campaign in English, in its
// not-found copy and in its meta description.
func TestMetaEnglishWording(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for key, want := range map[i18n.Key]string{
		i18n.KeyCampaignNotFound:     "Campaign not found",
		i18n.KeyCampaignNotFoundBody: "We could not find that campaign. See the deals running now.",
	} {
		if got := i18n.T(ctx, key); got != want {
			t.Errorf("i18n.T(%q) = %q, want %q", key, got, want)
		}
	}
	if got := campaign.Meta(ctx, "Autumn", widget.Photo{}).Description; got != "Autumn — deals at goen" {
		t.Errorf("Meta description = %q, want %q", got, "Autumn — deals at goen")
	}
}
