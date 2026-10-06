package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// runningCampaignFeaturing is the one definition of "a running campaign with
// something to buy features this product", which every query that asks it
// writes out in full: SQL has no function here to share it.
const runningCampaignFeaturing = `EXISTS (SELECT 1 FROM sale_campaign_products fp
	JOIN sale_campaigns fc ON fc.id = fp.campaign_id
	WHERE fp.product_id = p.id AND fc.is_active AND fc.starts_at <= now() AND fc.ends_at > now()
	AND EXISTS (SELECT 1 FROM sale_campaign_products cp
		JOIN products cprod ON cprod.id = cp.product_id AND cprod.status = 'active'
		JOIN product_variants v ON v.product_id = cprod.id AND v.is_active
		WHERE cp.campaign_id = fc.id AND v.stock_quantity > v.safety_stock))`

// The queries that carry the definition. ListedCampaigns, DepartmentCampaign and
// RunningCampaignOfProduct read the campaign half only and are not copies.
var carriers = map[string]int{
	"CategoryListing": 1, "SearchProducts": 1, "NewestProducts": 1, "CampaignProducts": 1,
	"CompareProducts": 1, "DepartmentColourStory": 1, "HomeTiles": 1, "RelatedProducts": 1,
	"BoughtTogether": 1, "WishlistItems": 1, "DealsHaveSomethingToBuy": 1, "DealProductsCount": 1,
	// The list tests it and the card column says it.
	"DealProducts": 2,
}

var (
	queryName     = regexp.MustCompile(`(?m)^-- name: (\w+)`)
	featuringOpen = regexp.MustCompile(`EXISTS\s*\(\s*SELECT 1 FROM sale_campaign_products fp`)
)

func squash(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.NewReplacer("( ", "(", " )", ")").Replace(s)
}

func TestEveryRunningCampaignCopyIsTheSameText(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("../*/query.sql")
	if err != nil {
		t.Fatal(err)
	}
	nested, err := filepath.Glob("../*/*/query.sql")
	if err != nil {
		t.Fatal(err)
	}
	want := squash(runningCampaignFeaturing)
	copies := map[string]int{}
	for _, file := range append(files, nested...) {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(src)
		names := queryName.FindAllStringSubmatchIndex(text, -1)
		for _, at := range featuringOpen.FindAllStringIndex(text, -1) {
			end := closing(text, at[0]+strings.Index(text[at[0]:], "("))
			if end < 0 {
				t.Fatalf("%s: an EXISTS at byte %d is never closed", file, at[0])
			}
			name := ""
			for _, n := range names {
				if n[0] < at[0] {
					name = text[n[2]:n[3]]
				}
			}
			copies[name]++
			if got := squash(text[at[0] : end+1]); got != want {
				t.Errorf("%s: %s carries a different running-campaign condition:\n got %s\nwant %s", file, name, got, want)
			}
		}
	}
	for name, n := range copies {
		if _, ok := carriers[name]; !ok {
			t.Errorf("%s writes the running-campaign condition and is not listed as a carrier", name)
		}
		if n != carriers[name] {
			t.Errorf("%s writes the running-campaign condition %d times, want %d", name, n, carriers[name])
		}
	}
	for name, n := range carriers {
		if copies[name] == 0 {
			t.Errorf("%s no longer carries the running-campaign condition (want %d copies)", name, n)
		}
	}
}

// closing is the index of the parenthesis that closes the one at open.
func closing(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}
