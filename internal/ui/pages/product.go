package pages

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// ProductImage is one entry in the gallery.
type ProductImage struct {
	URL    string
	Srcset string
	Alt    string
	Width  int32
	Height int32
}

// WidthText is the intrinsic width as an attribute value.
func (i ProductImage) WidthText() string { return strconv.FormatInt(int64(i.Width), 10) }

// HeightText is the intrinsic height as an attribute value.
func (i ProductImage) HeightText() string { return strconv.FormatInt(int64(i.Height), 10) }

// HasDimensions reports whether both are known.
func (i ProductImage) HasDimensions() bool { return i.Width > 0 && i.Height > 0 }

// ProductSpec is one row of the spec table.
type ProductSpec struct {
	Label string
	Value string
}

// ProductOptionValue is one choice in a picker.
type ProductOptionValue struct {
	// Value is the identity the URL carries; Label is what the visitor reads. A
	// href built from the label would resolve differently for another reader.
	Value     string
	Label     string
	Selected  bool
	Available bool
	Href      string
}

// ProductOption is one picker.
type ProductOption struct {
	Name   string
	Label  string
	Values []ProductOptionValue
}

// RatingBar is one row of the rating histogram.
type RatingBar struct {
	Stars   int
	Count   int64
	Percent int
}

// StarsText is the bar's star count as text.
func (b RatingBar) StarsText() string { return strconv.Itoa(b.Stars) }

// CountText is how many reviews gave this many stars.
func (b RatingBar) CountText() string { return strconv.FormatInt(b.Count, 10) }

// PercentStyle is the inline width for the bar's fill.
func (b RatingBar) PercentStyle() string { return "width:" + strconv.Itoa(b.Percent) + "%" }

// ProductReview is one published review.
type ProductReview struct {
	Rating   int
	Title    string
	Body     string
	Author   string
	Verified bool
	Date     string
}

// RatingText is the review's own score.
func (r ProductReview) RatingText() string { return strconv.Itoa(r.Rating) }

// DisplayAuthor is the reviewer's name, or a stand-in when they gave none and
// when erase_user has taken it away.
//
// NOT the verified-buyer heading, which this borrowed: a name is optional at
// registration and erasure blanks it, so 23 of 24 reviews were bylined 已購買的
// 顧客 — every one of them unverified. product_reviews_verified_is_real exists
// to stop a false verified claim, and the byline made it in words beside the
// badge that carries the real one.
func (r ProductReview) DisplayAuthor(ctx context.Context) string {
	if r.Author == "" {
		return i18n.T(ctx, i18n.KeyAnonymousReviewer)
	}
	return r.Author
}

// ProductView is everything the detail page renders.
type ProductView struct {
	Saved        bool
	Slug         string
	Name         string
	Summary      string
	Description  string
	WarrantyNote string
	// WarrantyMonths is 0 when the shop has stated no term, and registration is refused.
	WarrantyMonths    int32
	FreeDeliveryCents int64
	Brand             string
	CategorySlug      string
	CategoryName      string
	Crumbs            []Crumb

	Images  []ProductImage
	Options []ProductOption
	Specs   []ProductSpec

	SelectionOK  bool
	Exact        bool
	VariantID    string
	SKU          string
	PriceCents   int64
	CompareCents int64
	Sellable     bool
	Available    int32

	Rating        float64
	RatingCount   int64
	RatingBars    []RatingBar
	Reviews       []ProductReview
	SignedIn      bool
	CanReview     bool
	WouldVerify   bool
	ReviewErrors  map[string]string
	ReviewDraft   ReviewDraft
	NotifyOutcome string
	Comparing     []string
	Questions     []Question
	AskOutcome    string
	AlsoBought    []ProductTile

	Related []ProductTile
}

// ProductMeta is the chrome view model for a product page.
func ProductMeta(v *ProductView) layouts.Page {
	desc := v.Summary
	if desc == "" {
		desc = v.Name
	}
	return layouts.Page{
		Title: v.Name + " — " + v.Brand, Description: desc,
		Nav: v.RootSlug(),
	}
}

// HasWarranty reports whether the shop has stated a term for this product.
func (v *ProductView) HasWarranty() bool { return v.WarrantyMonths > 0 }

// WarrantyText is the cover in the visitor's language.
func (v *ProductView) WarrantyText(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyPDPWarranty),
		strconv.FormatInt(int64(v.WarrantyMonths), 10))
}

// RootSlug is the top-level category this product sits under.
func (v *ProductView) RootSlug() string {
	if len(v.Crumbs) > 0 {
		return v.Crumbs[0].Slug
	}
	return v.CategorySlug
}

// Price is the resolved variant's price.
func (v *ProductView) Price() string { return twd(v.PriceCents) }

// Compare is its struck-through original, shown only when OnSale.
func (v *ProductView) Compare() string { return twd(v.CompareCents) }

// OnSale reports whether to show a struck-through price.
func (v *ProductView) OnSale() bool { return v.Sellable && v.CompareCents > v.PriceCents }

