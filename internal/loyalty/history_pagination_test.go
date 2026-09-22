package loyalty

import (
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHistoryCursorKeepsOwnerAndExactGroupBoundary(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	at := time.Date(2026, time.September, 22, 1, 2, 3, 123456000, time.UTC)
	u, err := url.Parse(nextHistoryURL("owner", at, id))
	if err != nil {
		t.Fatal(err)
	}
	token := u.Query().Get("after")
	c := readHistoryCursor("owner", []string{token})
	if !c.Valid || c.ID != id || !c.At.Equal(at) {
		t.Fatal("cursor lost its complete group boundary")
	}
	if readHistoryCursor("other", []string{token}).Valid {
		t.Fatal("cursor escaped its owner")
	}
	if readHistoryCursor("owner", []string{"bad"}).Valid {
		t.Fatal("malformed cursor accepted")
	}
}
