// Package stock is the back office's stock desk: the variants list with its sold-out
// filter, one variant's movement ledger, and the writes that move stock, retire
// a variant or reprice it.
package stock

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/product/inventory"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/web/formtext"
)

var (
	ErrNotFound = errors.New("stock: not found")
	// ErrRefused is a write the database declined; its message is the database's
	// own, because that names the rule.
	ErrRefused          = errors.New("stock: refused")
	ErrMovementConflict = errors.New("stock: movement conflict")
)

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("stock: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

// variantPosition is a reader's place in the variants list. The query builds it
// as PageCursor, so its fields are the ordering values and nothing else.
type variantPosition struct {
	Number   int32
	Name     string
	Position int32
	ID       uuid.UUID
}

// movementPosition is a reader's place in one variant's ledger.
type movementPosition struct {
	ID uuid.UUID
	At time.Time
}

func (s *Store) Variants(ctx context.Context, soldOutOnly bool, term, after string) (VariantsView, error) {
	term = formtext.SearchTerm(term)
	soldOut := ""
	if soldOutOnly {
		soldOut = "1"
	}
	scope := keyset.ScopeURL("/admin/stock", "soldout", soldOut, "q", term)
	from, resumed := keyset.Resume(scope, after, func(p variantPosition) bool {
		// Postgres refuses a NUL in text, so a crafted name would turn a bad link
		// into a 500 instead of the first page.
		return p.ID != uuid.Nil && !strings.ContainsRune(p.Name, 0)
	})
	rows, err := s.q.AdminVariants(ctx, db.AdminVariantsParams{Locale: string(i18n.FromContext(ctx)), HasCursor: resumed, AfterNumber: from.Number, AfterName: from.Name, AfterPosition: from.Position, AfterID: from.ID, SoldOutOnly: soldOutOnly, EscapedTerm: db.EscapeLike(term), RowLimit: keyset.PageLimit})
	if err != nil {
		return VariantsView{}, fmt.Errorf("read variants: %w", err)
	}
	rows, bound := keyset.PageBound(scope, resumed, rows, keyset.PageSize, func(r *db.AdminVariantsRow) string { return r.PageCursor })
	view := VariantsView{Bound: bound, SoldOutOnly: soldOutOnly, Term: term}
	for i := range rows {
		view.Variants = append(view.Variants, variantRow(&rows[i]))
	}
	return view, nil
}

// Adjust moves stock through the ledger, which record_inventory_movement
// is the only door to. The idempotency key is the caller's, so a resubmitted
// form is one adjustment.
func (s *Store) Adjust(ctx context.Context, sku string, delta int32, actorID, key string) error {
	v, err := s.q.AdminVariantBySKU(ctx, sku)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read variant: %w", err)
	}
	actor, err := uuid.Parse(actorID)
	if err != nil {
		return fmt.Errorf("adjust stock: actor %q is not a user id: %w", actorID, err)
	}
	before := audit.State{audit.FieldSKU: sku}
	err = audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionAdjustStock, Table: "product_variants", ID: audit.EntityID(v.ID),
		Before: before,
		After:  audit.State{audit.FieldDelta: delta},
	},
		func(ctx context.Context, q *db.Queries) error {
			replaced, lockErr := lockVariant(ctx, q, v.ID, sku)
			if lockErr != nil {
				return lockErr
			}
			before[audit.FieldStock] = replaced.StockQuantity
			if moveErr := q.AdjustStock(ctx, db.AdjustStockParams{
				VariantID: v.ID, Delta: delta, IdempotencyKey: key, ActorUserID: actor,
			}); moveErr != nil {
				return db.WrapRefusal(moveErr, ErrRefused)
			}
			return nil
		})
	return s.settleReplay(ctx, err, v.ID, delta, inventory.ReasonAdjustment, key)
}

