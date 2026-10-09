package loyalty

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

func (s *Store) creditRecipient(ctx context.Context, view *admin.CreditView) error {
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

func validateCreditGrant(view *admin.CreditView) (uuid.UUID, bool) {
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
		Before: nil, After: map[string]any{"amount_cents": amountCents, "reason": reason},
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin %s: %w", event.Action, err)
	}
	defer pgtx.Rollback(ctx, tx)
	q := s.q.WithTx(tx)
	entryID, err := q.PostStoreCredit(ctx, db.PostStoreCreditParams{
		UserID: customerID, AmountCents: amountCents, Reason: reason,
		ActorUserID: actorID, OperationID: operationID,
	})
	if err != nil {
		return 0, pgerr.WrapRefusal(err, ErrRefused)
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

func (s *Store) Credit(ctx context.Context, after ...string) (admin.CreditView, error) {
	return s.CreditForCustomer(ctx, "", after...)
}

func (s *Store) CreditForCustomer(ctx context.Context, customer string, after ...string) (admin.CreditView, error) {
	view := admin.CreditView{}
	user, err := s.creditCustomer(ctx, customer)
	if err != nil {
		return view, err
	}
	if customer != "" {
		view.FilterCustomerID, view.FilterCustomerName, view.Email = user.ID.String(), user.FullName, user.Email
	}
	scope := web.ScopeURL("/admin/credit", "customer", view.FilterCustomerID)
	from, resumed := web.ResumeKeyset(scope, after, func(p position) bool { return p.ID != uuid.Nil })
	rows, err := s.q.RecentCredit(ctx, db.RecentCreditParams{HasCustomer: customer != "", CustomerID: user.ID, HasCursor: resumed, AfterAt: from.At, AfterID: from.ID, RowLimit: web.PageLimit})
	if err != nil {
		return admin.CreditView{}, fmt.Errorf("read credit ledger: %w", err)
	}
	rows, bound := web.PageBound(scope, resumed, rows, web.PageSize, func(r *db.RecentCreditRow) string { return r.PageCursor })
	view.Bound = bound
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, admin.CreditEntry{
			Email:        r.Email,
			AmountCents:  r.AmountCents,
			Reason:       r.Reason,
			At:           shoptime.Minute(r.CreatedAt),
			CustomerID:   r.CustomerID,
			OrderNumber:  r.OrderNumber,
			ReturnID:     r.ReturnID,
			BalanceCents: r.BalanceCents,
			ActorName:    r.ActorName,
		})
	}
	return view, nil
}

func (s *Store) creditCustomer(ctx context.Context, customer string) (db.CreditCustomerByIDRow, error) {
	var empty db.CreditCustomerByIDRow
	if customer == "" {
		return empty, nil
	}
	id, parseErr := uuid.Parse(customer)
	if parseErr != nil || id == uuid.Nil {
		return empty, ErrNotFound
	}
	user, err := s.q.CreditCustomerByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, fmt.Errorf("read ledger customer: %w", err)
	}
	if user.FullName == "" {
		user.FullName = user.Email
	}
	return user, nil
}
