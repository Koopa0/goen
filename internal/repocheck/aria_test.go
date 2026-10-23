package repocheck_test

import (
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestEveryRefusedFieldNamesItsError holds the half of a rejection only a
// screen reader depends on. aria-invalid announces THAT a field is wrong; the
// reason is a paragraph beside it, and the two are connected by nothing unless
// aria-describedby says so. The corpus is derived from the SOURCE rather than a
// list, so a new template is covered the moment it uses aria-invalid.
func TestEveryRefusedFieldNamesItsError(t *testing.T) {
	t.Parallel()

	sources := ariaTemplates(t)

	// An id is an error only if the element carrying it is one. A field
	// described by its own HINT satisfies "points at something that exists"
	// while announcing the rules it has just broken and never the refusal.
	errorID := regexp.MustCompile(
		`<[^>]*(?:class="[^"]*(?:ui-error-text|ui-alert--error)[^"]*"|role="alert")[^>]*\bid="([^"]+)"|` +
			`<[^>]*\bid="([^"]+)"[^>]*(?:class="[^"]*(?:ui-error-text|ui-alert--error)[^"]*"|role="alert")`)
	describedBy := regexp.MustCompile(`aria-describedby=(?:"([^"]*)"|\{ [^}]*"([^"]+)"[^}]*\})`)

	var covered int
	for _, name := range slices.Sorted(maps.Keys(sources)) {
		src := sources[name]
		if !strings.Contains(src, "aria-invalid") {
			continue
		}
		covered++

		// Every element that can be marked invalid carries a describedby.
		invalids := strings.Count(src, "aria-invalid")
		if got := len(describedBy.FindAllString(src, -1)); got < invalids {
			t.Errorf("%s marks %d fields invalid and attaches %d messages — "+
				"a field announced as invalid with no reason attached",
				name, invalids, got)
		}

		// And every id it names is an ERROR that this file defines.
		defined := map[string]bool{}
		for _, m := range errorID.FindAllStringSubmatch(src, -1) {
			for _, g := range m[1:] {
				if g != "" {
					defined[g] = true
				}
			}
		}
		for _, m := range describedBy.FindAllStringSubmatch(src, -1) {
			for ref := range strings.FieldsSeq(m[1] + " " + m[2]) {
				if !strings.HasSuffix(ref, "-error") {
					continue // a hint is a legitimate second reference
				}
				if !defined[ref] {
					t.Errorf("%s points a refused field at %q, which it does not "+
						"render as an error", name, ref)
				}
			}
		}
	}

	// A pattern that matched nothing would report a clean sweep of an empty set.
	if covered < 10 {
		t.Errorf("only %d templates were examined; the corpus is 13 files deep", covered)
	}
}

// TestEveryRefusableControlCanBeMarkedInvalid is the direction its neighbour
// cannot look: that one starts from aria-invalid and asks what it points at, so
// a refusable control that never gets the attribute is invisible to it. The
// corpus is every field a view model can carry an error for, derived from the
// Err/HasErr calls in the templates rather than from a list.
func TestEveryRefusableControlCanBeMarkedInvalid(t *testing.T) {
	t.Parallel()

	sources := ariaTemplates(t)
	// v.Err("name") / v.HasErr("name") — the view model saying this field is
	// refusable, which is the definition the templates already use.
	refusable := regexp.MustCompile(`\.(?:Has)?Err\("([a-z_]+)"\)`)

	checked := 0
	for _, name := range slices.Sorted(maps.Keys(sources)) {
		src := sources[name]
		fields := map[string]bool{}
		for _, m := range refusable.FindAllStringSubmatch(src, -1) {
			fields[m[1]] = true
		}
		if len(fields) == 0 {
			continue
		}
		checked++
		for field := range fields {
			// Three legitimate shapes, and the third is the one the back office
			// uses everywhere: the attribute lives INSIDE the same HasErr block
			// that renders the message. Never a blanket `Invalid(name)` — the
			// checkout's field() helper contains one, so accepting it would pass
			// every field in that file including controls carrying nothing.
			marked := strings.Contains(src, `Invalid("`+field+`")`) ||
				strings.Contains(src, `@field("`+field+`"`)
			for _, block := range guardedBlocks(src, field) {
				// role="alert" is announced on render, so a FORM-level banner
				// needs no control marked: there is no one control it refers to.
				if strings.Contains(block, "aria-invalid") || strings.Contains(block, `role="alert"`) {
					marked = true
				}
			}
			if !marked {
				t.Errorf("%s can refuse %q and never marks it aria-invalid — the "+
					"message renders on screen and a screen reader is told nothing",
					name, field)
			}
		}
	}
	if checked < 5 {
		t.Fatalf("only %d templates carry a refusable field; the pattern stopped matching", checked)
	}
}

// guardedBlocks is the body of every `if …HasErr("field") {` block in src,
// matched by counting braces rather than by a regex: templ nests, and a
// pattern stopping at the first close brace reads half a block.
func guardedBlocks(src, field string) []string {
	var out []string
	needle := `HasErr("` + field + `") {`
	for i := 0; ; {
		at := strings.Index(src[i:], needle)
		if at < 0 {
			return out
		}
		start := i + at + len(needle)
		depth := 1
		j := start
		for ; j < len(src) && depth > 0; j++ {
			switch src[j] {
			case '{':
				depth++
			case '}':
				depth--
			}
		}
		out = append(out, src[start:j])
		i = j
	}
}

// fieldComponents is the one template the aria checks leave out: widget.templ,
// the shared field components.
const fieldComponents = "internal/ui/widget/widget.templ"

// ariaTemplates is every template under internal/ that writes its own markup,
// which is all of them except widget.templ, the shared field components. Those
// write aria-invalid from a Go expression (aria-describedby={ p.Describes }) that
// the describedby pattern above cannot read; their contract, that Invalid is
// always paired with Describes, is checked at every call site by
// TestEveryRefusedFieldStillNamesItsError. A rename of that file stops the test
// rather than leaving the exemption on nothing.
func ariaTemplates(t *testing.T) map[string]string {
	t.Helper()
	sources := templSources(t)
	if _, ok := sources[fieldComponents]; !ok {
		t.Fatalf("%s is gone: point fieldComponents at the shared field components", fieldComponents)
	}
	delete(sources, fieldComponents)
	return sources
}
