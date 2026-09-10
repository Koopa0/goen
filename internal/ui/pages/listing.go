package pages

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

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
	Slug     string
	Name     string
	Crumbs   []Crumb
	Products []ProductTile
	Brands   []FacetOption
	Total    int64
	Page     int
	PageSize int

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

// ListingMeta is the chrome view model for a category page.
func ListingMeta(ctx context.Context, v ListingView) layouts.Page {
	return layouts.Page{
		Title:       v.Name,
		Description: fmt.Sprintf(i18n.T(ctx, i18n.KeyListingDescription), v.Name),
		Nav:         v.RootSlug(),
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
func (v ListingView) HasNext() bool { return v.Page < v.Pages() }

// PrevHref and NextHref are the neighbouring pages under these filters.
func (v ListingView) PrevHref() string { return v.PageHref(v.Page - 1) }
func (v ListingView) NextHref() string { return v.PageHref(v.Page + 1) }

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
func (v ListingView) PageText() string { return strconv.Itoa(v.Page) }

// PagesText is the page count as text.
func (v ListingView) PagesText() string { return strconv.Itoa(v.Pages()) }

// SearchView is everything the search results page renders.
type SearchView struct {
	Query     string
	Products  []ProductTile
	Total     int64
	Page      int
	PageSize  int
	Campaigns []CampaignSummary
	Path      string
}

// SearchMeta is the chrome view model for the search page.
func SearchMeta(ctx context.Context, q string) layouts.Page {
	if q == "" {
		return layouts.Page{Title: i18n.T(ctx, i18n.KeySearchTitle)}
	}
	return layouts.Page{Title: fmt.Sprintf(i18n.T(ctx, i18n.KeySearchFor), q)}
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

func (v SearchView) PrevHref() string { return v.PageHref(v.Page - 1) }
func (v SearchView) NextHref() string { return v.PageHref(v.Page + 1) }

// PageHref builds a search URL.
func (v SearchView) PageHref(n int) string {
	if v.Path != "" {
		if n > 1 {
			return v.Path + "?page=" + strconv.Itoa(n)
		}
		return v.Path
	}
	u := "/search?q=" + queryEscape(v.Query)
	if n > 1 {
		u += "&page=" + strconv.Itoa(n)
	}
	return u
}

func (v SearchView) PageText() string  { return strconv.Itoa(v.Page) }
func (v SearchView) PagesText() string { return strconv.Itoa(v.Pages()) }

// HasCampaigns reports whether any promotion is running.
func (v SearchView) HasCampaigns() bool { return len(v.Campaigns) > 0 }
