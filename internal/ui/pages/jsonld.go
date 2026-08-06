package pages

import (
	"encoding/json"
	"strconv"
	"strings"
)

// ProductJSONLD describes a product to a search engine.
//
// Built as a map rather than a struct because schema.org nests loosely typed
// objects and a Go struct mirroring it would be six types nobody reads. It is
// encoded, so nothing in it can escape the script tag.
//
// The point is what a search result SHOWS: a title alone is a link, while a
// title with a price, a currency and "in stock" is a decision a shopper can
// make before they click.
func ProductJSONLD(v *ProductView, baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	doc := map[string]any{
		"@context":    "https://schema.org",
		"@type":       "Product",
		"name":        v.Name,
		"description": v.Summary,
		"sku":         v.SKU,
		"brand":       map[string]any{"@type": "Brand", "name": v.Brand},
		"offers": map[string]any{
			"@type":         "Offer",
			"url":           base + "/p/" + v.Slug,
			"priceCurrency": "TWD",
			// schema.org wants a decimal string. The cents are exact, so this
			// divides once here rather than letting a float near money.
			"price": dollars(v.PriceCents),
			// InStock and OutOfStock are the schema's own vocabulary; a shop
			// that reports its own words here is a shop the crawler ignores.
			"availability": availability(v.Sellable),
		},
	}
	if v.HasImages() {
		doc["image"] = base + v.Images[0].URL
	}
	if v.RatingCount > 0 {
		doc["aggregateRating"] = map[string]any{
			"@type":       "AggregateRating",
			"ratingValue": strconv.FormatFloat(v.Rating, 'f', 1, 64),
			"reviewCount": v.RatingCount,
		}
	}
	return encode(doc)
}

// BreadcrumbJSONLD describes where a page sits, so a search result shows the
// path rather than a bare URL.
func BreadcrumbJSONLD(crumbs []Crumb, name, baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	items := make([]any, 0, len(crumbs)+1)
	for i, c := range crumbs {
		items = append(items, map[string]any{
			"@type":    "ListItem",
			"position": i + 1,
			"name":     c.Name,
			"item":     base + "/c/" + c.Slug,
		})
	}
	items = append(items, map[string]any{
		"@type": "ListItem", "position": len(crumbs) + 1, "name": name,
	})
	return encode(map[string]any{
		"@context":        "https://schema.org",
		"@type":           "BreadcrumbList",
		"itemListElement": items,
	})
}

// availability is schema.org's vocabulary for whether it can be bought.
func availability(inStock bool) string {
	if inStock {
		return "https://schema.org/InStock"
	}
	return "https://schema.org/OutOfStock"
}

// dollars renders cents as a decimal string, exactly.
//
// Integer division and a padded remainder rather than a float: schema.org wants
// a decimal, and formatting money through float64 is how a price ends in
// .9999999.
func dollars(cents int64) string {
	sub := strconv.FormatInt(cents%100, 10)
	if len(sub) == 1 {
		sub = "0" + sub
	}
	return strconv.FormatInt(cents/100, 10) + "." + sub
}

// encode marshals and returns "" on failure.
//
// A page without structured data is a page a crawler reads normally; a page
// with a broken script tag is worse. Failing quietly to nothing is the right
// direction here, and json.Marshal on a map of strings and numbers has no
// realistic failure anyway.
func encode(doc map[string]any) string {
	b, err := json.Marshal(doc)
	if err != nil {
		return ""
	}
	return string(b)
}

// JSONLDSet puts several documents in one block.
//
// schema.org accepts a top-level ARRAY, which is what lets a product page carry
// its Product and its BreadcrumbList without two script elements — and
// layouts.Page carries one StructuredData string, so one block is what fits.
//
// Empty documents are dropped rather than emitted as nulls: a search engine
// reading a null entry is a search engine that stops reading.
func JSONLDSet(docs ...string) string {
	kept := make([]string, 0, len(docs))
	for _, d := range docs {
		if d != "" {
			kept = append(kept, d)
		}
	}
	switch len(kept) {
	case 0:
		return ""
	case 1:
		return kept[0]
	default:
		return "[" + strings.Join(kept, ",") + "]"
	}
}
