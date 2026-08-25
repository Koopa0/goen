package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

// Store is the database side of taking money. It holds the pool rather than a
// DBTX because processing a webhook spans two writes that must commit together.
type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
	// tx is set only on the store ProcessWebhook hands to its apply function.
	// A refused posting function ABORTS the transaction it runs in — PostgreSQL
	// ignores every later command with 25P02 — so an effect that is allowed to
	// fail and be recorded has to run inside a SAVEPOINT. Without one, money
	// arriving for a cancelled order took the whole webhook transaction down
	// with it: the event was never marked processed, the handler answered 500,
	// and Stripe retried something that can never succeed.
	tx pgx.Tx
}

// NewStore returns a Store over pool.
func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("payment: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

// Order reads what an order OWES, through the order_amount_owed the funding
// check and the capture guard also read.
func (s *Store) Order(ctx context.Context, number string) (*Order, error) {
	row, err := s.q.OrderTotalByNumber(ctx, number)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read order %s for payment: %w", number, err)
	}

	paid, err := s.q.OrderIsPaid(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("read payment state of order %s: %w", number, err)
	}

	lines, err := s.q.OrderLinesForPayment(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("read lines of order %s: %w", number, err)
	}

	// No live hold is not an error: the caller then opens no session at all.
	holdUntil, err := s.q.OrderHoldExpiry(ctx, row.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("read stock hold of order %s: %w", number, err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		holdUntil = time.Time{}
	}

	o := &Order{
		Number:        row.OrderNumber,
		TotalCents:    row.TotalCents,
		Email:         row.Email,
		Paid:          paid,
		Fulfillment:   row.FulfillmentStatus,
		HoldExpiresAt: holdUntil,
		Lines:         make([]Line, 0, len(lines)),
	}
	for i := range lines {
		l := &lines[i]
		o.Lines = append(o.Lines, Line{
			Name:      l.ProductName,
			Label:     l.VariantLabel.String,
			UnitCents: l.UnitPriceCents,
			Quantity:  l.Quantity,
		})
	}
	return o, nil
}

// Attempt is what a POST to the pay route needs to know about the payments this
// order has already had.
type Attempt struct {
	// SessionID is a session still open at Stripe for what the order owes NOW.
	SessionID string
	// Prior is how many payment rows the order has had, live or not.
	Prior int32
}

// PaymentAttempt reports whether a Checkout Session is already open for this
// order at this figure, which is the first defence against a double charge.
func (s *Store) PaymentAttempt(ctx context.Context, number string, owedCents int64) (*Attempt, error) {
	row, err := s.q.PaymentAttemptForOrder(ctx, db.PaymentAttemptForOrderParams{
		OrderNumber: number, OwedCents: owedCents,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read payment attempts of order %s: %w", number, err)
	}
	return &Attempt{SessionID: row.LiveSession, Prior: row.PriorAttempts}, nil
}

// OpenPayment records that a Checkout Session was created for an order.
func (s *Store) OpenPayment(ctx context.Context, number, sessionID string, amountCents int64) error {
	row, err := s.q.OrderTotalByNumber(ctx, number)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read order %s to open payment: %w", number, err)
	}
	if _, err := s.q.OpenPayment(ctx, db.OpenPaymentParams{
		OrderID:             row.ID,
		ProviderRef:         sessionID,
		IntendedAmountCents: amountCents,
	}); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.ConstraintName == "payments_open_refuses_settled_order" {
			return fmt.Errorf("%w: order %s", ErrNotOpenable, number)
		}
		return fmt.Errorf("open payment for order %s: %w", number, err)
	}
	return nil
}

