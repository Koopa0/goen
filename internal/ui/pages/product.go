package pages

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/productlabel"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
)

type ProductImage struct {
	URL    string
	Srcset string
	Alt    string
	Width  int32
	Height int32
	// ShowsOption is true for a photograph of one option value rather than the product.
	ShowsOption bool
}

func (i ProductImage) WidthText() string { return strconv.FormatInt(int64(i.Width), 10) }

func (i ProductImage) HeightText() string { return strconv.FormatInt(int64(i.Height), 10) }

func (i ProductImage) HasDimensions() bool { return i.Width > 0 && i.Height > 0 }

type ProductSpec struct {
	Label string
	Value string
}

type ProductOptionValue struct {
	// Value is the identity the URL carries; a href built from Label would resolve
	// differently for another reader.
	Value     string
	Label     string
	Selected  bool
	Available bool
	Href      string
	SwatchHex string
}

type ProductOption struct {
	Name   string
	Label  string
	Values []ProductOptionValue
}

// Fixed is true where the product comes in one value of this option only, so there is nothing to pick.
func (o ProductOption) Fixed() bool { return len(o.Values) == 1 }

// HasSwatches is all or nothing per option: one row of choices should look like one row, and a
// colour beside a word reads as two kinds of thing.
func (o ProductOption) HasSwatches() bool {
	if len(o.Values) == 0 {
		return false
	}
	for _, v := range o.Values {
		if v.SwatchHex == "" {
			return false
		}
	}
	return true
}

// SelectedLabel exists because a swatch shows a colour and no words, so the name has to be somewhere.
func (o ProductOption) SelectedLabel() string {
	for _, v := range o.Values {
		if v.Selected {
			return v.Label
		}
	}
	return ""
}

type RatingBar struct {
	Stars   int
	Count   int64
	Percent int
}

func (b RatingBar) StarsText() string { return strconv.Itoa(b.Stars) }

func (b RatingBar) CountText() string { return strconv.FormatInt(b.Count, 10) }

// WidthClass is a class app.css carries, never an inline style: the CSP has no
// 'unsafe-inline' under style-src, so a refused width draws no bar. The nearest
// ten-step is taken, not the one below, so the error is never one-sided.
func (b RatingBar) WidthClass() string {
	return "goen-pdp__barfill--" + strconv.Itoa((b.Percent+5)/10*10)
}

type ReviewStanding int

const (
	ReviewSignedOut ReviewStanding = iota
	ReviewNotDelivered
	// ReviewAlreadyWritten counts a hidden review too: it still holds the unique index.
	ReviewAlreadyWritten
	ReviewOpen
)

type ProductReview struct {
	Rating   int
	Title    string
	Body     string
	Author   string
	Verified bool
	Date     shoptime.Date
}

func (r ProductReview) RatingText() string { return strconv.Itoa(r.Rating) }

// DisplayAuthor gives a stand-in that must not borrow the verified-buyer wording: the badge beside it
// carries a claim product_reviews_verified_is_real guards, and the byline is not covered.
func (r ProductReview) DisplayAuthor(ctx context.Context) string {
	if r.Author == "" {
		return i18n.T(ctx, i18n.KeyAnonymousReviewer)
	}
	return maskedName(i18n.FromContext(ctx), r.Author)
}

// maskedName exists because reviews are public: a full name beside a purchase is
// more than the shopper agreed to show.
func maskedName(l i18n.Locale, name string) string {
	first, size := utf8.DecodeRuneInString(strings.TrimSpace(name))
	if size == 0 || first == utf8.RuneError && size == 1 {
		return "○○"
	}
	if l == i18n.En {
		return strings.ToUpper(string(first)) + "."
	}
	return string(first) + "○○"
}

