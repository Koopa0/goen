package health

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

// disputeReadTimeout keeps the health page from waiting on Stripe.
const disputeReadTimeout = 4 * time.Second

// DisputeSource is Stripe's view of the disputes the shop has yet to answer.
type DisputeSource interface {
	DisputesNeedingResponse(ctx context.Context) ([]payment.Dispute, error)
	DisputeURL(id string) string
}

// OrderNumbersBySession maps Checkout Session ids to the order number of the
// goen payment that opened each; a session goen never recorded is absent.
func (s *Store) OrderNumbersBySession(ctx context.Context, sessionIDs []string) (map[string]string, error) {
	rows, err := s.q.OrderNumbersByProviderRef(ctx, sessionIDs)
	if err != nil {
		return nil, fmt.Errorf("read orders of disputed payments: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.ProviderRef] = r.OrderNumber
	}
	return out, nil
}

// readDisputes never reports "none" for a read that did not finish: a failure
// or timeout is Unknown.
func readDisputes(
	ctx context.Context, src DisputeSource, timeout time.Duration, log *slog.Logger,
	orders func(ctx context.Context, sessionIDs []string) (map[string]string, error),
) admin.DisputeState {
	if src == nil {
		return admin.DisputeState{}
	}
	stripeCtx, cancelStripe := context.WithTimeout(ctx, timeout)
	defer cancelStripe()
	found, err := src.DisputesNeedingResponse(stripeCtx)
	if err != nil {
		log.WarnContext(ctx, "read disputes from Stripe", "error", err)
		return admin.DisputeState{Configured: true, Unknown: true}
	}
	var sessionIDs []string
	for i := range found {
		if found[i].SessionID != "" {
			sessionIDs = append(sessionIDs, found[i].SessionID)
		}
	}
	var matched map[string]string
	ordersUnknown := false
	if len(sessionIDs) > 0 {
		// Its own budget: a slow Stripe read must not leave the query an expired
		// context and make every order look like none.
		orderCtx, cancelOrders := context.WithTimeout(ctx, timeout)
		defer cancelOrders()
		if matched, err = orders(orderCtx, sessionIDs); err != nil {
			log.ErrorContext(ctx, "match disputes to orders", "error", err)
			ordersUnknown = true
		}
	}
	return admin.DisputeState{
		Configured: true, OrdersUnknown: ordersUnknown, Items: openDisputes(found, matched, src.DisputeURL),
	}
}

func openDisputes(found []payment.Dispute, orderOf map[string]string, url func(id string) string) []admin.OpenDispute {
	out := make([]admin.OpenDispute, len(found))
	for i := range found {
		d := &found[i]
		out[i] = admin.OpenDispute{
			URL: url(d.ID), OrderNumber: orderOf[d.SessionID], AmountCents: d.AmountCents, Currency: d.Currency,
		}
		if !d.RespondBy.IsZero() {
			out[i].RespondBy = shoptime.Minute(d.RespondBy)
		}
	}
	return out
}
