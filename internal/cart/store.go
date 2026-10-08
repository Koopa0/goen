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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/i18n"
	invoicepkg "github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
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
	row, err := s.q.CartByToken(ctx, HashToken(token))
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
	id, err := s.q.CreateCart(ctx, db.CreateCartParams{TokenHash: HashToken(token), UserID: userID})
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
	if !v.IsActive || pages.ProductStatus(v.Status) != pages.ProductActive || v.SellableQuantity <= 0 {
		return false, ErrUnavailable
	}
	capacity, err := q.CartLineCapacity(ctx, db.CartLineCapacityParams{
		CartID: cartID, VariantID: variantID,
	})
	if err != nil {
		return false, fmt.Errorf("count cart lines: %w", err)
	}
	if !capacity.AlreadyPresent && capacity.LineCount >= invoicepkg.MaxIssueProductLines {
		return false, ErrTooManyItems
	}
	wanted := capacity.ExistingQuantity + requested
	// wanted was read under the cart row lock mutateCart holds; AddCartItem
	// overwrites, so a caller without that lock would lose a concurrent add.
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
	if !v.IsActive || pages.ProductStatus(v.Status) != pages.ProductActive || v.SellableQuantity <= 0 {
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
	defer pgtx.Rollback(ctx, tx)
	q := s.q.WithTx(tx)
	if err := lockCart(ctx, q, cartID); err != nil {
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

func lockCart(ctx context.Context, q *db.Queries, cartID uuid.UUID) error {
	locked, err := q.LockCarts(ctx, []uuid.UUID{cartID})
	if err != nil {
		return fmt.Errorf("lock cart: %w", err)
	}
	if len(locked) != 1 {
		return ErrNotFound
	}
	return nil
}

// Count is UNITS, not lines, for the header badge.
func (s *Store) Count(ctx context.Context, cartID uuid.UUID) (int64, error) {
	n, err := s.q.CartItemCount(ctx, cartID)
	if err != nil {
		return 0, fmt.Errorf("count cart: %w", err)
	}
	return n, nil
}

// View reads prices and availability fresh every time: a cart line is not a
// promise.
func (s *Store) View(ctx context.Context, cartID uuid.UUID) (pages.CartView, error) {
	rows, err := s.q.CartLines(ctx, db.CartLinesParams{
		CartID: cartID, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return pages.CartView{}, fmt.Errorf("read cart lines: %w", err)
	}

	view := pages.CartView{Lines: make([]pages.CartLine, 0, len(rows))}
	for i := range rows {
		r := &rows[i]
		if r.TaxType != rows[0].TaxType {
			view.MixedTaxTypes = true
		}
		line := pages.CartLine{
			VariantID:    r.VariantID.String(),
			Slug:         r.Slug,
			Name:         r.Name,
			Brand:        r.Brand,
			SKU:          r.SKU,
			Label:        optionLabel(r.OptionNames, r.OptionValues),
			UnitCents:    r.PriceCents,
			Quantity:     r.Quantity,
			Available:    r.SellableQuantity,
			ImageURL:     assets.ProductImageURL(r.ImageKey),
			ImageSrcset:  assets.ProductImageSrcsetAt(r.ImageKey, int(r.ImageWidth)),
			ImageAlt:     r.ImageAlt,
			CompareCents: r.CompareAtPriceCents.Int64,
			Unavailable:  pages.ProductStatus(r.ProductStatus) != pages.ProductActive || !r.IsActive || r.SellableQuantity <= 0,
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

func optionLabel(names, values []string) string {
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

// SavedAddresses gives a guest none because the QUERY scopes to the owner,
// never a short-circuit here.
func (s *Store) SavedAddresses(ctx context.Context, owner uuid.NullUUID) ([]pages.SavedAddress, error) {
	rows, err := s.q.SavedAddresses(ctx, owner.UUID)
	if err != nil {
		return nil, fmt.Errorf("read saved addresses: %w", err)
	}
	out := make([]pages.SavedAddress, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, pages.SavedAddress{
			ID: r.ID.String(), Label: r.Label.String, Name: r.RecipientName,
			Phone: r.Phone, PostalCode: r.PostalCode, City: r.City,
			District: r.District, Street: r.Street, Default: r.IsDefault,
		})
	}
	return out, nil
}

func (s *Store) QuoteShipping(ctx context.Context, versionID uuid.UUID, subtotalCents int64, postalCode string) (Quote, error) {
	v, err := s.q.ShippingVersion(ctx, db.ShippingVersionParams{
		ID: versionID, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Quote{}, ErrNotFound
		}
		return Quote{}, fmt.Errorf("read shipping version: %w", err)
	}
	return quoteFor(ctx, s.q, &v, subtotalCents, postalCode)
}

// PlaceOrder passes transaction-bound queries to quoteFor so no price read
// escapes its locks.
func quoteFor(
	ctx context.Context,
	q *db.Queries,
	v *db.ShippingVersionRow,
	subtotalCents int64,
	postalCode string,
) (Quote, error) {
	zone, err := q.ShippingZoneFor(ctx, db.ShippingZoneForParams{
		VersionID: v.ID, PostalCode: postalCode,
		Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return Quote{}, fmt.Errorf("read shipping zone: %w", err)
	}
	return Quote{
		FeeCents:  ShippingFee(v.FeeCents, v.FreeOverCents.Int64, subtotalCents),
		Surcharge: zone.SurchargeCents,
		ZoneName:  zone.ZoneName,
	}, nil
}

// ShippingChoices is per cart: a method whose carrier refuses a 27-inch monitor
// is not a choice for a basket with one in it.
func (s *Store) ShippingChoices(ctx context.Context, cartID uuid.UUID, subtotalCents int64) ([]pages.ShippingChoice, error) {
	rows, err := s.q.ShippingChoices(ctx, db.ShippingChoicesParams{
		Locale: string(i18n.FromContext(ctx)),
		CartID: cartID,
	})
	if err != nil {
		return nil, fmt.Errorf("read shipping choices: %w", err)
	}
	out := make([]pages.ShippingChoice, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		fee := ShippingFee(r.FeeCents, r.FreeOverCents.Int64, subtotalCents)
		// A method that costs nothing anyway names no amount: the cart must not say it reached one.
		threshold := r.FreeOverCents.Int64
		if r.FeeCents <= 0 {
			threshold = 0
		}
		out = append(out, pages.ShippingChoice{
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

// classifyCheckout is bound to the SQLSTATE: 57014 is query_canceled, which
// statement_timeout and an operator's pg_cancel_backend both raise, and neither
// is a defect in what was submitted.
func classifyCheckout(err error) error {
	if err == nil {
		return nil
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "57014" {
		return fmt.Errorf("%w: %w", ErrBusy, err)
	}
	return err
}

// placeOrder recomputes the shipping fee from the version rather than the form.
// quote is not pricing input: it is compared with the locked recomputation so
// an order cannot differ from what the customer confirmed. attemptID makes a
// resubmitted checkout find its own order.
func (s *Store) placeOrder(
	ctx context.Context,
	cartID uuid.UUID,
	userID uuid.NullUUID,
	shippingVersionID uuid.UUID,
	addr *order.Delivery,
	inv *Invoice,
	couponCode string,
	shown checkoutQuoteID,
	attemptID checkoutAttemptID,
) (number string, err error) {
	// One classifier at the boundary: every statement below runs under the
	// pool's statement_timeout, and a lock wait that outlives it is the
	// ordinary shape of a busy shop.
	defer func() { err = classifyCheckout(err) }()

	// Nothing reads checkout_attempts on the pool first: claimCheckoutKey asks
	// the same question under the lock, and the early copy is the one that can
	// be wrong.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin checkout: %w", err)
	}
	defer pgtx.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	prior, taken, err := claimCheckoutKey(ctx, q, cartID, attemptID)
	if err != nil {
		return "", err
	}
	if taken {
		return prior, nil
	}
	if userID.Valid {
		// Erasure/adoption start at the user aggregate. Hold its KEY SHARE
		// before checkoutCartSnapshot takes the cart row, so no path owns cart
		// -> wait user while another owns user -> wait cart.
		if _, lockErr := q.LockUserForCheckout(ctx, userID.UUID); lockErr != nil {
			if errors.Is(lockErr, pgx.ErrNoRows) {
				return "", ErrNotFound
			}
			return "", fmt.Errorf("lock account for checkout: %w", lockErr)
		}
	}
	terms, err := lockCheckoutTerms(
		ctx, q, cartID, userID, shippingVersionID, addr, couponCode, shown,
	)
	if err != nil {
		return "", err
	}

	placed, err := q.CreateOrder(ctx, db.CreateOrderParams{
		UserID:             userID,
		ShippingVersionID:  terms.ship.ID,
		ShippingMethodCode: terms.ship.Code,
		ShippingMethodName: terms.ship.Name,
		ShippingCents:      terms.shippingFee,
		DiscountCents:      terms.discount,
		CustomerNote:       text(addr.Note),
		// Every later message about this order is sent in this language, not in
		// whatever the trigger happened to be reading.
		Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		return "", fmt.Errorf("create order: %w", err)
	}

	if err := writeOrderParts(ctx, q, &orderParts{
		orderID:       placed.ID,
		lines:         terms.lines,
		userID:        userID,
		discountCents: terms.discount,
		invoice:       inv,
		coupon:        terms.coupon,
		orderNumber:   placed.OrderNumber,
		address:       addr,
		totalCents:    terms.gross,
		creditCents:   terms.creditCents,
	}); err != nil {
		return "", err
	}

	if err := finishOrder(ctx, q, placed.ID, cartID, addr, attemptID); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.Code == "23514" && pgErr.ConstraintName == "orders_single_tax_type" {
			return "", ErrMixedTaxTypes
		}
		return "", fmt.Errorf("commit checkout: %w", err)
	}
	return placed.OrderNumber, nil
}

// checkoutTerms is the server-owned snapshot after every row it depends on is
// locked and the quote matched; no unlocked or form-supplied amount can enter
// the order write below.
type checkoutTerms struct {
	lines       []db.CartLinesRow
	ship        db.ShippingVersionRow
	coupon      *Coupon
	shippingFee int64
	discount    int64
	gross       int64
	creditCents int64
}

func lockCheckoutTerms(
	ctx context.Context,
	q *db.Queries,
	cartID uuid.UUID,
	userID uuid.NullUUID,
	shippingVersionID uuid.UUID,
	addr *order.Delivery,
	couponCode string,
	shown checkoutQuoteID,
) (*checkoutTerms, error) {
	lines, err := checkoutCartSnapshot(ctx, q, cartID, i18n.FromContext(ctx))
	if err != nil {
		return nil, err
	}
	if len(lines) > invoicepkg.MaxIssueProductLines {
		return nil, ErrTooManyItems
	}

	ship, err := q.ShippingVersion(ctx, db.ShippingVersionParams{
		ID: shippingVersionID, Locale: string(i18n.FromContext(ctx)),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read shipping version: %w", err)
	}

	// The destination is re-derived from the method just read and the address
	// trimmed to it, as the fee is recomputed rather than trusted.
	to, ok := destination.For(ship.DestinationKind)
	if !ok {
		return nil, fmt.Errorf("shipping method %s has an unknown destination %q",
			ship.Code, ship.DestinationKind)
	}
	addr.To = to
	addr.DropOtherDestination()

	subtotal, err := subtotalOf(lines)
	if err != nil {
		return nil, err
	}
	creditCents, err := lockedAvailableCredit(ctx, q, userID)
	if err != nil {
		return nil, err
	}

	canonicalCouponCode := NormaliseCode(couponCode)
	var coupon *Coupon
	if canonicalCouponCode != "" {
		if lockErr := q.LockCouponForCheckout(ctx, canonicalCouponCode); lockErr != nil {
			return nil, fmt.Errorf("lock coupon %q: %w", canonicalCouponCode, lockErr)
		}
		coupon, err = couponByCode(ctx, q, canonicalCouponCode)
		if err != nil {
			return nil, err
		}
	}

	shippingFee, discount, err := priceOrder(ctx, q, &ship, subtotal, addr.PostalCode, coupon)
	if err != nil {
		return nil, err
	}
	gross, err := checkoutGross(subtotal, shippingFee, discount)
	if err != nil {
		return nil, fmt.Errorf("total locked checkout: %w", err)
	}
	creditCents = min(creditCents, gross)
	lockedID, quoteErr := (checkoutQuote{
		CartID:            cartID,
		Lines:             checkoutQuoteLines(lines),
		ShippingVersionID: shippingVersionID,
		ShippingCents:     shippingFee,
		CouponCode:        canonicalCouponCode,
		DiscountCents:     discount,
		CreditCents:       creditCents,
	}).ID()
	if quoteErr != nil {
		return nil, fmt.Errorf("build locked checkout quote: %w", quoteErr)
	}
	if lockedID != shown {
		return nil, errCheckoutChanged
	}
	return &checkoutTerms{
		lines: lines, ship: ship, coupon: coupon,
		shippingFee: shippingFee, discount: discount,
		gross: gross, creditCents: creditCents,
	}, nil
}

func checkoutQuoteLines(lines []db.CartLinesRow) []checkoutQuoteLine {
	quoted := make([]checkoutQuoteLine, 0, len(lines))
	for i := range lines {
		line := &lines[i]
		quoted = append(quoted, checkoutQuoteLine{
			VariantID: line.VariantID,
			Quantity:  line.Quantity,
			UnitCents: line.PriceCents,
		})
	}
	return quoted
}

// lockedAvailableCredit keeps an existing account's row locked until the order
// transaction commits, the same row every credit posting must lock.
func lockedAvailableCredit(
	ctx context.Context,
	q *db.Queries,
	userID uuid.NullUUID,
) (int64, error) {
	if !userID.Valid {
		return 0, nil
	}
	balance, err := q.LockAvailableCredit(ctx, userID.UUID)
	if err != nil {
		return 0, fmt.Errorf("lock store credit: %w", err)
	}
	return max(balance, 0), nil
}

// checkoutCartSnapshot linearizes checkout against every cart writer, then
// locks product and stock roots by UUID before reading publication, prices and
// availability.
func checkoutCartSnapshot(
	ctx context.Context,
	q *db.Queries,
	cartID uuid.UUID,
	locale i18n.Locale,
) ([]db.CartLinesRow, error) {
	if err := lockCart(ctx, q, cartID); err != nil {
		return nil, err
	}
	if err := q.LockCartCatalogue(ctx, cartID); err != nil {
		return nil, fmt.Errorf("lock cart catalogue: %w", err)
	}
	lines, err := q.CartLines(ctx, db.CartLinesParams{
		CartID: cartID, Locale: string(locale),
	})
	if err != nil {
		return nil, fmt.Errorf("read cart for checkout: %w", err)
	}
	if len(lines) == 0 {
		return nil, ErrEmpty
	}
	return lines, nil
}

type orderParts struct {
	orderID       uuid.UUID
	lines         []db.CartLinesRow
	userID        uuid.NullUUID
	discountCents int64
	invoice       *Invoice
	coupon        *Coupon
	orderNumber   string
	address       *order.Delivery
	totalCents    int64
	creditCents   int64
}

// writeOrderParts runs in the caller's transaction because each part is part of
// what the order IS.
func writeOrderParts(ctx context.Context, q *db.Queries, p *orderParts) error {
	if err := writeOrderLines(ctx, q, p.orderID, p.lines); err != nil {
		return err
	}

	// Without the hold two customers can order the last unit and both succeed;
	// record_inventory_movement's floor check refuses the second.
	if err := holdOrderStock(ctx, q, p.orderID, p.lines); err != nil {
		return err
	}

	// order_events is append-only, so an order whose history does not start
	// with 'placed' could never be repaired.
	if err := q.RecordPlacedEvent(ctx, p.orderID); err != nil {
		return fmt.Errorf("record placed event: %w", err)
	}

	// Spent here because orders_funded_to_leave_pending reads the ledger, so a
	// later debit leaves a fully-credited order looking unpaid. Spend exactly
	// what the customer confirmed, never whatever balance exists now; the
	// ledger guard catches a balance that fell below it while checkout waited.
	if err := spendCredit(ctx, q, p.orderID, p.userID, p.totalCents, p.creditCents); err != nil {
		return err
	}

	if invErr := writeInvoicePreference(
		ctx, q, p.orderID, p.invoice, p.address,
	); invErr != nil {
		return invErr
	}

	if couponErr := redeemCoupon(
		ctx, q, p.coupon, p.discountCents, p.orderID, p.userID,
	); couponErr != nil {
		return couponErr
	}

	if mailErr := enqueueOrderPlaced(ctx, q, p.orderNumber, p.address, p.totalCents); mailErr != nil {
		return mailErr
	}

	// Store credit paying the whole order is money received now, and 營業稅法 §32
	// invoices money received before dispatch; picking is too late.
	if p.creditCents > 0 && p.creditCents == p.totalCents {
		if dueErr := invoicepkg.EnqueueDue(ctx, q, &outbox.InvoiceDue{
			OrderNumber: p.orderNumber, Trigger: "commit:" + p.orderNumber,
		}); dueErr != nil {
			return dueErr
		}
	}

	return nil
}

// writeInvoicePreference records the immutable filing identity: delivery
// details can later be erased, while a committed sale and any refund against it
// still have a tax lifecycle.
func writeInvoicePreference(
	ctx context.Context,
	q *db.Queries,
	orderID uuid.UUID,
	inv *Invoice,
	addr *order.Delivery,
) error {
	if inv == nil {
		inv = &Invoice{Type: invoicepkg.PreferenceMember}
	}
	buyerName := addr.RecipientName
	if inv.Type == invoicepkg.PreferenceCompany {
		buyerName = inv.CompanyName
	}
	if err := q.CreateInvoicePreference(ctx, db.CreateInvoicePreferenceParams{
		OrderID:       orderID,
		InvoiceType:   string(inv.Type),
		CarrierCode:   inv.MobileBarcode,
		DonationCode:  inv.DonationCode,
		TaxID:         inv.TaxID,
		CustomerName:  buyerName,
		CustomerEmail: addr.Email,
	}); err != nil {
		return fmt.Errorf("record invoice preference: %w", err)
	}
	return nil
}

// A free-delivery coupon zeroes the BASE RATE and not the outlying-island
// surcharge, the same line the free-over threshold is held to.
func priceOrder(
	ctx context.Context,
	q *db.Queries,
	ship *db.ShippingVersionRow,
	subtotal int64,
	postalCode string,
	coupon *Coupon,
) (shippingCents, discountCents int64, err error) {
	quote, err := quoteFor(ctx, q, ship, subtotal, postalCode)
	if err != nil {
		return 0, 0, err
	}
	shippingCents, err = quote.Total()
	if err != nil {
		return 0, 0, fmt.Errorf("total shipping quote: %w", err)
	}

	// Applied against what the cart holds NOW: amount and minimum-spend
	// eligibility can have changed since the form was rendered.
	if coupon != nil {
		var freeShipping bool
		discountCents, freeShipping, err = coupon.Apply(subtotal)
		if err != nil {
			return 0, 0, err
		}
		if freeShipping {
			shippingCents = quote.Surcharge
		}
	}
	return shippingCents, discountCents, nil
}

func finishOrder(
	ctx context.Context,
	q *db.Queries,
	orderID, cartID uuid.UUID,
	addr *order.Delivery,
	attemptID checkoutAttemptID,
) error {
	if err := q.CreateOrderPrivateData(ctx, db.CreateOrderPrivateDataParams{
		OrderID:         orderID,
		Email:           text(addr.Email),
		RecipientName:   text(addr.RecipientName),
		Phone:           text(addr.Phone),
		PostalCode:      addr.PostalCode,
		City:            addr.City,
		District:        addr.District,
		Street:          addr.Street,
		PickupChain:     string(addr.PickupChain),
		PickupStoreCode: addr.PickupStoreCode,
		PickupStoreName: addr.PickupStoreName,
	}); err != nil {
		return fmt.Errorf("create order private data: %w", err)
	}

	if err := q.RecordCheckoutAttempt(ctx, db.RecordCheckoutAttemptParams{
		IdempotencyKey: attemptID.String(),
		CartID:         uuid.NullUUID{UUID: cartID, Valid: true},
		OrderID:        uuid.NullUUID{UUID: orderID, Valid: true},
	}); err != nil {
		return fmt.Errorf("record checkout attempt: %w", err)
	}

	if err := q.ClearCart(ctx, cartID); err != nil {
		return fmt.Errorf("clear cart: %w", err)
	}
	// The draft held what this order was typed from; it ends with the order.
	if err := q.ClearCheckoutDraft(ctx, cartID); err != nil {
		return fmt.Errorf("clear checkout draft: %w", err)
	}
	return nil
}

// claimCheckoutKey uses an advisory lock rather than an early INSERT, whose row
// would hold the key with order_id NULL.
func claimCheckoutKey(
	ctx context.Context, q *db.Queries, cartID uuid.UUID, attemptID checkoutAttemptID,
) (prior string, placed bool, err error) {
	key := attemptID.String()
	if lockErr := q.LockCheckoutKey(ctx, key); lockErr != nil {
		return "", false, fmt.Errorf("lock checkout key: %w", lockErr)
	}
	// Asked INSIDE the lock: the request that just waited is exactly the one
	// any earlier read would have missed.
	attempt, found, attemptErr := checkoutAttempt(ctx, q, attemptID)
	if attemptErr != nil || !found {
		return "", false, attemptErr
	}
	if !attempt.CartID.Valid || attempt.CartID.UUID != cartID {
		return "", false, errCheckoutKeyConflict
	}
	return checkoutAttemptOrder(ctx, q, attempt.OrderID)
}

func priorOrderTx(
	ctx context.Context,
	q *db.Queries,
	cartID uuid.UUID,
	attemptID checkoutAttemptID,
) (number string, found bool, err error) {
	attempt, found, err := checkoutAttempt(ctx, q, attemptID)
	if err != nil || !found {
		return "", false, err
	}
	if !attempt.CartID.Valid || attempt.CartID.UUID != cartID {
		return "", false, nil
	}
	return checkoutAttemptOrder(ctx, q, attempt.OrderID)
}

func checkoutAttempt(
	ctx context.Context,
	q *db.Queries,
	attemptID checkoutAttemptID,
) (db.CheckoutAttemptRow, bool, error) {
	attempt, err := q.CheckoutAttempt(ctx, attemptID.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return db.CheckoutAttemptRow{}, false, nil
	}
	if err != nil {
		return db.CheckoutAttemptRow{}, false, fmt.Errorf("read checkout attempt: %w", err)
	}
	return attempt, true, nil
}

func checkoutAttemptOrder(
	ctx context.Context,
	q *db.Queries,
	orderID uuid.NullUUID,
) (number string, found bool, err error) {
	if !orderID.Valid {
		return "", false, nil
	}
	number, err = q.OrderNumberByID(ctx, orderID.UUID)
	if err != nil {
		return "", false, fmt.Errorf("read checkout attempt order: %w", err)
	}
	return number, true, nil
}

// The cart binding stops an idempotency token from becoming cross-cart order
// access; Handler uses priorOrder only so a lost-response retry converges.
func (s *Store) priorOrder(
	ctx context.Context,
	cartID uuid.UUID,
	attemptID checkoutAttemptID,
) (number string, found bool, err error) {
	return priorOrderTx(ctx, s.q, cartID, attemptID)
}

func (s *Store) Order(ctx context.Context, number string) (pages.OrderView, error) {
	o, err := s.q.OrderSummaryByNumber(ctx, db.OrderSummaryByNumberParams{
		Number: number, Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pages.OrderView{}, ErrNotFound
		}
		return pages.OrderView{}, fmt.Errorf("read order: %w", err)
	}
	lines, err := s.q.OrderPageLines(ctx, db.OrderPageLinesParams{OrderID: o.ID, Locale: i18n.FromContext(ctx).Tag()})
	if err != nil {
		return pages.OrderView{}, fmt.Errorf("read order lines: %w", err)
	}

	hold, err := s.q.OrderHoldSpan(ctx, o.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return pages.OrderView{}, fmt.Errorf("read order stock hold: %w", err)
	}

	now := time.Now()
	view := pages.OrderView{
		Number:       o.OrderNumber,
		Status:       order.FulfillmentStatus(o.FulfillmentStatus),
		Email:        o.Email,
		ShippingName: o.ShippingMethodName,
		Committed:    o.Committed,
		OwedCents:    o.OwedCents,
		DeliveryTo: pages.Delivery{
			PostalCode: o.PostalCode, City: o.City, District: o.District, Street: o.Street,
			PickupChain: pickup.Chain(o.PickupChain), PickupStoreCode: o.PickupStoreCode,
			PickupStoreName: o.PickupStoreName,
		}.Line(),
		SubtotalCents: o.SubtotalCents,
		ShippingCents: o.ShippingCents,
		DiscountCents: o.DiscountCents, DiscountReason: o.DiscountReason,
		CreditCents: o.CreditCents,
		TaxCents:    o.TaxCents,
		PlacedAt:    o.PlacedAt,
		Now:         now,
		HoldUntil:   hold.HeldUntil,
		Pickup:      o.PickupChain != "",
	}
	ids := make([]uuid.UUID, 0, len(lines))
	byID := make(map[uuid.UUID]pages.OrderLine, len(lines))
	for i := range lines {
		l := &lines[i]
		line := pages.OrderLine{
			SKU: l.SKU, Name: l.ProductName, Label: l.VariantLabel.String,
			UnitCents: l.UnitPriceCents, Quantity: l.Quantity,
			ImageURL:       assets.ProductImageURL(l.ImageKey),
			ImageSrcset:    assets.ProductImageSrcsetAt(l.ImageKey, int(l.ImageWidth)),
			ImageAlt:       l.ImageAlt,
			WarrantyMonths: int(l.WarrantyMonths.Int32),
		}
		view.Lines = append(view.Lines, line)
		ids = append(ids, l.ID)
		byID[l.ID] = line
	}

	events, err := s.q.OrderTimeline(ctx, o.ID)
	if err != nil {
		return pages.OrderView{}, fmt.Errorf("read order timeline: %w", err)
	}
	for _, e := range events {
		view.Timeline = append(view.Timeline, pages.OrderEvent{
			Kind: order.EventKind(e.Kind), Note: e.Note,
			At: e.OccurredAt,
		})
	}

	if view.Invoice, err = s.orderInvoice(ctx, o.ID); err != nil {
		return pages.OrderView{}, err
	}
	if view.Shipments, view.Unshipped, err = s.orderParcels(ctx, o.ID, ids, byID); err != nil {
		return pages.OrderView{}, err
	}
	if view.Returned, err = s.orderReturned(ctx, o.ID); err != nil {
		return pages.OrderView{}, err
	}
	return view, nil
}

func (s *Store) orderParcels(
	ctx context.Context, orderID uuid.UUID, ids []uuid.UUID, byID map[uuid.UUID]pages.OrderLine,
) (parcels []pages.OrderShipment, unshipped []pages.OrderLine, err error) {
	shipments, err := s.q.OrderTracking(ctx, orderID)
	if err != nil {
		return nil, nil, fmt.Errorf("read order tracking: %w", err)
	}
	if len(shipments) == 0 {
		return nil, nil, nil
	}
	parcelLines, err := s.q.OrderParcelLines(ctx, orderID)
	if err != nil {
		return nil, nil, fmt.Errorf("read order parcels: %w", err)
	}
	registrations, err := s.q.OrderWarrantyRegistrations(ctx, orderID)
	if err != nil {
		return nil, nil, fmt.Errorf("read order warranties: %w", err)
	}
	parcels, unshipped = cutParcels(shipments, parcelLines, registrations, ids, byID)
	return parcels, unshipped, nil
}

// cutParcels hands each parcel the units of the lines that went in it, and returns what no parcel carries yet. A
// line's units are numbered across its parcels in shipment order, as RegisterWarranty numbers them, so a
// registration belongs to the parcel its unit went in. A parcel not yet delivered carries no day: the database
// reads shop_today() there.
func cutParcels(
	shipments []db.OrderTrackingRow, parcelLines []db.OrderParcelLinesRow, registrations []db.OrderWarrantyRegistrationsRow,
	ids []uuid.UUID, byID map[uuid.UUID]pages.OrderLine,
) (parcels []pages.OrderShipment, unshipped []pages.OrderLine) {
	registered := make(map[uuid.UUID][]db.OrderWarrantyRegistrationsRow)
	for _, r := range registrations {
		registered[r.OrderLineID] = append(registered[r.OrderLineID], r)
	}
	shipped := make(map[uuid.UUID]int32, len(byID))
	for i := range shipments {
		sh := &shipments[i]
		parcel := pages.OrderShipment{
			Carrier: carrier.Carrier(sh.Carrier), Tracking: sh.TrackingNumber, ShippedAt: sh.ShippedAt,
		}
		if sh.DeliveredAt.Valid {
			parcel.DeliveredAt, parcel.RescissionEnds, parcel.GoodwillEnds = sh.DeliveredAt.Time, sh.RescissionEnds, sh.GoodwillEnds
		}
		for _, pl := range parcelLines {
			if pl.ShipmentID != sh.ID {
				continue
			}
			share := byID[pl.OrderLineID]
			share.Quantity = pl.Quantity
			before := shipped[pl.OrderLineID]
			for _, r := range registered[pl.OrderLineID] {
				if int32(r.UnitNo) > before && int32(r.UnitNo) <= before+pl.Quantity {
					share.Registered++
					share.WarrantyUntil = r.ExpiresOn
				}
			}
			shipped[pl.OrderLineID] = before + pl.Quantity
			parcel.Lines = append(parcel.Lines, share)
		}
		parcels = append(parcels, parcel)
	}
	for _, id := range ids {
		if left := byID[id].Quantity - shipped[id]; left > 0 {
			rest := byID[id]
			rest.Quantity = left
			unshipped = append(unshipped, rest)
		}
	}
	return parcels, unshipped
}

// orderReturned is nil until every unit the order sold is in an approved or completed return.
func (s *Store) orderReturned(ctx context.Context, orderID uuid.UUID) (*pages.OrderReturned, error) {
	returned, err := s.q.ReturnedOrders(ctx, []uuid.UUID{orderID})
	if err != nil {
		return nil, fmt.Errorf("read returned order: %w", err)
	}
	if len(returned) == 0 {
		return nil, nil //nolint:nilnil // an order not returned in full is not an error
	}
	returns, err := s.q.OrderReturns(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("read order returns: %w", err)
	}
	out := &pages.OrderReturned{}
	for _, r := range returns {
		out.At = r.PaidOutAt
		out.RefundCents += r.RefundCents
	}
	return out, nil
}

func (s *Store) orderInvoice(ctx context.Context, orderID uuid.UUID) (*pages.OrderInvoice, error) {
	docs, err := s.q.OrderInvoiceDocuments(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("read order invoice: %w", err)
	}
	if len(docs) == 0 {
		return nil, nil //nolint:nilnil // no invoice filed yet is not an error
	}
	pref, err := s.q.OrderInvoicePreference(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("read order invoice preference: %w", err)
	}
	out := &pages.OrderInvoice{
		Type: invoicepkg.Preference(pref.InvoiceType), MobileBarcode: pref.CarrierCode,
		DonationCode: pref.DonationCode, TaxID: pref.TaxID,
	}
	for i := range docs {
		d := &docs[i]
		out.Documents = append(out.Documents, pages.OrderInvoiceDocument{
			Allowance: invoicepkg.DocumentKind(d.Kind) == invoicepkg.DocumentAllowance, Number: d.Number, RandomCode: d.ProviderRef,
			AmountCents: d.AmountCents, Voided: invoicepkg.DocumentStatus(d.Status) == invoicepkg.DocumentVoided,
			IssuedOn: shoptime.Day(d.IssuedAt),
		})
	}
	return out, nil
}

func text(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// holdOrderStock derives the idempotency key from the order and variant, so a
// retried checkout cannot double-hold.
func holdOrderStock(ctx context.Context, q *db.Queries, orderID uuid.UUID, lines []db.CartLinesRow) error {
	holdFor := pgtype.Interval{
		Microseconds: int64(holdTTL / time.Microsecond),
		Valid:        true,
	}
	for i := range lines {
		l := &lines[i]
		if _, err := q.HoldForOrder(ctx, db.HoldForOrderParams{
			OrderID:        orderID,
			VariantID:      l.VariantID,
			Quantity:       l.Quantity,
			HoldFor:        holdFor,
			IdempotencyKey: "hold:" + orderID.String() + ":" + l.VariantID.String(),
		}); err != nil {
			if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
				pgErr.ConstraintName == "inventory_never_negative" {
				return ErrUnavailable
			}
			return fmt.Errorf("hold %d of variant %s: %w", l.Quantity, l.VariantID, err)
		}
	}
	return nil
}

// subtotalOf is re-checked INSIDE the transaction, because the last unit could
// have sold since the cart page rendered.
func subtotalOf(lines []db.CartLinesRow) (int64, error) {
	var subtotal int64
	for i := range lines {
		l := &lines[i]
		if pages.ProductStatus(l.ProductStatus) != pages.ProductActive || !l.IsActive || l.Quantity > l.SellableQuantity {
			return 0, ErrUnavailable
		}
		var err error
		subtotal, err = addCheckoutLine(subtotal, l.PriceCents, l.Quantity)
		if err != nil {
			return 0, fmt.Errorf("total cart: %w", err)
		}
	}
	return subtotal, nil
}

// writeOrderLines COPIES the price and warranty promise: a later catalogue
// change must not rewrite either.
func writeOrderLines(ctx context.Context, q *db.Queries, orderID uuid.UUID, lines []db.CartLinesRow) error {
	for i := range lines {
		l := &lines[i]
		if err := q.CreateOrderLine(ctx, db.CreateOrderLineParams{
			OrderID:        orderID,
			ProductID:      uuid.NullUUID{UUID: l.ProductID, Valid: true},
			VariantID:      uuid.NullUUID{UUID: l.VariantID, Valid: true},
			SKU:            l.SKU,
			ProductName:    l.Name,
			VariantLabel:   text(optionLabel(l.OptionNames, l.OptionValues)),
			WarrantyNote:   l.WarrantyNote,
			WarrantyMonths: l.WarrantyMonths,
			UnitPriceCents: l.PriceCents,
			Quantity:       l.Quantity,
			Position:       int32(i),
		}); err != nil {
			// The cart read this line's name and SKU earlier in the
			// transaction; an admin rename committing in between makes the
			// snapshot stale, and the refreshed quote is what the shopper needs
			// to see.
			if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
				(pgErr.ConstraintName == "order_lines_name_matches_product" ||
					pgErr.ConstraintName == "order_lines_sku_matches_variant") {
				return errCheckoutChanged
			}
			return fmt.Errorf("create order line: %w", err)
		}
	}
	return nil
}

// spendCredit is bounded by the order total and the ledger refuses a balance
// that fell while placement waited; a later increase is deliberately not
// auto-spent.
func spendCredit(
	ctx context.Context,
	q *db.Queries,
	orderID uuid.UUID,
	userID uuid.NullUUID,
	owedCents int64,
	spend int64,
) error {
	if spend == 0 {
		return nil
	}
	if !userID.Valid || spend < 0 || spend > owedCents {
		return errCheckoutChanged
	}
	if _, err := q.SpendCredit(ctx, db.SpendCreditParams{
		AmountCents: -spend,
		OrderID:     orderID,
	}); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.ConstraintName == "store_credit_never_negative" {
			return ErrCreditChanged
		}
		return fmt.Errorf("spend %d cents of credit on order %s: %w", spend, orderID, err)
	}
	return nil
}

func (s *Store) AvailableCredit(ctx context.Context, userID uuid.NullUUID) (int64, error) {
	if !userID.Valid {
		return 0, nil
	}
	balance, err := s.q.AvailableCredit(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("read store credit: %w", err)
	}
	if balance < 0 {
		return 0, nil
	}
	return balance, nil
}

func (s *Store) ItemCount(ctx context.Context, cartID uuid.UUID) (int, error) {
	n, err := s.q.CartItemCount(ctx, cartID)
	if err != nil {
		return 0, fmt.Errorf("count cart items: %w", err)
	}
	return int(n), nil
}
