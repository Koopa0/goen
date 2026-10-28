package campaigns

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/picture"
	"github.com/koopa0/goen/internal/ui/chart"
	"github.com/koopa0/goen/internal/ui/tone"
	"github.com/koopa0/goen/internal/ui/widget"
)

type Row struct {
	Slug         string
	Title        string
	Products     int64
	Active       bool
	Running      bool
	Sellable     bool
	StartsAtText string
	EndsAtText   string
}

func (c Row) State(ctx context.Context) string {
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

func (c Row) Live() bool { return c.Running && c.Sellable }

func (c Row) ProductsText() string { return strconv.FormatInt(c.Products, 10) }

func (c Row) Href() string { return "/admin/campaigns/" + c.Slug }

func (c Row) ToggleAction() string { return "/admin/campaigns/" + c.Slug + "/active" }

func (c Row) NextActive() string { return nextActive(c.Active) }

func (c Row) ToggleLabel(ctx context.Context) string {
	return widget.ToggleLabel(ctx, c.Active)
}

type Product struct {
	Slug          string
	Name          string
	Image         picture.Image
	PriceCents    int64
	CompareCents  int64
	SellableUnits int64
	HasVariant    bool
	// The selected regular-price variant can coexist with an unavailable discounted one.
	HasDiscountedVariant bool
	Draft                bool
}

func (p Product) imageURL() string { return p.Image.URL() }

func (p Product) hasMarkdown() bool {
	return p.HasVariant && p.CompareCents > p.PriceCents
}

func (p Product) priceText(ctx context.Context) string {
	if p.hasMarkdown() {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCampProductPrice), money.TWD(p.CompareCents),
			money.TWD(p.PriceCents), (p.CompareCents-p.PriceCents)*100/p.CompareCents)
	}
	return money.TWD(p.PriceCents)
}

func (p Product) stockText(ctx context.Context) string {
	if p.SellableUnits <= 0 {
		return i18n.T(ctx, i18n.KeySoldOut)
	}
	return i18n.Count(ctx, i18n.KeyAdminCampProductStock, p.SellableUnits, p.SellableUnits)
}

type View struct {
	keyset.Bound

	Rows   []Row
	Notice widget.Result
	Errors map[string]string
	Draft  Draft
}

type Draft struct {
	Slug    string
	Title   string
	TitleEn string
	Days    string
	Tone    string
}

func (d Draft) ToneOrStone() tone.Tone { return tone.Resolve(d.Tone) }

func (v View) Empty() bool { return len(v.Rows) == 0 }

func (v View) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

func (v View) Err(f string) string { return v.Errors[f] }

// Detail is what the edit page reads of a campaign. The dates are
// datetime-local field values, not display text.
type Detail struct {
	Title                      string
	Label                      string // the title in the reader's language, as the reports name the campaign
	Starts, Ends               time.Time
	StartsAtInput, EndsAtInput string
	Active, Running, Sellable  bool
}

type CampaignView struct {
	Detail

	ImageAltDraft   string
	ImageAltEnDraft string

	Slug     string
	Term     string
	Matches  []Product
	Products []Product
	Notice   widget.Result
	Image    widget.HeaderImage
	Tone     string
	Errors   map[string]string

	// Results is nil when there is nothing to tell yet; ResultsUnavailable
	// that it could not be read.
	Results            *Results
	ResultsUnavailable bool
}

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

func (v *CampaignView) ToggleLabel(ctx context.Context) string {
	return widget.ToggleLabel(ctx, v.Active)
}

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

// Results is what the products on a campaign's list sold on each shop
// day: Days days before the campaign, then the first Days days of it.
type Results struct {
	Units    chart.Series
	Days     int
	Campaign chart.Span
	Products int
	Cut      string // the time of day today is counted up to
}

func (r *Results) sold() (before, during int64) {
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
func (r *Results) Reached() bool {
	before, during := r.sold()
	return r.Days >= minResultDays && before+during >= minResultUnits
}

// Drawn reports whether the days are drawn. The columns want days with sales
// too, so ten units on two days are told in the sentence.
func (r *Results) Drawn() bool {
	return r.Reached() && r.Units.Density() >= chart.DensitySparse
}

// Facts says what was sold before and during: the caption of the columns and,
// where they are not drawn, the whole of the results.
func (r *Results) Facts(ctx context.Context) string {
	before, during := r.sold()
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCampFacts),
		i18n.Count(ctx, i18n.KeyChartColumnDays, int64(r.Days), r.Days),
		i18n.Count(ctx, i18n.KeyAdminRepUnitCount, during, during),
		i18n.Count(ctx, i18n.KeyAdminRepUnitCount, before, before))
}

// Hint says what the columns wait for; it is empty once they have it.
func (r *Results) Hint(ctx context.Context) string {
	if r.Reached() {
		return ""
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCampFactsHint),
		i18n.Count(ctx, i18n.KeyChartColumnDays, minResultDays, minResultDays),
		i18n.Count(ctx, i18n.KeyAdminRepUnitCount, minResultUnits, minResultUnits))
}

// Note says what the figures count, and up to when while today is going.
func (r *Results) Note(ctx context.Context) string {
	note := i18n.Count(ctx, i18n.KeyAdminCampResultsNote, int64(r.Products), r.Products)
	if r.Units.Partial {
		return note + " " + fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepPaidNote), r.Cut)
	}
	return note + " " + i18n.T(ctx, i18n.KeyAdminCampBasis)
}

// Columns is the days drawn: the days before in grey, the campaign's bracketed.
func (r *Results) Columns(ctx context.Context) chart.ColumnsProps {
	units := r.Units
	units.Label = i18n.T(ctx, i18n.KeyAdminCampUnits)
	return chart.ColumnsProps{
		Series: units, Spans: []chart.Span{r.Campaign},
		Previous: r.Days, PreviousLabel: i18n.T(ctx, i18n.KeyAdminCampBefore),
		Caption: r.Facts(ctx), Note: r.Note(ctx),
		DayHeading:   i18n.T(ctx, i18n.KeyAdminRepDate),
		SpanHeading:  i18n.T(ctx, i18n.KeyChartPeriod),
		PartialLabel: fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepUntil), r.Cut),
	}
}