type ProductView struct {
	LabelFacts  *productlabel.Facts
	Saved       bool
	Slug        string
	Name        string
	Summary     string
	Description string
	// DescriptionUntranslated is true when the page is not Chinese and the shop wrote no
	// description in its language, so Description is the Chinese one.
	DescriptionUntranslated bool
	WarrantyNote            string
	// WarrantyMonths is 0 when the shop has stated no term, and registration is refused.
	WarrantyMonths int32
	Rules          ShopRules
	Brand          string
	CategorySlug   string
	CategoryName   string
	Crumbs         []Crumb

	Images  []ProductImage
	Options []ProductOption
	Specs   []ProductSpec

	// Campaign is the running campaign featuring the product; CompareCents is struck only while it runs.
	Campaign ProductCampaign

	SelectionOK bool
	Exact       bool
	// PriceVaries reports that dearer variants exist than the one priced here.
	PriceVaries         bool
	AnySellable         bool
	VariantID           string
	SKU                 string
	PriceCents          int64
	CompareCents        int64
	Sellable            bool
	ExpectedArrival     time.Time
	ExpectedArrivalText string
	Available           int32

	Rating         float64
	RatingCount    int64
	RatingBars     []RatingBar
	Reviews        []ProductReview
	SignedIn       bool
	ReviewStanding ReviewStanding
	ReviewPosted   bool
	ReviewErrors   map[string]string
	ReviewDraft    ReviewDraft
	NotifyOutcome  NotifyOutcome
	NotifyEmail    string
	AccountEmail   string
	Comparing      []string
	Comparable     bool
	Questions      []Question
	AskOutcome     string
	// AskDraft replays a refused question so a 422 does not empty the textarea.
	AskDraft     string
	AddedOutcome AddOutcome
	AlsoBought   []ProductTile

	Related []ProductTile

	// Tone is the department's; the gallery's mat and the band wear it.
	Tone Tone
}

// StarHalves keeps an average of 4.5 at four stars and half of the fifth, not five.
func (v *ProductView) StarHalves() int { return max(0, min(int(v.Rating*2+0.5), 10)) }

func (v *ProductView) WishlistLabel(ctx context.Context) string {
	if v.Saved {
		return i18n.T(ctx, i18n.KeyWishlistRemove)
	}
	return i18n.T(ctx, i18n.KeyWishlistAdd)
}

func (v *ProductView) SavedText() string {
	if v.Saved {
		return "true"
	}
	return "false"
}

// CategoryTrail includes the direct category so the visible breadcrumb and
// structured data describe the same catalogue path.
func (v *ProductView) CategoryTrail() []Crumb {
	return append(slices.Clone(v.Crumbs), Crumb{Slug: v.CategorySlug, Name: v.CategoryName})
}

// Trail leaves the last step without a link: a keyboard would have to pass through a link to
// where you already are for nothing.
func (v *ProductView) Trail(ctx context.Context) []components.Crumb {
	trail := []components.Crumb{{Label: i18n.T(ctx, i18n.KeyHome), Href: "/"}}
	for _, c := range v.CategoryTrail() {
		trail = append(trail, components.Crumb{Label: c.Name, Href: "/c/" + c.Slug})
	}
	return append(trail, components.Crumb{Label: v.Name})
}

func ProductMeta(v *ProductView) layouts.Page {
	desc := v.Summary
	if desc == "" {
		desc = v.Name
	}
	page := layouts.Page{
		Title: v.Name, Description: desc,
		Nav: v.RootSlug(),
	}
	// The first photograph is the one the page opens with, at the rendition the gallery serves.
	if v.HasImages() {
		img := v.Images[0]
		alt := img.Alt
		if alt == "" {
			alt = v.Name
		}
		page.Share = layouts.ShareImage{Path: img.URL, Width: img.Width, Height: img.Height, Alt: alt}
	}
	return page
}

func (v *ProductView) HasWarranty() bool { return v.WarrantyMonths > 0 }

func (v *ProductView) WarrantyText(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyPDPWarranty),
		strconv.FormatInt(int64(v.WarrantyMonths), 10))
}

func (v *ProductView) RootSlug() string {
	if len(v.Crumbs) > 0 {
		return v.Crumbs[0].Slug
	}
	return v.CategorySlug
}

func (v *ProductView) Price() string { return twd(v.PriceCents) }

// PriceFrom is true when the visitor has chosen no variant yet, or the one they chose is the
// cheapest and dearer ones exist.
func (v *ProductView) PriceFrom() bool { return v.PriceVaries && !v.Exact }

func (v *ProductView) Compare() string { return twd(v.CompareCents) }

func (v *ProductView) OnSale() bool {
	return v.Campaign.Running() && v.Sellable && v.CompareCents > v.PriceCents
}

func (v *ProductView) CanBuy() bool { return v.SelectionOK && v.Exact && v.Sellable }

// InStock is only ever shown once a variant is settled: on the bare product URL no
// one variant's stock can be described.
func (v *ProductView) InStock() bool {
	return v.CanBuy() && !v.LowStock() && !v.SoldOut() && !v.AllSoldOut()
}

