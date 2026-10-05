package health

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/pgtx"
)

// CompletePaymentResolution is the operator's explicit conclusion after a
// provider-complete Checkout Session had no capture outcome goen could apply.
// Paid and safe-to-retry are financially opposite facts.
type CompletePaymentResolution uint8

const (
	completePaymentResolutionUnknown CompletePaymentResolution = iota
	CompletePaymentPaid
	// CompletePaymentUnpaidOrRefunded releases the gate only after staff confirm
	// that Stripe took no money or that every cent was returned.
	CompletePaymentUnpaidOrRefunded
)

func parseCompletePaymentResolution(s string) (CompletePaymentResolution, bool) {
	switch strings.TrimSpace(s) {
	case "paid":
		return CompletePaymentPaid, true
	case "unpaid_or_refunded":
		return CompletePaymentUnpaidOrRefunded, true
	default:
		return completePaymentResolutionUnknown, false
	}
}

func (r CompletePaymentResolution) auditValue() string {
	switch r {
	case CompletePaymentPaid:
		return "paid_attributed"
	case CompletePaymentUnpaidOrRefunded:
		return "unpaid_or_fully_refunded"
	default:
		return "invalid"
	}
}

func paymentEventSafeReleaseSubmitted(s string) bool {
	return strings.TrimSpace(s) == "fully_refunded_or_accounted"
}

// ReleasePaymentEventAfterRefundOrAccounting records the only safe release
// conclusion for an event goen accepted and could not apply: provider money was
// fully refunded, or a succeeded local payment already accounts for it. Merely
// inspecting an event must not terminate its linked attempt and open another
// place to charge.
//
// The row keeps its reason. Clearing the flag would delete what happened, and
// what happened is the part worth reading afterwards.
func (s *Store) ReleasePaymentEventAfterRefundOrAccounting(
	ctx context.Context, eventID string,
) error {
	if strings.TrimSpace(eventID) == "" {
		return ErrInvalid
	}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionReconcilePayment, Table: "payment_webhook_events", ID: uuid.NullUUID{},
		After: map[string]any{
			"event":      eventID,
			"resolution": "fully_refunded_or_already_accounted",
		},
	}, func(ctx context.Context, q *db.Queries) error {
		reconciled, err := q.ReleasePaymentEvent(ctx, eventID)
		if err != nil {
			return fmt.Errorf("mark %s reconciled: %w", eventID, err)
		}
		if !reconciled {
			// Nothing outstanding under that id: already dealt with, or never
			// flagged. The row count is the answer, not a read beforehand.
			return ErrNotFound
		}
		return nil
	})
}

// ReconcileCompletePayment records one explicit money outcome for a provider-
// complete Session whose capture outcome was not represented by a flaggable
// webhook event. Paid attribution runs through capture_payment and all of its
// side effects; only confirmed-unpaid/fully-refunded releases a later attempt.
// The provider ref remains distinct from the event id ReleasePaymentEventAfterRefundOrAccounting takes.
func (s *Store) ReconcileCompletePayment(
	ctx context.Context, providerRef string, resolution CompletePaymentResolution,
) error {
	if strings.TrimSpace(providerRef) == "" ||
		resolution == completePaymentResolutionUnknown {
		return ErrInvalid
	}

	event := audit.Event{
		Action: audit.ActionReconcilePayment, Table: "payments", ID: uuid.NullUUID{},
		After: map[string]any{
			"provider_ref": providerRef,
			"resolution":   resolution.auditValue(),
		},
	}

	// Paid attribution has capture side effects supplied by internal/payment,
	// and the audit must share their transaction.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin %s: %w", event.Action, err)
	}
	defer pgtx.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	switch resolution {
	case CompletePaymentPaid:
		attributed, captureErr := payment.AttributeCompleteCapture(ctx, tx, providerRef)
		if captureErr != nil {
			if errors.Is(captureErr, payment.ErrReleasedStockRequiresRefund) {
				return fmt.Errorf("%w: %w", ErrPaymentRequiresRefund, captureErr)
			}
			return captureErr
		}
		if !attributed {
			return ErrNotFound
		}
	case CompletePaymentUnpaidOrRefunded:
		released, releaseErr := q.ReleaseCompletePayment(ctx, providerRef)
		if releaseErr != nil {
			return fmt.Errorf("release complete payment %s: %w", providerRef, releaseErr)
		}
		if !released {
			return ErrNotFound
		}
	default:
		return ErrInvalid
	}

	if err := audit.In(ctx, q, event); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", event.Action, err)
	}
	return nil
}
