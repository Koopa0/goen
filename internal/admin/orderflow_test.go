package admin

import (
	"testing"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestOnlyAnOrderStillGoingSomewhereCanHaveItsDeliveryCorrected(t *testing.T) {
	t.Parallel()
	for status, want := range map[pages.FulfillmentStatus]bool{
		pages.FulfillmentPending:   true,
		pages.FulfillmentPicking:   true,
		pages.FulfillmentShipped:   false,
		pages.FulfillmentDelivered: false,
		pages.FulfillmentCompleted: false,
		pages.FulfillmentCancelled: false,
	} {
		if got := correctable(status); got != want {
			t.Errorf("correctable(%s) = %t, want %t", status, got, want)
		}
	}
}

// The database admits a refund before shipment from the paid state and from
// picking; the page offers it in both.
func TestTheRefundBeforeShipmentIsOfferedFromPaidAndPicking(t *testing.T) {
	t.Parallel()
	for status, want := range map[pages.FulfillmentStatus]bool{
		pages.FulfillmentPending:   true,
		pages.FulfillmentPicking:   true,
		pages.FulfillmentShipped:   false,
		pages.FulfillmentCancelled: false,
	} {
		offered, open := beforeShipmentRefundState(&db.BeforeShipmentRefundRow{
			FulfillmentStatus: string(status), Committed: true,
		})
		if offered != want || open {
			t.Errorf("committed %s: offered=%t open=%t, want offered=%t open=false", status, offered, open, want)
		}
	}
	if offered, _ := beforeShipmentRefundState(&db.BeforeShipmentRefundRow{
		FulfillmentStatus: string(pages.FulfillmentPending), Committed: false,
	}); offered {
		t.Error("an unpaid order is offered a refund")
	}
}

// A refund before shipment that has finished is a cancellation. The queue must
// not call it completed, which is what a return that came back is called.
func TestAFinishedRefundBeforeShipmentIsNotCalledCompleted(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		cancelled := returnStatusText(ctx, returns.StatusCompleted, true)
		if cancelled != i18n.T(ctx, i18n.KeyAdminReturnCancelledRefunded) {
			t.Errorf("%s: a finished refund before shipment reads %q", locale, cancelled)
		}
		if returned := returnStatusText(ctx, returns.StatusCompleted, false); returned != i18n.T(ctx, i18n.KeyAdminReturnCompleted) {
			t.Errorf("%s: a completed return reads %q", locale, returned)
		}
		if open := returnStatusText(ctx, returns.StatusApproved, true); open != i18n.T(ctx, i18n.KeyAdminReturnApproved) {
			t.Errorf("%s: a refund still being paid reads %q, want its own status", locale, open)
		}
	}
}
