// Package product renders goen's product detail page.
package product

import (
	"errors"
	"net/url"
	"strings"
)

// ErrNotFound is returned when a slug names no active product.
var ErrNotFound = errors.New("product: not found")

// RelatedCount is how many other products from the same category the page shows.
const RelatedCount = 4

// ReviewCount is how many reviews the page lists.
const ReviewCount = 6

const maxOptionRunes = 64

const maxOptions = 8

// Selection is the option values a URL picks, keyed by option name.
type Selection map[string]string

// ParseSelection reads a selection from a query string, ignoring the keys the
// page uses for other purposes.
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
	Available    int32
	Options      map[string]string
}

// Matches reports whether this variant satisfies every value in sel. A
// selection naming fewer options than the variant has still matches.
func (v Variant) Matches(sel Selection) bool {
	for name, want := range sel {
		if got, ok := v.Options[name]; !ok || got != want {
			return false
		}
	}
	return true
}

// Resolve picks the variant a selection names, preferring one that can be
// bought. exact reports whether the selection pinned a single combination.
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
	return first, matches >= 1 && len(sel) >= len(first.Options)
}

// OptionValue is one entry in a picker.
type OptionValue struct {
	Value    string
	Selected bool
	// Available is whether this value leads to anything buyable given the OTHER
	// choices already made.
	Available bool
	Href      string
	// Label is what the visitor reads; Value is what the URL carries.
	Label string
}

// Option is one picker: a group name and its values.
type Option struct {
	// Name is the option's identity, what the URL carries. Never localized.
	Name   string
	Label  string
	Values []OptionValue
}

// BuildOptions turns the product's option groups into pickers, with each value
// carrying the URL that selects it.
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

func labelOr(label, canonical string) string {
	if label == "" {
		return canonical
	}
	return label
}

func anySellable(variants []Variant, sel Selection) bool {
	for _, v := range variants {
		if v.Sellable && v.Matches(sel) {
			return true
		}
	}
	return false
}

// Href is the product URL for a selection.
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
