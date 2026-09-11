package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
}

// webhookTx applies one claimed webhook inside the transaction that records it.
// Its queries and every savepoint are bound to the same transaction. It is valid
// only during the processWebhook callback and must not be retained.
type webhookTx struct {
	q         *db.Queries
	tx        pgx.Tx
	eventID   string
	objectRef string
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
	hold, err := s.q.OrderHoldExpiry(ctx, db.OrderHoldExpiryParams{
		OrderID: row.ID,
		RequiredLifetime: pgtype.Interval{
			Microseconds: (minSessionLifetime + sessionStartMargin).Microseconds(), Valid: true,
		},
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("read stock hold of order %s: %w", number, err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		hold.ExpiresAt = time.Time{}
	}

	o := &Order{
		Number:            row.OrderNumber,
		TotalCents:        row.TotalCents,
		Email:             row.Email,
		Paid:              paid,
		Fulfillment:       row.FulfillmentStatus,
		HoldExpiresAt:     hold.ExpiresAt,
		holdCoversSession: hold.CoversSession,
		Lines:             make([]Line, 0, len(lines)),
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
	// SessionID is the order's one non-terminal provider session, at any figure.
	SessionID string
	// IntendedAmountCents is what that Session was opened to collect. It is
	// retained even when the order's current owed amount has changed, because a
	// provider-complete obsolete Session must consume its original generation.
	IntendedAmountCents int64
	// MatchesOwed says that session was opened for what the order owes now.
	MatchesOwed bool
	// NeedsReconciliation says a linked provider event still needs a person;
	// opening another place to pay would risk charging the order twice.
	NeedsReconciliation bool
	// Prior is how many payment rows the order has had, live or not.
	Prior int32
}

// PaymentAttempt reports the order's provider state before another Checkout
// Session is considered. An old-amount session is returned rather than hidden:
// it remains a place the customer can pay until Stripe confirms it expired.
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
	return &Attempt{
		SessionID: row.LiveSession, IntendedAmountCents: row.LiveSessionAmountCents,
		MatchesOwed:         row.LiveSessionMatchesOwed,
		NeedsReconciliation: row.NeedsReconciliation, Prior: row.PriorAttempts,
	}, nil
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
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
			switch pgErr.ConstraintName {
			case "payments_open_refuses_settled_order", "payments_open_refuses_funded_order",
				"payments_open_matches_order",
				"payments_open_needs_reconciliation", "payments_open_refuses_seen_provider_ref",
				"payments_one_active_per_order":
				return fmt.Errorf("%w: order %s: %w", ErrNotOpenable, number, err)
			}
		}
		return fmt.Errorf("open payment for order %s: %w", number, err)
	}
	return nil
}

// cancelExpiredPayment closes the local attempt only after Stripe accepted the
// handler's explicit expiration request. It is package-private so no caller can
// turn an unverified browser claim into a payment state transition.
func (s *Store) cancelExpiredPayment(ctx context.Context, sessionID string) error {
	if err := s.q.CancelPayment(ctx, sessionID); err != nil {
		return fmt.Errorf("cancel expired session %s: %w", sessionID, err)
	}
	return nil
}

// recordExpiredPayment persists the terminal provider fact only after Stripe
// has confirmed expiration. It deliberately bypasses order admission rules:
// the order changing while Stripe was called is why this tombstone is needed.
func (s *Store) recordExpiredPayment(
	ctx context.Context, number, sessionID string, amountCents int64,
) error {
	row, err := s.q.OrderTotalByNumber(ctx, number)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read order %s for expired payment: %w", number, err)
	}
	if _, err := s.q.RecordExpiredPayment(ctx, db.RecordExpiredPaymentParams{
		OrderID: row.ID, ProviderRef: sessionID, IntendedAmountCents: amountCents,
	}); err != nil {
		return fmt.Errorf("record expired session %s for order %s: %w", sessionID, number, err)
	}
	return nil
}

// recordCompletePayment consumes a provider-complete idempotency generation
// without claiming the Session expired or that money was captured. The posting
// function chooses requires_reconciliation or the terminal reconciled state
// from the durable webhook history while holding the shared provider lock.
func (s *Store) recordCompletePayment(
	ctx context.Context, number, sessionID string, amountCents int64,
) error {
	row, err := s.q.OrderTotalByNumber(ctx, number)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read order %s for complete payment: %w", number, err)
	}
	if _, err := s.q.RecordCompletePayment(ctx, db.RecordCompletePaymentParams{
		OrderID: row.ID, ProviderRef: sessionID, IntendedAmountCents: amountCents,
	}); err != nil {
		return fmt.Errorf("record complete session %s for order %s: %w", sessionID, number, err)
	}
	return nil
}