// CanBuy reports whether the page can offer an add-to-cart button.
func (v *ProductView) CanBuy() bool { return v.SelectionOK && v.Exact && v.Sellable }

// NeedsChoice reports whether the visitor still has an option to pick.
func (v *ProductView) NeedsChoice() bool { return v.SelectionOK && !v.Exact }

// SoldOut reports whether the pinned combination exists but cannot be bought.
func (v *ProductView) SoldOut() bool { return v.SelectionOK && v.Exact && !v.Sellable }

// LowStock reports whether the remaining quantity is worth naming.
func (v *ProductView) LowStock() bool { return v.Sellable && v.Available > 0 && v.Available <= 5 }

// AvailableText is the buyable quantity as text.
func (v *ProductView) AvailableText() string { return strconv.FormatInt(int64(v.Available), 10) }

// MaxQuantity bounds the quantity input to what can actually be sold.
func (v *ProductView) MaxQuantity() string {
	n := min(v.Available, 99)
	if n < 1 {
		n = 1
	}
	return strconv.FormatInt(int64(n), 10)
}

// HasImages reports whether the gallery has anything to show.
func (v *ProductView) HasImages() bool { return len(v.Images) > 0 }

// HasSpecs reports whether the spec table has rows.
func (v *ProductView) HasSpecs() bool { return len(v.Specs) > 0 }

// HasReviews reports whether any review is listed.
func (v *ProductView) HasReviews() bool { return len(v.Reviews) > 0 }

// HasRelated reports whether the same-category row has products.
func (v *ProductView) HasRelated() bool { return len(v.Related) > 0 }

// HasRating reports whether anyone has rated this product.
func (v *ProductView) HasRating() bool { return v.RatingCount > 0 }

// RatingText is the average rating to one decimal.
func (v *ProductView) RatingText() string { return strconv.FormatFloat(v.Rating, 'f', 1, 64) }

// ReviewCountText is how many people have rated it.
func (v *ProductView) ReviewCountText() string { return strconv.FormatInt(v.RatingCount, 10) }

// RatingLabel is the summary as one sentence for assistive technology.
func (v *ProductView) RatingLabel(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyRatingSummary), v.RatingText(), v.ReviewCountText())
}

// ReviewDraft carries a refused review form's values back into it.
type ReviewDraft struct {
	Rating int
	Title  string
	Body   string
}

// IsRating reports whether n is the chosen star count, for the radio group.
func (d ReviewDraft) IsRating(n int) bool { return d.Rating == n }

// ReviewAction is where the review form posts.
func (v *ProductView) ReviewAction() string { return "/p/" + v.Slug + "/reviews" }

// HasReviewErr reports whether a review field was refused.
func (v *ProductView) HasReviewErr(f string) bool { _, ok := v.ReviewErrors[f]; return ok }

// ReviewErr is why a review field was refused.
func (v *ProductView) ReviewErr(f string) string { return v.ReviewErrors[f] }

// ReviewStars is the rating drawn as stars.
func (r ProductReview) ReviewStars() string { return starsOf(r.Rating) }

// RatingLabel is what a screen reader is told, because the stars are punctuation to it.
func (r ProductReview) RatingLabel(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyRatingOutOf), strconv.Itoa(r.Rating))
}

func starsOf(n int) string {
	n = max(0, min(n, 5))
	return strings.Repeat("★", n) + strings.Repeat("☆", 5-n)
}

// NotifyTaken reports whether a restock request was just recorded.
func (v *ProductView) NotifyTaken() bool { return v.NotifyOutcome == "1" }

// NotifyRefused reports whether the address was not usable.
func (v *ProductView) NotifyRefused() bool { return v.NotifyOutcome == "bad" }

// NotifyAction is where the restock form posts.
func (v *ProductView) NotifyAction() string { return "/p/" + v.Slug + "/notify" }

// HasRecommendations reports whether the strip has anything real to show.
func (v *ProductView) HasRecommendations() bool { return len(v.AlsoBought) > 0 }

// HasQuestions reports whether anybody has asked anything.
func (v *ProductView) HasQuestions() bool { return len(v.Questions) > 0 }

// AskTaken reports whether a question was just recorded.
func (v *ProductView) AskTaken() bool { return v.AskOutcome == "1" }

// AskRefused reports whether the question was not usable.
func (v *ProductView) AskRefused() bool { return v.AskOutcome == "bad" }

// AskAction is where the question form posts.
func (v *ProductView) AskAction() string { return "/p/" + v.Slug + "/questions" }

// CompareHref adds this product to a comparison, carrying whatever was already there.
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

// AlreadyComparing reports whether this product is already in the set.
func (v *ProductView) AlreadyComparing() bool {
	return slices.Contains(v.Comparing, v.Slug)
}

// ComparingFull reports whether the set has no room left.
func (v *ProductView) ComparingFull() bool { return len(v.Comparing) >= 4 }

// FreeDelivery is the threshold the guarantee strip states, or "" for none.
func (v *ProductView) FreeDelivery() string { return FreeDeliveryText(v.FreeDeliveryCents) }
