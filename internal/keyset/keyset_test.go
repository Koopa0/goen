package keyset

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

// TestNextURLRoundTrip checks that a token made by NextURL keeps its list's
// scope and the database timestamp's precision when Read gives it back, and is
// refused under another scope.
func TestNextURLRoundTrip(t *testing.T) {
	t.Parallel()
	scope := ScopeURL("/admin/orders", "q", "two words & more", "status", "pending")
	next, ok := NextURL(scope, `{"ID":"a","At":"2026-09-22T01:02:03.123456Z"}`)
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
	token := u.Query().Get(Param)
	p, ok := Read[testPosition](scope, token)
	if !ok || p.ID != "a" || p.At.Nanosecond() != 123456000 {
		t.Fatalf("token lost its position or the database timestamp precision: %+v %v", p, ok)
	}
	if _, ok := Read[testPosition]("/admin/orders?q=changed", token); ok {
		t.Fatal("token escaped its filter scope")
	}
}

// TestReadMalformed checks that Read refuses malformed tokens.
func TestReadMalformed(t *testing.T) {
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
		if _, ok := Read[testPosition](scope, bad); ok {
			t.Errorf("%s: malformed token accepted", name)
		}
	}
}

// TestPageOf covers the boundary, which is the only place
// this can be wrong: at exactly a page there is nothing more to say, and at one
// row past it there is.
func TestPageOf(t *testing.T) {
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

const testPageSize = 50

// TestPageLimit checks that PageLimit asks for one more row than PageSize
// shows.
//
// A query asks for one more row than it shows; a limit equal to the size could
// never report that anything was left behind.
func TestPageLimit(t *testing.T) {
	t.Parallel()
	if PageLimit != PageSize+1 {
		t.Errorf("PageLimit = %d and PageSize = %d", PageLimit, PageSize)
	}
}

// TestPageBoundEmptyPage checks that PageBound keeps a restart door on an empty
// later page and leaves an empty first page in its own empty state.
func TestPageBoundEmptyPage(t *testing.T) {
	t.Parallel()
	scope := "/admin/orders"
	key := func(*int) string { return "" }
	_, empty := PageBound(scope, true, []int{}, testPageSize, key)
	if empty.First != scope || !empty.PastEnd || empty.Next != "" {
		t.Fatal("empty later page lost its restart door")
	}
	_, first := PageBound(scope, false, []int{}, testPageSize, key)
	if first.PastEnd || first.First != "" {
		t.Fatal("an empty first page must keep its own empty state")
	}
}

// TestResumeAcceptable checks that Resume returns a position the list accepts
// and refuses one the list cannot accept, and a request that carries none.
func TestResumeAcceptable(t *testing.T) {
	t.Parallel()
	const scope = "/admin/x"
	accept := func(p testPosition) bool { return p.ID != "" }
	good, _ := NextURL(scope, `{"ID":"a"}`)
	token := func(u string) []string { return []string{u[len(scope)+len("?after="):]} }
	if p, ok := Resume(scope, token(good), accept); !ok || p.ID != "a" {
		t.Errorf("a minted position was refused: %v %v", p, ok)
	}
	empty, _ := NextURL(scope, `{"ID":""}`)
	if _, ok := Resume(scope, token(empty), accept); ok {
		t.Error("a position the list refuses was accepted")
	}
	if _, ok := Resume(scope, nil, accept); ok {
		t.Error("no position resumed")
	}
}