// Receive books a delivery in, through the ledger's own 'receipt' reason,
// so that goods a shop bought are distinguishable in its own ledger from a
// staff member correcting a miscount.
func (s *Store) Receive(ctx context.Context, sku string, quantity int32, actorID, key string) error {
	v, err := s.q.AdminVariantBySKU(ctx, sku)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read variant: %w", err)
	}
	actor, err := uuid.Parse(actorID)
	if err != nil {
		return fmt.Errorf("receive stock: actor %q is not a user id: %w", actorID, err)
	}
	before := audit.State{audit.FieldSKU: sku}
	err = audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionReceiveStock, Table: "product_variants", ID: audit.EntityID(v.ID),
		Before: before,
		After:  audit.State{audit.FieldReceived: quantity, audit.FieldPreorderReleaseOn: ""},
	},
		func(ctx context.Context, q *db.Queries) error {
			replaced, lockErr := lockVariant(ctx, q, v.ID, sku)
			if lockErr != nil {
				return lockErr
			}
			before[audit.FieldStock] = replaced.StockQuantity
			before[audit.FieldPreorderReleaseOn] = arrivalInput(replaced.PreorderReleaseOn)
			if moveErr := q.ReceiveStock(ctx, db.ReceiveStockParams{
				VariantID: v.ID, Delta: quantity, IdempotencyKey: key, ActorUserID: actor,
			}); moveErr != nil {
				return db.WrapRefusal(moveErr, ErrRefused)
			}
			if clearErr := q.SetVariantArrival(ctx, db.SetVariantArrivalParams{ID: v.ID}); clearErr != nil {
				return db.WrapRefusal(clearErr, ErrRefused)
			}
			return nil
		})
	return s.settleReplay(ctx, err, v.ID, quantity, inventory.ReasonReceipt, key)
}

// lockVariant holds the variant's row for the rest of the write and reads what
// the write replaces. The caller's audit Event carries the map it fills in, which
// audit.Run encodes only after the write.
func lockVariant(ctx context.Context, q *db.Queries, id uuid.UUID, sku string) (db.LockVariantForChangeRow, error) {
	v, err := q.LockVariantForChange(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.LockVariantForChangeRow{}, ErrNotFound
	}
	if err != nil {
		return db.LockVariantForChangeRow{}, fmt.Errorf("lock variant %s: %w", sku, err)
	}
	return v, nil
}

// settleReplay turns the ledger's refusal of a key it already holds into the
// success it earlier answered, when the held movement is this very one. The
// refusal rolled back the whole statement, so stock moved once; reporting it
// as refused sends a staff member to re-enter it from a fresh form, which
// lands it twice. A key reused for a different movement stays refused.
func (s *Store) settleReplay(
	ctx context.Context, err error, variantID uuid.UUID, delta int32, reason inventory.MovementReason, key string,
) error {
	if err == nil || !db.IsConstraint(err, "inventory_movements_idempotency_key") {
		return err
	}
	applied, checkErr := s.q.StockMovementApplied(ctx, db.StockMovementAppliedParams{
		IdempotencyKey: key, VariantID: variantID, Delta: delta, Reason: string(reason),
	})
	if checkErr != nil {
		return errors.Join(err, fmt.Errorf("check recorded stock movement: %w", checkErr))
	}
	if !applied {
		return errors.Join(err, ErrMovementConflict)
	}
	return nil
}

// SetActive retires or restores a variant. A refusal here is
// sale_campaign_variant_still_valid (the last discounted variant of a product
// some campaign features) or, at commit, the deferred
// products_active_has_variant (the last active variant of a published product).
func (s *Store) SetActive(ctx context.Context, sku string, active bool) error {
	v, err := s.q.AdminVariantBySKU(ctx, sku)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read variant: %w", err)
	}
	before := audit.State{audit.FieldSKU: sku}
	err = audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionRetireVariant, Table: "product_variants", ID: audit.EntityID(v.ID),
		Before: before,
		After:  audit.State{audit.FieldActive: active},
	},
		func(ctx context.Context, q *db.Queries) error {
			replaced, lockErr := lockVariant(ctx, q, v.ID, sku)
			if lockErr != nil {
				return lockErr
			}
			before[audit.FieldActive] = replaced.IsActive
			return q.SetVariantActive(ctx, db.SetVariantActiveParams{
				ID: v.ID, IsActive: active,
			})
		})
	return db.WrapRefusal(err, ErrRefused)
}

