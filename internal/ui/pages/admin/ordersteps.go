package admin

import (
	"context"
	"fmt"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
)

func (v *OrderView) nextStepSummary(ctx context.Context) string {
	switch {
	case v.RefundOpen:
		return i18n.T(ctx, i18n.KeyAdminOrderNextRefund)
	case v.AllowanceAttention != "":
		return i18n.T(ctx, i18n.KeyAdminOrderNextAllowanceAttention)
	case v.AllowanceAwaitingUntil != "":
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminOrderNextAllowanceAwaiting), v.AllowanceAwaitingUntil)
	case v.Unpaid && v.Status == order.FulfillmentPending:
		return i18n.T(ctx, i18n.KeyAdminOrderNextUnpaid)
	// A refund can suppress picking while the order remains pending; Next
	// carries the order desk's pruned legal steps.
	case v.Status == order.FulfillmentPending && v.hasPickingStep():
		return i18n.T(ctx, i18n.KeyAdminOrderNextPending)
	case v.Status == order.FulfillmentPicking:
		return i18n.T(ctx, i18n.KeyAdminOrderNextPicking)
	case v.Status == order.FulfillmentShipped:
		return i18n.T(ctx, i18n.KeyAdminOrderNextShipped)
	case v.Status == order.FulfillmentDelivered && len(v.Shippable) > 0:
		return i18n.T(ctx, i18n.KeyAdminOrderNextPartialDelivery)
	case v.Status == order.FulfillmentDelivered:
		return i18n.T(ctx, i18n.KeyAdminOrderNextDelivered)
	default:
		return i18n.T(ctx, i18n.KeyAdminOrderNextNone)
	}
}

func (v *OrderView) primaryStepIndex() int {
	for i, step := range v.Next {
		if step.Value != order.FulfillmentCancelled {
			return i
		}
	}
	return -1
}

func (step Transition) submitLabel(ctx context.Context) string {
	switch step.Value {
	case order.FulfillmentPicking:
		return i18n.T(ctx, i18n.KeyAdminQueueStartPicking)
	case order.FulfillmentDelivered:
		return i18n.T(ctx, i18n.KeyAdminOrderStepDelivered)
	case order.FulfillmentCompleted:
		return i18n.T(ctx, i18n.KeyAdminOrderStepCompleted)
	case order.FulfillmentCancelled:
		return i18n.T(ctx, i18n.KeyAdminOrderStepCancelled)
	default:
		return step.Label
	}
}

func (v *OrderView) hasPickingStep() bool {
	for _, step := range v.Next {
		if step.Value == order.FulfillmentPicking {
			return true
		}
	}
	return false
}
