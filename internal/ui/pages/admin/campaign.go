package admin

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/chart"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

type CampaignRow struct {
	Slug         string
	Title        string
	Products     int64
	Active       bool
	Running      bool
	Sellable     bool
	StartsAtText string
	EndsAtText   string
}

func (c CampaignRow) State(ctx context.Context) string {
	return campaignState(ctx, c.Active, c.Running, c.Sellable)
}

func campaignState(ctx context.Context, active, running, sellable bool) string {
	switch {
	case !active:
		return i18n.T(ctx, i18n.KeyAdminCampaignOff)
	case !running:
		return i18n.T(ctx, i18n.KeyAdminCampaignOutside)
	case !sellable:
		return i18n.T(ctx, i18n.KeyAdminCampaignHidden)
	default:
		return i18n.T(ctx, i18n.KeyAdminCampaignRunning)
	}
}

func nextActive(active bool) string { return strconv.FormatBool(!active) }

func toggleLabel(ctx context.Context, active bool) string {
	if active {
		return i18n.T(ctx, i18n.KeyAdminToggleOff)
	}
	return i18n.T(ctx, i18n.KeyAdminToggleOn)
}

func (c CampaignRow) Live() bool { return c.Running && c.Sellable }

func (c CampaignRow) ProductsText() string { return strconv.FormatInt(c.Products, 10) }

func (c CampaignRow) Href() string { return "/admin/campaigns/" + c.Slug }

func (c CampaignRow) ToggleAction() string { return "/admin/campaigns/" + c.Slug + "/active" }

func (c CampaignRow) NextActive() string { return nextActive(c.Active) }

func (c CampaignRow) ToggleLabel(ctx context.Context) string { return toggleLabel(ctx, c.Active) }

type CampaignProduct struct {
	Slug string
	Name string
}

type CampaignsView struct {
	web.Bound

	Rows   []CampaignRow
	Notice components.Result
	Errors map[string]string
	Draft  CampaignDraft
}

type CampaignDraft struct {
	Slug    string
	Title   string
	TitleEn string
	Days    string
	Tone    string
}

func (d CampaignDraft) ToneOrStone() pages.Tone { return pages.ResolveTone(d.Tone) }

func (v CampaignsView) Empty() bool { return len(v.Rows) == 0 }

