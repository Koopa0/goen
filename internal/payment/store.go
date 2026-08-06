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

// Store is the database side of taking money.
//
// It holds the pool rather than a DBTX because processing a webhook spans two
// writes that must succeed or fail together — see [Store.ProcessWebhook].
type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

// NewStore returns a Store over pool.
func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("payment: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

// Order reads what an order OWES — its total less the store credit already spent on
// it, through order_amount_owed, which is the one definition the funding check and
// the capture guard also read.
//
// It used to read the gross total, and that was a way to lose money: the credit was
// debited at checkout, Stripe was asked for the full amount, and the capture guard
// then refused the payment because the order owed less. The customer paid, the
// webhook rolled back on every retry, and the order stayed unpaid forever.
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

	// No live hold is not an error: the sweeper has already put the stock back,
	// and the answer to "when does the hold run out" is "it has". The caller
	// refuses to open a session rather than sending somebody to a checkout for
	// goods that are back on the shelf.
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
	// SessionID is a Checkout Session still open at Stripe for exactly what the
	// order owes now, or "" when there is none to send the customer back to.
	SessionID string
	// Prior is how many payment rows the order has had, live or not. It goes into
	// the Stripe idempotency key — see [SessionKey].
	Prior int32
}

// PaymentAttempt reports whether a Checkout Session is already open for this
// order at this figure.
//
// It is the FIRST line of defence against charging one order twice. Without it
// every POST to the pay route created a new session and a new requires_payment
// row — open_payment only dedupes on (order_id, provider_ref), and each session
// brings its own id — so two tabs meant two real charges, with
// payments_one_capture_per_order refusing the second only after the money had
// left the customer's account.
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
//
// It runs BEFORE the customer is redirected, so a capture arriving over the
// webhook always has a row to land on. Opening it afterwards would leave a
// window in which Stripe reports money goen cannot attribute.
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
		return fmt.Errorf("open payment for order %s: %w", number, err)
	}
	return nil
}

// ProcessWebhook records an event and applies its effect, atomically.
//
// claimed is false when this delivery is a redelivery of an event already
// processed — the handler answers 200 and does nothing.
//
// # Why one transaction
//
// The claim and the effect used to be two calls, and the failure that exposes
// is the worst one available here: a capture that errors AFTER the claim
// committed leaves the event marked seen. Stripe retries, the retry is told
// "already seen", answers 200, and stops. The money is captured at Stripe and
// the order stays unpaid forever, with nothing in the logs after the first
// error to say so.
//
// Rolling the claim back with the effect is what makes a Stripe retry actually
// retry. The idempotency the (provider, event_id) key provides is only real if
// the row is not there unless the work is done.
//
// apply may be nil, for an event goen records but does not act on.
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
	// Rollback after a successful Commit is a no-op, so this is the safety net
	// for every early return below — and, for a failed apply, the thing that
	// un-claims the event.
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit

	txStore := &Store{pool: s.pool, q: s.q.WithTx(tx)}

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

// Capture posts money against the payment the session opened, and reports the
// order it belongs to.
//
// The webhook says WHAT happened. It does not say which order: that comes from
// the payment row goen wrote at open time, keyed on the session id. A webhook
// naming an order directly would let a forged — or merely misrouted — event
// mark the wrong one paid.
//
// The amount is checked against what was asked for. Stripe should never capture
// a different figure from the session it was given, and if it does, that is a
// reconciliation problem for a human rather than something to silently accept.
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

	if _, err := s.q.CapturePayment(ctx, db.CapturePaymentParams{
		ProviderRef:         c.SessionID,
		CapturedAmountCents: c.AmountRecv,
		CardBrand:           c.CardBrand,
		CardLast4:           c.CardLast4,
	}); err != nil {
		// Money for an order somebody called off while the session was still open
		// at Stripe. It is told apart from every other refusal because it is the
		// only one a retry can never fix — 'cancelled' is a terminal fulfilment
		// state — and because the money is real and needs refunding by hand.
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.ConstraintName == "payments_refuse_cancelled_order" {
			return row.OrderNumber, fmt.Errorf("%w: order %s, session %s",
				ErrOrderCancelled, row.OrderNumber, c.SessionID)
		}
		return "", fmt.Errorf("capture payment for order %s: %w", row.OrderNumber, err)
	}

	// The history entry rides the webhook's transaction, so an order cannot be
	// paid without its timeline saying when. No actor: the actor is Stripe.
	if err := s.q.RecordPaidEvent(ctx, db.RecordPaidEventParams{
		OrderID: row.ID, Note: text(cardLabel(c)),
	}); err != nil {
		return "", fmt.Errorf("record paid event for order %s: %w", row.OrderNumber, err)
	}

	// Points, in the SAME transaction as the capture.
	//
	// Awarding them afterwards would lose them when the process dies in
	// between; awarding them before would give points for money that has not
	// arrived. The function is idempotent on the order, so a webhook Stripe
	// delivered twice awards once — which is what lets this ride the same
	// at-least-once delivery the capture already tolerates.
	//
	// A guest order earns nothing and returns zero rather than erroring: guest
	// checkout is supported and points are a membership benefit.
	if _, err := s.q.AwardOrderPoints(ctx, db.AwardOrderPointsParams{
		OrderID: row.ID, ValidityDays: LoyaltyValidityDays,
		WindowDays: MembershipWindowDays,
	}); err != nil {
		return "", fmt.Errorf("award points for order %s: %w", row.OrderNumber, err)
	}

	// The receipt, in the same transaction for the same reason the points are.
	// A payment page that says 已付款 is a page; the email is the record the
	// customer looks for a month later.
	if err := enqueueOrderPaid(ctx, s.q, row.ID, &OrderPaid{
		OrderNumber: row.OrderNumber, AmountCents: c.AmountRecv, Card: cardLabel(c),
	}); err != nil {
		return "", err
	}
	return row.OrderNumber, nil
}

// CancelSession marks an abandoned checkout's payment cancelled.
//
// It runs in the webhook's own transaction, alongside the claim, for the reason
// the capture does: an event marked seen whose effect did not happen is an
// event Stripe will never resend.
//
// cancel_payment refuses a succeeded row, so an expiry that races a capture
// leaves the money where it is. That guard is in the FUNCTION rather than
// here, which is what makes it true for every caller.
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

// cardLabel is what the history shows about how an order was paid. Empty when
// Stripe sent no card details, which is the usual case.
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

// LoyaltyValidityDays mirrors loyalty.Validity.
//
// Not imported: internal/payment has no other reason to depend on
// internal/loyalty, and one number is a poor reason to couple two features.
// TestTheLoyaltyConstantsMatchTheProgramme keeps them equal.
//
// This comment used to name TestTheAwardWindowMatchesTheProgramme, which does // named-test-exempt: this line RECORDS the name that was wrong
// not exist — in a comment whose own subject is that failure, since the real
// test's doc records that both constants once claimed a guard that had never
// been written. The fix wrote the test and left the wrong name behind. It also
// cited admin.OutboxMaxAttempts as the same arrangement; that constant is gone,
// deliberately, because /admin/health imports internal/outbox and reads the real
// number now.
const LoyaltyValidityDays int32 = 365

// MembershipWindowDays mirrors loyalty.MembershipWindow, for the same reason
// and kept equal by the same test.
const MembershipWindowDays int32 = 365
