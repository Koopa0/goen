package pages

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEveryRefusedFieldNamesItsError holds the half of a rejection that only one
// person needs and nobody else can see.
//
// The write-face rule says a refused form re-renders at 422 with the values
// intact and aria-invalid on each control it refused. aria-invalid announces
// THAT a field is wrong; the reason is a paragraph beside it, and the two are
// connected by nothing unless aria-describedby says so. So a screen reader read
// "edit text, invalid" and stopped — the error was on screen, in the DOM, and
// unreachable by the one visitor who could not simply look at it.
//
// Forty-three fields across thirteen templates were in that state, which is
// most of the forms on the site: the checkout's own field() helper had it right
// from the day it was written and nothing else copied it.
//
// Derived from the SOURCE rather than a list, so a fourteenth template fails
// here the moment somebody adds aria-invalid to it.
//
// This is a static check on purpose. check-layout carries the same rule against
// a real browser, and every row it measures is a GET — no page it visits has
// ever rendered a refused field, so that one has no subject today and this is
// where the coverage actually is.
func TestEveryRefusedFieldNamesItsError(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*.templ")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates to read: %v", err)
	}

	// An id is an error only if the element carrying it is one. A field
	// described by its own HINT satisfies "points at something that exists"
	// while announcing the rules it has just broken and never the refusal.
	errorID := regexp.MustCompile(
		`<[^>]*(?:class="[^"]*(?:ui-error-text|ui-alert--error)[^"]*"|role="alert")[^>]*\bid="([^"]+)"|` +
			`<[^>]*\bid="([^"]+)"[^>]*(?:class="[^"]*(?:ui-error-text|ui-alert--error)[^"]*"|role="alert")`)
	describedBy := regexp.MustCompile(`aria-describedby=(?:"([^"]*)"|\{ [^}]*"([^"]+)"[^}]*\})`)

	var covered int
	for _, name := range files {
		body, err := os.ReadFile(name) //nolint:gosec // G304: paths come from globbing this package
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src := string(body)
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
// cannot look.
//
// That one starts from `aria-invalid` and asks what it points at, so a control a
// form can REFUSE that never gets the attribute at all is invisible to it. The
// checkout's three choosers were exactly that: they were <a> elements until the
// chooser moved inside the form, so the rule genuinely did not apply — and
// making them controls a form can refuse did not carry it across. A screen
// reader was told nothing at all about a refused delivery method.
//
// The corpus is every field a view model can carry an error for, derived from
// the Err/HasErr calls in the templates rather than from a list.
func TestEveryRefusableControlCanBeMarkedInvalid(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*.templ")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates to read: %v", err)
	}
	// v.Err("name") / v.HasErr("name") — the view model saying this field is
	// refusable, which is the definition the templates already use.
	refusable := regexp.MustCompile(`\.(?:Has)?Err\("([a-z_]+)"\)`)

	checked := 0
	for _, name := range files {
		body, readErr := os.ReadFile(name) //nolint:gosec // G304: globbed from this package
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		src := string(body)
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
			// that renders the message. A guard looking only for Invalid(field)
			// reported forty-three correct fields as defects.
			// Never a blanket `Invalid(name)`: the checkout's field() helper
			// contains it, so accepting it made every field in that file pass —
			// including the three radio groups that carry nothing at all. The
			// helper covers exactly the names it is CALLED with.
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