// SetPrice reprices a variant. product_variants_compare_at_is_higher
// refuses a "sale" that is not a saving.
func (s *Store) SetPrice(ctx context.Context, sku string, price, compareAt int64) error {
	v, err := s.q.AdminVariantBySKU(ctx, sku)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read variant: %w", err)
	}
	var cmp pgtype.Int8
	if compareAt > 0 {
		cmp = pgtype.Int8{Int64: compareAt, Valid: true}
	}
	before := audit.State{audit.FieldSKU: sku}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionRepriceVariant, Table: "product_variants", ID: audit.EntityID(v.ID),
		Before: before,
		After:  audit.State{audit.FieldPriceCents: price, audit.FieldCompareAtCents: compareAt},
	},
		func(ctx context.Context, q *db.Queries) error {
			replaced, lockErr := lockVariant(ctx, q, v.ID, sku)
			if lockErr != nil {
				return lockErr
			}
			before[audit.FieldPriceCents] = replaced.PriceCents
			if err := q.SetVariantPrice(ctx, db.SetVariantPriceParams{
				ID: v.ID, PriceCents: price, CompareAtPriceCents: cmp,
			}); err != nil {
				return db.WrapRefusal(err, ErrRefused)
			}
			return nil
		})
}

func variantRow(r *db.AdminVariantsRow) Variant {
	return Variant{
		SKU: r.SKU, Slug: r.Slug, ProductName: r.ProductName, Brand: r.Brand,
		ArrivalInput: arrivalInput(r.PreorderReleaseOn),
		PriceCents:   r.PriceCents, CompareCents: r.CompareAtPriceCents.Int64,
		Stock: r.StockQuantity, Safety: r.SafetyStock,
		Active: r.IsActive, Options: r.OptionValues,
		// One per rendered row, so the adjust form's key is spent by that form
		// alone and not by whichever stock level the variant next returns to.
		FormID: uuid.NewString(),
	}
}

// MovementPageSize bounds one page of a variant's stock ledger. The running
// total is computed over the WHOLE ledger, so a page is still truthful.
const MovementPageSize = 50

// Movements reads the variant, a page of its ledger and, on the first page, its
// days up to now in one snapshot, so the stock it shows is the last day's.
func (s *Store) Movements(ctx context.Context, sku string, now time.Time, after string) (MovementsView, error) {
	scope := "/admin/stock/" + url.PathEscape(sku)
	from, resumed := keyset.Resume(scope, after, func(p movementPosition) bool { return p.ID != uuid.Nil })
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return MovementsView{}, fmt.Errorf("begin movements snapshot: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := db.New(tx)
	v, err := q.AdminVariantBySKU(ctx, sku)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MovementsView{}, ErrNotFound
		}
		return MovementsView{}, fmt.Errorf("read variant %s: %w", sku, err)
	}
	rows, err := q.VariantMovements(ctx, db.VariantMovementsParams{HasCursor: resumed, AfterID: from.ID,
		SKU: sku, RowLimit: MovementPageSize + 1,
	})
	if err != nil {
		return MovementsView{}, fmt.Errorf("read movements of %s: %w", sku, err)
	}

	rows, bound := keyset.PageBound(scope, resumed, rows, MovementPageSize, func(r *db.VariantMovementsRow) string { return r.PageCursor })
	view := MovementsView{
		Bound: bound,
		SKU:   v.SKU, ProductName: v.ProductName, Slug: v.Slug,
		Stock: v.StockQuantity, Safety: v.SafetyStock,
		FormID: uuid.NewString(),
		Rows:   make([]Movement, 0, len(rows)),
	}
	if !resumed {
		if view.Days, err = readStockDays(ctx, q, sku, now); err != nil {
			return MovementsView{}, err
		}
	}
	for i := range rows {
		m := &rows[i]
		view.Rows = append(view.Rows, Movement{
			At:          shoptime.Minute(m.CreatedAt),
			Delta:       m.Delta,
			Reason:      inventory.MovementReason(m.Reason),
			OrderNumber: m.OrderNumber,
			Actor:       m.Actor,
			Running:     m.RunningTotal,
		})
	}
	return view, nil
}

