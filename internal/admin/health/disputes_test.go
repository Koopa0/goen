package health

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/payment"
)

type fakeDisputes struct {
	found []payment.Dispute
	err   error
	block bool
	delay time.Duration
}

func (f fakeDisputes) DisputesNeedingResponse(ctx context.Context) ([]payment.Dispute, error) {
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	time.Sleep(f.delay)
	return f.found, f.err
}

func (fakeDisputes) DisputeURL(id string) string { return "https://stripe.example/disputes/" + id }

var quiet = slog.New(slog.DiscardHandler)

func noOrders(context.Context, []string) (map[string]string, error) { return nil, nil }

// TestReadDisputesMatchesOrders: readDisputes matches orders and formats the
// deadline in shop time. Each dispute's checkout session is matched to its
// order number, a session with no order stays without one, and the response
// deadline reads in the shop's zone.
func TestReadDisputesMatchesOrders(t *testing.T) {
	t.Parallel()
	// 2026-10-31 23:59:59 UTC is 2026-11-01 07:59 in Taipei.
	due := time.Date(2026, 10, 31, 23, 59, 59, 0, time.UTC)
	src := fakeDisputes{found: []payment.Dispute{
		{ID: "dp_1", AmountCents: 129000, Currency: "twd", RespondBy: due, SessionID: "cs_1"},
		{ID: "dp_2", AmountCents: 700, SessionID: "cs_unknown"},
	}}
	orders := func(_ context.Context, ids []string) (map[string]string, error) {
		if diff := cmp.Diff([]string{"cs_1", "cs_unknown"}, ids); diff != "" {
			t.Errorf("sessions asked of the database (-want +got):\n%s", diff)
		}
		return map[string]string{"cs_1": "GN-100"}, nil
	}
	got := readDisputes(t.Context(), src, time.Second, quiet, orders)
	want := DisputeState{Configured: true, Items: []OpenDispute{
		{URL: "https://stripe.example/disputes/dp_1", OrderNumber: "GN-100", AmountCents: 129000, Currency: "twd", RespondBy: "2026-11-01 07:59"},
		{URL: "https://stripe.example/disputes/dp_2", AmountCents: 700},
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("readDisputes() mismatch (-want +got):\n%s", diff)
	}
}

// TestReadDisputesStripeUnavailable: readDisputes is unknown when Stripe fails
// or times out. The state is configured and unknown, lists nothing, and does
// not count as healthy.
func TestReadDisputesStripeUnavailable(t *testing.T) {
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

// TestReadDisputesNoSource: readDisputes without a Stripe source leaves the row
// out. The state is unconfigured and healthy.
func TestReadDisputesNoSource(t *testing.T) {
	t.Parallel()
	got := readDisputes(t.Context(), nil, time.Second, quiet, noOrders)
	if got.Configured || !got.Healthy() {
		t.Errorf("readDisputes(nil) = %+v, want an unconfigured, healthy state", got)
	}
}

// TestReadDisputesOrderLookupFails: readDisputes flags a failed order lookup
// instead of claiming there is no order. The dispute is still listed,
// OrdersUnknown is set, and the state does not count as healthy.
func TestReadDisputesOrderLookupFails(t *testing.T) {
	t.Parallel()
	src := fakeDisputes{found: []payment.Dispute{{ID: "dp_1", AmountCents: 100, SessionID: "cs_1"}}}
	got := readDisputes(t.Context(), src, time.Second, quiet,
		func(context.Context, []string) (map[string]string, error) { return nil, errors.New("db down") })
	if got.Unknown || !got.OrdersUnknown || len(got.Items) != 1 || got.Items[0].OrderNumber != "" {
		t.Errorf("readDisputes() = %+v, want the dispute listed with OrdersUnknown", got)
	}
	if got.Healthy() {
		t.Error("a failed order lookup counted as healthy")
	}
}

// TestReadDisputesOrderLookupOwnTime: readDisputes gives the order lookup its
// own time after a slow Stripe. A Stripe read that used up its budget does not
// leave the order lookup with an expired context.
func TestReadDisputesOrderLookupOwnTime(t *testing.T) {
	t.Parallel()
	src := fakeDisputes{found: []payment.Dispute{{ID: "dp_1", SessionID: "cs_1"}}, delay: 60 * time.Millisecond}
	got := readDisputes(t.Context(), src, 50*time.Millisecond, quiet,
		func(ctx context.Context, _ []string) (map[string]string, error) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return map[string]string{"cs_1": "GN-1"}, nil
		})
	if got.OrdersUnknown || got.Items[0].OrderNumber != "GN-1" {
		t.Errorf("readDisputes() = %+v, want the order found", got)
	}
}

// TestDisputeState_HealthyOpenDispute: an open dispute is not healthy. A
// dispute waiting for a response does not count as healthy.
func TestDisputeState_HealthyOpenDispute(t *testing.T) {
	t.Parallel()
	state := DisputeState{Configured: true, Items: []OpenDispute{{}}}
	if state.Healthy() {
		t.Error("a dispute waiting for a response counted as healthy")
	}
}
