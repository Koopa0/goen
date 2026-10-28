// Package credits is the staff desk for store credit; it holds the recent
// postings, one customer's ledger and the grant form; it does not hold
// membership tiers (admin/tiers).
package credits

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/money"
)

var (
	ErrNotFound = errors.New("credits: not found")
	ErrInvalid  = errors.New("credits: invalid input")
	// ErrRefused is a write the database declined; its message is the database's
	// own, because that names the rule.
	ErrRefused = errors.New("credits: refused")
)

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("credits: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

func (s *Store) creditRecipient(ctx context.Context, view *View) error {
	user, err := s.q.CustomerByEmail(ctx, strings.TrimSpace(view.Email))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read credit recipient: %w", err)
	}
	balance, err := s.q.CreditBalance(ctx, uuid.NullUUID{UUID: user.ID, Valid: true})
	if err != nil {
		return fmt.Errorf("read recipient balance: %w", err)
	}
	view.CustomerID, view.CustomerName, view.Email, view.BalanceCents = user.ID.String(), user.FullName, user.Email, balance
	return nil
}

func validateCreditGrant(view *View) (uuid.UUID, bool) {
	cents, amountOK := money.ParsePositiveDollars(view.Amount, MaxCreditGrant)
	view.GrantCents = cents
	view.AmountInvalid = !amountOK
	view.ReasonInvalid = strings.TrimSpace(view.Reason) == "" || utf8.RuneCountInString(view.Reason) > MaxCreditReasonRunes
	operationID, err := uuid.Parse(view.OperationID)
	if err != nil || operationID == uuid.Nil {
		view.OperationID = uuid.NewString()
		return uuid.Nil, false
	}
	return operationID, !view.AmountInvalid && !view.ReasonInvalid
}

// MaxCreditGrant bounds one posting, in cents: NT$100,000. Not a schema limit,
// a fat-finger guard on a form that gives money away.
const MaxCreditGrant = 10000000

// MaxCreditReasonRunes matches the back-office form and the durable ledger.
const MaxCreditReasonRunes = 200

