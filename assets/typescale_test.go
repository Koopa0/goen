package assets

import (
	"io/fs"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// typeCapRem is the largest a storefront font may be drawn, in rem: the
// display size, --type-display-l's upper bound. One title above it outshouts
// the page's one display line.
const typeCapRem = 3.5

var (
	// fontDecl finds a font-size or font shorthand declaration, or a token of
	// the storefront's scale that a font shorthand reads; customAny any custom
	// property; cssLength one length.
	fontDecl  = regexp.MustCompile(`(?:^|[;\s])(font-size|font|--type-[a-z0-9-]+):\s*([^;]+);`)
	customAny = regexp.MustCompile(`(--[a-z0-9-]+):\s*([^;{}]+);`)
	cssLength = regexp.MustCompile(`^(-?\d*\.?\d+)(rem|px|em|%|[a-z]*v[whib]|vmin|vmax|cq[whib]|cqmin|cqmax)$`)
)

// TestAppCSSTypeCap checks that no storefront rule draws a font larger than the
// display size. It reads every font-size and font declaration in app.css and
// every --type-* token, follows each var() to its declarations in base.css and
// app.css, and takes the largest size the value can compute to: the upper bound
// of a clamp(), the least argument of a min(). A size in viewport or container
// units with no rem bound grows without limit, so it fails as well.
func TestAppCSSTypeCap(t *testing.T) {
	t.Parallel()

	sheets := make(map[string]string)
	for _, name := range []string{BaseCSS, AppCSS} {
		body, err := fs.ReadFile(files, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		sheets[name] = cssComment.ReplaceAllString(string(body), "")
	}
	custom := make(map[string][]string)
	for _, name := range []string{BaseCSS, AppCSS} {
		for _, m := range customAny.FindAllStringSubmatch(sheets[name], -1) {
			custom[m[1]] = append(custom[m[1]], strings.TrimSpace(m[2]))
		}
	}

	checked := 0
	for _, rule := range cssRule.FindAllStringSubmatch(sheets[AppCSS], -1) {
		selector := strings.Join(strings.Fields(rule[1]), " ")
		for _, d := range fontDecl.FindAllStringSubmatch(rule[2], -1) {
			prop, value := d[1], strings.TrimSpace(d[2])
			shorthand := prop != "font-size"
			for _, v := range expandVars(value, custom, 0) {
				size, ok := maxRem(v, shorthand)
				if !ok {
					continue
				}
				checked++
				if size > typeCapRem {
					t.Errorf("%s: %s { %s: %s } can draw at %s, above the %grem display size",
						AppCSS, selector, prop, value, remString(size), typeCapRem)
				}
			}
		}
	}
	if checked < 100 {
		t.Fatalf("read %d font sizes in %s, want the storefront's hundreds", checked, AppCSS)
	}
}

func remString(size float64) string {
	if math.IsInf(size, 1) {
		return "any size (a viewport or container unit with no rem bound)"
	}
	return strconv.FormatFloat(size, 'f', -1, 64) + "rem"
}

// expandVars returns value with each var() replaced by a declaration of the
// property it names, one result for each combination of declarations; a var()
// naming nothing takes its fallback, if any.
func expandVars(value string, custom map[string][]string, depth int) []string {
	start := strings.Index(value, "var(")
	if start < 0 || depth > 8 {
		return []string{value}
	}
	end := closingParen(value, start+len("var"))
	if end < 0 {
		return []string{value}
	}
	name, fallback, _ := strings.Cut(value[start+len("var("):end], ",")
	candidates := custom[strings.TrimSpace(name)]
	if len(candidates) == 0 {
		candidates = []string{strings.TrimSpace(fallback)}
	}
	var out []string
	for _, c := range candidates {
		out = append(out, expandVars(value[:start]+c+value[end+1:], custom, depth+1)...)
	}
	return out
}

// closingParen returns the index of the parenthesis closing the one at open.
func closingParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitTop splits s at each sep outside parentheses.
func splitTop(s string, sep func(rune) bool) []string {
	var parts []string
	depth, from := 0, 0
	for i, r := range s {
		switch {
		case r == '(':
			depth++
		case r == ')':
			depth--
		case depth == 0 && sep(r):
			parts = append(parts, s[from:i])
			from = i + 1
		}
	}
	parts = append(parts, s[from:])
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// maxRem returns the largest size value can compute to, in rem, and false when
// it is not a size, such as inherit. In a font shorthand the size is the last
// term before the line height's slash.
func maxRem(value string, shorthand bool) (float64, bool) {
	if !shorthand {
		return lengthRem(value)
	}
	beforeSlash := splitTop(value, func(r rune) bool { return r == '/' })
	if len(beforeSlash) == 0 {
		return 0, false
	}
	size, found := 0.0, false
	for _, term := range splitTop(beforeSlash[0], func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' }) {
		if v, ok := lengthRem(term); ok {
			size, found = v, true
		}
	}
	return size, found
}

// lengthRem is the largest value of one length term in rem: +Inf for a
// viewport or container unit, which no rem bounds.
func lengthRem(term string) (float64, bool) {
	term = strings.TrimSpace(term)
	if fn, args, ok := cssFunction(term); ok {
		return functionRem(fn, args)
	}
	m := cssLength.FindStringSubmatch(term)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	switch m[2] {
	case "rem", "em":
		return n, true
	case "px":
		return n / 16, true
	case "%":
		return n / 100, true
	}
	return math.Inf(1), true
}

// functionRem is the largest value of a clamp(), min(), max() or calc() in rem.
func functionRem(fn, args string) (float64, bool) {
	list := splitTop(args, func(r rune) bool { return r == ',' })
	var sizes []float64
	for _, a := range list {
		if v, ok := lengthRem(a); ok {
			sizes = append(sizes, v)
		}
	}
	switch fn {
	case "clamp":
		// The upper bound; the preferred value never draws above it.
		if len(list) != 3 {
			return 0, false
		}
		return lengthRem(list[2])
	case "min":
		if len(sizes) == 0 {
			return 0, false
		}
		least := sizes[0]
		for _, v := range sizes[1:] {
			least = math.Min(least, v)
		}
		return least, true
	case "max":
		return maxOf(sizes)
	case "calc":
		// A sum: each length counted at its largest.
		total, found := 0.0, false
		for _, part := range splitTop(args, func(r rune) bool { return r == '+' || r == '-' || r == ' ' }) {
			if v, ok := lengthRem(part); ok {
				total, found = total+math.Abs(v), true
			}
		}
		return total, found
	}
	return 0, false
}

func maxOf(sizes []float64) (float64, bool) {
	if len(sizes) == 0 {
		return 0, false
	}
	most := sizes[0]
	for _, v := range sizes[1:] {
		most = math.Max(most, v)
	}
	return most, true
}

// cssFunction splits a whole term such as clamp(a, b, c) into its name and
// its arguments.
func cssFunction(term string) (name, args string, ok bool) {
	open := strings.IndexByte(term, '(')
	if open <= 0 || !strings.HasSuffix(term, ")") || closingParen(term, open) != len(term)-1 {
		return "", "", false
	}
	return term[:open], term[open+1 : len(term)-1], true
}
