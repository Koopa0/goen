package pages

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// fieldProps matches one components.FieldProps literal, from the brace that
// opens it to the one that closes it. Nested braces inside are attribute maps,
// which is why this is not a lazy match to the first closing brace.
var fieldProps = regexp.MustCompile(`(?s)components\.FieldProps\{(.*?)\n\s*\}\)`)

// TestEveryRefusedFieldStillNamesItsError is the other half of
// TestEveryRefusedFieldNamesItsError, and it exists because the first half
// stopped covering these pages.
//
// That test derives its corpus from templates that write aria-invalid
// themselves. The field components write it instead, from one package away, so
// a page that marks a field invalid without naming the element carrying the
// reason is now invisible to it: the attribute is in components.templ, and the
// decision is here. A screen reader would announce "invalid" and never the
// reason — the exact failure the original guard was built to refuse.
//
// So this reads the decision rather than the markup: Invalid without Describes
// is the defect, wherever the attribute is eventually written.
func TestEveryRefusedFieldStillNamesItsError(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*.templ")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates to read: %v", err)
	}

	var offenders []string
	var checked int
	for _, name := range files {
		body, readErr := os.ReadFile(name) //nolint:gosec // G304: paths come from globbing this package
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		src := string(body)
		for _, m := range fieldProps.FindAllStringSubmatchIndex(src, -1) {
			literal := src[m[2]:m[3]]
			if !strings.Contains(literal, "Invalid:") {
				continue
			}
			checked++
			if strings.Contains(literal, "Describes:") {
				continue
			}
			line := 1 + strings.Count(src[:m[0]], "\n")
			offenders = append(offenders, name+":"+strconv.Itoa(line)+"  "+
				strings.Join(strings.Fields(literal), " "))
		}
	}

	if checked == 0 {
		t.Fatal("no FieldProps sets Invalid anywhere; this guard has no subject")
	}
	if len(offenders) > 0 {
		t.Errorf("%d refused fields name no error element. aria-invalid tells a "+
			"screen reader THAT the field is wrong; aria-describedby is the only "+
			"thing that tells it why. Set Describes to the id of the element "+
			"carrying the reason:\n%s", len(offenders), strings.Join(offenders, "\n"))
	}
}