// GrantCredit puts store credit on a customer's account. The amount is in cents
// and must be positive: a correction is its own posting with its own reason, so
// the ledger reads as a history rather than a figure somebody edited.
func (s *Store) GrantCredit(ctx context.Context, customerID uuid.UUID, amountCents int64, reason string, operationID uuid.UUID) (balanceCents int64, err error) {
	reason = strings.TrimSpace(reason)
	if customerID == uuid.Nil || reason == "" || utf8.RuneCountInString(reason) > MaxCreditReasonRunes || amountCents <= 0 || operationID == uuid.Nil {
		return 0, ErrInvalid
	}
	if amountCents > MaxCreditGrant {
		return 0, ErrInvalid
	}
	// The authenticated request context, not a parallel caller argument, owns
	// ledger attribution. The database role is shared by all staff requests, so
	// this is the last trustworthy per-person boundary before the role-specific
	// posting function verifies that the durable user is still staff/admin.
	actorID, ok := audit.Actor(ctx)
	if !ok {
		return 0, audit.ErrNoActor
	}

	event := audit.Event{
		Action: audit.ActionGrantCredit, Table: "store_credit_entries", ID: audit.EntityID(customerID),
		// The customer is named by ID and never by address: audit_events is
		// append-only and erase_user does not reach it, so an email written here
		// would outlive the erasure meant to remove it.
		Before: nil, After: audit.State{audit.FieldAmountCents: amountCents, audit.FieldReason: reason},
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin %s: %w", event.Action, err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)
	entryID, err := q.PostStoreCredit(ctx, db.PostStoreCreditParams{
		UserID: customerID, AmountCents: amountCents, Reason: reason,
		ActorUserID: actorID, OperationID: operationID,
	})
	if err != nil {
		return 0, db.WrapRefusal(err, ErrRefused)
	}
	// A retry of the same durable request observes the original posting and must
	// not manufacture a second audit row claiming money moved again.
	if entryID != uuid.Nil {
		if auditErr := audit.In(ctx, q, event); auditErr != nil {
			return 0, auditErr
		}
	}
	// Read INSIDE the same transaction, so the number shown is the one this grant
	// produced and not one a concurrent spend moved.
	balanceCents, err = q.CreditBalance(ctx, uuid.NullUUID{UUID: customerID, Valid: true})
	if err != nil {
		return 0, fmt.Errorf("read credit balance: %w", err)
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return 0, fmt.Errorf("commit %s: %w", event.Action, commitErr)
	}
	return balanceCents, nil
}

// position is a reader's place in the ledger. The query builds it as
// PageCursor, so its fields are the ordering values and nothing else.
type position struct {
	ID uuid.UUID
	At time.Time
}

// Ledger is one page of store-credit postings, newest first.
type Ledger struct {
	Postings []Posting
	Bound    keyset.Bound
	// Customer is who the page is narrowed to; nil on the page for every customer.
	Customer *Customer
}

// Customer is the holder of the account a Ledger is narrowed to.
type Customer struct {
	ID    uuid.UUID
	Email string
	Name  string
}

// Posting is one ledger row. Kind is store_credit_entries.kind, and Reason is
// only what staff typed. CustomerID and Email are empty once the account's
// owner is erased; ActorEmail is empty for a posting no person made.
type Posting struct {
	Kind        string
	AmountCents int64
	Reason      string
	At          time.Time
	CustomerID  string
	Email       string
	OrderNumber string
	ReturnID    string
	ActorEmail  string
	// RunningCents is the account's total through this posting in (created_at, id)
	// order. Only a Ledger narrowed to one customer carries it.
	RunningCents int64
}

// Credit reads one page of the credit ledger: every customer's when customer is
// not valid, else that customer's alone, with the running total of each posting.
// A customer who does not exist is ErrNotFound; one who has never held credit
// has an empty page.
func (s *Store) Credit(ctx context.Context, customer uuid.NullUUID, after string) (Ledger, error) {
	const desk = "/admin/credits"
	if !customer.Valid {
		return s.allPostings(ctx, desk, after)
	}
	holder, err := s.q.CreditCustomerByID(ctx, customer.UUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Ledger{}, ErrNotFound
	}
	if err != nil {
		return Ledger{}, fmt.Errorf("read ledger customer: %w", err)
	}
	ledger, err := s.customerPostings(ctx, keyset.ScopeURL(desk, "customer", holder.ID.String()), holder.ID, after)
	ledger.Customer = &Customer{ID: holder.ID, Email: holder.Email, Name: holder.FullName}
	return ledger, err
}

func (s *Store) allPostings(ctx context.Context, scope, after string) (Ledger, error) {
	from, resumed := keyset.Resume(scope, after, func(p position) bool { return p.ID != uuid.Nil })
	rows, err := s.q.RecentCredit(ctx, db.RecentCreditParams{HasCursor: resumed, AfterAt: from.At, AfterID: from.ID, RowLimit: keyset.PageLimit})
	if err != nil {
		return Ledger{}, fmt.Errorf("read credit ledger: %w", err)
	}
	rows, bound := keyset.PageBound(scope, resumed, rows, keyset.PageSize, func(r *db.RecentCreditRow) string { return r.PageCursor })
	ledger := Ledger{Bound: bound}
	for i := range rows {
		r := &rows[i]
		ledger.Postings = append(ledger.Postings, Posting{
			Kind: r.Kind, AmountCents: r.AmountCents, Reason: r.Reason, At: r.CreatedAt,
			CustomerID: r.CustomerID, Email: r.Email, OrderNumber: r.OrderNumber,
			ReturnID: r.ReturnID, ActorEmail: r.ActorEmail,
		})
	}
	return ledger, nil
}

func (s *Store) customerPostings(ctx context.Context, scope string, customer uuid.UUID, after string) (Ledger, error) {
	from, resumed := keyset.Resume(scope, after, func(p position) bool { return p.ID != uuid.Nil })
	rows, err := s.q.CustomerCredit(ctx, db.CustomerCreditParams{CustomerID: customer, HasCursor: resumed, AfterAt: from.At, AfterID: from.ID, RowLimit: keyset.PageLimit})
	if err != nil {
		return Ledger{}, fmt.Errorf("read customer credit ledger: %w", err)
	}
	rows, bound := keyset.PageBound(scope, resumed, rows, keyset.PageSize, func(r *db.CustomerCreditRow) string { return r.PageCursor })
	ledger := Ledger{Bound: bound}
	for i := range rows {
		r := &rows[i]
		ledger.Postings = append(ledger.Postings, Posting{
			Kind: r.Kind, AmountCents: r.AmountCents, Reason: r.Reason, At: r.CreatedAt,
			CustomerID: customer.String(), OrderNumber: r.OrderNumber, ReturnID: r.ReturnID,
			ActorEmail: r.ActorEmail, RunningCents: r.RunningCents,
		})
	}
	return ledger, nil
}