func (v *ProductView) NeedsChoice() bool { return v.SelectionOK && !v.Exact }

func (v *ProductView) AllSoldOut() bool { return v.SelectionOK && !v.AnySellable }

func (v *ProductView) SoldOut() bool { return v.SelectionOK && v.Exact && !v.Sellable }

func (v *ProductView) LowStock() bool { return v.Sellable && v.Available > 0 && v.Available <= 5 }

func (v *ProductView) AvailableText() string { return strconv.FormatInt(int64(v.Available), 10) }

func (v *ProductView) MaxQuantity() string {
	n := max(min(v.Available, 99), 1)
	return strconv.FormatInt(int64(n), 10)
}

func (v *ProductView) HasImages() bool { return len(v.Images) > 0 }

// BandPhoto is the second photograph: the first is the gallery's.
func (v *ProductView) BandPhoto() (ProductImage, bool) {
	if len(v.Images) < 2 {
		return ProductImage{}, false
	}
	return v.Images[1], true
}

// GalleryFollowsChoice is true only when a photograph shows one value: only then can choosing another reorder the gallery.
func (v *ProductView) GalleryFollowsChoice() bool {
	return slices.ContainsFunc(v.Images, func(i ProductImage) bool { return i.ShowsOption })
}

func (v *ProductView) ChoiceSwap() string {
	if v.GalleryFollowsChoice() {
		return "#gallery,#buybar"
	}
	return "#buybar"
}

func (v *ProductView) BuyBarFollows() string {
	if v.NotifyOffered() {
		return "restock"
	}
	return "add-to-cart"
}

// BuyBarOutcome repeats the notice under the add button, out of sight whenever the bar is up.
func (v *ProductView) BuyBarOutcome(ctx context.Context) string {
	switch {
	case v.JustAdded():
		return i18n.T(ctx, i18n.KeyAddedToCart)
	case v.AddAdjusted():
		return i18n.T(ctx, i18n.KeyAddAdjusted)
	case v.CartFull():
		return i18n.T(ctx, i18n.KeyCartLineLimit)
	case v.AddRefused():
		return i18n.T(ctx, i18n.KeyAddRefused)
	}
	return ""
}

func (v *ProductView) HasSpecs() bool { return len(v.Specs) > 0 }

// HasRelated needs two: one card alone reads as a mistake.
func (v *ProductView) HasRelated() bool { return len(v.Related) >= 2 }

func (v *ProductView) HasRating() bool { return v.RatingCount > 0 }

func (v *ProductView) RatingText() string { return strconv.FormatFloat(v.Rating, 'f', 1, 64) }

func (v *ProductView) ReviewCountText() string { return strconv.FormatInt(v.RatingCount, 10) }

func (v *ProductView) RatingLabel(ctx context.Context) string {
	return i18n.Count(ctx, i18n.KeyRatingSummary, v.RatingCount, v.RatingText(), v.ReviewCountText())
}

type ReviewDraft struct {
	Rating int
	Title  string
	Body   string
}

func (d ReviewDraft) IsRating(n int) bool { return d.Rating == n }

// ReviewBodyMaxRunes is the same number as product.MaxReviewBodyRunes.
const ReviewBodyMaxRunes = 2000

// ReviewBodyMinRunes is the same number as product.MinReviewBodyRunes.
const ReviewBodyMinRunes = 5

// ReviewAction ends in a fragment that rides into the 422 page's address, so a refused review opens at the form.
func (v *ProductView) ReviewAction() string { return "/p/" + v.Slug + "/reviews#write-review" }

func (v *ProductView) ReviewBodyHint(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyReviewBodyHint), ReviewBodyMinRunes, ReviewBodyMaxRunes)
}

// aria-describedby only while the field is valid; the Textarea sets it itself when refused.
func (v *ProductView) reviewBodyAttrs() templ.Attributes {
	attrs := templ.Attributes{
		"rows": "5", "required": true,
		"minlength": strconv.Itoa(ReviewBodyMinRunes), "maxlength": strconv.Itoa(ReviewBodyMaxRunes),
	}
	if !v.HasReviewErr("body") {
		attrs["aria-describedby"] = "review-body-hint"
	}
	return attrs
}

func (v *ProductView) HasReviewErr(f string) bool { _, ok := v.ReviewErrors[f]; return ok }

