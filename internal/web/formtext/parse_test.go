package formtext

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		{name: "lone byte in a value", body: "name=x&message=%E9%E9%E9", want: ErrUnstorable},
		{name: "truncated sequence in a value", body: "name=%E7%8E", want: ErrUnstorable},
		{name: "overlong encoding in a value", body: "name=%C0%AF", want: ErrUnstorable},
		{name: "not UTF-8 in a name", body: "%E9=x", want: ErrUnstorable},
		{name: "not UTF-8 in the query", query: "?next=%E9", body: "name=x", want: ErrUnstorable},
		{name: "NUL in a value", body: "email=a%00%40example.com", want: ErrUnstorable},
		{name: "NUL in a name", body: "na%00me=x", want: ErrUnstorable},
		{name: "NUL in the query", query: "?number=GO%00", body: "name=x", want: ErrUnstorable},
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

func TestParseLongTextFormKeepsTextAndBoundsTheEncodedBody(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name          string
		query         string
		body          string
		runes         int
		limit         int64
		wantText      string
		wantTextError bool
		wantSizeError bool
	}{
		{name: "twenty thousand four-byte runes", body: url.Values{"message": {strings.Repeat("\U0001F331", 20000)}}.Encode(), runes: 20000, limit: 305536, wantText: strings.Repeat("\U0001F331", 20000)},
		{name: "exact byte limit", body: "message=" + strings.Repeat("x", 65552), runes: 2, limit: 65560, wantText: strings.Repeat("x", 65552)},
		{name: "one byte over", body: "message=" + strings.Repeat("x", 65553), runes: 2, limit: 65560, wantSizeError: true},
		{name: "invalid UTF-8", body: "message=%E9", runes: 20000, limit: 305536, wantTextError: true},
		{name: "NUL", body: "message=%00", runes: 20000, limit: 305536, wantTextError: true},
		{name: "invalid query text", query: "?next=%00", body: "message=valid", runes: 20000, limit: 305536, wantTextError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/form"+tt.query, strings.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			err := ParseLongTextForm(httptest.NewRecorder(), r, tt.runes)
			if tt.wantSizeError {
				size, ok := errors.AsType[*http.MaxBytesError](err)
				if !ok || size.Limit != tt.limit {
					t.Fatalf("encoded body refusal=%v, want byte limit %d", err, tt.limit)
				}
				return
			}
			if tt.wantTextError {
				if !errors.Is(err, ErrUnstorable) {
					t.Fatalf("unstorable text refusal=%v, want ErrUnstorable", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := r.PostFormValue("message"); got != tt.wantText {
				t.Errorf("parsed text length/value=%d/%q, want %d bytes unchanged", len(got), got[:min(len(got), 16)], len(tt.wantText))
			}
		})
	}
}

func TestOrdinaryFormsKeepTheirExistingBodyLimit(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/contact", strings.NewReader("message="+strings.Repeat("x", 64<<10)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	err := ParseForm(httptest.NewRecorder(), r)
	size, ok := errors.AsType[*http.MaxBytesError](err)
	if !ok || size.Limit != 65536 {
		t.Fatalf("ordinary form refusal=%v, want 65536-byte cap", err)
	}
}
