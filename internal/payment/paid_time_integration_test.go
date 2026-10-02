//go:build integration

package payment_test

import (
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/payment"
)

func TestPaymentAndTimelineKeepTheProviderEventTime(t *testing.T) {
	for _, paidEventType := range []string{"checkout.session.completed", "checkout.session.async_payment_succeeded"} {
		t.Run(paidEventType, func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				created any
				exact   bool
			}{
				{name: "delayed delivery", created: int64(-7200), exact: true},
				{name: "future event", created: int64(7200)},
				{name: "missing time"},
				{name: "zero time", created: int64(0)},
				{name: "negative time", created: int64(-1)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					ctx := t.Context()
					s := payment.NewStore(pool)
					h := payment.NewHandler(s, enabledGateway(t), alwaysPlacedHere{}, slog.New(slog.DiscardHandler), false)
					number, id, session := openOrder(t, s, 100000, "paid_time")
					var before time.Time
					if err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&before); err != nil {
						t.Fatal(err)
					}

					unpaid := sessionEvent("evt_unpaid_"+uuid.NewString(), session, "unpaid", 100000)
					unpaid["created"] = before.Add(-24 * time.Hour).Unix()
					body, header := signed(t, unpaid)
					if w := deliver(t, h, body, header); w.Code != http.StatusOK {
						t.Fatalf("unpaid completion status = %d", w.Code)
					}
					if got := moneyOf(t, ctx, number, id, session); !got.untouched() {
						t.Fatalf("unpaid completion changed money: %+v", got)
					}

					paid := sessionEvent("evt_paid_time_"+uuid.NewString(), session, "paid", 100000)
					paid["type"] = paidEventType
					created := tc.created
					if offset, ok := created.(int64); ok && (tc.exact || offset > 0) {
						created = before.Unix() + offset
					}
					if created != nil {
						paid["created"] = created
					}
					body, header = signed(t, paid)
					if w := deliver(t, h, body, header); w.Code != http.StatusOK {
						t.Fatalf("paid delivery status = %d", w.Code)
					}
					var paidAt, occurredAt, after time.Time
					if err := pool.QueryRow(ctx, `
						SELECT p.paid_at, e.occurred_at, clock_timestamp()
						FROM payments p JOIN order_events e ON e.order_id = p.order_id AND e.kind = 'paid'
						WHERE p.provider_ref = $1`, session).Scan(&paidAt, &occurredAt, &after); err != nil {
						t.Fatal(err)
					}
					if !paidAt.Equal(occurredAt) {
						t.Errorf("payment time %s differs from paid timeline %s", paidAt, occurredAt)
					}
					if tc.exact {
						if want := time.Unix(created.(int64), 0); !paidAt.Equal(want) {
							t.Errorf("payment time = %s, want provider event time %s", paidAt, want)
						}
					} else if paidAt.Before(before) || paidAt.After(after) {
						t.Errorf("payment time %s outside database capture interval [%s, %s]", paidAt, before, after)
					}

					// A new event ID for the same settled session must not move its time.
					duplicate := sessionEvent("evt_later_paid_"+uuid.NewString(), session, "paid", 100000)
					duplicate["created"] = before.Add(-48 * time.Hour).Unix()
					for _, event := range []map[string]any{paid, duplicate} {
						body, header = signed(t, event)
						if w := deliver(t, h, body, header); w.Code != http.StatusOK {
							t.Fatalf("redelivery status = %d", w.Code)
						}
					}
					var settledAt, timelineAt time.Time
					var events int
					if err := pool.QueryRow(ctx, `
						SELECT p.paid_at, min(e.occurred_at), count(*)
						FROM payments p JOIN order_events e ON e.order_id = p.order_id AND e.kind = 'paid'
						WHERE p.provider_ref = $1 GROUP BY p.paid_at`, session).Scan(&settledAt, &timelineAt, &events); err != nil {
						t.Fatal(err)
					}
					if events != 1 || !settledAt.Equal(paidAt) || !timelineAt.Equal(paidAt) {
						t.Errorf("redelivery left payment=%s timeline=%s events=%d, want unchanged %s and one event", settledAt, timelineAt, events, paidAt)
					}
				})
			}
		})
	}
}
