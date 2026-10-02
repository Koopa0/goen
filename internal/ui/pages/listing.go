package pages

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
)

type partialKey struct{}

// AsPartial marks a render as one whose results htmx will swap into a page that
// is already showing: it must not take focus, because the control the shopper
// just changed still has it.
func AsPartial(ctx context.Context) context.Context {
	return context.WithValue(ctx, partialKey{}, true)
}

func isPartial(ctx context.Context) bool {
	partial, ok := ctx.Value(partialKey{}).(bool)
	return ok && partial
}

func queryEscape(s string) string { return url.QueryEscape(s) }

// Crumb is one ancestor in a category's breadcrumb trail.
type Crumb struct {
	Slug string
	Name string
}

// FacetOption is one checkbox in the filter panel.
type FacetOption struct {
	Value    string
	Label    string
	Count    int64
	Selected bool
}

// CountText is the option's product count as text.
func (o FacetOption) CountText() string { return strconv.FormatInt(o.Count, 10) }

// ListingView is everything a category listing page renders.
type ListingView struct {
	Slug   string
	Name   string
	Crumbs []Crumb
	// Theme is the category's tone and photograph, or its nearest ancestor's.
	Theme    *Theme
	Products []ProductTile
	Brands   []FacetOption
	Total    int64
	// Page and PageSize are int32 so that the two share a word: the view is
	// passed by value, and one more field would put it over the lint's size limit.
	Page     int32
	PageSize int32

	Query    string
	Filtered bool

	InStockOnly bool
	MinPrice    int64 // minor units; 0 is no bound
	MaxPrice    int64
	Sort        string
}

// MinPriceText and MaxPriceText are the bounds in whole dollars.
func (v ListingView) MinPriceText() string { return priceField(v.MinPrice) }
func (v ListingView) MaxPriceText() string { return priceField(v.MaxPrice) }

func priceField(cents int64) string {
	if cents <= 0 {
		return ""
	}
	return strconv.FormatInt(cents/100, 10)
}

// SortOption is one entry in the sort control.
type SortOption struct {
	Value    string
	Label    string
	Selected bool
}

// SortOptions is the ordering choices, with the active one marked.
func (v ListingView) SortOptions(ctx context.Context) []SortOption {
	opts := []SortOption{
		{Value: "", Label: i18n.T(ctx, i18n.KeySortNewest)},
		{Value: "price_asc", Label: i18n.T(ctx, i18n.KeySortPriceAsc)},
		{Value: "price_desc", Label: i18n.T(ctx, i18n.KeySortPriceDesc)},
		{Value: "rating", Label: i18n.T(ctx, i18n.KeySortRating)},
	}
	for i := range opts {
		opts[i].Selected = opts[i].Value == v.Sort
	}
	return opts
}

// Trail is the breadcrumb, from the shop's front page down to this category.
// The last step carries no href: it is the page you are on, and a link to here
// is a link to nowhere.
func (v ListingView) Trail(ctx context.Context) []components.Crumb {
	trail := []components.Crumb{{Label: i18n.T(ctx, i18n.KeyHome), Href: "/"}}
	for _, c := range v.Crumbs {
		trail = append(trail, components.Crumb{Label: c.Name, Href: "/c/" + c.Slug})
	}
	return append(trail, components.Crumb{Label: v.Name})
}

// ListingMeta is the chrome view model for a category page.
func ListingMeta(ctx context.Context, v ListingView) layouts.Page {
	return layouts.Page{
		Title: v.Name,
		Nav:   v.RootSlug(),
		Share: v.Theme.Image().share(v.Name),
	}
}

// RootSlug is the top-level category this listing sits under.
func (v ListingView) RootSlug() string {
	if len(v.Crumbs) > 0 {
		return v.Crumbs[0].Slug
	}
	return v.Slug
}

// TotalText is the number of matching products as text.
func (v ListingView) TotalText() string { return strconv.FormatInt(v.Total, 10) }

// Empty reports whether this page has nothing to show.
func (v ListingView) Empty() bool { return len(v.Products) == 0 }

// featuredCount is how many products lead a long listing in larger cards, and
// featuredFloor the listing size beyond which the lead row is worth having: on a
// short shelf it would be the whole page twice.
const (
	featuredCount = 4
	featuredFloor = 8
)

// hasFeatured reports whether the lead row shows. Only the unfiltered first page
// has one: a filtered shelf is a search for something, and page two is a
// continuation, so neither wants the shop's picks above it.
func (v ListingView) hasFeatured() bool {
	return !v.Filtered && v.Page <= 1 && v.Total > featuredFloor && len(v.Products) > featuredCount
}

