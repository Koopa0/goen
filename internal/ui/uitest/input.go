package uitest

import (
	"html"
	"regexp"
	"testing"
)

// AssertRefusedInput checks that the input with the id shows the raw text the
// person typed, is marked invalid, and is described by a nonempty error
// element named id+"-error".
func AssertRefusedInput(t *testing.T, body, id, raw string) {
	t.Helper()
	input := InputElementByID(t, body, id)
	if got := InputAttribute(t, input, "value"); got != raw {
		t.Errorf("input %q value = %q, want raw %q", id, got, raw)
	}
	if got := InputAttribute(t, input, "aria-invalid"); got != "true" {
		t.Errorf("input %q aria-invalid = %q, want true", id, got)
	}
	errorID := id + "-error"
	if got := InputAttribute(t, input, "aria-describedby"); got != errorID {
		t.Errorf("input %q aria-describedby = %q, want %q", id, got, errorID)
	}
	if !regexp.MustCompile(`<p[^>]*id="` + regexp.QuoteMeta(errorID) + `"[^>]*>[^<]+</p>`).
		MatchString(body) {
		t.Errorf("input %q has no nonempty error element %q", id, errorID)
	}
}

// AssertTextNumberControl checks that the input with the id is a text field
// with a numeric keyboard and none of the min, max or step a browser would
// ignore on it.
func AssertTextNumberControl(t *testing.T, body, id string) {
	t.Helper()
	input := InputElementByID(t, body, id)
	if got := InputAttribute(t, input, "type"); got != "text" {
		t.Errorf("input %q type = %q, want text", id, got)
	}
	if got := InputAttribute(t, input, "inputmode"); got != "numeric" {
		t.Errorf("input %q inputmode = %q, want numeric", id, got)
	}
	for _, attribute := range []string{"min", "max", "step"} {
		if got := InputAttribute(t, input, attribute); got != "" {
			t.Errorf("input %q retains ineffective %s=%q", id, attribute, got)
		}
	}
}

// InputAttribute returns the unescaped value of the named attribute in an
// input's opening tag, or "" when it has none.
func InputAttribute(t *testing.T, input, name string) string {
	t.Helper()
	match := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `="([^"]*)"`).FindStringSubmatch(input)
	if len(match) != 2 {
		return ""
	}
	return html.UnescapeString(match[1])
}

// InputElementByID returns the opening tag of the input with the id, and
// fails the test when the page has none.
func InputElementByID(t *testing.T, body, id string) string {
	t.Helper()
	match := regexp.MustCompile(`<input\b[^>]*\bid="` + regexp.QuoteMeta(id) + `"[^>]*>`).
		FindString(body)
	if match == "" {
		t.Fatalf("no input with id %q in rendered page", id)
	}
	return match
}

// TextareaByID returns the opening tag and the text of the textarea with the
// id, without the newline a browser drops after the opening tag.
func TextareaByID(t *testing.T, body, id string) (tag, text string) {
	t.Helper()
	match := regexp.MustCompile(`(<textarea\b[^>]*\bid="` + regexp.QuoteMeta(id) + `"[^>]*>)\n?([^<]*)</textarea>`).
		FindStringSubmatch(body)
	if match == nil {
		t.Fatalf("no textarea with id %q in rendered page", id)
	}
	return match[1], html.UnescapeString(match[2])
}
