package admin

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/ui/pages"
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
	var c pageCursor
	if len(after) == 0 || len(after[0]) > 4096 {
		return c
	}
	raw, err := base64.RawURLEncoding.DecodeString(after[0])
	if err != nil {
		return c
	}
	saved, body, ok := strings.Cut(string(raw), "\n")
	if !ok || saved != scope || json.Unmarshal([]byte(body), &c) != nil || c.ID == uuid.Nil {
		return pageCursor{}
	}
	c.Valid = true
	return c
}

func (c pageCursor) bound(scope string, more bool, limit int, last string) pages.ListBound {
	b := pages.Bound(more, limit)
	b.Paged = true
	b.Empty = last == ""
	if c.Valid {
		b.First = scope
	}
	if more && last != "" {
		token := base64.RawURLEncoding.EncodeToString([]byte(scope + "\n" + last))
		u, err := url.Parse(scope)
		if err != nil {
			return b
		}
		q := u.Query()
		q.Set("after", token)
		u.RawQuery = q.Encode()
		b.Next = u.String()
	}
	return b
}

func pageURL(path string, pairs ...string) string {
	q := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			q.Set(pairs[i], pairs[i+1])
		}
	}
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}
