package returns

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/returns"
)

// TestStatusLabelKnown: every known return status has an admin label. It holds
// the closed set together: a status with no catalogue entry renders as itself,
// never panics, so each known status needs an entry of its own.
func TestStatusLabelKnown(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, status := range []returns.Status{
			returns.StatusRequested,
			returns.StatusApproved,
			returns.StatusRejected,
			returns.StatusCompleted,
		} {
			label := statusLabel(ctx, status)
			if label == "" || label == string(status) {
				t.Errorf("statusLabel(%q) in %s = %q, want a catalogue label",
					status, locale, label)
			}
		}
	}
}

// TestStatusLabelUnknown: an unknown return status renders as itself.
func TestStatusLabelUnknown(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	unknown := returns.Status("legacy_foo")
	if got := statusLabel(ctx, unknown); got != "legacy_foo" {
		t.Fatalf("statusLabel(%q) = %q, want the raw status", unknown, got)
	}
}

// TestStatusTextBeforeShipment: a finished refund before shipment is not called
// completed.
//
// A refund before shipment that has finished is a cancellation. The queue must
// not call it completed, which is what a return that came back is called.
func TestStatusTextBeforeShipment(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		cancelled := statusText(ctx, returns.StatusCompleted, true, false)
		if cancelled != i18n.T(ctx, i18n.KeyAdminReturnCancelledRefunded) {
			t.Errorf("%s: a finished refund before shipment reads %q", locale, cancelled)
		}
		if returned := statusText(ctx, returns.StatusCompleted, false, false); returned != i18n.T(ctx, i18n.KeyAdminReturnCompleted) {
			t.Errorf("%s: a completed return reads %q", locale, returned)
		}
		if open := statusText(ctx, returns.StatusApproved, true, false); open != i18n.T(ctx, i18n.KeyAdminReturnApproved) {
			t.Errorf("%s: a refund still being paid reads %q, want its own status", locale, open)
		}
	}
}

// TestStatusTextUnsent: a finished refund of units never sent is not called
// completed.
//
// A finished refund of units that were never sent is not a return that came
// back either.
func TestStatusTextUnsent(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		if got := statusText(ctx, returns.StatusCompleted, false, true); got != i18n.T(ctx, i18n.KeyAdminReturnUnsentRefunded) {
			t.Errorf("%s: a finished unsent refund reads %q", locale, got)
		}
		if open := statusText(ctx, returns.StatusApproved, false, true); open != i18n.T(ctx, i18n.KeyAdminReturnApproved) {
			t.Errorf("%s: an unsent refund still being paid reads %q, want its own status", locale, open)
		}
	}
}
