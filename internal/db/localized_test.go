package db_test

import (
	"regexp"
	"testing"
)

var (
	readsSpecText = regexp.MustCompile(`\bproduct_specs\b`)
	bareSpecText  = regexp.MustCompile(`(?:^|[\s,(])(?:\w+\.)?(?:label|value)\b(?:\s|,|$)`)

	// Both tables: product_option(_values)? misses product_options entirely.
	readsOptionText = regexp.MustCompile(`\bproduct_option(s|_values)?\b`)

	readsProductText = regexp.MustCompile(`\bproducts\b`)
	bareProductName  = regexp.MustCompile(`(?:^|[\s,(])(?:\w+\.)?name\b(?:\s|,|$)`)

	// Each split body carries the NEXT query's introduction: strip comments or prose is matched.
	sqlComment        = regexp.MustCompile(`(?m)--.*$`)
	readsCategoryName = regexp.MustCompile(`\bcategories\b`)
	bareCategoryName  = regexp.MustCompile(`(?:^|[\s,(])(?:\w+\.)?name\b(?:\s|,|$)`)
	localizes         = regexp.MustCompile(`localized_name\(`)
)

// TestEveryCategoryNameIsLocalized holds localized_name as the one definition of a category name.
func TestEveryCategoryNameIsLocalized(t *testing.T) {
	t.Parallel()

	allowed := map[string]string{
		"ManagedCategories": "the back office, which is Chinese by decision — and it " +
			"shows BOTH names, because it is where the translation is entered",
		"CreateCategory":  "the write",
		"RenameCategory":  "the write",
		"AdminCategories": "back office: the product form's category select",
		"AdminProducts":   "back office: the product list's category column",
	}

	used := map[string]bool{}
	for path, src := range queryFiles(t) {
		for _, q := range splitQueries(src) {
			body := sqlComment.ReplaceAllString(q.body, "")
			if !readsCategoryName.MatchString(body) ||
				!bareCategoryName.MatchString(body) {
				continue
			}
			if _, ok := allowed[q.name]; ok {
				used[q.name] = true
				continue
			}
			if localizes.MatchString(body) {
				continue
			}
			t.Errorf("%s: query %s reads a category name without localized_name().\n"+
				"  A category name is in the header of every page. One query that "+
				"skips this shows a visitor Phones at the top and 手機 in the crumb "+
				"on the same page. If it is back-office or a write, name it in the "+
				"allowlist with the reason.", path, q.name)
		}
	}
	for name, why := range allowed {
		if !used[name] {
			t.Errorf("the allowlist exempts %q (%s) and no query by that name reads a "+
				"category name — the entry is stale, or the pattern stopped matching",
				name, why)
		}
	}
}

// TestEverySpecLabelIsLocalized is the same rule for a spec label and its value.
func TestEverySpecLabelIsLocalized(t *testing.T) {
	t.Parallel()

	allowed := map[string]string{
		"AdminProductSpecs": "the back office, which shows BOTH pairs — it is where " +
			"the translation is entered",
		"AddProductSpec": "the write",
	}

	used := map[string]bool{}
	for path, src := range queryFiles(t) {
		for _, q := range splitQueries(src) {
			body := sqlComment.ReplaceAllString(q.body, "")
			if !readsSpecText.MatchString(body) || !bareSpecText.MatchString(body) {
				continue
			}
			if _, ok := allowed[q.name]; ok {
				used[q.name] = true
				continue
			}
			if localizes.MatchString(body) {
				continue
			}
			t.Errorf("%s: query %s reads a spec label or value without "+
				"localized_name().\n  The 規格表 is what /compare is FOR. If it is "+
				"back-office or a write, name it in the allowlist with the reason.",
				path, q.name)
		}
	}
	for name, why := range allowed {
		if !used[name] {
			t.Errorf("the allowlist exempts %q (%s) and no query by that name reads a "+
				"spec label — the entry is stale, or the pattern stopped matching",
				name, why)
		}
	}
}

