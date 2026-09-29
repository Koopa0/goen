package admin

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// pageCursor holds the ordering values, so deleting the boundary row does not
// erase the reader's position. Scope binds that position to its queue and filters.
type pageCursor struct {
	ID       uuid.UUID
	At       time.Time
	Rank     bool
	Priority bool
	Number   int32
	Name     string
	Position int32
	Valid    bool `json:"-"`
}

func readPageCursor(scope string, after []string) pageCursor {
	if len(after) == 0 {
		return pageCursor{}
	}
	c, ok := web.ReadKeyset[pageCursor](scope, after[0])
	// Postgres refuses a NUL in text, so a crafted name would turn a bad link
	// into a 500 instead of the first page.
	if !ok || c.ID == uuid.Nil || strings.ContainsRune(c.Name, 0) {
		return pageCursor{}
	}
	c.Valid = true
	return c
}

// pageBound trims a read made with PageLimit to its page and builds the
// navigation beside it. key names the last row's position, which each query
// builds in SQL as PageCursor.
func pageBound[T any](c pageCursor, scope string, rows []T, size int, key func(*T) string) ([]T, pages.ListBound) {
	rows, more := web.PageOf(rows, size)
	var b pages.ListBound
	if c.Valid {
		b.First = scope
	}
	if len(rows) == 0 {
		b.PastEnd = c.Valid
		return rows, b
	}
	if more {
		b.Next, _ = web.NextKeysetURL(scope, key(&rows[len(rows)-1]))
	}
	return rows, b
}
