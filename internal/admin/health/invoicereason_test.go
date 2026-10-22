package health

import (
	"maps"
	"slices"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
)

// TestInvoiceReasonLabelsCoverReasons: every invoice reason has a label. It is
// the guard between the issuer, which stores an invoice.Reason, and the health
// page, which explains it. A reason added to the invoice package without a
// label fails here, in both languages, instead of showing staff "no explanation
// is available" for a row they must act on.
func TestInvoiceReasonLabelsCoverReasons(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		unknown := i18n.T(ctx, i18n.KeyAdminHPReasonUnknown)
		for _, reason := range invoice.Reasons() {
			key, labelled := invoiceReasonLabels[reason]
			if !labelled {
				t.Errorf("%s: invoice reason %q has no label", locale.Tag(), reason)
				continue
			}
			label := i18n.T(ctx, key)
			if label == "" || label == unknown {
				t.Errorf("%s: invoice reason %q is labelled %q, which explains nothing", locale.Tag(), reason, label)
			}
			if got := (StrandedClaim{LastError: string(reason)}).ReasonText(ctx); got != label {
				t.Errorf("%s: the page says %q for %q, want its label %q", locale.Tag(), got, reason, label)
			}
		}
	}
}

// TestInvoiceReasonLabelsNoneLeftOver: no invoice reason label is left over. It
// is the other direction: a label keyed by a value that is no longer a reason
// is dead copy which would hide a rename.
func TestInvoiceReasonLabelsNoneLeftOver(t *testing.T) {
	t.Parallel()
	known := invoice.Reasons()
	for _, reason := range slices.Sorted(maps.Keys(invoiceReasonLabels)) {
		if !slices.Contains(known, reason) {
			t.Errorf("label for %q, which invoice.Reasons does not list", reason)
		}
	}
}
