package returns

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

// Store is the database side of a customer's return request.
//
// It holds the pool because opening a request writes a header and its lines,
// and a header with no lines is a request for nothing.
type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

// NewStore returns a Store over pool.
func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("returns: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

// Order is an order as the return form sees it.
type Order struct {
	ID          uuid.UUID
	Number      string
	Fulfillment string
	Lines       []Line
	HasOpen     bool
	Existing    []Existing
}

// Existing is a request already on this order.
type Existing struct {
	Status     string
	Reason     string
	Resolution string
	CreatedAt  string
	DecidedAt  string
}

// Returnable reports whether any line still has units that can be sent back.
func (o *Order) Returnable() bool {
	for i := range o.Lines {
		if o.Lines[i].Returnable > 0 {
			return true
		}
	}
	return false
}

// Order reads what an order offers a return form.
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
	for i := range existing {
		e := &existing[i]
		o.Existing = append(o.Existing, Existing{
			Status: e.Status, Reason: e.Reason, Resolution: e.Resolution.String,
			CreatedAt: e.CreatedAt.Format("2006-01-02 15:04"),
			DecidedAt: nullableTime(e.DecidedAt),
		})
	}
	return o, nil
}

// Open files a return request.
//
// The header and its lines are ONE transaction. A request with no lines is a
// row in the back office queue asking for nothing, and return_request_lines is
// where every quantity guard lives — so a header committed on its own is a
// claim that has passed no check at all.
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

	// What the form asked for, checked against what this order can offer. The
	// database enforces the same ceiling; refusing here is what turns a
	// constraint violation into a message the customer can act on.
	allowed := make(map[string]int32, len(o.Lines))
	for i := range o.Lines {
		allowed[o.Lines[i].ID] = o.Lines[i].Returnable
	}
	wanted := make(map[uuid.UUID]int32, len(req.Lines))
	for id, qty := range req.Lines {
		if qty == 0 {
			continue
		}
		lineID, wantErr := parseWanted(id, qty, allowed)
		if wantErr != nil {
			return wantErr
		}
		wanted[lineID] = qty
	}
	if len(wanted) == 0 {
		return ErrInvalid
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin return request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	requestID, err := q.CreateReturnRequest(ctx, db.CreateReturnRequestParams{
		OrderID: o.ID, RequestedByUserID: userID, Reason: req.Reason,
	})
	if err != nil {
		return fmt.Errorf("create return request for %s: %w", number, err)
	}
	for lineID, qty := range wanted {
		if lineErr := q.CreateReturnRequestLine(ctx, db.CreateReturnRequestLineParams{
			OrderID: o.ID, ReturnRequestID: requestID,
			OrderLineID: lineID, Quantity: qty,
		}); lineErr != nil {
			// return_within_shipment is the rule most likely to speak here, and
			// it means the form was built against a state that has since moved.
			return fmt.Errorf("%w: %s", ErrInvalid, lineErr.Error())
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit return request: %w", err)
	}
	return nil
}

func nullableTime(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.Format("2006-01-02 15:04")
}

// parseWanted turns one submitted line into a validated id, or refuses it.
//
// Split out of Open so that function stays under the complexity limit; the
// check itself is the same one the database makes, made here so an over-claim
// reaches the customer as a message rather than as a constraint violation.
func parseWanted(id string, qty int32, allowed map[string]int32) (uuid.UUID, error) {
	ceiling, ok := allowed[id]
	if !ok || qty > ceiling {
		return uuid.UUID{}, ErrInvalid
	}
	lineID, err := uuid.Parse(id)
	if err != nil {
		return uuid.UUID{}, ErrInvalid
	}
	return lineID, nil
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
