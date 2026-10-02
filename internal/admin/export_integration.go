//go:build integration

package admin

import (
	"context"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/email"
)

type CompletePaymentResolution = completePaymentResolution

const (
	CompletePaymentPaid             = completePaymentPaid
	CompletePaymentUnpaidOrRefunded = completePaymentUnpaidOrRefunded
)

func (s *Store) ReconcileCompletePayment(
	ctx context.Context,
	providerRef string,
	resolution CompletePaymentResolution,
) error {
	return s.reconcileCompletePayment(ctx, providerRef, resolution)
}

// The audit actions integration fixtures assert by name. Every other action is
// package-private: the trail's vocabulary is the store's, not a caller's.
const (
	ActionAdjustStock              = actionAdjustStock
	ActionAuthorizeAllowanceResend = actionAuthorizeAllowanceResend
	ActionCreateCampaign           = actionCreateCampaign
	ActionFeatureProduct           = actionFeatureProduct
	ActionGrantCredit              = actionGrantCredit
	ActionHideQuestion             = actionHideQuestion
	ActionPublishProduct           = actionPublishProduct
	ActionReconcilePayment         = actionReconcilePayment
	ActionRepriceVariant           = actionRepriceVariant
	ActionSetCampaignWindow        = actionSetCampaignWindow
	ActionUpdateProduct            = actionUpdateProduct
)

// EnqueueShippedNotice exposes the dispatch-notice producer only to integration
// fixtures, which cannot drive a whole fulfilment to reach it.
func (s *Store) EnqueueShippedNotice(ctx context.Context, orderID uuid.UUID, carrier, tracking string) error {
	return enqueueOrderShipped(ctx, s.q, orderID, &email.OrderShipped{Carrier: carrier, Tracking: tracking})
}
