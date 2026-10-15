package audit_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

// actionLedger is every action goen has ever written into audit_events.action,
// sorted. audit_events is append-only and erase_user does not reach it, so an
// action stays here after the code stops writing it: old rows still name it,
// and /admin/audit must keep a phrase for it. Never delete an entry; add one,
// with its phrase in pages.actionLabels, in the PR that first writes the
// action. It holds:
//   - every Action constant in action.go;
//   - newsletter.compose and newsletter.send, which the newsletter package
//     writes through its own wrapper because it may not import this package;
//   - the actions stored functions write: invoice.allowance_provider_invalid,
//     return.refund_before_shipment, return.refund_unsent, refund.provider_attempt,
//     refund.provider_rejected and refund.provider_<status> for the five
//     statuses refunds_provider_outcome_known allows;
//   - invoice.issue, invoice.void and invoice.allowance, which the schema also
//     builds as 'invoice.' || kind (they are Action constants as well).
var actionLedger = []string{
	"answer.hide",
	"banner.create",
	"banner.toggle",
	"brand.create",
	"brand.delete",
	"brand.rename",
	"campaign.create",
	"campaign.feature",
	"campaign.image.clear",
	"campaign.image.set",
	"campaign.toggle",
	"campaign.tone.set",
	"campaign.unfeature",
	"campaign.window.set",
	"category.create",
	"category.delete",
	"category.image.clear",
	"category.image.set",
	"category.rename",
	"coupon.create",
	"coupon.toggle",
	"credit.grant",
	"customer.view",
	"faq.category.create",
	"faq.category.delete",
	"faq.category.rename",
	"faq.create",
	"faq.delete",
	"faq.update",
	"hero.create",
	"hero.promote",
	"hero.toggle",
	"image.attach",
	"image.detach",
	"image.move",
	"image.option",
	"invoice.allowance",
	"invoice.allowance_provider_invalid",
	"invoice.allowance_resend_authorized",
	"invoice.issue",
	"invoice.void",
	"message.handle",
	"message.reopen",
	"newsletter.compose",
	"newsletter.send",
	"option.add",
	"option.value.add",
	"order.advance",
	"order.delivery",
	"order.note.clear",
	"order.note.create",
	"order.note.replace",
	"order.ship",
	"payment.reconciled",
	"product.create",
	"product.invoice_line.set",
	"product.label.set",
	"product.status",
	"product.update",
	"question.answer",
	"question.hide",
	"question.show",
	"refund.provider_attempt",
	"refund.provider_cancelled",
	"refund.provider_failed",
	"refund.provider_pending",
	"refund.provider_rejected",
	"refund.provider_requires_action",
	"refund.provider_succeeded",
	"return.complete",
	"return.decide",
	"return.inspect",
	"return.refund_before_shipment",
	"return.refund_unsent",
	"review.hide",
	"review.show",
	"shipment.delivered",
	"shipping.method.create",
	"shipping.method.toggle",
	"shipping.publish",
	"shipping.surcharge",
	"shipping.zone.create",
	"shipping.zone.delete",
	"shipping.zone.prefixes",
	"spec.add",
	"spec.remove",
	"staff.factor.remove",
	"staff.grant",
	"staff.revoke",
	"stock.adjust",
	"stock.receive",
	"tier.create",
	"tier.delete",
	"variant.arrival.set",
	"variant.create",
	"variant.reprice",
	"variant.retire",
}

// TestEveryActionConstantIsInTheLedger reads the Action constants of action.go
// (only that file: actions written elsewhere are listed by hand and guarded by
// TestEverySchemaWrittenActionIsInTheLedger) and requires each in actionLedger.
func TestEveryActionConstantIsInTheLedger(t *testing.T) {
	t.Parallel()

	declared := declaredActions(t)
	// 82 on fac167bf; a parser that stops matching must not pass over nothing.
	if len(declared) < 80 {
		t.Fatalf("found %d Action constants in action.go, want at least 80: the parser is not reading the const block", len(declared))
	}
	for _, action := range declared {
		if !slices.Contains(actionLedger, action) {
			t.Errorf("Action %q is not in actionLedger: add it, sorted (audit_events keeps it forever), and give it a phrase in pages.actionLabels", action)
		}
	}
}

// TestEverySchemaWrittenActionIsInTheLedger covers the actions stored
// functions write. A literal action is read from the schema and must be in the
// ledger. An action built as 'prefix' || value cannot be read as a literal, so
// each such prefix must have at least one ledger action under it.
func TestEverySchemaWrittenActionIsInTheLedger(t *testing.T) {
	t.Parallel()

	literals, prefixes := schemaActions(t)
	for _, action := range literals {
		if !slices.Contains(actionLedger, action) {
			t.Errorf("schema writes action %q, which is not in actionLedger: add it, sorted, and give it a phrase in pages.actionLabels", action)
		}
	}
	// 'invoice.' and 'refund.provider_' on fac167bf.
	if len(prefixes) < 2 {
		t.Fatalf("found %d concatenated action prefixes, want at least 2: the pattern has stopped matching", len(prefixes))
	}
	for _, prefix := range prefixes {
		found := slices.ContainsFunc(actionLedger, func(action string) bool { return strings.HasPrefix(action, prefix) })
		if !found {
			t.Errorf("schema builds actions as %q || value, but actionLedger has none starting with it", prefix)
		}
	}
}

func TestEveryLedgerActionHasAPhrase(t *testing.T) {
	t.Parallel()

	for _, action := range actionLedger {
		for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			t.Run(action+"/"+string(locale), func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				if got := (admin.AuditEntry{Action: action}).Label(ctx); got == action {
					t.Errorf("Label(%q) in %s is the raw code: the action has no staff-facing phrase (add it to pages.actionLabels)", action, locale)
				}
			})
		}
	}
}

func TestActionLedgerIsSortedWithoutDuplicates(t *testing.T) {
	t.Parallel()

	for i := 1; i < len(actionLedger); i++ {
		switch prev, cur := actionLedger[i-1], actionLedger[i]; {
		case prev == cur:
			t.Errorf("actionLedger lists %q twice", cur)
		case prev > cur:
			t.Errorf("actionLedger is not sorted: %q comes after %q", cur, prev)
		}
	}
	// 93 on fac167bf; the ledger only grows.
	if len(actionLedger) < 93 {
		t.Errorf("actionLedger has %d entries, want at least 93: an entry was deleted", len(actionLedger))
	}
}
