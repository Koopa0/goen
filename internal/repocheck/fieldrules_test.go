package repocheck_test

import (
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// A customer-facing email input takes its pattern, keyboard and message from
// internal/ui/fieldrule, so the browser says what the server would. The back
// office is exempt: its forms are not part of the shopper's checkout. Every
// template under internal/ is read, wherever the package serving it lives.
func TestEveryCustomerEmailFieldCarriesTheSharedRule(t *testing.T) {
	t.Parallel()
	emailInput := regexp.MustCompile(`Type:\s+"email"`)
	sources := templSources(t)
	var checked int
	for _, name := range slices.Sorted(maps.Keys(sources)) {
		if backOffice(name) {
			continue
		}
		src := sources[name]
		inputs := len(emailInput.FindAllString(src, -1))
		if inputs == 0 {
			continue
		}
		checked += inputs
		if rules := strings.Count(src, "fieldrule.Email.Attrs"); rules < inputs {
			t.Errorf("%s has %d email inputs and %d carry fieldrule.Email", name, inputs, rules)
		}
	}
	if checked == 0 {
		t.Fatal("no email inputs found; the census is reading the wrong files")
	}
}

// backOffice reports whether a template path (from the repository root) belongs
// to the staff screens: anything under internal/admin/, and a template whose
// file name starts with "admin" (the back-office layout).
func backOffice(name string) bool {
	return strings.HasPrefix(name, "internal/admin/") ||
		strings.HasPrefix(path.Base(name), "admin")
}
