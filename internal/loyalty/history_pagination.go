package loyalty

import (
	"encoding/base64"
	"strings"
	"time"

	"github.com/google/uuid"
)

// historyCursor names one complete ledger group after aggregation, so a spend
// across several award lots cannot be split between pages.
type historyCursor struct {
	ID    uuid.UUID
	At    time.Time
	Valid bool
}

func readHistoryCursor(owner string, after []string) historyCursor {
	if len(after) == 0 || len(after[0]) > 256 {
		return historyCursor{}
	}
	raw, err := base64.RawURLEncoding.DecodeString(after[0])
	if err != nil {
		return historyCursor{}
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 || parts[0] != owner {
		return historyCursor{}
	}
	at, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		return historyCursor{}
	}
	id, err := uuid.Parse(parts[2])
	if err != nil || id == uuid.Nil {
		return historyCursor{}
	}
	return historyCursor{ID: id, At: at, Valid: true}
}

func nextHistoryURL(owner string, at time.Time, id uuid.UUID) string {
	token := base64.RawURLEncoding.EncodeToString([]byte(owner + "|" + at.Format(time.RFC3339Nano) + "|" + id.String()))
	return "/account/points?after=" + token + "#ledger-heading"
}
