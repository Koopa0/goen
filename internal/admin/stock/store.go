// Package stock is the back office's stock desk: the variants list with its low
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
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/inventory"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

var (
	ErrNotFound = errors.New("stock: not found")
	// ErrRefused is a write the database declined; its message is the database's
	// own, because that names the rule.
	ErrRefused = errors.New("stock: refused")
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

func (s *Store) Variants(ctx context.Context, lowOnly bool, term string, after ...string) (admin.VariantsView, error) {
	term = web.SearchTerm(term)
	low := ""
	if lowOnly {
		low = "1"
	}
	scope := web.ScopeURL("/admin/stock", "low", low, "q", term)
	from, resumed := web.ResumeKeyset(scope, after, func(p variantPosition) bool {
		// Postgres refuses a NUL in text, so a crafted name would turn a bad link
		// into a 500 instead of the first page.
		return p.ID != uuid.Nil && !strings.ContainsRune(p.Name, 0)
	})
	rows, err := s.q.AdminVariants(ctx, db.AdminVariantsParams{Locale: string(i18n.FromContext(ctx)), HasCursor: resumed, AfterNumber: from.Number, AfterName: from.Name, AfterPosition: from.Position, AfterID: from.ID, LowOnly: lowOnly, EscapedTerm: catalog.EscapeLike(term), RowLimit: web.PageLimit})
	if err != nil {
		return admin.VariantsView{}, fmt.Errorf("read variants: %w", err)
	}
	rows, bound := web.PageBound(scope, resumed, rows, web.PageSize, func(r *db.AdminVariantsRow) string { return r.PageCursor })
	view := admin.VariantsView{Bound: bound, LowOnly: lowOnly, Term: term}
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
	before := map[string]any{"sku": sku}
	err = audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionAdjustStock, Table: "product_variants", ID: audit.EntityID(v.ID),
		Before: before,
		After:  map[string]any{"delta": delta},
	},
		func(ctx context.Context, q *db.Queries) error {
			replaced, lockErr := lockVariant(ctx, q, v.ID, sku)
			if lockErr != nil {
				return lockErr
			}
			before["stock"] = replaced.StockQuantity
			if moveErr := q.AdjustStock(ctx, db.AdjustStockParams{
				VariantID: v.ID, Delta: delta, IdempotencyKey: key, ActorUserID: actor,
			}); moveErr != nil {
				return pgerr.WrapRefusal(moveErr, ErrRefused)
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
	before := map[string]any{"sku": sku}
	err = audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionReceiveStock, Table: "product_variants", ID: audit.EntityID(v.ID),
		Before: before,
		After:  map[string]any{"received": quantity, "preorder_release_on": ""},
	},
		func(ctx context.Context, q *db.Queries) error {
			replaced, lockErr := lockVariant(ctx, q, v.ID, sku)
			if lockErr != nil {
				return lockErr
			}
			before["stock"] = replaced.StockQuantity
			before["preorder_release_on"] = arrivalInput(replaced.PreorderReleaseOn)
			if moveErr := q.ReceiveStock(ctx, db.ReceiveStockParams{
				VariantID: v.ID, Delta: quantity, IdempotencyKey: key, ActorUserID: actor,
			}); moveErr != nil {
				return pgerr.WrapRefusal(moveErr, ErrRefused)
			}
			if err := q.SetVariantArrival(ctx, db.SetVariantArrivalParams{ID: v.ID}); err != nil {
				return pgerr.WrapRefusal(err, ErrRefused)
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
	if err == nil || !pgerr.IsConstraint(err, "inventory_movements_idempotency_key") {
		return err
	}
	applied, checkErr := s.q.StockMovementApplied(ctx, db.StockMovementAppliedParams{
		IdempotencyKey: key, VariantID: variantID, Delta: delta, Reason: string(reason),
	})
	if checkErr != nil || !applied {
		return err
	}
	return nil
}

// SetActive retires or restores a variant. A refusal here is usually
// sale_campaign_variant_still_valid: the last discounted variant of a product
// some campaign features.
func (s *Store) SetActive(ctx context.Context, sku string, active bool) error {
	v, err := s.q.AdminVariantBySKU(ctx, sku)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read variant: %w", err)
	}
	before := map[string]any{"sku": sku}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionRetireVariant, Table: "product_variants", ID: audit.EntityID(v.ID),
		Before: before,
		After:  map[string]any{"active": active},
	},
		func(ctx context.Context, q *db.Queries) error {
			replaced, lockErr := lockVariant(ctx, q, v.ID, sku)
			if lockErr != nil {
				return lockErr
			}
			before["active"] = replaced.IsActive
			if err := q.SetVariantActive(ctx, db.SetVariantActiveParams{
				ID: v.ID, IsActive: active,
			}); err != nil {
				return pgerr.WrapRefusal(err, ErrRefused)
			}
			return nil
		})
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
	before := map[string]any{"sku": sku}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionRepriceVariant, Table: "product_variants", ID: audit.EntityID(v.ID),
		Before: before,
		After:  map[string]any{"price_cents": price, "compare_at_cents": compareAt},
	},
		func(ctx context.Context, q *db.Queries) error {
			replaced, lockErr := lockVariant(ctx, q, v.ID, sku)
			if lockErr != nil {
				return lockErr
			}
			before["price_cents"] = replaced.PriceCents
			if err := q.SetVariantPrice(ctx, db.SetVariantPriceParams{
				ID: v.ID, PriceCents: price, CompareAtPriceCents: cmp,
			}); err != nil {
				return pgerr.WrapRefusal(err, ErrRefused)
			}
			return nil
		})
}

