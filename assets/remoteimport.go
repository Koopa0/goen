package assets

import (
	"bytes"
	"net/url"
	"path"
	"strings"
)

// stripRemoteImports removes @import statements that fetch a stylesheet from
// another origin, and reports whether it removed any.
//
// This exists because assets/css/ds/ is vendored and may not be edited, and the
// design system's token sheet opens with an @import of Google Fonts. goen now
// serves its own fonts, so that request is dead weight the visitor pays for —
// and once style-src is 'self' the browser refuses it anyway and reports a
// policy violation on every page load. A violation report that is expected is
// worse than no report at all: it is noise a real one hides in.
//
// The rewrite happens where the file is read rather than where it is written,
// so the vendored bytes on disk stay exactly as the design system shipped them
// and a future version drops in without a patch to re-apply. What goen serves
// is goen's to decide; what it vendors is not goen's to change.
//
// It scans rather than pattern-matches. A regular expression over the whole
// file cannot tell an @import from the word @import inside a comment, and
// deleting a line out of a comment can take the comment's terminator with it
// and break every rule after it. It also cannot follow a statement that is
// written across two lines, which is legal CSS and which a vendor is free to
// start doing. The scanner knows where it is.
func stripRemoteImports(name string, raw []byte) ([]byte, bool) {
	if path.Ext(name) != ".css" || !bytes.Contains(raw, []byte("@import")) {
		return raw, false
	}

	src := string(raw)
	var out bytes.Buffer
	out.Grow(len(raw))
	changed := false

	for i := 0; i < len(src); {
		if end, skipped := skipSpan(src, i); skipped {
			out.WriteString(src[i:end])
			i = end
			continue
		}
		if !strings.HasPrefix(src[i:], "@import") {
			out.WriteByte(src[i])
			i++
			continue
		}
		end := statementEnd(src, i)
		if !importsRemotely(src[i:end]) {
			out.WriteString(src[i:end])
			i = end
			continue
		}
		changed = true
		// Take the line ending with it, so removing a rule does not leave a
		// blank line where the rule was.
		i = pastLineEnd(src, end)
	}
	if !changed {
		return raw, false
	}
	return out.Bytes(), true
}

// skipSpan reports a comment or a string starting at i, and where it ends. Both
// are copied through untouched: what is inside one is not code, and the scanner
// must not read it as any.
func skipSpan(src string, i int) (int, bool) {
	switch {
	case strings.HasPrefix(src[i:], "/*"):
		end := strings.Index(src[i+2:], "*/")
		if end < 0 {
			return len(src), true
		}
		return i + end + 4, true
	case src[i] == '"' || src[i] == '\'':
		return closingQuote(src, i), true
	default:
		return i, false
	}
}

// pastLineEnd returns the index just past the trailing whitespace and newline
// that follow i, if any.
func pastLineEnd(src string, i int) int {
	for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	if i < len(src) && src[i] == '\r' {
		i++
	}
	if i < len(src) && src[i] == '\n' {
		i++
	}
	return i
}

// closingQuote returns the index just past the string starting at i, or the end
// of src for a string nobody closed.
func closingQuote(src string, i int) int {
	quote := src[i]
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			j++
		case quote:
			return j + 1
		case '\n':
			// CSS strings do not span lines; an unterminated one ends here.
			return j
		}
	}
	return len(src)
}

// statementEnd returns the index just past the ";" that ends the at-rule
// starting at i, skipping over comments and strings on the way. A statement
// nobody terminated ends at the end of the file, which is what a browser does
// with it too.
func statementEnd(src string, i int) int {
	for j := i; j < len(src); {
		switch {
		case strings.HasPrefix(src[j:], "/*"):
			end := strings.Index(src[j+2:], "*/")
			if end < 0 {
				return len(src)
			}
			j += end + 4
		case src[j] == '"' || src[j] == '\'':
			j = closingQuote(src, j)
		case src[j] == ';':
			return j + 1
		case src[j] == '{':
			// @import takes no block; a brace means this is some other at-rule
			// and the scanner should not have been here. Stop before it.
			return j
		default:
			j++
		}
	}
	return len(src)
}

// importsRemotely reports whether an @import statement names another origin:
// a scheme followed by //, or the scheme-relative //host form. A relative
// import — @import "./tokens.css" — is how a stylesheet is assembled out of its
// own parts and is left alone.
func importsRemotely(statement string) bool {
	for _, target := range importTargets(statement) {
		if isRemote(target) {
			return true
		}
	}
	return false
}

// isRemote reports whether a URL names an origin other than the one serving it.
//
// net/url decides it rather than a prefix test. A host is exactly what "another
// origin" means — "//host/x.css" has one, "/static/x.css" and "./tokens.css" do
// not, and "data:text/css,…" carries a document rather than a location and has
// none either. Writing that as a string comparison would be a second, worse
// definition of rootedness; internal/web has the one this repository keeps, and
// assets must not depend on a feature package to borrow it.
func isRemote(target string) bool {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil {
		// Unparseable is not a fetch this code should be removing. The sheet is
		// served as it is and the browser decides.
		return false
	}
	return parsed.Host != ""
}

// importTargets pulls the URL out of an @import, in either spelling: a bare
// string, or url() around one.
func importTargets(statement string) []string {
	var out []string
	rest := strings.TrimPrefix(statement, "@import")
	for i := 0; i < len(rest); {
		switch {
		case strings.HasPrefix(rest[i:], "/*"):
			end := strings.Index(rest[i+2:], "*/")
			if end < 0 {
				return out
			}
			i += end + 4
		case rest[i] == '"' || rest[i] == '\'':
			end := closingQuote(rest, i)
			out = append(out, strings.Trim(rest[i:end], `"'`))
			i = end
		case strings.HasPrefix(strings.ToLower(rest[i:]), "url("):
			i += len("url(")
			start := i
			for i < len(rest) && rest[i] != ')' {
				if rest[i] == '"' || rest[i] == '\'' {
					i = closingQuote(rest, i)
					continue
				}
				i++
			}
			out = append(out, strings.Trim(strings.TrimSpace(rest[start:i]), `"'`))
		default:
			i++
		}
	}
	return out
}