// LineDays is how many shop days the stock line covers, today included.
const LineDays = 90

// readStockDays is the variant's last LineDays shop days up to now.
func readStockDays(ctx context.Context, q *db.Queries, sku string, now time.Time) ([]Day, error) {
	first := shoptime.FirstDay(now, LineDays)
	rows, err := q.VariantStockByDay(ctx, db.VariantStockByDayParams{
		SKU: sku, FromAt: first, FirstDay: shoptime.QueryDate(first), LastDay: shoptime.QueryDate(now),
	})
	if err != nil {
		return nil, fmt.Errorf("read stock by day of %s: %w", sku, err)
	}
	days := make([]Day, len(rows))
	for i, r := range rows {
		days[i] = Day{Day: r.Day, Stock: r.Stock, Received: r.Received, Receipts: r.Receipts, Moves: r.Moves}
	}
	return days, nil
}

func (s *Store) SetArrival(ctx context.Context, sku string, day pgtype.Date) error {
	v, err := s.q.AdminVariantBySKU(ctx, sku)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read variant: %w", err)
	}
	before := audit.State{audit.FieldSKU: sku}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionSetVariantArrival, Table: "product_variants", ID: audit.EntityID(v.ID),
		Before: before, After: audit.State{audit.FieldPreorderReleaseOn: arrivalInput(day)},
	}, func(ctx context.Context, q *db.Queries) error {
		replaced, err := lockVariant(ctx, q, v.ID, sku)
		if err != nil {
			return err
		}
		before[audit.FieldPreorderReleaseOn] = arrivalInput(replaced.PreorderReleaseOn)
		if err := q.SetVariantArrival(ctx, db.SetVariantArrivalParams{ID: v.ID, ArrivalOn: day}); err != nil {
			return fmt.Errorf("set variant arrival: %w", err)
		}
		return nil
	})
}

// DaysCover ranks the SKUs that sold or are sold out over the last shop days,
// at least inventory.CoverWindowDays of them, and counts the sold out ones the
// list leaves off. The stock and the ledger it is rolled back through are read
// in one snapshot, so a movement between the two reads cannot shift the level.
func (s *Store) DaysCover(ctx context.Context, days int, now time.Time) (listed []inventory.Risk, moreSoldOut int, err error) {
	from := shoptime.Midnight(now).AddDate(0, 0, 1-max(days, inventory.CoverWindowDays))
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, fmt.Errorf("begin stock snapshot: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	rows, err := q.StockAtRisk(ctx, db.StockAtRiskParams{FromAt: from, ToAt: now})
	if err != nil {
		return nil, 0, fmt.Errorf("read stock at risk: %w", err)
	}
	ids := make([]uuid.UUID, len(rows))
	for i := range rows {
		ids[i] = rows[i].VariantID
	}
	moves := map[uuid.UUID][]movement{}
	if len(ids) > 0 {
		ledger, err := q.StockMovementsSince(ctx, db.StockMovementsSinceParams{VariantIds: ids, FromAt: from})
		if err != nil {
			return nil, 0, fmt.Errorf("read stock movements: %w", err)
		}
		for i := range ledger {
			m := &ledger[i]
			moves[m.VariantID] = append(moves[m.VariantID], movement{at: m.CreatedAt, delta: m.Delta})
		}
	}
	risk := make([]inventory.Risk, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		risk = append(risk, inventory.Risk{
			SKU: r.SKU, Name: r.ProductName, Slug: r.Slug,
			Sales: inventory.Sales{
				Sellable: max(r.StockQuantity-r.SafetyStock, 0),
				Sold:     r.UnitsSold, Orders: r.OrdersSold,
				InStock: timeInStock(r.StockQuantity, r.SafetyStock, from, now, moves[r.VariantID]),
			},
			SoldOutAt: soldOutAt(r.StockQuantity, r.SafetyStock, now, moves[r.VariantID]),
		})
	}
	listed, moreSoldOut = inventory.Rank(risk)
	return listed, moreSoldOut, nil
}
