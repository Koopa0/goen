package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

var (
	// matchesAPhotographToAVariant is the heart of the rule: a variant's option
	// value compared with a photograph's.
	matchesAPhotographToAVariant = regexp.MustCompile(`(?i)\b\w+\.option_value_id\s*=\s*\w+\.option_value_id\b`)
	variantPrimaryImageView      = regexp.MustCompile(`(?is)\bCREATE\s+VIEW\s+variant_primary_image\b[^;]*;`)
	// productGallery is the product page's gallery, which orders every
	// photograph by the same rule; the view is its first.
	// TestVariantPrimaryImageFirst holds the two together.
	productGallery = regexp.MustCompile(`(?s)-- name: ProductImages :many\n[^;]*;`)
)

// TestVariantPrimaryImageOneView: every query that shows a cart or order line's
// photograph reads variant_primary_image, the one place that picks it, except
// those listed with a reason.
//
// The rule was written out in four line readers, and the copies had drifted
// from the product page: they skipped its "untagged before another value's"
// step and showed the silver watch for the black one (#2141).
func TestVariantPrimaryImageOneView(t *testing.T) {
	t.Parallel()

	sources := queryFiles(t)
	migrations, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("no migrations/*.up.sql found; the view has nowhere to be defined")
	}
	for _, path := range migrations {
		src, readErr := os.ReadFile(path) //nolint:gosec // G304: paths come from the migration glob
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		sources[path] = string(src)
	}
	paths := make([]string, 0, len(sources))
	for path := range sources {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	inView, inGallery := 0, 0
	for _, path := range paths {
		// Masking keeps every byte offset, so the gallery is found in the raw
		// text, whose query name is a comment the mask blanks.
		src := maskCalendarSQL(sources[path], true)
		view := variantPrimaryImageView.FindStringIndex(src)
		var gallery []int
		if filepath.ToSlash(path) == "../../internal/catalog/query.sql" {
			gallery = productGallery.FindStringIndex(sources[path])
		}
		for _, at := range matchesAPhotographToAVariant.FindAllStringIndex(src, -1) {
			switch {
			case within(at, view):
				inView++
			case within(at, gallery):
				inGallery++
			default:
				t.Errorf("%s:%s matches a photograph to a variant itself.\n"+
					"  Read variant_primary_image (product_id, variant_id) instead, so the "+
					"photograph a line shows is the one the product page opens on.",
					path, migrationLine(src, at[0]))
			}
		}
	}
	if inView != 1 {
		t.Errorf("found the rule %d times inside CREATE VIEW variant_primary_image, want 1: "+
			"the view is gone or no longer applies it, so this test guards nothing", inView)
	}
	if inGallery != 1 {
		t.Errorf("found the rule %d times in catalog.ProductImages, want 1", inGallery)
	}
}

func within(at, span []int) bool {
	return len(span) == 2 && at[0] >= span[0] && at[1] <= span[1]
}
