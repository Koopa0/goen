package cart

import (
	"context"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/email"
)

type lastMail struct{ body string }

func (m *lastMail) Send(_ context.Context, msg *email.Message) error {
	m.body = msg.Body
	return nil
}

// A customer can cancel while a payment is still open in another tab, so only
// payments Stripe confirmed took nothing may leave the mail saying so. Every
// status payments_status_known admits is listed, one order payment at a time.
func TestTheCancellationMailSaysNothingWasChargedOnlyWhenNoPaymentCouldHave(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		statuses []string
		flagged  bool
		charged  bool
	}{
		{"no payment", nil, false, false},
		{"expired session", []string{"cancelled"}, false, false},
		{"session still open", []string{"requires_payment"}, false, true},
		{"payment requires action", []string{"requires_action"}, false, true},
		{"payment processing", []string{"processing"}, false, true},
		{"complete awaiting its webhook", []string{"requires_reconciliation"}, false, true},
		{"reconciled", []string{"reconciled"}, false, true},
		{"captured", []string{"succeeded"}, false, true},
		{"expired and open", []string{"cancelled", "requires_payment"}, false, true},
		{"released after a refused capture", []string{"cancelled"}, true, true},
	} {
		sent := &lastMail{}
		n := email.New(sent, "https://goen.test", "", "")
		m := &email.OrderTerminal{Kind: email.TerminalCancelledByCustomer, Refunded: mayHaveTakenMoney(tc.statuses, tc.flagged)}
		if err := n.SendOrderTerminal(t.Context(), m, email.TerminalRecipient{Address: "reader@example.com", Name: "Reader", Locale: "en", OrderNumber: "GO-260101-000001"}); err != nil {
			t.Fatal(err)
		}
		if says := strings.Contains(sent.body, "It was not charged"); says == tc.charged {
			t.Errorf("%s: mail says it was not charged = %t:\n%s", tc.name, says, sent.body)
		}
		if names := strings.Contains(sent.body, "refunded in full"); names != tc.charged {
			t.Errorf("%s: mail names a refund = %t:\n%s", tc.name, names, sent.body)
		}
	}
}
