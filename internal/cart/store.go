package cart

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/cart/cartitem"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/product"
)

// Store holds the pool rather than a DBTX because multi-statement operations
// open and own their transactions.
type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("cart: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

// ByToken answers a cart attached to an account only to that account: adoption
// keeps the token the browser already holds, so after sign-out, or for the next
// customer at a shared computer, the token still names the account's cart. That
// is ErrNotYourCart, and the token is stale.
func (s *Store) ByToken(ctx context.Context, token string, requester uuid.NullUUID) (uuid.UUID, error) {
	if token == "" {
		return uuid.Nil, ErrNotFound
	}
	row, err := s.q.CartByToken(ctx, bearer.Digest(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, fmt.Errorf("read cart: %w", err)
	}
	if row.UserID.Valid && (!requester.Valid || row.UserID.UUID != requester.UUID) {
		return uuid.Nil, ErrNotYourCart
	}
	return row.ID, nil
}

// Create handles a race: two signed-in first writes can both observe no row;
// carts_one_per_user refuses the second insert, and the winner is the account
// cart.
func (s *Store) Create(ctx context.Context, token string, userID uuid.NullUUID) (uuid.UUID, error) {
	id, err := s.q.CreateCart(ctx, db.CreateCartParams{Digest: bearer.Digest(token), UserID: userID})
	if err != nil {
		if existing, ok := s.ownedCartIfTaken(ctx, userID, err); ok {
			return existing, nil
		}
		return uuid.Nil, fmt.Errorf("create cart: %w", err)
	}
	return id, nil
}

// The unique index is the one-cart rule; ownedCartIfTaken only names it.
func (s *Store) ownedCartIfTaken(ctx context.Context, userID uuid.NullUUID, err error) (uuid.UUID, bool) {
	if !userID.Valid {
		return uuid.Nil, false
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "carts_one_per_user" {
		return uuid.Nil, false
	}
	existing, readErr := s.ForUser(ctx, userID.UUID.String())
	if readErr != nil {
		return uuid.Nil, false
	}
	return existing, true
}

// Add checks before the write so an inactive variant is a message rather than a
// foreign-key error.
func (s *Store) Add(ctx context.Context, cartID, variantID uuid.UUID, quantity int32) error {
	adjusted := false
	err := s.mutateCart(ctx, cartID, func(q *db.Queries) error {
		lineAdjusted, addErr := addCartItem(ctx, q, cartID, variantID, quantity)
		if addErr != nil {
			return addErr
		}
		adjusted = lineAdjusted
		return nil
	})
	if err != nil {
		return err
	}
	if adjusted {
		return ErrQuantityAdjusted
	}
	return nil
}

// sellable is the one test of whether a variant may be put in a cart: listed,
// active and with stock the shelf can supply.
func sellable(v *db.VariantForCartRow) bool {
	return v.IsActive && product.Status(v.Status) == product.StatusActive && v.SellableQuantity > 0
}

// addCartItem runs through the caller's queries, which may be pool-backed or
// bound to a larger transaction.
func addCartItem(
	ctx context.Context,
	q *db.Queries,
	cartID, variantID uuid.UUID,
	quantity int32,
) (adjusted bool, err error) {
	requested := quantity
	v, err := q.VariantForCart(ctx, variantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, fmt.Errorf("read variant: %w", err)
	}
	if !sellable(&v) {
		return false, ErrUnavailable
	}
	capacity, err := q.CartLineCapacity(ctx, db.CartLineCapacityParams{
		CartID: cartID, VariantID: variantID,
	})
	if err != nil {
		return false, fmt.Errorf("count cart lines: %w", err)
	}
	if !capacity.AlreadyPresent && capacity.LineCount >= issuer.MaxIssueProductLines {
		return false, ErrTooManyItems
	}
	wanted := capacity.ExistingQuantity + requested
	// wanted was read under the cart row lock mutateCart (or adoption's
	// LockCarts) holds; AddCartItem overwrites, so a caller without that lock
	// would lose a concurrent add.
	target := min(wanted, v.SellableQuantity, MaxLineQuantity)
	if addErr := q.AddCartItem(ctx, db.AddCartItemParams{
		CartID: cartID, VariantID: variantID, Quantity: target,
	}); addErr != nil {
		return false, fmt.Errorf("add cart item: %w", addErr)
	}
	return target < wanted, nil
}

func (s *Store) SetQuantity(ctx context.Context, cartID, variantID uuid.UUID, quantity int32) error {
	adjusted := false
	err := s.mutateCart(ctx, cartID, func(q *db.Queries) error {
		lineAdjusted, setErr := setCartLineQuantity(ctx, q, cartID, variantID, quantity)
		if setErr != nil {
			return setErr
		}
		adjusted = lineAdjusted
		return nil
	})
	if err != nil {
		return err
	}
	if adjusted {
		return ErrQuantityAdjusted
	}
	return nil
}

func setCartLineQuantity(
	ctx context.Context, q *db.Queries, cartID, variantID uuid.UUID, quantity int32,
) (adjusted bool, err error) {
	if quantity <= 0 {
		if remErr := q.RemoveCartItem(ctx, db.RemoveCartItemParams{CartID: cartID, VariantID: variantID}); remErr != nil {
			return false, fmt.Errorf("remove cart item: %w", remErr)
		}
		return false, nil
	}
	v, err := q.VariantForCart(ctx, variantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, fmt.Errorf("read variant: %w", err)
	}
	if !sellable(&v) {
		return false, ErrUnavailable
	}
	requested := quantity
	if quantity > v.SellableQuantity {
		quantity = v.SellableQuantity
	}
	if err := q.SetCartItemQuantity(ctx, db.SetCartItemQuantityParams{
		CartID: cartID, VariantID: variantID, Quantity: quantity,
	}); err != nil {
		return false, fmt.Errorf("set cart quantity: %w", err)
	}
	return requested > quantity, nil
}

func (s *Store) Remove(ctx context.Context, cartID, variantID uuid.UUID) error {
	return s.mutateCart(ctx, cartID, func(q *db.Queries) error {
		if err := q.RemoveCartItem(ctx, db.RemoveCartItemParams{CartID: cartID, VariantID: variantID}); err != nil {
			return fmt.Errorf("remove cart item: %w", err)
		}
		return nil
	})
}

// mutateCart gives every standalone cart write the aggregate lock reorder,
// adoption and checkout use. q is valid only inside the transaction and must
// not be retained.
func (s *Store) mutateCart(
	ctx context.Context,
	cartID uuid.UUID,
	mutate func(*db.Queries) error,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin cart mutation: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)
	if err := Lock(ctx, q, cartID); err != nil {
		return err
	}
	if err := mutate(q); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit cart mutation: %w", err)
	}
	return nil
}

// Lock takes the row lock every writer of a cart holds before it touches the
// cart's lines, on the caller's binding: the cart's own writes take it in
// mutateCart, reorder in its transaction, checkout before it reads the lines it
// places.
func Lock(ctx context.Context, q *db.Queries, cartID uuid.UUID) error {
	locked, err := q.LockCarts(ctx, []uuid.UUID{cartID})
	if err != nil {
		return fmt.Errorf("lock cart: %w", err)
	}
	if len(locked) != 1 {
		return ErrNotFound
	}
	return nil
}

// View reads prices and availability fresh every time: a cart line is not a
// promise.
func (s *Store) View(ctx context.Context, cartID uuid.UUID) (View, error) {
	rows, err := s.q.CartLines(ctx, db.CartLinesParams{
		CartID: cartID, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return View{}, fmt.Errorf("read cart lines: %w", err)
	}

	view := View{
		Lines:       make([]Line, 0, len(rows)),
		HoldMinutes: int64(payment.HoldTTL / time.Minute), PayStartMinutes: int64(payment.PayWindow / time.Minute),
	}
	for i := range rows {
		r := &rows[i]
		photo := assets.Image{Digest: r.ImageDigest, BundledKey: r.ImageBundledKey, Width: int(r.ImageWidth)}
		if r.TaxType != rows[0].TaxType {
			view.MixedTaxTypes = true
		}
		line := Line{
			VariantID:    r.VariantID.String(),
			Slug:         r.Slug,
			Name:         r.Name,
			Brand:        r.Brand,
			SKU:          r.SKU,
			Label:        OptionLabel(r.OptionNames, r.OptionValues),
			TaxExempt:    r.TaxType == string(invoice.Exempt),
			UnitCents:    r.PriceCents,
			Quantity:     r.Quantity,
			Available:    r.SellableQuantity,
			ImageURL:     photo.URL(),
			ImageSrcset:  photo.Srcset(),
			ImageAlt:     r.ImageAlt,
			CompareCents: r.CompareAtPriceCents.Int64,
			Unavailable:  product.Status(r.ProductStatus) != product.StatusActive || !r.IsActive || r.SellableQuantity <= 0,
		}
		line.Short = !line.Unavailable && r.Quantity > r.SellableQuantity

		view.Lines = append(view.Lines, line)
		if !line.Unavailable {
			q := r.Quantity
			if line.Short {
				q = r.SellableQuantity
			}
			view.SubtotalCents += r.PriceCents * int64(q)
			view.ItemCount += int64(q)
		}
	}
	return view, nil
}

// Lines reads every line of a cart at current prices and availability on the
// caller's binding, the read View makes: checkout reads them inside its
// transaction, after Lock.
func Lines(ctx context.Context, q *db.Queries, cartID uuid.UUID, locale i18n.Locale) ([]db.CartLinesRow, error) {
	return q.CartLines(ctx, db.CartLinesParams{CartID: cartID, Locale: string(locale)})
}

// Items reads what the cart holds, every line the cart page lists and in its
// order, sellable or not.
func (s *Store) Items(ctx context.Context, cartID uuid.UUID) ([]cartitem.Item, error) {
	rows, err := s.q.CartLines(ctx, db.CartLinesParams{
		CartID: cartID, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return nil, fmt.Errorf("read cart lines: %w", err)
	}
	items := make([]cartitem.Item, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		photo := assets.Image{Digest: r.ImageDigest, BundledKey: r.ImageBundledKey, Width: int(r.ImageWidth)}
		items = append(items, cartitem.Item{
			VariantID:   r.VariantID,
			Name:        r.Name,
			Label:       OptionLabel(r.OptionNames, r.OptionValues),
			Quantity:    r.Quantity,
			ImageURL:    photo.URL(),
			ImageSrcset: photo.Srcset(),
			ImageAlt:    r.ImageAlt,
		})
	}
	return items, nil
}

// productReturnURL rebuilds the selection query from the variant row the server
// accepted; only added= comes from the handler outcome.
//
// The fragment names the buy column rather than the notice inside it: a visitor
// with no script arrives by navigation, and landing on the notice puts the
// price, picker and button above the fold line. The column carries
// scroll-margin-top for room above. With script the page never navigates and
// the fragment is only what the address ends up saying.
func (s *Store) productReturnURL(
	ctx context.Context, slug string, variantID uuid.UUID, outcome string,
) (string, error) {
	q := url.Values{}
	q.Set("added", outcome)
	if variantID == uuid.Nil {
		return "/p/" + slug + "?" + q.Encode() + "#buybox", nil
	}
	row, err := s.q.VariantProductSelection(ctx, variantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "/p/" + slug + "?" + q.Encode() + "#buybox", nil
		}
		return "", fmt.Errorf("read variant selection: %w", err)
	}
	if row.Slug != slug {
		return "/cart", nil
	}
	for i := range min(len(row.OptionNames), len(row.OptionValues)) {
		q.Set(row.OptionNames[i], row.OptionValues[i])
	}
	return "/p/" + row.Slug + "?" + q.Encode() + "#buybox", nil
}

// OptionLabel is a cart line's chosen options as one label, the values in the
// options' order; an order line keeps the same label as its snapshot.
func OptionLabel(names, values []string) string {
	n := min(len(names), len(values))
	if n == 0 {
		return ""
	}
	parts := make([]string, 0, n)
	for i := range n {
		parts = append(parts, values[i])
	}
	return strings.Join(parts, " · ")
}

// ShippingChoices is per cart: a method whose carrier refuses a 27-inch monitor
// is not a choice for a basket with one in it.
func (s *Store) ShippingChoices(ctx context.Context, cartID uuid.UUID, subtotalCents int64) ([]ShippingChoice, error) {
	rows, err := s.q.ShippingChoices(ctx, db.ShippingChoicesParams{
		Locale: string(i18n.FromContext(ctx)),
		CartID: cartID,
	})
	if err != nil {
		return nil, fmt.Errorf("read shipping choices: %w", err)
	}
	out := make([]ShippingChoice, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		fee := ShippingFee(r.FeeCents, r.FreeOverCents.Int64, subtotalCents)
		// A method that costs nothing anyway names no amount: the cart must not say it reached one.
		threshold := r.FreeOverCents.Int64
		if r.FeeCents <= 0 {
			threshold = 0
		}
		out = append(out, ShippingChoice{
			VersionID:       r.VersionID.String(),
			Code:            r.Code,
			DestinationKind: r.DestinationKind,
			Name:            r.Name,
			Carrier:         r.Carrier,
			FeeCents:        fee,
			Free:            fee == 0,
			FreeOverCents:   threshold,
			SurchargeZones:  r.SurchargeZones,
		})
	}
	return out, nil
}

func (s *Store) ItemCount(ctx context.Context, cartID uuid.UUID) (int, error) {
	n, err := s.q.CartItemCount(ctx, cartID)
	if err != nil {
		return 0, fmt.Errorf("count cart items: %w", err)
	}
	return int(n), nil
}
