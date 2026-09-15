package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
)

const disputeQueueLimit = 50

func (s *Store) Disputes(ctx context.Context, now time.Time) ([]pages.AdminDispute, error) {
	rows, err := s.q.DisputeQueue(ctx, db.DisputeQueueParams{
		Now:   now,
		Limit: disputeQueueLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("dispute queue: %w", err)
	}
	out := make([]pages.AdminDispute, len(rows))
	for i := range rows {
		row := &rows[i]
		out[i] = pages.AdminDispute{
			ID:          row.ID.String(),
			ProviderRef: row.ProviderRef,
			OrderNumber: textOrEmpty(row.OrderNumber),
			PaymentRef:  textOrEmpty(row.PaymentSessionRef),
			AmountCents: row.AmountCents,
			Status:      row.Status,
			Reason:      textOrEmpty(row.Reason),
			Overdue:     row.Overdue,
			CreatedAt:   shoptime.Minute(row.CreatedAt),
			Reviewed:    row.ReviewedAt.Valid,
			Disposition: textOrEmpty(row.Disposition),
			ReviewedAt:  reviewedAt(row.ReviewedAt),
			EvidenceDue: evidenceDue(row.EvidenceDueAt),
		}
	}
	return out, nil
}

func (s *Store) ReviewDispute(ctx context.Context, disputeID uuid.UUID, disposition string) error {
	return s.audited(ctx, Event{
		Action: actionReviewDispute, Table: "payment_disputes",
		ID:    uuid.NullUUID{UUID: disputeID, Valid: true},
		After: map[string]string{"disposition": disposition},
	}, func(ctx context.Context, q *db.Queries) error {
		actor, ok := actorFrom(ctx)
		if !ok {
			return ErrNoActor
		}
		return q.ReviewPaymentDispute(ctx, db.ReviewPaymentDisputeParams{
			DisputeID: disputeID, Actor: actor, Disposition: disposition,
		})
	})
}

func textOrEmpty(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}

func reviewedAt(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return shoptime.Minute(t.Time)
}

func evidenceDue(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