// LowStock is the variants at or under their safety stock, the dashboard's
// shortlist.
func (s *Store) LowStock(ctx context.Context, limit int32) ([]admin.Variant, error) {
	rows, err := s.q.AdminVariants(ctx, db.AdminVariantsParams{
		Locale: string(i18n.FromContext(ctx)), LowOnly: true, RowLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("read low stock: %w", err)
	}
	out := make([]admin.Variant, 0, len(rows))
	for i := range rows {
		out = append(out, variantRow(&rows[i]))
	}
	return out, nil
}

func variantRow(r *db.AdminVariantsRow) admin.Variant {
	return admin.Variant{
		SKU: r.SKU, Slug: r.Slug, ProductName: r.ProductName, Brand: r.Brand,
		ArrivalInput: arrivalInput(r.PreorderReleaseOn),
		PriceCents:   r.PriceCents, CompareCents: r.CompareAtPriceCents.Int64,
		Stock: r.StockQuantity, Safety: r.SafetyStock,
		Active: r.IsActive, ProductStatus: r.ProductStatus,
		Options: r.OptionValues,
		// One per rendered row, so the adjust form's key is spent by that form
		// alone and not by whichever stock level the variant next returns to.
		FormID: uuid.NewString(),
	}
}

// MovementPageSize bounds one page of a variant's stock ledger. The running
// total is computed over the WHOLE ledger, so a page is still truthful.
const MovementPageSize = 50

func (s *Store) Movements(ctx context.Context, sku string, after ...string) (admin.MovementsView, error) {
	scope := "/admin/stock/" + url.PathEscape(sku)
	from, resumed := web.ResumeKeyset(scope, after, func(p movementPosition) bool { return p.ID != uuid.Nil })
	v, err := s.q.AdminVariantBySKU(ctx, sku)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admin.MovementsView{}, ErrNotFound
		}
		return admin.MovementsView{}, fmt.Errorf("read variant %s: %w", sku, err)
	}
	rows, err := s.q.VariantMovements(ctx, db.VariantMovementsParams{HasCursor: resumed, AfterID: from.ID,
		SKU: sku, RowLimit: MovementPageSize + 1,
	})
	if err != nil {
		return admin.MovementsView{}, fmt.Errorf("read movements of %s: %w", sku, err)
	}

	rows, bound := web.PageBound(scope, resumed, rows, MovementPageSize, func(r *db.VariantMovementsRow) string { return r.PageCursor })
	view := admin.MovementsView{
		Bound: bound,
		SKU:   v.SKU, ProductName: v.ProductName, Slug: v.Slug,
		Stock: v.StockQuantity, Safety: v.SafetyStock,
		FormID: uuid.NewString(),
		Rows:   make([]admin.Movement, 0, len(rows)),
	}
	for i := range rows {
		m := &rows[i]
		view.Rows = append(view.Rows, admin.Movement{
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

func (s *Store) SetArrival(ctx context.Context, sku string, day pgtype.Date) error {
	v, err := s.q.AdminVariantBySKU(ctx, sku)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read variant: %w", err)
	}
	before := map[string]any{"sku": sku}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionSetVariantArrival, Table: "product_variants", ID: audit.EntityID(v.ID),
		Before: before, After: map[string]any{"preorder_release_on": arrivalInput(day)},
	}, func(ctx context.Context, q *db.Queries) error {
		replaced, err := lockVariant(ctx, q, v.ID, sku)
		if err != nil {
			return err
		}
		before["preorder_release_on"] = arrivalInput(replaced.PreorderReleaseOn)
		if err := q.SetVariantArrival(ctx, db.SetVariantArrivalParams{ID: v.ID, ArrivalOn: day}); err != nil {
			return fmt.Errorf("set variant arrival: %w", err)
		}
		return nil
	})
}