// Featured is the lead row: the first products in the shelf's own order.
func (v ListingView) Featured() []ProductTile {
	if !v.hasFeatured() {
		return nil
	}
	return FirstRowEager(v.Products[:featuredCount])
}

// Grid is the products the grid shows. Those in the lead row are not repeated.
func (v ListingView) Grid() []ProductTile {
	if v.hasFeatured() {
		return UnderLeadEager(v.Products[featuredCount:])
	}
	return FirstRowEager(v.Products)
}

// Children is the sub-categories the head offers as chips.
func (v ListingView) Children() []Crumb {
	if v.Theme == nil {
		return nil
	}
	return v.Theme.Children
}

// PageLinks is the numbered pager for this listing.
func (v ListingView) PageLinks() []PageLink {
	return pageWindow(int(v.Page), v.Pages(), v.PageHref)
}

// Pages is how many pages the current filters produce.
func (v ListingView) Pages() int {
	if v.PageSize <= 0 || v.Total <= 0 {
		return 1
	}
	n := int((v.Total + int64(v.PageSize) - 1) / int64(v.PageSize))
	return max(n, 1)
}

// HasPrev and HasNext report whether the pager's arrows are live.
func (v ListingView) HasPrev() bool { return v.Page > 1 }
func (v ListingView) HasNext() bool { return int(v.Page) < v.Pages() }

// PrevHref and NextHref are the neighbouring pages under these filters.
func (v ListingView) PrevHref() string { return v.PageHref(int(v.Page) - 1) }
func (v ListingView) NextHref() string { return v.PageHref(int(v.Page) + 1) }

// PageHref is the URL for a page of this listing under the current filters.
func (v ListingView) PageHref(n int) string {
	base := "/c/" + v.Slug
	q := v.Query
	if n > 1 {
		if q != "" {
			q += "&"
		}
		q += "page=" + strconv.Itoa(n)
	}
	if q == "" {
		return base
	}
	return base + "?" + q
}

// PageText is a page number as text.
func (v ListingView) PageText() string { return strconv.Itoa(int(v.Page)) }

// PagesText is the page count as text.
func (v ListingView) PagesText() string { return strconv.Itoa(v.Pages()) }

// FilterAction is where a filter submission should land: the results region.
func (v ListingView) FilterAction() string {
	return "/c/" + v.Slug + "#listing-results"
}

// AppliedChip is one active filter and the address of this listing without it.
type AppliedChip struct {
	Label string
	// Remove is the listing's URL with this one filter dropped.
	Remove string
	// RemoveLabel is the control's accessible name; its glyph says nothing.
	RemoveLabel string
}

// AppliedChips names each active filter for the summary bar.
func (v ListingView) AppliedChips(ctx context.Context) []AppliedChip {
	var chips []AppliedChip
	for _, b := range v.Brands {
		if b.Selected {
			chips = append(chips, v.chip(ctx, b.Label, "brand", b.Value))
		}
	}
	if v.InStockOnly {
		chips = append(chips, v.chip(ctx, i18n.T(ctx, i18n.KeyFacetInStock), "in_stock", ""))
	}
	if v.MinPrice > 0 || v.MaxPrice > 0 {
		chips = append(chips, v.chip(ctx, v.priceRangeChip(ctx), "min_price", "", "max_price"))
	}
	return chips
}

// chip is a chip whose link drops key (only the one value, when value is set)
// and any further keys from the listing's own query.
func (v ListingView) chip(ctx context.Context, label, key, value string, more ...string) AppliedChip {
	q, err := url.ParseQuery(v.Query)
	if err != nil {
		q = url.Values{}
	}
	if values := q[key]; value != "" {
		q.Del(key)
		for _, kept := range values {
			if kept != value {
				q.Add(key, kept)
			}
		}
	} else {
		q.Del(key)
	}
	for _, k := range more {
		q.Del(k)
	}
	href := "/c/" + v.Slug
	if enc := q.Encode(); enc != "" {
		href += "?" + enc
	}
	return AppliedChip{Label: label, Remove: href, RemoveLabel: fmt.Sprintf(i18n.T(ctx, i18n.KeyRemoveFilter), label)}
}

func (v ListingView) priceRangeChip(ctx context.Context) string {
	low := v.MinPriceText()
	high := v.MaxPriceText()
	switch {
	case low != "" && high != "":
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyPriceRangeChip), low, high)
	case low != "":
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyPriceFromChip), low)
	default:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyPriceUpToChip), high)
	}
}

// SearchView is everything the search results page renders.
type SearchView struct {
	Query     string
	Products  []ProductTile
	Total     int64
	Page      int
	PageSize  int
	Campaigns CampaignPage
	Path      string
	// Sort is the ordering asked for; empty is best match.
	Sort   string
	Newest []ProductTile
}

