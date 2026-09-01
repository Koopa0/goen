//go:build integration

package admin

import "context"

// CompletePaymentResolution exposes the closed operator conclusion only to
// integration fixtures.
type CompletePaymentResolution = completePaymentResolution

const (
	// CompletePaymentPaid exposes paid attribution only in integration builds.
	CompletePaymentPaid = completePaymentPaid
	// CompletePaymentUnpaidOrRefunded exposes the safe release outcome only in
	// integration builds.
	CompletePaymentUnpaidOrRefunded = completePaymentUnpaidOrRefunded
)

// ReconcileCompletePayment exposes the handler-owned audited reconciliation
// operation only in integration builds.
func (s *Store) ReconcileCompletePayment(
	ctx context.Context,
	providerRef string,
	resolution CompletePaymentResolution,
) error {
	return s.reconcileCompletePayment(ctx, providerRef, resolution)
}
