package assets

import (
	"bytes"
	"strings"
	"testing"
)

// TestStripRemoteImportsOnAdversarialStylesheets is one case per way a remote
// @import can be written, and one per way something that only looks like one
// can be. The first regex this was written with passed the plain case and
// failed four of these; the cases are here so the next implementation cannot.
func TestStripRemoteImportsOnAdversarialStylesheets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    string
		changed bool
	}{
		{
			name:    "the one the design system actually ships",
			in:      "@import url('https://fonts.googleapis.com/css2?family=Inter');\n.a { color: red; }\n",
			want:    ".a { color: red; }\n",
			changed: true,
		},
		{
			name:    "split across two lines",
			in:      "@import\n  url(\"https://fonts.googleapis.com/css2\");\n.a { color: red; }\n",
			want:    ".a { color: red; }\n",
			changed: true,
		},
		{
			name:    "a bare string with no url()",
			in:      "@import \"https://example.test/t.css\";\n.a { color: red; }\n",
			want:    ".a { color: red; }\n",
			changed: true,
		},
		{
			name:    "scheme-relative",
			in:      "@import url(//example.test/t.css);\n.a { color: red; }\n",
			want:    ".a { color: red; }\n",
			changed: true,
		},
		{
			name:    "with a media query after it",
			in:      "@import url('https://example.test/t.css') screen and (min-width: 40em);\n.a { color: red; }\n",
			want:    ".a { color: red; }\n",
			changed: true,
		},
		{
			name:    "a comment between the at-rule and its url",
			in:      "@import /* why */ url('https://example.test/t.css');\n.a { color: red; }\n",
			want:    ".a { color: red; }\n",
			changed: true,
		},
		{
			name: "inside a comment, and the comment must survive intact",
			in: "/* we used to write\n@import url(https://fonts.googleapis.com/css2);\nand stopped */\n" +
				".a { color: red; }\n",
			want: "/* we used to write\n@import url(https://fonts.googleapis.com/css2);\nand stopped */\n" +
				".a { color: red; }\n",
			changed: false,
		},
		{
			name:    "a relative import is how a sheet is assembled",
			in:      "@import \"./tokens.css\";\n@import url(components/x.css);\n.a { color: red; }\n",
			want:    "@import \"./tokens.css\";\n@import url(components/x.css);\n.a { color: red; }\n",
			changed: false,
		},
		{
			name:    "an absolute path is still this origin",
			in:      "@import url(/static/css/x.css);\n.a { color: red; }\n",
			want:    "@import url(/static/css/x.css);\n.a { color: red; }\n",
			changed: false,
		},
		{
			name:    "a data: url names no origin",
			in:      "@import url(data:text/css,.a{color:red});\n",
			want:    "@import url(data:text/css,.a{color:red});\n",
			changed: false,
		},
		{
			name:    "the word inside a string is not a statement",
			in:      ".a::after { content: \"@import url(https://example.test/x.css);\"; }\n",
			want:    ".a::after { content: \"@import url(https://example.test/x.css);\"; }\n",
			changed: false,
		},
		{
			name:    "one remote and one local, and only one goes",
			in:      "@import \"./a.css\";\n@import url('https://example.test/b.css');\n@import \"./c.css\";\n",
			want:    "@import \"./a.css\";\n@import \"./c.css\";\n",
			changed: true,
		},
		{
			name:    "unterminated, which a browser also reads to the end",
			in:      "@import url('https://example.test/t.css')",
			want:    "",
			changed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, changed := stripRemoteImports("vendor.css", []byte(tt.in))
			if changed != tt.changed {
				t.Errorf("changed = %v, want %v", changed, tt.changed)
			}
			if string(got) != tt.want {
				t.Errorf("stripRemoteImports:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

// TestStripRemoteImportsLeavesEverythingThatIsNotCSSAlone: the rewrite is a
// stylesheet rule, and a .js or .json file that happens to contain the word is
// not goen's to edit.
func TestStripRemoteImportsLeavesEverythingThatIsNotCSSAlone(t *testing.T) {
	t.Parallel()

	src := []byte("const s = \"@import url(https://example.test/x.css);\";\n")
	for _, name := range []string{"app.js", "data.json", "notes.txt"} {
		got, changed := stripRemoteImports(name, src)
		if changed || !bytes.Equal(got, src) {
			t.Errorf("%s was rewritten; only stylesheets are", name)
		}
	}
}

// TestIsRemoteNamesTheOriginsThatCount is the decision the rewrite turns on,
// checked on its own so a URL shape cannot be argued about through the scanner.
func TestIsRemoteNamesTheOriginsThatCount(t *testing.T) {
	t.Parallel()

	remote := []string{
		"https://fonts.googleapis.com/css2", "http://example.test/x.css",
		"//example.test/x.css", "HTTPS://EXAMPLE.TEST/x.css", " https://example.test/x ",
	}
	local := []string{
		"./tokens.css", "../tokens.css", "tokens.css", "/static/css/x.css",
		"data:text/css,.a{}", "blob:1234", "",
	}
	for _, u := range remote {
		if !isRemote(u) {
			t.Errorf("isRemote(%q) = false; it names another origin", u)
		}
	}
	for _, u := range local {
		if isRemote(u) {
			t.Errorf("isRemote(%q) = true; it names no other origin", u)
		}
	}
}

// TestTheVendoredSheetIsTheOneThatNeedsIt keeps the reason for this code
// visible: the design system's token sheet is where the remote @import lives.
func TestTheVendoredSheetIsTheOneThatNeedsIt(t *testing.T) {
	t.Parallel()

	served, rewritten := catalogue.bodies["css/ds/colors_and_type.css"]
	if !rewritten {
		t.Skip("the vendored token sheet no longer imports a font host; " +
			"stripRemoteImports may be able to go")
	}
	if strings.Contains(string(served), "fonts.googleapis.com") {
		t.Error("the served token sheet still names fonts.googleapis.com")
	}

	// And the sheet that assembles the design system out of its own parts keeps
	// every one of them: the rewrite is about other origins, not about @import.
	entry, err := files.ReadFile(DesignSystemCSS)
	if err != nil {
		t.Fatalf("read %s: %v", DesignSystemCSS, err)
	}
	if _, touched := catalogue.bodies[DesignSystemCSS]; touched {
		t.Errorf("%s was rewritten; its imports are all relative", DesignSystemCSS)
	}
	if want := strings.Count(string(entry), "@import"); want == 0 {
		t.Fatalf("%s imports nothing; the fixture this test rests on has moved", DesignSystemCSS)
	}
}