func (v CampaignsView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

func (v CampaignsView) Err(f string) string { return v.Errors[f] }

// CampaignDetail is what the edit page reads of a campaign. The dates are
// datetime-local field values, not display text.
type CampaignDetail struct {
	Title                      string
	Starts, Ends               time.Time
	StartsAtInput, EndsAtInput string
	Active, Running, Sellable  bool
}

type CampaignView struct {
	CampaignDetail

	Slug     string
	Term     string
	Matches  []CampaignProduct
	Products []CampaignProduct
	Notice   components.Result
	Image    Header
	Tone     string
	Errors   map[string]string

	// Results is nil when there is nothing to tell yet; ResultsUnavailable
	// that it could not be read.
	Results            *CampaignResults
	ResultsUnavailable bool
}

type Header struct {
	Key   string
	Alt   string
	AltEn string
	Width int32
}

func (i Header) Has() bool { return i.Key != "" }

func (i Header) URL() string { return assets.ProductImageURL(i.Key) }

func (i Header) Srcset() string { return assets.ProductImageSrcsetAt(i.Key, int(i.Width)) }

func (v *CampaignView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

func (v *CampaignView) Err(f string) string { return v.Errors[f] }

func (v *CampaignView) ToneAction() string { return "/admin/campaigns/" + v.Slug + "/tone" }

func (v *CampaignView) ImageAction() string { return "/admin/campaigns/" + v.Slug + "/image" }

func (v *CampaignView) ImageRemoveAction() string {
	return "/admin/campaigns/" + v.Slug + "/image/remove"
}

func (v *CampaignView) WindowAction() string { return "/admin/campaigns/" + v.Slug + "/window" }

func (v *CampaignView) ActiveAction() string { return "/admin/campaigns/" + v.Slug + "/active" }

func (v *CampaignView) StateText(ctx context.Context) string {
	return campaignState(ctx, v.Active, v.Running, v.Sellable)
}

func (v *CampaignView) ToggleLabel(ctx context.Context) string { return toggleLabel(ctx, v.Active) }

func (v *CampaignView) NextActive() string { return nextActive(v.Active) }

func (v *CampaignView) Searching() bool { return v.Term != "" }

func (v *CampaignView) Empty() bool { return len(v.Products) == 0 }

func (v *CampaignView) FeatureAction() string { return "/admin/campaigns/" + v.Slug + "/products" }

// The results are drawn once the campaign has run this many days and this many
// units have sold, before it and during it.
const (
	minResultDays  = 3
	minResultUnits = 10
)

// CampaignResults is what the products on a campaign's list sold on each shop
// day: Days days before the campaign, then the first Days days of it.
type CampaignResults struct {
	Units    chart.Series
	Days     int
	Campaign chart.Span
	Products int
	Cut      string // the time of day today is counted up to
}

func (r *CampaignResults) sold() (before, during int64) {
	for i, b := range r.Units.Buckets {
		if i < r.Days {
			before += b.Value
		} else {
			during += b.Value
		}
	}
	return before, during
}

// Reached reports whether the campaign has run long enough, and sold enough,
// to be told by day.
func (r *CampaignResults) Reached() bool {
	before, during := r.sold()
	return r.Days >= minResultDays && before+during >= minResultUnits
}

// Drawn reports whether the days are drawn. The columns want days with sales
// too, so ten units on two days are told in the sentence.
func (r *CampaignResults) Drawn() bool {
	return r.Reached() && r.Units.Density() >= chart.DensitySparse
}

// Facts says what was sold before and during: the caption of the columns and,
// where they are not drawn, the whole of the results.
func (r *CampaignResults) Facts(ctx context.Context) string {
	before, during := r.sold()
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCampFacts),
		i18n.Count(ctx, i18n.KeyChartColumnDays, int64(r.Days), r.Days),
		i18n.Count(ctx, i18n.KeyAdminRepUnitCount, during, during),
		i18n.Count(ctx, i18n.KeyAdminRepUnitCount, before, before))
}

// Hint says what the columns wait for; it is empty once they have it.
func (r *CampaignResults) Hint(ctx context.Context) string {
	if r.Reached() {
		return ""
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCampFactsHint),
		i18n.Count(ctx, i18n.KeyChartColumnDays, minResultDays, minResultDays),
		i18n.Count(ctx, i18n.KeyAdminRepUnitCount, minResultUnits, minResultUnits))
}

// Note says what the figures count, and up to when.
func (r *CampaignResults) Note(ctx context.Context) string {
	note := i18n.Count(ctx, i18n.KeyAdminCampResultsNote, int64(r.Products), r.Products)
	if r.Units.Partial {
		note += " " + fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepPaidNote), r.Cut)
	}
	return note
}

// Columns is the days drawn: the days before in grey, the campaign's bracketed.
func (r *CampaignResults) Columns(ctx context.Context) chart.ColumnsProps {
	units := r.Units
	units.Label = i18n.T(ctx, i18n.KeyAdminCampUnits)
	return chart.ColumnsProps{
		Series: units, Spans: []chart.Span{r.Campaign},
		Previous: r.Days, PreviousLabel: i18n.T(ctx, i18n.KeyAdminCampBefore),
		Caption: r.Facts(ctx), Note: r.Note(ctx),
		DayHeading:   i18n.T(ctx, i18n.KeyAdminRepDate),
		SpanHeading:  i18n.T(ctx, i18n.KeyAdminCampPeriod),
		PartialLabel: fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepUntil), r.Cut),
	}
}
