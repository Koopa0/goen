package account

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

const (
	orderPageSize = 20
	ordersScope   = "/account"
	ordersAnchor  = "#orders-heading"
)

// orderPosition's Owner binds a token to one account, so a token minted for
// another customer's list is refused here as well as by the query's user_id
// predicate.
type orderPosition struct {
	ID    uuid.UUID
	At    time.Time
	Owner string
}

type orderCursor struct {
	orderPosition

	Valid bool
}

func readOrderCursor(owner string, after []string) orderCursor {
	if len(after) == 0 {
		return orderCursor{}
	}
	pos, ok := web.ReadKeyset[orderPosition](ordersScope, after[0])
	if !ok || pos.ID == uuid.Nil || pos.Owner != owner {
		return orderCursor{}
	}
	return orderCursor{orderPosition: pos, Valid: true}
}

func orderBound[T any](c orderCursor, owner string, rows []T, key func(*T) (uuid.UUID, time.Time)) ([]T, pages.ListBound) {
	rows, more := web.PageOf(rows, orderPageSize)
	var b pages.ListBound
	if c.Valid {
		b.First = ordersScope + ordersAnchor
	}
	if len(rows) == 0 {
		b.PastEnd = c.Valid
		return rows, b
	}
	if more {
		id, at := key(&rows[len(rows)-1])
		if body, err := json.Marshal(orderPosition{ID: id, At: at, Owner: owner}); err == nil {
			if next, ok := web.NextKeysetURL(ordersScope, string(body)); ok {
				b.Next = next + ordersAnchor
			}
		}
	}
	return rows, b
}
