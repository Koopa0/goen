package catalog

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/ui/widget"
)

type partialKey struct{}

// AsPartial marks a render whose results htmx swaps into a page already showing:
// it must not take focus, because the control the shopper just changed has it.
func AsPartial(ctx context.Context) context.Context {
	return context.WithValue(ctx, partialKey{}, true)
}

func isPartial(ctx context.Context) bool {
	partial, ok := ctx.Value(partialKey{}).(bool)
	return ok && partial
}

// View is how a shelf draws its products: a grid of cards, or a list of rows
// that leads with each product's specifications.
type View string

const (
	ViewGrid View = ""
	ViewList View = "list"
)

// ParseView is the grid for anything but the list's own word.
func ParseView(s string) View {
	if View(s) == ViewList {
		return ViewList
	}
	return ViewGrid
}

type FacetOption struct {
	Value    string
	Label    string
	Count    int64
	Selected bool
}

func (o FacetOption) CountText() string { return strconv.FormatInt(o.Count, 10) }

// PriceOption is a price band beside a shelf, with how many products choosing
// it would list.
type PriceOption struct {
	Band     PriceBand
	Count    int64
	Selected bool
}

func (o PriceOption) CountText() string { return strconv.FormatInt(o.Count, 10) }

// CountID is the id of the option's count, which a filtered response replaces.
func (o PriceOption) CountID() string {
	if o.Band.ID == "" {
		return "price-count-any"
	}
	return "price-count-" + o.Band.ID
}

// Label names the band by its bounds.
func (o PriceOption) Label(ctx context.Context) string { return priceLabel(ctx, o.Band) }

func priceLabel(ctx context.Context, b PriceBand) string {
	switch {
	case b.Min > 0 && b.Max > 0:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyPriceRangeChip), money.TWD(b.Min), money.TWD(b.Max))
	case b.Min > 0:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyPriceFromChip), money.TWD(b.Min))
	case b.Max > 0:
		// The band ends a cent below a round figure, which is the figure it is under.
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyPriceUnder), money.TWD(b.Max+1))
	default:
		return i18n.T(ctx, i18n.KeyPriceAny)
	}
}

// TreeLink is a category in the tree beside a shelf: where it leads, how many
// products it holds, and whether the page is it (Current) or inside it (Open).
type TreeLink struct {
	Slug    string
	Name    string
	Href    string
	Count   int64
	Current bool
	Open    bool
}

func (l TreeLink) CountText() string { return strconv.FormatInt(l.Count, 10) }

// AriaCurrent marks the page itself, and the department the page is inside.
func (l TreeLink) AriaCurrent() string {
	switch {
	case l.Current:
		return "page"
	case l.Open:
		return "true"
	default:
		return ""
	}
}

// Spec is one of a product's specifications as the list shows it.
type Spec struct {
	Label string
	Value string
}

// Shelf is a page of products under the shopper's filters, with what narrows
// it: what a category, every product and a search have in common. Path is the
// page's address without a query (/c/audio, /shop, /search); Keep is the part
// of its query that is the page's own rather than a filter, a search's q.
type Shelf struct {
	Path     string
	Keep     url.Values
	Products []tile.Tile
	// Specs is filled for the list only, keyed by product slug.
	Specs  map[string][]Spec
	Facets []FacetGroup
	Prices []PriceOption
	// Departments is the catalogue's tree: links on a category's page, the
	// choice a search narrows to on the search page.
	Departments []TreeLink
	// Subs are the sub-categories of the department a category page is in,
	// under SubsTitle, the department's name.
	Subs      []TreeLink
	SubsTitle string
	Total     int64
	// Page and PageSize are int32 so they share a word.
	Page     int32
	PageSize int32
	Filters  Filters
	View     View
	// Searching is a search's shelf: best match is its unchosen order and its
	// departments are a filter, not a way to another page.
	Searching bool
}

const (
	activeCountID = "filters-active"
)

func (s *Shelf) Empty() bool { return s.Total == 0 }

func (s *Shelf) Filtered() bool { return s.Filters.Active() }

func (s *Shelf) TotalText() string { return strconv.FormatInt(s.Total, 10) }

func (s *Shelf) Grid() []tile.Tile { return tile.FirstRowEager(s.Products) }

// IsList is whether the shelf draws its list rather than its grid.
func (s *Shelf) IsList() bool { return s.View == ViewList }

