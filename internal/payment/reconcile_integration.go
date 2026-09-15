//go:build integration

package payment

import (
	"context"
	"fmt"
)

// ReconcileStoredDisputeEvent applies a dispute fact for a webhook row that was
// previously record-and-ignore without re-claiming the inbox row.
func (s *Store) ReconcileStoredDisputeEvent(ctx context.Context, eventID string, d Dispute) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reconcile dispute event %s: %w", eventID, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit

	webhook := &webhookTx{
		q: s.q.WithTx(tx), tx: tx, eventID: eventID, objectRef: d.ID,
	}
	if lockErr := webhook.q.LockPaymentProviderRef(ctx, d.ID); lockErr != nil {
		return fmt.Errorf("lock dispute %s: %w", d.ID, lockErr)
	}
	attributed, err := webhook.ApplyDispute(ctx, d, eventID)
	if err != nil {
		return err
	}
	if !attributed {
		if err := webhook.Unreconciled(ctx, webhookUnreconciled(
			webhookUnattributedDispute,
			"a dispute has no verified payment to attribute it to")); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit reconcile dispute event %s: %w", eventID, err)
	}
	return nil
}
