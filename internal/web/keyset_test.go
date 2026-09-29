package web

import (
	"encoding/base64"
	"net/url"
	"testing"
	"time"
)

type testPosition struct {
	ID string
	At time.Time
}

func TestKeysetTokenKeepsItsScopeAndPrecision(t *testing.T) {
	t.Parallel()
	scope := ScopeURL("/admin/orders", "q", "two words & more", "status", "pending")
	next, ok := NextKeysetURL(scope, `{"ID":"a","At":"2026-09-22T01:02:03.123456Z"}`)
	if !ok {
		t.Fatal("scope refused")
	}
	u, err := url.Parse(next)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("q") != "two words & more" || u.Query().Get("status") != "pending" {
		t.Fatal("next link dropped the filters")
	}
	token := u.Query().Get(KeysetParam)
	p, ok := ReadKeyset[testPosition](scope, token)
	if !ok || p.ID != "a" || p.At.Nanosecond() != 123456000 {
		t.Fatalf("token lost its position or the database timestamp precision: %+v %v", p, ok)
	}
	if _, ok := ReadKeyset[testPosition]("/admin/orders?q=changed", token); ok {
		t.Fatal("token escaped its filter scope")
	}
}

func TestReadKeysetRefusesMalformedTokens(t *testing.T) {
	t.Parallel()
	scope := "/admin/orders"
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	for name, bad := range map[string]string{
		"empty":           "",
		"not base64":      "!",
		"no separator":    enc("{}"),
		"wrong scope":     enc("/admin/stock\n{}"),
		"not json":        enc(scope + "\nnot json"),
		"wrong json type": enc(scope + "\n[1]"),
		"oversize":        enc(scope + "\n{\"ID\":\"" + string(make([]byte, 4096)) + "\"}"),
	} {
		if _, ok := ReadKeyset[testPosition](scope, bad); ok {
			t.Errorf("%s: malformed token accepted", name)
		}
	}
}

// TestPageOfSaysWhenThereIsMore covers the boundary, which is the only place
// this can be wrong: at exactly a page there is nothing more to say, and at one
// row past it there is.
func TestPageOfSaysWhenThereIsMore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rows     int
		size     int
		wantPage int
		wantMore bool
	}{
		{name: "empty", rows: 0, size: 50, wantPage: 0, wantMore: false},
		{name: "one", rows: 1, size: 50, wantPage: 1, wantMore: false},
		{name: "exactly a page", rows: 50, size: 50, wantPage: 50, wantMore: false},
		{name: "one past a page", rows: 51, size: 50, wantPage: 50, wantMore: true},
		{name: "a page of one", rows: 2, size: 1, wantPage: 1, wantMore: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rows := make([]int, tt.rows)
			for i := range rows {
				rows[i] = i
			}
			page, more := PageOf(rows, tt.size)
			if len(page) != tt.wantPage {
				t.Errorf("page holds %d rows, want %d", len(page), tt.wantPage)
			}
			if more != tt.wantMore {
				t.Errorf("more = %v, want %v", more, tt.wantMore)
			}
			// The extra row must not survive: a count taken downstream from the
			// page must never read one more than the page shows.
			for i, v := range page {
				if v != i {
					t.Errorf("page[%d] = %d; PageOf reordered or dropped the wrong row", i, v)
				}
			}
		})
	}
}

func TestScopeURLOmitsEmptyFilters(t *testing.T) {
	t.Parallel()
	if got := ScopeURL("/admin/stock", "low", ""); got != "/admin/stock" {
		t.Fatal(got)
	}
}
