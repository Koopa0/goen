package pages

import (
	"encoding/json"
	"strconv"
	"strings"
)

// ProductJSONLD describes a product to a search engine.
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
			"price":         dollars(v.PriceCents),
			"availability":  availability(v.Sellable),
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

// availability is schema.org's own vocabulary; a shop's own words here are
// words the crawler ignores.
func availability(inStock bool) string {
	if inStock {
		return "https://schema.org/InStock"
	}
	return "https://schema.org/OutOfStock"
}

// dollars renders cents as a decimal string. Integer division and a padded
// remainder, never a float: money through float64 is how a price ends .9999999.
func dollars(cents int64) string {
	sub := strconv.FormatInt(cents%100, 10)
	if len(sub) == 1 {
		sub = "0" + sub
	}
	return strconv.FormatInt(cents/100, 10) + "." + sub
}

// encode marshals and returns "" on failure: a page with no structured data
// still reads, a page with a broken script tag does not.
func encode(doc map[string]any) string {
	b, err := json.Marshal(doc)
	if err != nil {
		return ""
	}
	return string(b)
}

// JSONLDSet puts several documents in one block, which schema.org accepts as a
// top-level array. Empty documents are dropped rather than emitted as nulls.
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
