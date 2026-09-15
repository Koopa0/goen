package payment

import (
	"encoding/json"
	"time"

	stripe "github.com/stripe/stripe-go/v86"
)

// disputeEvents are the three lifecycle signals Stripe sends for card disputes.
var disputeEvents = map[stripe.EventType]bool{
	"charge.dispute.created": true,
	"charge.dispute.updated": true,
	"charge.dispute.closed":  true,
}

// Dispute is a verified provider dispute fact ready to reconcile locally.
type Dispute struct {
	ID              string
	ChargeID        string
	PaymentIntentID string
	Amount          int64
	Currency        string
	Status          string
	Reason          string
	EvidenceDue     *time.Time
	SeenAt          time.Time
	Movements       []DisputeMovement
}

// DisputeMovement is one withdrawn or reinstated balance fact on a dispute.
type DisputeMovement struct {
	Kind       string
	Amount     int64
	ProviderID string
}

// DisputeFrom reads a dispute webhook into a [Dispute].
func DisputeFrom(ev *stripe.Event) (Dispute, bool) {
	if ev == nil || ev.Data == nil || !disputeEvents[ev.Type] {
		return Dispute{}, false
	}
	var d stripe.Dispute
	if err := json.Unmarshal(ev.Data.Raw, &d); err != nil {
		return Dispute{}, false
	}
	if !ValidStripeID(d.ID) || d.Amount <= 0 || d.Charge == nil || !ValidStripeID(d.Charge.ID) {
		return Dispute{}, false
	}
	out := Dispute{
		ID: d.ID, ChargeID: d.Charge.ID, Amount: d.Amount,
		Currency: string(d.Currency), Status: string(d.Status), Reason: string(d.Reason),
		SeenAt: time.Unix(ev.Created, 0).UTC(),
	}
	if d.PaymentIntent != nil && ValidStripeID(d.PaymentIntent.ID) {
		out.PaymentIntentID = d.PaymentIntent.ID
	}
	if d.EvidenceDetails != nil && d.EvidenceDetails.DueBy > 0 {
		t := time.Unix(d.EvidenceDetails.DueBy, 0).UTC()
		out.EvidenceDue = &t
	}
	out.Movements = disputeMovements(d.BalanceTransactions)
	return out, true
}

func disputeMovements(transactions []*stripe.BalanceTransaction) []DisputeMovement {
	out := make([]DisputeMovement, 0, len(transactions))
	for _, bt := range transactions {
		if bt == nil || !ValidStripeID(bt.ID) || bt.Amount == 0 {
			continue
		}
		kind := "withdrawn"
		amount := bt.Amount
		if amount < 0 {
			kind = "reinstated"
			amount = -amount
		}
		out = append(out, DisputeMovement{Kind: kind, Amount: amount, ProviderID: bt.ID})
	}
	return out
}