// DepartmentsTotal is what the departments hold together: on a search, every
// match, since each product is filed under one department.
func (s *Shelf) DepartmentsTotal() int64 {
	var n int64
	for _, d := range s.Departments {
		n += d.Count
	}
	return n
}

// ActiveCount is how many filters are on: each brand, option value and band,
// the department and each switch.
func (s *Shelf) ActiveCount() int64 {
	f := &s.Filters
	n := int64(len(f.BrandSlugs) + len(f.OptionValues))
	for _, on := range []bool{f.InStockOnly, f.OnSaleOnly, f.Price.Bounded(), f.Department != ""} {
		if on {
			n++
		}
	}
	return n
}

// Query is the filters and the sort as an address writes them, rebuilt from
// the parsed values rather than the request, so a link carries nothing a
// visitor typed.
func (f Filters) Query() url.Values {
	q := url.Values{}
	if f.Department != "" {
		q.Set("c", f.Department)
	}
	for _, pair := range f.OptionValues {
		q.Add("opt", pair.Name+":"+pair.Value)
	}
	for _, b := range f.BrandSlugs {
		q.Add("brand", b)
	}
	if f.Price.ID != "" {
		q.Set("price", f.Price.ID)
	}
	if f.InStockOnly {
		q.Set("in_stock", "1")
	}
	if f.OnSaleOnly {
		q.Set("sale", "1")
	}
	if sort := f.Sort.Param(); sort != "" {
		q.Set("sort", sort)
	}
	return q
}

// href is the shelf's own address with q after what the page keeps and before
// the view, which outlasts every filter.
func (s *Shelf) href(q url.Values, page int) string {
	all := url.Values{}
	for k, vs := range s.Keep {
		all[k] = append([]string(nil), vs...)
	}
	for k, vs := range q {
		all[k] = append(all[k], vs...)
	}
	if s.View != ViewGrid {
		all.Set("view", string(s.View))
	}
	if page > 1 {
		all.Set("page", strconv.Itoa(page))
	}
	if enc := all.Encode(); enc != "" {
		return s.Path + "?" + enc
	}
	return s.Path
}

// ResetHref is the shelf with no filter and no sort, in the same view.
func (s *Shelf) ResetHref() string { return s.href(nil, 1) }

// ViewHref is this page of the shelf drawn the other way.
func (s *Shelf) ViewHref(v View) string {
	other := *s
	other.View = v
	return other.href(s.Filters.Query(), 1)
}

// FilterAction is where the form lands without htmx: the results, not the top.
func (s *Shelf) FilterAction() string { return s.Path + "#listing-results" }

// KeepFields are the hidden fields the form carries so a filtered address is
// still the page's own: a search's term and the view.
func (s *Shelf) KeepFields() []Field {
	var out []Field
	for k, vs := range s.Keep {
		for _, v := range vs {
			out = append(out, Field{Name: k, Value: v})
		}
	}
	if s.View != ViewGrid {
		out = append(out, Field{Name: "view", Value: string(s.View)})
	}
	return out
}

// Field is a hidden form field.
type Field struct {
	Name  string
	Value string
}

type SortOption struct {
	Value    string
	Label    string
	Selected bool
}

// SortOptions puts the page's own order first: newest on a category, best
// match on a search, where newest-first would bury the name that holds the
// whole query.
func (s *Shelf) SortOptions(ctx context.Context) []SortOption {
	first := i18n.T(ctx, i18n.KeySortNewest)
	if s.Searching {
		first = i18n.T(ctx, i18n.KeySortBestMatch)
	}
	opts := []SortOption{
		{Value: "", Label: first},
		{Value: "price_asc", Label: i18n.T(ctx, i18n.KeySortPriceAsc)},
		{Value: "price_desc", Label: i18n.T(ctx, i18n.KeySortPriceDesc)},
		{Value: "rating", Label: i18n.T(ctx, i18n.KeySortRating)},
	}
	chosen := s.Filters.Sort.Param()
	for i := range opts {
		opts[i].Selected = opts[i].Value == chosen
	}
	return opts
}

func (s *Shelf) Pages() int {
	if s.PageSize <= 0 || s.Total <= 0 {
		return 1
	}
	n := int((s.Total + int64(s.PageSize) - 1) / int64(s.PageSize))
	return max(n, 1)
}

func (s *Shelf) HasPrev() bool { return s.Page > 1 }
func (s *Shelf) HasNext() bool { return int(s.Page) < s.Pages() }

