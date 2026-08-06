// Package product renders goen's product detail page.
//
// The page's one hard problem is variant selection without scripting. A visitor
// picking 星霧藍 then 512GB has to see that combination's price and stock, and
// goen's write-face rule means the answer cannot be a click handler. So every
// combination is a URL: the pickers are links, the server resolves the query
// string to a variant, and the page renders that variant's price. With
// scripting off it works because there is nothing to switch off.
package product

import (
	"errors"
	"net/url"
	"strings"
)

// ErrNotFound is returned when a slug names no active product. A draft or
// archived product is a 404 on purpose — a URL that renders a draft is how an
// unannounced product leaks.
var ErrNotFound = errors.New("product: not found")

// RelatedCount is how many other products from the same category the page
// shows. Four fills one row of the desktop grid exactly.
const RelatedCount = 4

// ReviewCount is how many reviews the page lists. Enough to judge by; a full
// review list is its own page when there is enough to warrant one.
const ReviewCount = 6

// maxOptionRunes bounds one option value taken from a query string, so a
// crafted URL cannot become an unbounded map key or an oversized comparison.
const maxOptionRunes = 64

// maxOptions bounds how many option groups a selection may name. goen's
// products have two; the cap exists so a URL cannot make the resolver walk an
// arbitrary number of keys.
const maxOptions = 8

// Selection is the option values a URL picks, keyed by option name — 顏色 to
// 星霧藍, 容量 to 512GB.
//
// It is deliberately not a variant id. A URL naming a variant directly breaks
// the moment a variant is replaced, and it cannot express "the visitor picked a
// colour but not yet a capacity", which is exactly the state the page is in
// after the first click.
type Selection map[string]string

// ParseSelection reads a selection from a query string, ignoring the keys the
// page uses for other purposes.
//
// Values are bounded and unknown keys are kept rather than rejected: whether an
// option name is real is a question about this product, which the caller
// answers by matching against its variants. Dropping unknown keys here would
// silently turn a typo into "no filter" and render a page that contradicts its
// own URL.
func ParseSelection(q url.Values) Selection {
	sel := make(Selection, len(q))
	for k, vs := range q {
		if k == "" || len(vs) == 0 || reservedParam(k) {
			continue
		}
		if len(sel) >= maxOptions {
			break
		}
		v := strings.TrimSpace(vs[0])
		if v == "" {
			continue
		}
		if r := []rune(v); len(r) > maxOptionRunes {
			v = string(r[:maxOptionRunes])
		}
		if r := []rune(k); len(r) > maxOptionRunes {
			continue
		}
		sel[k] = v
	}
	return sel
}

// reservedParam reports whether a query key means something other than an
// option choice.
func reservedParam(k string) bool {
	switch k {
	case "page", "sort", "q", "added":
		return true
	default:
		return false
	}
}

// Variant is one buyable combination.
type Variant struct {
	ID           string
	SKU          string
	PriceCents   int64
	CompareCents int64 // 0 when not discounted
	Sellable     bool
	Available    int32 // how many may actually be bought
	// Options maps option name to the value this variant carries.
	Options map[string]string
}

// Matches reports whether this variant satisfies every value in sel. A
// selection naming fewer options than the variant has still matches — that is
// the partial state after one pick.
func (v Variant) Matches(sel Selection) bool {
	for name, want := range sel {
		if got, ok := v.Options[name]; !ok || got != want {
			return false
		}
	}
	return true
}

// Resolve picks the variant a selection names.
//
// It returns the first variant matching every chosen value, preferring one that
// can actually be bought: with only a colour chosen, the page should quote a
// capacity that is in stock rather than the first sold-out one in position
// order. exact reports whether the selection pinned a single combination, which
// is what decides whether the page can offer an add-to-cart button at all.
func Resolve(variants []Variant, sel Selection) (chosen Variant, exact bool) {
	var first Variant
	var found, pinned bool
	matches := 0

	for _, v := range variants {
		if !v.Matches(sel) {
			continue
		}
		matches++
		if !found {
			first, found = v, true
		}
		if v.Sellable && !pinned {
			first, pinned = v, true
		}
	}
	if !found {
		return Variant{}, false
	}
	// Exact when the selection names every option the variant carries, so no
	// further choice is left to make.
	return first, matches >= 1 && len(sel) >= len(first.Options)
}

// OptionValue is one entry in a picker.
type OptionValue struct {
	Value string
	// Selected is whether this value is the current choice.
	Selected bool
	// Available is whether choosing this value leads to anything buyable, given
	// the OTHER choices already made. A colour whose every capacity is sold out
	// is shown, but marked, rather than hidden: hiding it makes the page look
	// like the colour does not exist.
	Available bool
	// Href is the URL that selects this value, keeping the other choices.
	Href string
	// Label is what the visitor reads. Value is what the URL carries.
	Label string
}

// Option is one picker: a group name and its values.
type Option struct {
	// Name is the option's IDENTITY — what the URL carries and what variant
	// matching compares. Never localized: a link shared between two readers in
	// different languages has to select the same thing.
	Name string
	// Label is what the visitor reads, which is Name in Traditional Chinese and
	// the shop's translation in English.
	Label  string
	Values []OptionValue
}

// BuildOptions turns the product's option groups into pickers, with each value
// carrying the URL that selects it.
//
// The availability flag is computed against the OTHER selections, not against
// the whole product: with 512GB chosen, a colour is available only if that
// colour exists in 512GB. That is the same one-variant rule the listing
// enforces, applied to a picker.
func BuildOptions(slug string, groups map[string][]OptionChoice, order []string, labels map[string]string, variants []Variant, sel Selection) []Option {
	opts := make([]Option, 0, len(order))
	for _, name := range order {
		values := groups[name]
		o := Option{
			Name:   name,
			Label:  labelOr(labels[name], name),
			Values: make([]OptionValue, 0, len(values)),
		}
		for _, choice := range values {
			val := choice.Value
			// What the URL would select: this value, plus every other current
			// choice.
			next := make(Selection, len(sel)+1)
			for k, v := range sel {
				if k != name {
					next[k] = v
				}
			}
			next[name] = val

			o.Values = append(o.Values, OptionValue{
				Value:     val,
				Label:     labelOr(choice.Label, val),
				Selected:  sel[name] == val,
				Available: anySellable(variants, next),
				Href:      Href(slug, next),
			})
		}
		opts = append(opts, o)
	}
	return opts
}

// OptionChoice is one value on an axis: what the URL selects on, and what the
// visitor reads.
type OptionChoice struct {
	Value string
	Label string
}

// labelOr falls back to the canonical text. An option nobody has translated is
// readable in Chinese; an empty picker label is not readable at all.
func labelOr(label, canonical string) string {
	if label == "" {
		return canonical
	}
	return label
}

// anySellable reports whether any single variant satisfies every value in sel
// and can be bought.
func anySellable(variants []Variant, sel Selection) bool {
	for _, v := range variants {
		if v.Sellable && v.Matches(sel) {
			return true
		}
	}
	return false
}

// Href is the product URL for a selection. Keys are sorted by url.Values so the
// same selection always produces the same URL — otherwise two links to the same
// combination would differ only in parameter order.
func Href(slug string, sel Selection) string {
	if len(sel) == 0 {
		return "/p/" + slug
	}
	q := url.Values{}
	for k, v := range sel {
		q.Set(k, v)
	}
	return "/p/" + slug + "?" + q.Encode()
}
