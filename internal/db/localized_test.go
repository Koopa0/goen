package db_test

import (
	"regexp"
	"testing"
)

// A query that reads a category's NAME, and whether it localizes it.
//
// The pattern is the bare column: `c.name`, `categories.name`, or `name` in a
// query whose FROM is categories. localized_name() wrapping it is what the guard
// looks for, and the wrapped form does not match the bare one.
var (
	// A spec's label and value are read the same way and by the same rule: they are
	// what a customer reads on the product page and the row headers of /compare.
	readsSpecText = regexp.MustCompile(`\bproduct_specs\b`)
	bareSpecText  = regexp.MustCompile(`(?:^|[\s,(])(?:\w+\.)?(?:label|value)\b(?:\s|,|$)`)

	// An option axis and its values. Both are read by the PDP's picker and by the
	// cart line, and both are IDENTITY as well as text — so the guard asks that the
	// query carry a localized column, not that it stop selecting the canonical one.
	//
	// Both spellings and both tables: the first cut wrote
	// `product_option(_values)?` and matched product_option_values while missing
	// product_optionS entirely, so three allowlist entries looked stale and the
	// writes looked uncovered. The identity check is what said so.
	readsOptionText = regexp.MustCompile(`\bproduct_option(s|_values)?\b`)

	// A product's own copy. `name` is the identity a search matches and an order line
	// snapshots; `name_en` is what a visitor reads.
	readsProductText = regexp.MustCompile(`\bproducts\b`)
	bareProductName  = regexp.MustCompile(`(?:^|[\s,(])(?:\w+\.)?name\b(?:\s|,|$)`)

	// A -- comment, which splitQueries hands over as part of the PREVIOUS query's
	// body — the marker it cuts at is the next `-- name:`, so every query carries
	// the next one's introduction. Stripping comments is what makes the match about
	// SQL: the first version of this guard refused CurrentPromoBanner, a query with
	// no category in it at all, because the paragraph introducing NavCategories sat
	// inside its body.
	sqlComment        = regexp.MustCompile(`(?m)--.*$`)
	readsCategoryName = regexp.MustCompile(`\bcategories\b`)
	bareCategoryName  = regexp.MustCompile(`(?:^|[\s,(])(?:\w+\.)?name\b(?:\s|,|$)`)
	localizes         = regexp.MustCompile(`localized_name\(`)
)

// TestEveryCategoryNameIsLocalized holds the header of every page.
//
// A category name was treated as CONTENT — the shop's to say however it likes, the
// way a product description is — and that was wrong in the one place it mattered
// most: the header carries five of them on every page of the site, so an English
// visitor met a Chinese navigation bar above a page whose every other word had been
// translated. Worse, the header held its OWN hard-coded copy of the names, so there
// were two answers to "what is this category called" and they could disagree.
//
// localized_name(name, name_en, locale) is the one definition now. This is what
// stops the sixth query being written without it: the failure mode is a page whose
// header says Phones and whose breadcrumb says 手機, which nobody notices in review
// because both are correct in isolation.
//
// Named exceptions are the BACK OFFICE, which is Chinese by decision, and the
// queries that read a name to write or match it rather than to show it.
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

	// Which allowlist entries were actually reached. Counting matches instead was
	// the first version, and it was blind: an entry naming a query that no longer
	// exists left the total above the entry count and the check passed. An
	// exemption for something that is not there reads as covered forever.
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

// TestEverySpecLabelIsLocalized is the same rule for the 規格表.
//
// A spec label is what the product page prints beside a number and what /compare
// uses as a row header — 規格看得懂 is the promise, and an English visitor reading
// 螢幕 above 6.3" OLED is not the version of that promise the shop meant to make.
//
// One exception is interesting enough to be worth reading: CompareSpecs counts how
// many products share a spec on the UNTRANSLATED label, because grouping by what
// the reader sees would split 螢幕 from Screen and report each as stated by one
// product. The rows are the same spec; only the words differ.
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
//
// 顏色 and 星霧藍 are what a visitor reads on the product page, and they were the
// last chrome on the buying mainline still Chinese for everybody — the axis heading
// and every swatch on it.
//
// The exceptions are the interesting part, and each is about IDENTITY rather than
// about the back office:
//
//   - ProductVariants and CartItems aggregate the canonical option names and values
//     to MATCH a variant against a URL selection. Localizing those would make a
//     shared link resolve differently for a reader in another language, and a cart
//     line's stored selection stop matching the variant it names.
//   - the writes.
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
//
// CLAUDE.md's editorial line — that translating product copy is a job for a person
// and not a lookup table — was read for a long time as "so the copy stays Chinese for
// everybody". Those are different claims. goen still never invents a translation; a
// shop that HAS one can now say so, and a product with none renders its Chinese copy,
// which is readable and visibly untranslated.
//
// The exemptions divide into three kinds and each is worth reading:
//
//   - SNAPSHOTS. order_lines.product_name is what was bought, recorded at purchase.
//     Localizing a receipt after the fact makes the shop's record disagree with the
//     document somebody was emailed.
//   - MATCHING. Search compares against both names, deliberately: an English visitor
//     typing "case" must find 保護殼, and a Chinese visitor must still find it after
//     somebody adds an English name. Matching only the localized column would make
//     the catalogue searchable in one language at a time.
//   - the back office and the writes.
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
