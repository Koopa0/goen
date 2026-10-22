package formtext

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHasControlChars(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		in   string
		want bool
	}{
		{"plain text", "王小明 Ada", false},
		{"empty", "", false},
		{"newline", "a\nb", true},
		{"NUL", "a\x00b", true},
		{"C1 control", "a\u0085b", true},
	} {
		if got := HasControlChars(tt.in); got != tt.want {
			t.Errorf("HasControlChars(%q) = %t, want %t", tt.in, got, tt.want)
		}
	}
}

// RefuseUnstorable turns back only what PostgreSQL cannot hold.
func TestRefuseUnstorable(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		target  string
		refused bool
	}{
		{"plain", "/search?q=pro", false},
		{"CJK", "/search?q=%E6%89%8B%E6%A9%9F", false},
		{"no query", "/", false},
		{"a pair that does not decode is left alone", "/search?q=%zz&sort=rating", false},
		{"NUL in the path", "/p/%00", true},
		{"not UTF-8 in the path", "/p/%E9", true},
		{"NUL in a value", "/search?q=%00", true},
		{"not UTF-8 in a value", "/search?q=%E9", true},
		{"not UTF-8 in a name", "/search?%E9=1", true},
		{"NUL beside a pair that does not decode", "/search?q=%zz&x=%00", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var reached, refused bool
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
			refuse := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				refused = true
				w.WriteHeader(http.StatusBadRequest)
			})
			w := httptest.NewRecorder()
			RefuseUnstorable(next, false, refuse).ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.target, http.NoBody))
			if refused != tt.refused || reached == tt.refused {
				t.Errorf("%s: refused=%t reached=%t, want refused=%t", tt.target, refused, reached, tt.refused)
			}
		})
	}
}
