package health

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

type fakeDisputes struct {
	found []payment.Dispute
	err   error
	block bool
}

func (f fakeDisputes) DisputesNeedingResponse(ctx context.Context) ([]payment.Dispute, error) {
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.found, f.err
}

func (fakeDisputes) DisputeURL(id string) string { return "https://stripe.example/disputes/" + id }

var quiet = slog.New(slog.DiscardHandler)

func noOrders(context.Context, []string) (map[string]string, error) { return nil, nil }

func TestReadDisputesMatchesOrdersAndFormatsTheDeadlineInShopTime(t *testing.T) {
	t.Parallel()
	// 2026-10-31 23:59:59 UTC is 2026-11-01 07:59 in Taipei.
	due := time.Date(2026, 10, 31, 23, 59, 59, 0, time.UTC)
	src := fakeDisputes{found: []payment.Dispute{
		{ID: "dp_1", AmountCents: 129000, RespondBy: due, SessionID: "cs_1"},
		{ID: "dp_2", AmountCents: 700, SessionID: "cs_unknown"},
	}}
	orders := func(_ context.Context, ids []string) (map[string]string, error) {
		if diff := cmp.Diff([]string{"cs_1", "cs_unknown"}, ids); diff != "" {
			t.Errorf("sessions asked of the database (-want +got):\n%s", diff)
		}
		return map[string]string{"cs_1": "GN-100"}, nil
	}
	got := readDisputes(t.Context(), src, time.Second, quiet, orders)
	want := admin.DisputeState{Configured: true, Items: []admin.OpenDispute{
		{URL: "https://stripe.example/disputes/dp_1", OrderNumber: "GN-100", AmountCents: 129000, RespondBy: "2026-11-01 07:59"},
		{URL: "https://stripe.example/disputes/dp_2", AmountCents: 700},
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("readDisputes() mismatch (-want +got):\n%s", diff)
	}
}

func TestReadDisputesIsUnknownWhenStripeFailsOrTimesOut(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]fakeDisputes{
		"error":   {err: errors.New("stripe down")},
		"timeout": {block: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := readDisputes(t.Context(), src, 20*time.Millisecond, quiet, noOrders)
			if !got.Configured || !got.Unknown || len(got.Items) != 0 {
				t.Errorf("readDisputes() = %+v, want Configured and Unknown", got)
			}
			if got.Healthy() {
				t.Error("an unread Stripe counted as healthy")
			}
		})
	}
}

func TestReadDisputesWithoutAStripeSourceLeavesTheRowOut(t *testing.T) {
	t.Parallel()
	got := readDisputes(t.Context(), nil, time.Second, quiet, noOrders)
	if got.Configured || !got.Healthy() {
		t.Errorf("readDisputes(nil) = %+v, want an unconfigured, healthy state", got)
	}
}

func TestReadDisputesKeepsTheDisputesWhenTheOrderLookupFails(t *testing.T) {
	t.Parallel()
	src := fakeDisputes{found: []payment.Dispute{{ID: "dp_1", AmountCents: 100, SessionID: "cs_1"}}}
	got := readDisputes(t.Context(), src, time.Second, quiet,
		func(context.Context, []string) (map[string]string, error) { return nil, errors.New("db down") })
	if got.Unknown || len(got.Items) != 1 || got.Items[0].OrderNumber != "" {
		t.Errorf("readDisputes() = %+v, want the dispute listed with no order", got)
	}
}

func TestAnOpenDisputeIsNotHealthy(t *testing.T) {
	t.Parallel()
	state := admin.DisputeState{Configured: true, Items: []admin.OpenDispute{{}}}
	if state.Healthy() {
		t.Error("a dispute waiting for a response counted as healthy")
	}
}
