package ordernotice_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/ordernotice"
)

// The producer and the mail worker each keep their own copy of the kinds, so
// the strings on the wire are the only thing joining them.
func TestProducerAndConsumerAgreeOnTheKindsOnTheWire(t *testing.T) {
	t.Parallel()
	for producer, consumer := range map[ordernotice.Kind]email.TerminalKind{
		ordernotice.CancelledByCustomer:        email.TerminalCancelledByCustomer,
		ordernotice.CancelledByStaff:           email.TerminalCancelledByStaff,
		ordernotice.CancelledByPaymentDeadline: email.TerminalCancelledByPaymentDeadline,
		ordernotice.Delivered:                  email.TerminalDelivered,
		ordernotice.Collected:                  email.TerminalCollected,
	} {
		if string(producer) != string(consumer) {
			t.Errorf("producer kind %q is %q to the consumer", producer, consumer)
		}
	}
}

// The whole payload crosses the same wire: a field the consumer reads under
// another name arrives as its zero value, and a refunded order's mail would
// then say nothing was charged.
func TestTheConsumerReadsEveryFieldTheProducerWrites(t *testing.T) {
	t.Parallel()
	sent := ordernotice.Message{OrderID: uuid.New(), Kind: ordernotice.CancelledByPaymentDeadline, Refunded: true}
	wire, err := json.Marshal(sent)
	if err != nil {
		t.Fatal(err)
	}
	var got email.OrderTerminal
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatal(err)
	}
	if got.OrderID != sent.OrderID || string(got.Kind) != string(sent.Kind) || got.Refunded != sent.Refunded {
		t.Errorf("producer wrote %s, consumer read %+v", wire, got)
	}
}