// AttributeCompleteCapture posts a complete Session that staff have verified
// as paid. tx is required (rather than a pool-backed query handle) so the
// capture, order timeline, loyalty award, receipt and caller's audit row either
// all commit or all roll back. The amount comes from the immutable payment row
// inside attribute_complete_payment_paid; no operator-supplied amount crosses
// this boundary.
func AttributeCompleteCapture(
	ctx context.Context, tx pgx.Tx, providerRef string,
) (bool, error) {
	if tx == nil {
		return false, ErrNoTransaction
	}
	q := db.New(tx)
	row, err := q.AttributeCompletePaymentPaid(ctx, providerRef)
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.ConstraintName == "payments_capture_refuses_released_stock" {
			return false, fmt.Errorf("%w: provider reference %s: %w",
				ErrReleasedStockRequiresRefund, providerRef, err)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("attribute complete payment %s: %w", providerRef, err)
	}
	capture := Capture{SessionID: providerRef, AmountRecv: row.AmountCents}
	if err := CompleteFunding(ctx, q, row.OrderID, row.OrderNumber, capture); err != nil {
		return false, err
	}
	return true, nil
}

// processWebhook records an event and applies its effect in ONE transaction, so
// a failed effect un-claims it and Stripe's retry actually retries. claimed is
// false for a redelivery; apply may be nil.
func (s *Store) processWebhook(
	ctx context.Context,
	ev *webhookEvent,
	apply func(context.Context, *webhookTx) error,
) (claimed bool, err error) {
	if !json.Valid(ev.Payload) {
		return false, errors.New("webhook payload is not valid JSON")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin webhook transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit

	webhook := &webhookTx{
		q: s.q.WithTx(tx), tx: tx, eventID: ev.ID, objectRef: ev.ObjectRef,
	}

	// A Checkout Session may emit its webhook while the request that created it
	// is still persisting the local payment row. open_payment takes this same
	// transaction lock first, making the two outcomes exhaustive: the webhook
	// sees the payment, or the opener sees the durable event and refuses a late
	// link. Events without a provider object have no identity that could be
	// linked later.
	if ev.ObjectRef != "" {
		if lockErr := webhook.q.LockPaymentProviderRef(ctx, ev.ObjectRef); lockErr != nil {
			return false, fmt.Errorf("lock provider reference %s for webhook %s: %w",
				ev.ObjectRef, ev.ID, lockErr)
		}
	}

	n, err := webhook.q.RecordWebhookEvent(ctx, db.RecordWebhookEventParams{
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
		if err := apply(ctx, webhook); err != nil {
			return true, err
		}
	}

	if err := webhook.q.MarkWebhookProcessed(ctx, ev.ID); err != nil {
		return true, fmt.Errorf("mark webhook %s processed: %w", ev.ID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return true, fmt.Errorf("commit webhook %s: %w", ev.ID, err)
	}
	return true, nil
}

// postCapture calls the posting function inside a SAVEPOINT.
//
// capture_payment can be refused — payments_refuse_cancelled_order is the whole
// reason ErrOrderCancelled exists — and a refusal ABORTS the transaction, so
// every later command fails with 25P02. Without the savepoint the caller cannot
// record what happened, mark the event processed, or commit.
func (w *webhookTx) postCapture(ctx context.Context, c Capture) error {
	post := func(q *db.Queries) error {
		_, err := q.CapturePayment(ctx, db.CapturePaymentParams{
			ProviderRef:         c.SessionID,
			CapturedAmountCents: c.AmountRecv,
			CardBrand:           c.CardBrand,
			CardLast4:           c.CardLast4,
		})
		return err
	}
	sp, err := w.tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("open a savepoint for the capture: %w", err)
	}
	if postErr := post(w.q.WithTx(sp)); postErr != nil {
		// Back to the savepoint, so the transaction is usable and the caller can
		// say what happened.
		_ = sp.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck // the error being reported is postErr
		return postErr
	}
	if err := sp.Commit(ctx); err != nil {
		return fmt.Errorf("release the capture savepoint: %w", err)
	}
	return nil
}

// Unreconciled marks this claimed event as accepted and NOT acted on, so a
// person has to. The event identity belongs to the transaction capability;
// callers cannot redirect the outcome to a different event.
//
// It runs inside processWebhook's transaction, so an event cannot be recorded
// as seen without also being recorded as needing somebody.
func (w *webhookTx) Unreconciled(ctx context.Context, reason string) error {
	marked, err := w.q.MarkWebhookUnreconciled(ctx, db.MarkWebhookUnreconciledParams{
		EventID: w.eventID, Reason: reason,
	})
	if err != nil {
		return fmt.Errorf("mark webhook %s unreconciled: %w", w.eventID, err)
	}
	if !marked {
		return fmt.Errorf("mark webhook %s unreconciled: event was absent, processed, reconciled or already flagged", w.eventID)
	}
	return nil
}

// Capture posts money against the payment the session opened. WHICH order comes
// from the payment row goen wrote at open time, never from the event.
func (w *webhookTx) Capture(ctx context.Context, c Capture) (orderNumber string, err error) {
	if c.SessionID != w.objectRef {
		return "", fmt.Errorf("webhook object %q cannot capture session %q",
			w.objectRef, c.SessionID)
	}
	row, err := w.q.OrderByPaymentRef(ctx, c.SessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("find order for session %s: %w", c.SessionID, err)
	}
	if c.AmountRecv != row.IntendedAmountCents {
		return row.OrderNumber, fmt.Errorf("%w: session %s captured %d against an intent of %d for order %s",
			errCaptureRefused,
			c.SessionID, c.AmountRecv, row.IntendedAmountCents, row.OrderNumber)
	}
	// Manual paid attribution can win the provider-reference lock before a late
	// signed webhook. The money and its side effects already committed together;
	// the event is still marked processed, but must not append them twice.
	if row.Status == "succeeded" {
		return row.OrderNumber, nil
	}

	if err := w.postCapture(ctx, c); err != nil {
		return capturePostingError(row.OrderNumber, c.SessionID, err)
	}
	if err := CompleteFunding(ctx, w.q, row.ID, row.OrderNumber, c); err != nil {
		return "", err
	}
	return row.OrderNumber, nil
}

// CompleteFunding appends the paid timeline event, loyalty award and receipt
// once an order's funding has closed. A card capture passes the provider facts;
// a zero-owed picking transition passes none.
func CompleteFunding(
	ctx context.Context, q *db.Queries, orderID uuid.UUID, orderNumber string, c Capture,
) error {
	has, err := q.OrderHasPaidEvent(ctx, orderID)
	if err != nil {
		return fmt.Errorf("read paid event of order %s: %w", orderNumber, err)
	}
	if has {
		return nil
	}

	if err := q.RecordPaidEvent(ctx, db.RecordPaidEventParams{
		OrderID: orderID, Note: text(cardLabel(c)),
	}); err != nil {
		return fmt.Errorf("record paid event for order %s: %w", orderNumber, err)
	}

	if _, err := q.AwardOrderPoints(ctx, orderID); err != nil {
		return fmt.Errorf("award points for order %s: %w", orderNumber, err)
	}

	if err := enqueueOrderPaid(ctx, q, orderID, &OrderPaid{
		OrderNumber: orderNumber, AmountCents: c.AmountRecv, Card: cardLabel(c),
	}); err != nil {
		return err
	}
	return nil
}

func capturePostingError(orderNumber, sessionID string, cause error) (string, error) {
	pgErr, ok := errors.AsType[*pgconn.PgError](cause)
	if !ok {
		return "", fmt.Errorf("capture payment for order %s: %w", orderNumber, cause)
	}
	if pgErr.ConstraintName == "payments_refuse_cancelled_order" {
		return orderNumber, fmt.Errorf("%w: order %s, session %s",
			ErrOrderCancelled, orderNumber, sessionID)
	}
	if isDurableCaptureRefusal(pgErr.ConstraintName) {
		// These are deterministic facts about this payment/order/payload. The
		// savepoint restored the transaction, so the caller can commit the event
		// as unreconciled rather than ask Stripe to retry identical money forever.
		return orderNumber, fmt.Errorf("%w: capture for order %s was refused by %s",
			errCaptureRefused, orderNumber, pgErr.ConstraintName)
	}
	return "", fmt.Errorf("capture payment for order %s: %w", orderNumber, cause)
}

// durableCaptureRefusals is the complete set of stable database refusals a
// verified capture can reach.
var durableCaptureRefusals = [...]string{
	"payments_capture_matches_order",
	"payments_capture_refuses_released_stock",
	"payments_last4_format",
	"payments_no_regression",
	"payments_one_capture_per_order",
	"payments_require_complete_order",
}

// isDurableCaptureRefusal classifies only constraints a verified capture can
// reach and identical webhook bytes cannot repair. A terminal local transition
// and an existing capture for the order are just as permanent as an amount or
// completeness mismatch. In particular, payments_captured_in_range is absent:
// Capture first proves the provider amount equals the already range-checked
// intended amount, so that branch is unreachable and should not be advertised
// as handled.
func isDurableCaptureRefusal(constraint string) bool {
	return slices.Contains(durableCaptureRefusals[:], constraint)
}

// CancelSession marks this webhook object's abandoned checkout cancelled. The
// provider reference belongs to the transaction capability: accepting another
// one here would let a callback mutate an identity whose advisory lock it does
// not hold. cancel_payment refuses a succeeded row, so expiry racing capture
// changes nothing.
func (w *webhookTx) CancelSession(ctx context.Context) error {
	if w.objectRef == "" {
		return errors.New("cancel payment for webhook without a provider object")
	}
	if err := w.q.CancelPayment(ctx, w.objectRef); err != nil {
		return fmt.Errorf("cancel payment for session %s: %w", w.objectRef, err)
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
func cardLabel(c Capture) string {
	if c.CardBrand == "" || c.CardLast4 == "" {
		return ""
	}
	return c.CardBrand + " ****" + c.CardLast4
}

// text is a nullable string for a column where "" means absent.
func text(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}
