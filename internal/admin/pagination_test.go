package admin

import (
	"encoding/base64"
	"net/url"
	"testing"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/web"
)

func nextToken(t *testing.T, scope, position string) string {
	t.Helper()
	next, ok := web.NextKeysetURL(scope, position)
	if !ok {
		t.Fatal("scope refused")
	}
	u, err := url.Parse(next)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(web.KeysetParam)
}

func TestReadPageCursorAcceptsOnlyUsablePositions(t *testing.T) {
	t.Parallel()
	scope := web.ScopeURL("/admin/orders", "q", "x")
	const id = `"ID":"12345678-1234-1234-1234-123456789abc"`
	c := readPageCursor(scope, []string{nextToken(t, scope, `{`+id+`,"At":"2026-09-22T01:02:03.123456Z"}`)})
	if !c.Valid || c.At.Nanosecond() != 123456000 {
		t.Fatal("cursor lost the database timestamp precision")
	}
	if readPageCursor("/admin/orders?q=changed", []string{nextToken(t, scope, `{`+id+`}`)}).Valid {
		t.Fatal("cursor escaped its filter scope")
	}
	for name, position := range map[string]string{
		"no id":          `{}`,
		"null":           `null`,
		"nil id":         `{"ID":"00000000-0000-0000-0000-000000000000"}`,
		"int32 overflow": `{` + id + `,"Number":99999999999}`,
		"NUL in name":    `{` + id + `,"Name":"\u0000"}`,
	} {
		if readPageCursor(scope, []string{nextToken(t, scope, position)}).Valid {
			t.Errorf("%s: invalid cursor accepted", name)
		}
	}
	if readPageCursor(scope, []string{base64.RawURLEncoding.EncodeToString([]byte("junk"))}).Valid || readPageCursor(scope, nil).Valid {
		t.Fatal("garbage accepted")
	}
}

func TestPageBoundKeepsARestartDoorOnAnEmptyLaterPage(t *testing.T) {
	t.Parallel()
	scope := "/admin/orders"
	key := func(r *db.Order) string { return "" }
	later := pageCursor{Valid: true}
	_, empty := pageBound(later, scope, []db.Order{}, PageSize, key)
	if empty.First != scope || !empty.PastEnd || empty.Next != "" {
		t.Fatal("empty later page lost its restart door")
	}
	_, first := pageBound(pageCursor{}, scope, []db.Order{}, PageSize, key)
	if first.PastEnd || first.First != "" {
		t.Fatal("an empty first page must keep its own empty state")
	}
}
