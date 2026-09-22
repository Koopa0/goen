// Package ordernotice queues terminal order facts without retaining delivery PII.
package ordernotice

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/outbox"
)

// Kind identifies the fact and who initiated a cancellation.
type Kind string

const (
	CancelledByCustomer Kind = "cancelled_by_customer"
	CancelledByStaff    Kind = "cancelled_by_staff"
	Delivered           Kind = "delivered"
	Collected           Kind = "collected"
)

// Message contains only the order identity and its committed event.
type Message struct {
	OrderID uuid.UUID `json:"order_id"`
	Kind    Kind      `json:"kind"`
}

// Enqueue must use the caller's order transaction so failed transitions send nothing.
func Enqueue(ctx context.Context, q *db.Queries, orderID uuid.UUID, kind Kind) error {
	payload, err := json.Marshal(Message{OrderID: orderID, Kind: kind})
	if err != nil {
		return fmt.Errorf("encode terminal order notice: %w", err)
	}
	if err := q.EnqueueMessage(ctx, db.EnqueueMessageParams{Topic: outbox.TopicOrderTerminal, DedupeKey: orderID.String() + ":" + string(kind), Payload: payload}); err != nil {
		return fmt.Errorf("enqueue terminal order notice: %w", err)
	}
	return nil
}
