package assets

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// atRuleBody is the text inside the first at-rule that prelude matches from
// its start, and the sheet with that whole at-rule taken out; the match may run
// past the at-rule's brace to pick it by its first rule. found is false where
// nothing matches.
func atRuleBody(sheet string, prelude *regexp.Regexp) (body, rest string, found bool) {
	at := prelude.FindStringIndex(sheet)
	if at == nil {
		return "", sheet, false
	}
	open := strings.IndexByte(sheet[at[0]:], '{')
	if open < 0 {
		return "", sheet, false
	}
	start := at[0] + open + 1
	depth := 1
	for i := start; i < len(sheet); i++ {
		switch sheet[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return sheet[start:i], sheet[:at[0]] + sheet[i+1:], true
			}
		}
	}
	return "", sheet, false
}

// TestAppCSSCheckoutAsDrawn checks the checkout as the board draws it: the
// page's own box, so the name starts where the header's text does, and the
// form beside a summary held in view, two columns only inside a query of that
// box measured in rem. A rule that narrows the box centres the page in it; two
// columns set by a viewport query stay side by side at 200% text, where neither
// has room for its words.
func TestAppCSSCheckoutAsDrawn(t *testing.T) {
	t.Parallel()

	body, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	sheet := cssComment.ReplaceAllString(string(body), "")
	declared := func(sheet, selector string) []string {
		var blocks []string
		for _, rule := range cssRule.FindAllStringSubmatch(sheet, -1) {
			for s := range strings.SplitSeq(rule[1], ",") {
				if strings.Join(strings.Fields(s), " ") == selector {
					blocks = append(blocks, rule[2])
				}
			}
		}
		return blocks
	}
	declares := func(blocks []string, decl string) bool {
		for _, block := range blocks {
			if regexp.MustCompile(decl).MatchString(block) {
				return true
			}
		}
		return false
	}

	maxWidth := regexp.MustCompile(`max-width:\s*([^;]+);`)
	for _, block := range declared(sheet, ".goen-checkout") {
		for _, m := range maxWidth.FindAllStringSubmatch(block, -1) {
			if strings.TrimSpace(m[1]) != "var(--page-box)" {
				t.Errorf("%s: .goen-checkout is narrowed to %s, which centres the page in its box", AppCSS, m[1])
			}
		}
	}
	if !declares(declared(sheet, ".goen-checkout"), `container:\s*checkout\s*/\s*inline-size;`) {
		t.Errorf("%s: .goen-checkout is not the inline-size container named checkout", AppCSS)
	}

	query, rest, found := atRuleBody(sheet, regexp.MustCompile(`@container checkout \(min-width: \d+(\.\d+)?rem\)`))
	if !found {
		t.Fatalf("%s: no @container checkout (min-width: <n>rem) query sets the two columns", AppCSS)
	}
	twoTracks := `grid-template-columns:\s*minmax\(0,\s*\d+fr\)\s+minmax\(0,\s*\d+fr\);`
	if !declares(declared(query, ".goen-checkout__layout"), twoTracks) {
		t.Errorf("%s: inside the checkout's query .goen-checkout__layout is not two columns", AppCSS)
	}
	if !declares(declared(query, ".goen-checkout .goen-checkout__summary"), `position:\s*sticky;`) {
		t.Errorf("%s: inside the checkout's query the summary is not held in view", AppCSS)
	}
	columns := regexp.MustCompile(`grid-template-columns:\s*([^;]+);`)
	for _, block := range declared(rest, ".goen-checkout__layout") {
		for _, m := range columns.FindAllStringSubmatch(block, -1) {
			if strings.TrimSpace(m[1]) != "1fr" {
				t.Errorf("%s: outside the checkout's query .goen-checkout__layout has the columns %s, not one", AppCSS, m[1])
			}
		}
	}
}
