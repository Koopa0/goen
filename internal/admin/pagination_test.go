package admin

import (
	"encoding/base64"
	"net/url"
	"testing"
)

func TestPageCursorKeepsItsQueueAndFilters(t *testing.T) {
	t.Parallel()
	scope := pageURL("/admin/orders", "q", "two words & more", "status", "pending")
	b := (pageCursor{}).bound(scope, true, 50, `{"ID":"12345678-1234-1234-1234-123456789abc","At":"2026-09-22T01:02:03.123456Z"}`)
	u, err := url.Parse(b.Next)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("q") != "two words & more" || u.Query().Get("status") != "pending" {
		t.Fatal("pagination dropped the filters")
	}
	raw := u.Query().Get("after")
	c := readPageCursor(scope, []string{raw})
	if !c.Valid || c.At.Nanosecond() != 123456000 {
		t.Fatal("cursor lost the database timestamp precision")
	}
	if readPageCursor("/admin/orders?q=changed", []string{raw}).Valid {
		t.Fatal("cursor escaped its filter scope")
	}
	for _, bad := range []string{"!", base64.RawURLEncoding.EncodeToString([]byte(scope + "\n{}")), base64.RawURLEncoding.EncodeToString([]byte(scope + "\nnull"))} {
		if readPageCursor(scope, []string{bad}).Valid {
			t.Fatalf("invalid cursor accepted: %q", bad)
		}
	}
	empty := c.bound(scope, false, 50, "")
	if empty.First != scope || !empty.Empty || empty.Next != "" {
		t.Fatal("empty later page lost its restart door")
	}
}
