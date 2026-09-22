package account

import (
	"encoding/base64"
	"strings"
	"time"

	"github.com/google/uuid"
)

const orderPageSize = 20

// orderCursor stores the immutable sort values rather than an offset, so new
// orders do not displace older orders between requests.
type orderCursor struct {
	ID    uuid.UUID
	At    time.Time
	Valid bool
}

func readOrderCursor(owner string, after []string) orderCursor {
	if len(after) == 0 || len(after[0]) > 256 {
		return orderCursor{}
	}
	raw, err := base64.RawURLEncoding.DecodeString(after[0])
	if err != nil {
		return orderCursor{}
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 || parts[0] != owner {
		return orderCursor{}
	}
	at, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		return orderCursor{}
	}
	id, err := uuid.Parse(parts[2])
	if err != nil || id == uuid.Nil {
		return orderCursor{}
	}
	return orderCursor{ID: id, At: at, Valid: true}
}

func nextOrdersURL(owner string, at time.Time, id uuid.UUID) string {
	token := base64.RawURLEncoding.EncodeToString([]byte(owner + "|" + at.Format(time.RFC3339Nano) + "|" + id.String()))
	return "/account?after=" + token + "#orders-heading"
}
