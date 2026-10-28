package campaign_test

import (
	"strings"
	"testing"
	"time"

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

// TestPageFactsAndDays: a running campaign page says its end in one phrase and
// draws its days; one not started or ended states its facts.
func TestPageFactsAndDays(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		startsAt     time.Time
		endsAt       time.Time
		want, absent []string
		filled       int
	}{
		{"running", day(1), day(11), []string{`<p class="goen-pagehead__ends">`, "至 10\u00a0月 10\u00a0日", "剩\u00a08\u00a0天"}, []string{"<dt>", "開始", "已結束", "限時"}, 1},
		{"one day left", day(1), day(4), []string{"明天結束"}, []string{"<dt>", "已結束"}, 1},
		{"last day", day(1), day(3), []string{"今天結束"}, []string{"<dt>", "已結束"}, 1},
		{"not started", day(5), day(9), []string{"<dt>開始</dt>", "<dt>結束</dt>", "<dt>商品</dt>", "10\u00a0月 5\u00a0日", "還沒開始"}, []string{"已結束", "<dt>剩餘</dt>", "goen-pagehead__ends", `data-cell`}, 0},
		{"ended", day(-8), day(2), []string{"已結束", "<dt>結束</dt>", "已結束。"}, []string{"<dt>開始</dt>", "<dt>剩餘</dt>", "今天結束", "goen-pagehead__ends", `data-cell="today"`}, 10},
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

// TestPageEmptyHasNoPicture: the empty campaign says so in words alone. The
// other pages' rows of this test are catalog's TestListingEmptyStatesHaveNoPicture.
func TestPageEmptyHasNoPicture(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	page := renderComponent(t, ctx, campaign.Page(frame.Page{}, campaign.View{Title: "x"}))
	_, rest, ok := strings.Cut(page, `class="ui-empty">`)
	if !ok {
		t.Fatal("empty campaign renders no ui-empty box")
	}
	box, _, _ := strings.Cut(rest, `</div>`)
	for _, picture := range []string{`<img`, `goen-medallion`} {
		if strings.Contains(box, picture) {
			t.Errorf("empty campaign draws %q", picture)
		}
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

// TestPageHeadSaysPeriodInOnePhrase holds that a running campaign's head is its
// name, the phrase of its end and the track of its days, in that order and in
// both languages, whatever the length of the name; the product count is the
// grid's to say.
func TestPageHeadSaysPeriodInOnePhrase(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		title  string
		phrase string
	}{
		{i18n.ZhHant, "秋日選物", "至 10\u00a0月 10\u00a0日"},
		{i18n.ZhHant, "年終感謝祭全館滿額再折活動最後加碼", "至 10\u00a0月 10\u00a0日"},
		{i18n.En, "Autumn desk week", "until Oct\u00a010,"},
		{i18n.En, "The year-end thank-you sale with the largest discounts of the season", "until Oct\u00a010,"},
	} {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		got := renderComponent(t, ctx, campaign.Page(frame.Page{Title: "c"}, campaign.View{
			Slug: "c", Title: tt.title, Products: []tile.Tile{{Slug: "p", Name: "p", PriceCents: 80000, InStock: true}},
			Schedule: campaign.NewSchedule(ctx, tt.title, 1, day(1), day(11), scheduleNow),
		}))
		title := strings.Index(got, `<h1 class="goen-pagehead__title">`+tt.title)
		when := strings.Index(got, `<p class="goen-pagehead__ends">`)
		track := strings.Index(got, `class="ui-period"`)
		if title < 0 || when < title || track < when {
			t.Errorf("%s %q: head reads title %d, phrase %d, track %d; want them in that order", tt.locale, tt.title, title, when, track)
			continue
		}
		if phrase := got[when:track]; !strings.Contains(tag.ReplaceAllString(phrase, ""), tt.phrase) || !strings.Contains(phrase, `<time datetime="2026-10-10">`) {
			t.Errorf("%s %q: the phrase lacks %q and a time element:\n%s", tt.locale, tt.title, tt.phrase, phrase)
		}
		if strings.Contains(got, "ui-statline") {
			t.Errorf("%s %q: the head still states facts", tt.locale, tt.title)
		}
	}
}
