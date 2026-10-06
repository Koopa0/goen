package returnpage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/shoptime"
)

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("returnpage: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

type Order struct {
	ID          uuid.UUID
	Number      string
	Fulfillment string
	Lines       []Line
	HasOpen     bool
	Existing    []Existing
}

type Existing struct {
	Status     string
	Reason     string
	Resolution string
	CreatedAt  string
	DecidedAt  string
}

func (o *Order) Returnable() bool {
	for i := range o.Lines {
		if o.Lines[i].Returnable > 0 {
			return true
		}
	}
	return false
}

func (s *Store) Order(ctx context.Context, number string) (*Order, error) {
	row, err := s.q.OrderForReturn(ctx, number)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read order %s for return: %w", number, err)
	}

	lines, err := s.q.ReturnableLines(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("read returnable lines of %s: %w", number, err)
	}
	open, err := s.q.HasOpenReturn(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("read open returns of %s: %w", number, err)
	}
	existing, err := s.q.ReturnsForOrder(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("read returns of %s: %w", number, err)
	}

	o := &Order{
		ID: row.ID, Number: row.OrderNumber, Fulfillment: row.FulfillmentStatus,
		HasOpen: open,
		Lines:   make([]Line, 0, len(lines)),
	}
	for i := range lines {
		l := &lines[i]
		o.Lines = append(o.Lines, Line{
			ID: l.ID.String(), SKU: l.SKU, Name: l.ProductName,
			Label: l.VariantLabel.String, UnitCents: l.UnitPriceCents,
			Returnable: l.Returnable,
		})
	}
	now := time.Now()
	for i := range existing {
		e := &existing[i]
		o.Existing = append(o.Existing, Existing{
			Status: e.Status, Reason: e.Reason, Resolution: e.Resolution.String,
			CreatedAt: shoptime.DateTimeText(ctx, e.CreatedAt, now),
			DecidedAt: nullableTime(ctx, e.DecidedAt, now),
		})
	}
	return o, nil
}

// Open files the header and its lines in one transaction: every quantity guard
// lives on the lines, so a header committed alone has passed no check at all.
func (s *Store) Open(ctx context.Context, number string, userID uuid.NullUUID, req *Request) error {
	if err := req.Validate(); err != nil {
		return err
	}

	o, err := s.Order(ctx, number)
	if err != nil {
		return err
	}
	if o.HasOpen {
		return ErrAlreadyOpen
	}
	if !o.Returnable() {
		return ErrNotReturnable
	}

	wanted, err := wantedLines(req.Lines, o.Lines)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin return request: %w", err)
	}
	defer pgtx.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	requestID, err := q.CreateReturnRequest(ctx, db.CreateReturnRequestParams{
		OrderID: o.ID, RequestedByUserID: userID, Reason: req.Reason,
	})
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.ConstraintName == "return_requests_one_open" {
			return ErrAlreadyOpen
		}
		return fmt.Errorf("create return request for %s: %w", number, err)
	}
	for lineID, qty := range wanted {
		if lineErr := q.CreateReturnRequestLine(ctx, db.CreateReturnRequestLineParams{
			OrderID: o.ID, ReturnRequestID: requestID,
			OrderLineID: lineID, Quantity: qty,
		}); lineErr != nil {
			return returnLineError(lineID, lineErr)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.ConstraintName == "return_credit_requires_live_account" {
			return ErrAccountErased
		}
		return fmt.Errorf("commit return request: %w", err)
	}
	return nil
}

func wantedLines(submitted map[string]int32, lines []Line) (map[uuid.UUID]int32, error) {
	allowed := make(map[string]int32, len(lines))
	for i := range lines {
		allowed[lines[i].ID] = lines[i].Returnable
	}
	wanted := make(map[uuid.UUID]int32, len(submitted))
	for id, qty := range submitted {
		if qty == 0 {
			continue
		}
		lineID, err := parseWanted(id, qty, allowed)
		if err != nil {
			return nil, err
		}
		wanted[lineID] = qty
	}
	if len(wanted) == 0 {
		return nil, ErrInvalid
	}
	return wanted, nil
}

func returnLineError(lineID uuid.UUID, err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
		pgErr.ConstraintName == "return_within_shipment" {
		return ErrTooMany
	}
	return fmt.Errorf("add return line for order line %s: %w", lineID, err)
}

func nullableTime(ctx context.Context, t pgtype.Timestamptz, now time.Time) string {
	if !t.Valid {
		return ""
	}
	return shoptime.DateTimeText(ctx, t.Time, now)
}

// parseWanted repeats the database's check so it becomes a message on the form.
func parseWanted(id string, qty int32, allowed map[string]int32) (uuid.UUID, error) {
	ceiling, ok := allowed[id]
	if !ok {
		return uuid.UUID{}, ErrInvalid
	}
	if qty > ceiling {
		return uuid.UUID{}, ErrTooMany
	}
	lineID, err := uuid.Parse(id)
	if err != nil {
		return uuid.UUID{}, ErrInvalid
	}
	return lineID, nil
}