// TestEveryOptionLabelIsLocalized is the picker's half of the same rule.
func TestEveryOptionLabelIsLocalized(t *testing.T) {
	t.Parallel()

	allowed := map[string]string{
		"ProductVariants": "matching, not display: these are the canonical values a " +
			"URL selection is compared against",
		"AdminProductOptions": "the back office, which shows BOTH — it is where the " +
			"translation is entered",
		"AddProductOption":         "the write",
		"AddProductOptionValue":    "the write",
		"ProductOptionCount":       "counts axes, reads no text",
		"SetVariantOptionValue":    "the write",
		"AdminVariantOptionValues": "back office: the variant list's option column",
	}

	used := map[string]bool{}
	for path, src := range queryFiles(t) {
		for _, q := range splitQueries(src) {
			body := sqlComment.ReplaceAllString(q.body, "")
			if !readsOptionText.MatchString(body) {
				continue
			}
			if _, ok := allowed[q.name]; ok {
				used[q.name] = true
				continue
			}
			if localizes.MatchString(body) {
				continue
			}
			t.Errorf("%s: query %s reads an option name or value without "+
				"localized_name().\n  The picker's heading and its swatches are what a "+
				"visitor reads. If the query MATCHES on those values rather than "+
				"showing them, name it in the allowlist with the reason — and keep "+
				"selecting the canonical column, because the URL carries it.",
				path, q.name)
		}
	}
	for name, why := range allowed {
		if !used[name] {
			t.Errorf("the allowlist exempts %q (%s) and no query by that name reads an "+
				"option name — the entry is stale, or the pattern stopped matching",
				name, why)
		}
	}
}

// TestEveryProductNameIsLocalized is the last of the four.
func TestEveryProductNameIsLocalized(t *testing.T) {
	t.Parallel()

	allowed := map[string]string{
		"SearchProducts": "matches BOTH names — searchable in one language at a time " +
			"would be worse than not translating at all",
		"SearchProductsCount": "the same predicate as SearchProducts, and it has to stay " +
			"the same or the count disagrees with the rows",
		"AdminProduct":          "back office: shows both, it is where the copy is typed",
		"AdminProducts":         "back office: the catalogue list",
		"AdminVariants":         "back office: the stock list",
		"AdminVariantBySKU":     "back office: the stock adjustment form",
		"AdminReviews":          "back office: the moderation queue",
		"AdminCampaignProducts": "back office: the campaign curation list",
		"UnansweredQuestions":   "back office: the question queue",
		"BestSellersSince":      "back office: the report",
		"StockAtRisk":           "back office: the report",
		"ManagedCategories":     "back office: counts products, reads no product name",
		"CategoryBrands": "the brand FACET: b.name is a brand, and a brand name is a " +
			"proper noun that reads the same in both languages",
		"ManagedBrands":            "back office: the brand list, brand names again",
		"AdminProductOptions":      "back office, and it joins products only to find the slug",
		"AdminVariantOptionValues": "back office: the variant list's option column",
		"AddProductOption":         "the write",
		"CreateProduct":            "the write",
		"UpdateProduct":            "the write",
	}

	used := map[string]bool{}
	for path, src := range queryFiles(t) {
		for _, q := range splitQueries(src) {
			body := sqlComment.ReplaceAllString(q.body, "")
			if !readsProductText.MatchString(body) || !bareProductName.MatchString(body) {
				continue
			}
			if _, ok := allowed[q.name]; ok {
				used[q.name] = true
				continue
			}
			if localizes.MatchString(body) {
				continue
			}
			t.Errorf("%s: query %s reads a product name without localized_name().\n"+
				"  If it SNAPSHOTS the name, MATCHES on it, or is back-office, name it "+
				"in the allowlist with the reason.", path, q.name)
		}
	}
	for name, why := range allowed {
		if !used[name] {
			t.Errorf("the allowlist exempts %q (%s) and no query by that name reads a "+
				"product name — the entry is stale, or the pattern stopped matching",
				name, why)
		}
	}
}