func (s *Shelf) PrevHref() string { return s.PageHref(int(s.Page) - 1) }
func (s *Shelf) NextHref() string { return s.PageHref(int(s.Page) + 1) }

func (s *Shelf) PageHref(n int) string { return s.href(s.Filters.Query(), n) }

func (s *Shelf) PageText() string  { return strconv.Itoa(int(s.Page)) }
func (s *Shelf) PagesText() string { return strconv.Itoa(s.Pages()) }

type AppliedChip struct {
	Label       string
	Remove      string
	RemoveLabel string
}

// AppliedChips are the filters that are on, each with the address of the shelf
// without it.
func (s *Shelf) AppliedChips(ctx context.Context) []AppliedChip {
	var chips []AppliedChip
	f := &s.Filters
	if f.Department != "" {
		for _, d := range s.Departments {
			if d.Slug == f.Department {
				chips = append(chips, s.chip(ctx, d.Name, "c", ""))
			}
		}
	}
	for i := range s.Facets {
		group := &s.Facets[i]
		for _, option := range group.Options {
			if option.Selected {
				label := option.Label
				if group.Kind == FacetVariantOption {
					label = fmt.Sprintf(i18n.T(ctx, i18n.KeyOptionFilterValue), group.Label, label)
				}
				chips = append(chips, s.chip(ctx, label, group.Param(), option.Value))
			}
		}
	}
	if f.Price.Bounded() {
		chips = append(chips, s.chip(ctx, priceLabel(ctx, f.Price), "price", ""))
	}
	if f.InStockOnly {
		chips = append(chips, s.chip(ctx, i18n.T(ctx, i18n.KeyFacetInStock), "in_stock", ""))
	}
	if f.OnSaleOnly {
		chips = append(chips, s.chip(ctx, i18n.T(ctx, i18n.KeyOnSale), "sale", ""))
	}
	return chips
}

// chip's link drops key, or only its one value when value is set.
func (s *Shelf) chip(ctx context.Context, label, key, value string) AppliedChip {
	q := s.Filters.Query()
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
	return AppliedChip{Label: label, Remove: s.href(q, 1), RemoveLabel: fmt.Sprintf(i18n.T(ctx, i18n.KeyRemoveFilter), label)}
}

// ListingView is a category's shelf, or every product's (Slug empty): the
// shelf under the name, the crumbs above it.
type ListingView struct {
	Shelf

	Slug   string
	Name   string
	Crumbs []Crumb
	Theme  *Theme
}

// Trail leaves the last step without an href: it is the page you are on.
func (v *ListingView) Trail(ctx context.Context) []widget.Crumb {
	trail := []widget.Crumb{{Label: i18n.T(ctx, i18n.KeyHome), Href: "/"}}
	for _, c := range v.Crumbs {
		trail = append(trail, widget.Crumb{Label: c.Name, Href: "/c/" + c.Slug})
	}
	return append(trail, widget.Crumb{Label: v.Name})
}

func ListingMeta(ctx context.Context, v *ListingView) frame.Page {
	return frame.Page{
		Title: v.Name,
		Nav:   v.RootSlug(),
		Share: frame.NewShareImage(v.Theme.Image(), v.Name),
	}
}

func (v *ListingView) RootSlug() string {
	if len(v.Crumbs) > 0 {
		return v.Crumbs[0].Slug
	}
	return v.Slug
}

// Children are the sub-categories a phone offers under the name, where the
// tree beside a wider shelf is folded away.
func (v *ListingView) Children() []TreeLink { return v.Subs }

// SearchView is a search: its term, how many products it matched before any
// filter (Matches), and their shelf. A search that matched nothing offers the
// newest products instead.
type SearchView struct {
	Shelf

	Query   string
	Matches int
	Newest  []tile.Tile
}

func SearchMeta(ctx context.Context, q string) frame.Page {
	if q == "" {
		return frame.Page{Title: i18n.T(ctx, i18n.KeySearchTitle)}
	}
	return frame.Page{
		Title:       fmt.Sprintf(i18n.T(ctx, i18n.KeySearchFor), q),
		SearchQuery: q,
	}
}

func (v *SearchView) Searched() bool { return v.Query != "" }

// Unmatched is a search for a term nothing in the catalogue holds, which is
// not the same as filters that leave none of its matches.
func (v *SearchView) Unmatched() bool { return v.Searched() && v.Matches == 0 }
