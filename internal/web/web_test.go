package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAFormWhoseTextCannotBeStoredIsRefused: a byte sequence that is not UTF-8 passes
// every rune-counting validator and is refused only by PostgreSQL, as a 500.
// ParseForm is the one place every form handler reads through, so it refuses
// the request there, and each caller answers 400.
func TestAFormWhoseTextCannotBeStoredIsRefused(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		query string
		body  string
		want  error
	}{
		{name: "UTF-8", body: "name=%E7%8E%8B%E5%B0%8F%E6%98%8E&message=hello", want: nil},
		{name: "lone byte in a value", body: "name=x&message=%E9%E9%E9", want: ErrFormText},
		{name: "truncated sequence in a value", body: "name=%E7%8E", want: ErrFormText},
		{name: "overlong encoding in a value", body: "name=%C0%AF", want: ErrFormText},
		{name: "not UTF-8 in a name", body: "%E9=x", want: ErrFormText},
		{name: "not UTF-8 in the query", query: "?next=%E9", body: "name=x", want: ErrFormText},
		{name: "NUL in a value", body: "email=a%00%40example.com", want: ErrFormText},
		{name: "NUL in a name", body: "na%00me=x", want: ErrFormText},
		{name: "NUL in the query", query: "?number=GO%00", body: "name=x", want: ErrFormText},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
				"/contact"+tt.query, strings.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			err := ParseForm(httptest.NewRecorder(), r)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ParseForm(%q) = %v, want %v", tt.query+" "+tt.body, err, tt.want)
			}
			if tt.want == nil && r.PostFormValue("name") != "王小明" {
				t.Errorf("name = %q, want the submitted value", r.PostFormValue("name"))
			}
		})
	}
}