func (v *ProductView) ReviewErr(f string) string { return v.ReviewErrors[f] }

// NotifyOutcome is what a restock request came to, carried in ?notify=.
type NotifyOutcome string

const (
	NotifyRecorded           NotifyOutcome = "1"
	NotifyRecordedForAccount NotifyOutcome = "account"
	NotifyBadAddress         NotifyOutcome = "bad"
	NotifyVariantUnavailable NotifyOutcome = "unavailable"
	NotifyNoOption           NotifyOutcome = "option"
)

// NotifyOffered is true where a notice can be asked for: once a combination is settled and sold out, and, while
// every option is sold out, before any is picked, where the request is refused until one is. A request refused for
// want of a pick keeps its form whatever the stock is now, or the 422 would drop the address it was sent with.
func (v *ProductView) NotifyOffered() bool {
	return v.SoldOut() || v.NeedsChoice() && (v.AllSoldOut() || v.NotifyNeedsOption())
}

// NotifyVariant is empty until a combination is picked: the default variant is only the cheapest, not the one wanted.
func (v *ProductView) NotifyVariant() string {
	if v.NeedsChoice() {
		return ""
	}
	return v.VariantID
}

func (v *ProductView) NotifyNeedsOption() bool { return v.NotifyOutcome == NotifyNoOption }

// OptionInvalid marks the choices a refused request left unpicked.
func (v *ProductView) OptionInvalid(o ProductOption) bool {
	return v.NotifyNeedsOption() && v.NotifyOffered() && o.SelectedLabel() == ""
}

func (v *ProductView) NotifyTaken() bool {
	return v.NotifyOutcome == NotifyRecorded || v.NotifyOutcome == NotifyRecordedForAccount
}

func (v *ProductView) NotifyConfirmation(ctx context.Context) string {
	if v.NotifyOutcome == NotifyRecordedForAccount && v.AccountEmail != "" {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyRestockDoneTo), v.AccountEmail)
	}
	return i18n.T(ctx, i18n.KeyRestockDone)
}

func (v *ProductView) NotifyEmailValue() string {
	if v.NotifyEmail != "" {
		return v.NotifyEmail
	}
	return v.AccountEmail
}

func (v *ProductView) NotifyRefused() bool { return v.NotifyOutcome == NotifyBadAddress }

func (v *ProductView) NotifyUnavailable() bool { return v.NotifyOutcome == NotifyVariantUnavailable }

// NotifyAction carries the chosen options so the redirect that follows lands on the same selection.
func (v *ProductView) NotifyAction() string {
	q := url.Values{}
	for i := range v.Options {
		for _, val := range v.Options[i].Values {
			if val.Selected {
				q.Set(v.Options[i].Name, val.Value)
			}
		}
	}
	if len(q) == 0 {
		return "/p/" + v.Slug + "/notify"
	}
	return "/p/" + v.Slug + "/notify?" + q.Encode()
}

func (v *ProductView) HasRecommendations() bool { return len(v.AlsoBought) > 0 }

func (v *ProductView) HasQuestions() bool { return len(v.Questions) > 0 }

func (v *ProductView) AskTaken() bool { return v.AskOutcome == "1" }

func (v *ProductView) AskRefused() bool { return v.AskOutcome == "bad" }

// AddOutcome is carried in ?added= back to the page the form was on.
type AddOutcome string

const (
	AddOutcomeAdded       AddOutcome = "added"
	AddOutcomeAdjusted    AddOutcome = "adjusted"
	AddOutcomeUnavailable AddOutcome = "unavailable"
	AddOutcomeUnknown     AddOutcome = "unknown"
	// AddOutcomeFull is another distinct product that would make the order too large
	// for one provider invoice.
	AddOutcomeFull AddOutcome = "full"
)

func (v *ProductView) JustAdded() bool { return v.AddedOutcome == AddOutcomeAdded }

func (v *ProductView) AddAdjusted() bool { return v.AddedOutcome == AddOutcomeAdjusted }

func (v *ProductView) AddRefused() bool {
	return v.AddedOutcome == AddOutcomeUnavailable || v.AddedOutcome == AddOutcomeUnknown
}

func (v *ProductView) CartFull() bool { return v.AddedOutcome == AddOutcomeFull }

func (v *ProductView) AskAction() string { return "/p/" + v.Slug + "/questions" }

