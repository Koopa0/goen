package account

import (
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestOrderCursorIsScopedAndKeepsTimestampPrecision(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	at := time.Date(2026, time.September, 22, 1, 2, 3, 123456000, time.UTC)
	u, err := url.Parse(nextOrdersURL("owner", at, id))
	if err != nil {
		t.Fatal(err)
	}
	token := u.Query().Get("after")
	got := readOrderCursor("owner", []string{token})
	if !got.Valid || got.ID != id || !got.At.Equal(at) {
		t.Fatal("order cursor lost its boundary")
	}
	if readOrderCursor("another", []string{token}).Valid {
		t.Fatal("cursor escaped its owner")
	}
	if readOrderCursor("owner", []string{"malformed"}).Valid {
		t.Fatal("malformed cursor accepted")
	}
}
