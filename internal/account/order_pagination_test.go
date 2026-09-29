package account

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/web"
)

func TestOrderTokenKeepsItsOwnerAndTimestampPrecision(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	at := time.Date(2026, time.September, 22, 1, 2, 3, 123456000, time.UTC)
	rows := make([]db.UserOrdersRow, orderPageSize+1)
	rows[orderPageSize-1] = db.UserOrdersRow{ID: id, PlacedAt: at}
	_, b := orderBound(orderCursor{}, "owner", rows,
		func(o *db.UserOrdersRow) (uuid.UUID, time.Time) { return o.ID, o.PlacedAt })
	u, err := url.Parse(b.Next)
	if err != nil {
		t.Fatal(err)
	}
	token := u.Query().Get("after")
	got := readOrderCursor("owner", []string{token})
	if !got.Valid || got.ID != id || !got.At.Equal(at) {
		t.Fatal("order cursor lost its boundary")
	}
	if readOrderCursor("another", []string{token}).Valid {
		t.Fatal("a token minted for one customer was accepted for another")
	}
}

func TestOrderCursorRefusesUnusableTokens(t *testing.T) {
	t.Parallel()
	mint := func(pos orderPosition) string {
		body, err := json.Marshal(pos)
		if err != nil {
			t.Fatal(err)
		}
		next, ok := web.NextKeysetURL(ordersScope, string(body))
		if !ok {
			t.Fatal("scope refused")
		}
		u, err := url.Parse(next)
		if err != nil {
			t.Fatal(err)
		}
		return u.Query().Get(web.KeysetParam)
	}
	for name, token := range map[string]string{
		"malformed":       "malformed",
		"nil id":          mint(orderPosition{Owner: "owner"}),
		"owner mismatch":  mint(orderPosition{ID: uuid.New(), Owner: "another"}),
		"no owner at all": mint(orderPosition{ID: uuid.New()}),
	} {
		if readOrderCursor("owner", []string{token}).Valid {
			t.Errorf("%s: unusable token accepted", name)
		}
	}
}

func TestAnEmptyLaterPageKeepsItsRestartDoor(t *testing.T) {
	t.Parallel()
	key := func(o *db.UserOrdersRow) (uuid.UUID, time.Time) { return o.ID, o.PlacedAt }
	_, later := orderBound(orderCursor{Valid: true}, "owner", []db.UserOrdersRow{}, key)
	if !later.PastEnd || later.First != "/account#orders-heading" || later.Next != "" {
		t.Fatalf("empty later page: %+v", later)
	}
	_, first := orderBound(orderCursor{}, "owner", []db.UserOrdersRow{}, key)
	if first.PastEnd || first.First != "" {
		t.Fatalf("an empty first page must keep its own empty state: %+v", first)
	}
}