// AskSignInHref writes the hash as %23 in the query so it is part of next, not a fragment on /signin.
func (v *ProductView) AskSignInHref() string {
	return "/signin?next=/p/" + v.Slug + "%23questions"
}

func (v *ProductView) CompareHref() string {
	var b strings.Builder
	b.WriteString("/compare")
	sep := "?"
	for _, slug := range v.Comparing {
		if slug == v.Slug {
			continue
		}
		b.WriteString(sep)
		b.WriteString("p=")
		b.WriteString(slug)
		sep = "&"
	}
	b.WriteString(sep)
	b.WriteString("p=")
	b.WriteString(v.Slug)
	return b.String()
}

func (v *ProductView) AlreadyComparing() bool {
	return slices.Contains(v.Comparing, v.Slug)
}

func (v *ProductView) CurrentCompareHref() string {
	return "/compare?" + url.Values{"p": v.Comparing}.Encode()
}

func (v *ProductView) ComparingFull() bool { return len(v.Comparing) >= MaxCompare }

// maxHighlights is how many specification values stand under the name.
const maxHighlights = 3

// Highlights are the first specification values, written one after another under the name.
func (v *ProductView) Highlights() []string {
	var out []string
	for _, s := range v.Specs {
		if s.Value != "" && len(out) < maxHighlights {
			out = append(out, s.Value)
		}
	}
	return out
}

// BuyFacts are the terms the buyer is told beside the button: the warranty the product carries, the
// shop's right to cancel, the stock hold only while there is stock to hold, and free delivery.
func (v *ProductView) BuyFacts(ctx context.Context) []components.Stat {
	stats := make([]components.Stat, 0, 4)
	if v.HasWarranty() {
		stats = append(stats, components.Stat{
			Label: i18n.T(ctx, i18n.KeySectionWarranty),
			Value: components.StatCount(int64(v.WarrantyMonths), countUnit(ctx, i18n.KeyUnitMonths, int64(v.WarrantyMonths))),
		})
	}
	stats = append(stats, v.Rules.rescissionStat(ctx))
	if v.AnySellable {
		stats = append(stats, v.Rules.holdStat(ctx))
	}
	return append(stats, v.Rules.freeDeliveryStat(ctx))
}

// RestockNote is the sentence over the notify form, asking for a pick only while there is none.
func (v *ProductView) RestockNote(ctx context.Context) string {
	if v.NeedsChoice() {
		return i18n.T(ctx, i18n.KeyRestockPick)
	}
	return i18n.T(ctx, i18n.KeyRestockNote)
}

// ValueSoldOut is the words an option value that leads to nothing buyable carries: sold out for the
// whole product, otherwise only for this combination of choices.
func (v *ProductView) ValueSoldOut(ctx context.Context) string {
	if v.AllSoldOut() {
		return i18n.T(ctx, i18n.KeySoldOut)
	}
	return i18n.T(ctx, i18n.KeyVariantUnavailable)
}

func (v *ProductView) ArrivalDay() string { return shoptime.Day(v.ExpectedArrival) }

func (v *ProductView) ArrivalText() string {
	if !v.SoldOut() || v.ExpectedArrival.IsZero() {
		return ""
	}
	return v.ExpectedArrivalText
}

func (v *ProductView) LabelRows(ctx context.Context) []productlabel.Fact {
	return v.LabelFacts.Rows(ctx)
}

// ProductCampaign is the running campaign that features a product, or the zero value.
type ProductCampaign struct {
	Slug  string
	Title string
	End   CampaignEnd
}

func NewProductCampaign(slug, title string, endsAt, now time.Time) ProductCampaign {
	return ProductCampaign{Slug: slug, Title: title, End: NewCampaignEnd(endsAt, now)}
}

func (c ProductCampaign) Running() bool { return c.Slug != "" }

func (c ProductCampaign) Href() string { return "/s/" + c.Slug }

// Source splits the sentence naming the campaign around its title, which the page links, and what is left of
// it, which the page sets apart.
func (c ProductCampaign) Source(ctx context.Context) (before, middle, after string) {
	const title, left = "\x00", "\x01"
	key := i18n.KeyCampaignPriceDaysLeft
	if c.End.EndsByTomorrow() {
		key = i18n.KeyCampaignPriceToday
	}
	before, rest, _ := strings.Cut(fmt.Sprintf(i18n.T(ctx, key), title, left, c.End.Day(ctx)), title)
	middle, after, _ = strings.Cut(rest, left)
	return before, middle, after
}