// ProcessWebhook records an event and applies its effect in ONE transaction, so
// a failed effect un-claims it and Stripe's retry actually retries. claimed is
// false for a redelivery; apply may be nil.
func (s *Store) ProcessWebhook(
	ctx context.Context,
	ev *WebhookEvent,
	apply func(context.Context, *Store) error,
) (claimed bool, err error) {
	if !json.Valid(ev.Payload) {
		return false, errors.New("webhook payload is not valid JSON")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin webhook transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit

	txStore := &Store{pool: s.pool, q: s.q.WithTx(tx), tx: tx}

	n, err := txStore.q.RecordWebhookEvent(ctx, db.RecordWebhookEventParams{
		EventID:   ev.ID,
		Type:      ev.Type,
		ObjectRef: pgtype.Text{String: ev.ObjectRef, Valid: ev.ObjectRef != ""},
		Payload:   ev.Payload,
	})
	if err != nil {
		return false, fmt.Errorf("record webhook event %s: %w", ev.ID, err)
	}
	if n != 1 {
		return false, nil
	}

	if apply != nil {
		if err := apply(ctx, txStore); err != nil {
			return true, err
		}
	}

	if err := txStore.q.MarkWebhookProcessed(ctx, ev.ID); err != nil {
		return true, fmt.Errorf("mark webhook %s processed: %w", ev.ID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return true, fmt.Errorf("commit webhook %s: %w", ev.ID, err)
	}
	return true, nil
}

// postCapture calls the posting function inside a SAVEPOINT when it is running
// in somebody else's transaction.
//
// capture_payment can be refused — payments_refuse_cancelled_order is the whole
// reason ErrOrderCancelled exists — and a refusal ABORTS the transaction, so
// every later command fails with 25P02. Without the savepoint the caller cannot
// record what happened, mark the event processed, or commit: the webhook
// answered 500 and Stripe retried a capture that can never succeed, forever.
func (s *Store) postCapture(ctx context.Context, c *Capture) error {
	post := func(q *db.Queries) error {
		_, err := q.CapturePayment(ctx, db.CapturePaymentParams{
			ProviderRef:         c.SessionID,
			CapturedAmountCents: c.AmountRecv,
			CardBrand:           c.CardBrand,
			CardLast4:           c.CardLast4,
		})
		return err
	}
	if s.tx == nil {
		return post(s.q)
	}
	sp, err := s.tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("open a savepoint for the capture: %w", err)
	}
	if postErr := post(s.q.WithTx(sp)); postErr != nil {
		// Back to the savepoint, so the transaction is usable and the caller can
		// say what happened.
		_ = sp.Rollback(ctx) //nolint:errcheck // the error being reported is postErr
		return postErr
	}
	return sp.Commit(ctx)
}

// Unreconciled marks an event as accepted and NOT acted on, so a person has to.
//
// Called from inside ProcessWebhook's transaction, which is what makes it
// impossible to mark an event seen without also marking that it needs somebody
// — the rule ProcessWebhook already holds for the effect, applied to the
// outcome.
func (s *Store) Unreconciled(ctx context.Context, eventID, reason string) error {
	if err := s.q.MarkWebhookUnreconciled(ctx, db.MarkWebhookUnreconciledParams{
		EventID: eventID, Reason: reason,
	}); err != nil {
		return fmt.Errorf("mark webhook %s unreconciled: %w", eventID, err)
	}
	return nil
}

// Capture posts money against the payment the session opened. WHICH order comes
// from the payment row goen wrote at open time, never from the event.
func (s *Store) Capture(ctx context.Context, c *Capture) (orderNumber string, err error) {
	row, err := s.q.OrderByPaymentRef(ctx, c.SessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("find order for session %s: %w", c.SessionID, err)
	}
	if c.AmountRecv != row.IntendedAmountCents {
		return "", fmt.Errorf("session %s captured %d against an intent of %d for order %s",
			c.SessionID, c.AmountRecv, row.IntendedAmountCents, row.OrderNumber)
	}

	if err := s.postCapture(ctx, c); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.ConstraintName == "payments_refuse_cancelled_order" {
			return row.OrderNumber, fmt.Errorf("%w: order %s, session %s",
				ErrOrderCancelled, row.OrderNumber, c.SessionID)
		}
		return "", fmt.Errorf("capture payment for order %s: %w", row.OrderNumber, err)
	}

	if err := s.q.RecordPaidEvent(ctx, db.RecordPaidEventParams{
		OrderID: row.ID, Note: text(cardLabel(c)),
	}); err != nil {
		return "", fmt.Errorf("record paid event for order %s: %w", row.OrderNumber, err)
	}

	if _, err := s.q.AwardOrderPoints(ctx, db.AwardOrderPointsParams{
		OrderID: row.ID, ValidityDays: LoyaltyValidityDays,
		WindowDays: MembershipWindowDays,
	}); err != nil {
		return "", fmt.Errorf("award points for order %s: %w", row.OrderNumber, err)
	}

	if err := enqueueOrderPaid(ctx, s.q, row.ID, &OrderPaid{
		OrderNumber: row.OrderNumber, AmountCents: c.AmountRecv, Card: cardLabel(c),
	}); err != nil {
		return "", err
	}
	return row.OrderNumber, nil
}

// CancelSession marks an abandoned checkout's payment cancelled. cancel_payment
// refuses a succeeded row, so an expiry racing a capture changes nothing.
func (s *Store) CancelSession(ctx context.Context, sessionID string) error {
	if err := s.q.CancelPayment(ctx, sessionID); err != nil {
		return fmt.Errorf("cancel payment for session %s: %w", sessionID, err)
	}
	return nil
}

// OrderBelongsTo reports whether userID owns the named order.
func (s *Store) OrderBelongsTo(ctx context.Context, number, userID string) (bool, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return false, nil //nolint:nilerr // an unparseable id simply owns nothing
	}
	owns, err := s.q.OrderBelongsTo(ctx, db.OrderBelongsToParams{
		OrderNumber: number, UserID: uuid.NullUUID{UUID: id, Valid: true},
	})
	if err != nil {
		return false, fmt.Errorf("check order ownership: %w", err)
	}
	return owns, nil
}

// cardLabel is what the history shows about how an order was paid.
func cardLabel(c *Capture) string {
	if c.CardBrand == "" || c.CardLast4 == "" {
		return ""
	}
	return c.CardBrand + " ****" + c.CardLast4
}

// text is a nullable string for a column where "" means absent.
func text(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

// LoyaltyValidityDays mirrors loyalty.Validity, kept equal by TestTheLoyaltyConstantsMatchTheProgramme.
// This comment used to name TestTheAwardWindowMatchesTheProgramme, which does // named-test-exempt: this line RECORDS the name that was wrong
// not exist.
const LoyaltyValidityDays int32 = 365

// MembershipWindowDays mirrors loyalty.MembershipWindow.
const MembershipWindowDays int32 = 365