// SortOptions is the search's orderings, with the active one marked. Its default
// is best match: newest-first would bury the name that holds the whole query.
func (v SearchView) SortOptions(ctx context.Context) []SortOption {
	opts := []SortOption{
		{Value: "", Label: i18n.T(ctx, i18n.KeySortBestMatch)},
		{Value: "price_asc", Label: i18n.T(ctx, i18n.KeySortPriceAsc)},
		{Value: "price_desc", Label: i18n.T(ctx, i18n.KeySortPriceDesc)},
		{Value: "rating", Label: i18n.T(ctx, i18n.KeySortRating)},
	}
	for i := range opts {
		opts[i].Selected = opts[i].Value == v.Sort
	}
	return opts
}

// SearchMeta is the chrome view model for the search page.
func SearchMeta(ctx context.Context, q string) layouts.Page {
	if q == "" {
		return layouts.Page{Title: i18n.T(ctx, i18n.KeySearchTitle)}
	}
	return layouts.Page{
		Title:       fmt.Sprintf(i18n.T(ctx, i18n.KeySearchFor), q),
		SearchQuery: q,
	}
}

// Searched reports whether a term was actually submitted.
func (v SearchView) Searched() bool { return v.Query != "" }

// Empty reports whether a search ran and matched nothing.
func (v SearchView) Empty() bool { return v.Searched() && len(v.Products) == 0 }

// TotalText is the number of matches as text.
func (v SearchView) TotalText() string { return strconv.FormatInt(v.Total, 10) }

// Pages is how many pages of results the current term produces.
func (v SearchView) Pages() int {
	if v.PageSize <= 0 || v.Total <= 0 {
		return 1
	}
	n := int((v.Total + int64(v.PageSize) - 1) / int64(v.PageSize))
	return max(n, 1)
}

func (v SearchView) HasPrev() bool { return v.Page > 1 }
func (v SearchView) HasNext() bool { return v.Page < v.Pages() }

// PageLinks is the numbered pager for these results.
func (v SearchView) PageLinks() []PageLink {
	return pageWindow(v.Page, v.Pages(), v.PageHref)
}

func (v SearchView) PrevHref() string { return v.PageHref(v.Page - 1) }
func (v SearchView) NextHref() string { return v.PageHref(v.Page + 1) }

// PageHref builds a search URL.
func (v SearchView) PageHref(n int) string {
	if v.Path != "" {
		q := url.Values{}
		if n > 1 {
			q.Set("page", strconv.Itoa(n))
		}
		if v.Campaigns.Page > 1 {
			q.Set("campaign_page", strconv.Itoa(v.Campaigns.Page))
		}
		if len(q) > 0 {
			return v.Path + "?" + q.Encode()
		}
		return v.Path
	}
	u := "/search?q=" + queryEscape(v.Query)
	if v.Sort != "" {
		u += "&sort=" + queryEscape(v.Sort)
	}
	if n > 1 {
		u += "&page=" + strconv.Itoa(n)
	}
	return u
}

func (v SearchView) PageText() string  { return strconv.Itoa(v.Page) }
func (v SearchView) PagesText() string { return strconv.Itoa(v.Pages()) }

// HasCampaigns reports whether any promotion is running.
func (v SearchView) HasCampaigns() bool { return len(v.Campaigns.Rows) > 0 }

func (v SearchView) CampaignPageHref(page int) string {
	q := url.Values{}
	if v.Page > 1 {
		q.Set("page", strconv.Itoa(v.Page))
	}
	if page > 1 {
		q.Set("campaign_page", strconv.Itoa(page))
	}
	href := "/deals"
	if len(q) > 0 {
		href += "?" + q.Encode()
	}
	return href + "#campaigns"
}

// PageLink is one step in a numbered pager. A Gap has no address: it stands for
// the pages left out between two that are shown.
type PageLink struct {
	Href    string
	Label   string
	Current bool
	Gap     bool
}

// pageWindow lists the first and last page and the one on either side of the
// current, with a gap where pages are left out, so a long run stays one row.
func pageWindow(current, pages int, href func(int) string) []PageLink {
	var out []PageLink
	last := 0
	for n := 1; n <= pages; n++ {
		if n != 1 && n != pages && (n < current-1 || n > current+1) {
			continue
		}
		if last != 0 && n-last > 1 {
			out = append(out, PageLink{Gap: true})
		}
		out = append(out, PageLink{Href: href(n), Label: strconv.Itoa(n), Current: n == current})
		last = n
	}
	return out
}
