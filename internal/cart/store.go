package cart

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	invoicepkg "github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Store reads and writes carts and places orders. It holds the pool rather than
// a DBTX because multi-statement operations open and own their transactions.
type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

// NewStore returns a Store over pool.
func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("cart: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

// CartByToken returns the cart a token names, or ErrNotFound.
func (s *Store) CartByToken(ctx context.Context, token string) (uuid.UUID, error) {
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
	return row.ID, nil
}

// Create opens a new cart and returns its id. Two signed-in first writes can
// both observe no row; carts_one_per_user refuses the second insert, and the
// winner is the account cart.
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

// ownedCartIfTaken rereads the account cart after carts_one_per_user refuses
// an insert. The unique index is the one-cart rule; this only names it.
func (s *Store) ownedCartIfTaken(ctx context.Context, userID uuid.NullUUID, err error) (uuid.UUID, bool) {
	if !userID.Valid {
		return uuid.Nil, false
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "carts_one_per_user" {
		return uuid.Nil, false
	}
	existing, readErr := s.CartForUser(ctx, userID.UUID.String())
	if readErr != nil {
		return uuid.Nil, false
	}
	return existing, true
}

// Add puts a variant in a cart, checked before the write so an inactive one is a
// message rather than a foreign-key error.
func (s *Store) Add(ctx context.Context, cartID, variantID uuid.UUID, quantity int32) error {
	return s.mutateCart(ctx, cartID, func(q *db.Queries) error {
		return addCartItem(ctx, q, cartID, variantID, quantity)
	})
}

// addCartItem applies the cart's availability and quantity rules through the
// caller's queries, which may be pool-backed or bound to a larger transaction.
func addCartItem(
	ctx context.Context,
	q *db.Queries,
	cartID, variantID uuid.UUID,
	quantity int32,
) error {
	v, err := q.VariantForCart(ctx, variantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read variant: %w", err)
	}
	if !v.IsActive || v.Status != "active" || v.SellableQuantity <= 0 {
		return ErrUnavailable
	}
	if quantity > v.SellableQuantity {
		quantity = v.SellableQuantity
	}
	capacity, err := q.CartLineCapacity(ctx, db.CartLineCapacityParams{
		CartID: cartID, VariantID: variantID,
	})
	if err != nil {
		return fmt.Errorf("count cart lines: %w", err)
	}
	if !capacity.AlreadyPresent && capacity.LineCount >= invoicepkg.MaxIssueProductLines {
		return ErrTooManyItems
	}
	if err := q.AddCartItem(ctx, db.AddCartItemParams{
		CartID: cartID, VariantID: variantID, Quantity: quantity,
	}); err != nil {
		return fmt.Errorf("add cart item: %w", err)
	}
	return nil
}

// SetQuantity changes a line, removing it at zero.
func (s *Store) SetQuantity(ctx context.Context, cartID, variantID uuid.UUID, quantity int32) error {
	return s.mutateCart(ctx, cartID, func(q *db.Queries) error {
		if quantity <= 0 {
			if err := q.RemoveCartItem(ctx, db.RemoveCartItemParams{CartID: cartID, VariantID: variantID}); err != nil {
				return fmt.Errorf("remove cart item: %w", err)
			}
			return nil
		}
		if err := q.SetCartItemQuantity(ctx, db.SetCartItemQuantityParams{
			CartID: cartID, VariantID: variantID, Quantity: quantity,
		}); err != nil {
			return fmt.Errorf("set cart quantity: %w", err)
		}
		return nil
	})
}

// Remove drops a line.
func (s *Store) Remove(ctx context.Context, cartID, variantID uuid.UUID) error {
	return s.mutateCart(ctx, cartID, func(q *db.Queries) error {
		if err := q.RemoveCartItem(ctx, db.RemoveCartItemParams{CartID: cartID, VariantID: variantID}); err != nil {
			return fmt.Errorf("remove cart item: %w", err)
		}
		return nil
	})
}

// mutateCart gives every standalone cart write the same aggregate lock used by
// reorder, account adoption and checkout. The callback is valid only inside
// this transaction and must not retain q.
func (s *Store) mutateCart(
	ctx context.Context,
	cartID uuid.UUID,
	mutate func(*db.Queries) error,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin cart mutation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
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

// Count is how many UNITS a cart holds, for the header badge.
func (s *Store) Count(ctx context.Context, cartID uuid.UUID) (int64, error) {
	n, err := s.q.CartItemCount(ctx, cartID)
	if err != nil {
		return 0, fmt.Errorf("count cart: %w", err)
	}
	return n, nil
}

// View reads a cart for display. Prices and availability are read fresh every
// time: a cart line is not a promise.
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
			ImageAlt:     r.ImageAlt,
			CompareCents: r.CompareAtPriceCents.Int64,
			Unavailable:  r.ProductStatus != "active" || !r.IsActive || r.SellableQuantity <= 0,
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

// optionLabel renders a variant's option values, joined with a middle dot.
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

// SavedAddresses is the delivery addresses an account has. A guest gets none
// because the QUERY scopes to the owner, never a short-circuit here.
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

// QuoteShipping is what one method charges to send this order to this address.
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

// quoteFor prices a version already read through the caller's query binding.
// PlaceOrder passes transaction-bound queries so no price read escapes its locks.
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

// ShippingChoices is the shipping methods a checkout may offer FOR THIS CART: a
// method whose carrier refuses a 27-inch monitor is not a choice for a basket
// with one in it.
func (s *Store) ShippingChoices(ctx context.Context, cartID uuid.UUID, subtotalCents int64) ([]pages.ShippingChoice, error) {
	rows, err := s.q.ShippingChoices(ctx, db.ShippingChoicesParams{
		Locale: string(i18n.FromContext(ctx)),
		CartID: cartID,
	})
	if err != nil {
		return nil, fmt.Errorf("read shipping choices: %w", err)
	}
	out := make([]pages.ShippingChoice, 0, len(rows))
	for _, r := range rows {
		fee := ShippingFee(r.FeeCents, r.FreeOverCents.Int64, subtotalCents)
		out = append(out, pages.ShippingChoice{
			VersionID:       r.VersionID.String(),
			Code:            r.Code,
			DestinationKind: r.DestinationKind,
			Name:            r.Name,
			Carrier:         r.Carrier,
			FeeCents:        fee,
			Free:            fee == 0,
		})
	}
	return out, nil
}

// classifyCheckout maps the database ending a statement onto ErrBusy, so the
// handler re-renders the form the customer filled in instead of answering 500.
// Bound to the SQLSTATE: 57014 is query_canceled, which statement_timeout and an
// operator's pg_cancel_backend both raise, and neither is a defect in what was
// submitted.
func classifyCheckout(err error) error {
	if err == nil {
		return nil
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "57014" {
		return fmt.Errorf("%w: %w", ErrBusy, err)
	}
	return err
}

// PlaceOrder turns a cart into an order in ONE transaction, recomputing the
// shipping fee from the version rather than taking it from the form. quote is
// not trusted as pricing input: it is compared with the locked recomputation so
// an order cannot differ from what the customer confirmed. attemptID makes
// a resubmitted checkout find its own order.
func (s *Store) placeOrder(
	ctx context.Context,
	cartID uuid.UUID,
	userID uuid.NullUUID,
	shippingVersionID uuid.UUID,
	addr *Address,
	inv *Invoice,
	couponCode string,
	shown checkoutQuoteID,
	attemptID checkoutAttemptID,
) (number string, err error) {
	// One classifier at the boundary rather than a check at each return: every
	// statement below runs under the pool's statement_timeout, and a lock wait
	// that outlives it is the ordinary shape of a busy shop rather than a fault.
	defer func() { err = classifyCheckout(err) }()

	// Nothing reads checkout_attempts on the pool first: claimCheckoutKey asks the
	// same question under the lock, and the copy that can be wrong is the early one.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin checkout: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	prior, taken, err := claimCheckoutKey(ctx, q, cartID, attemptID)
	if err != nil {
		return "", err
	}
	if taken {
		return prior, nil
	}
	if userID.Valid {
		// Erasure/adoption start at the user aggregate. Hold its compatible
		// KEY SHARE before checkoutCartSnapshot takes the cart row, so no path
		// can own cart -> wait user while another owns user -> wait cart.
		lockedUser, lockErr := q.LockUserForCheckout(ctx, userID.UUID)
		if lockErr != nil {
			return "", fmt.Errorf("lock account for checkout: %w", lockErr)
		}
		if !lockedUser {
			return "", ErrNotFound
		}
	}
	terms, err := lockCheckoutTerms(
		ctx, q, cartID, userID, shippingVersionID, addr, couponCode, shown,
	)
	if err != nil {
		return "", err
	}

	order, err := q.CreateOrder(ctx, db.CreateOrderParams{
		UserID:             userID,
		ShippingVersionID:  terms.ship.ID,
		ShippingMethodCode: terms.ship.Code,
		ShippingMethodName: terms.ship.Name,
		ShippingCents:      terms.shippingFee,
		DiscountCents:      terms.discount,
		CustomerNote:       text(addr.Note),
		// Every later message about this order is sent in this language, never in
		// whatever the thing that triggered it happened to be reading.
		Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		return "", fmt.Errorf("create order: %w", err)
	}

	if err := writeOrderParts(ctx, q, &orderParts{
		orderID:       order.ID,
		lines:         terms.lines,
		userID:        userID,
		discountCents: terms.discount,
		invoice:       inv,
		coupon:        terms.coupon,
		orderNumber:   order.OrderNumber,
		address:       addr,
		totalCents:    terms.gross,
		creditCents:   terms.creditCents,
	}); err != nil {
		return "", err
	}

	if err := finishOrder(ctx, q, order.ID, cartID, addr, attemptID); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit checkout: %w", err)
	}
	return order.OrderNumber, nil
}

// checkoutTerms is the server-owned commercial snapshot after every aggregate
// row it depends on has been locked and the customer's quote has matched it.
// It is consumed inside the same transaction; no unlocked or form-supplied
// amount can enter the order write below.
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
	addr *Address,
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

	// The destination is re-derived from the method just read and the address is
	// trimmed to it, the way the shipping fee is recomputed rather than trusted.
	to, ok := DestinationFor(ship.DestinationKind)
	if !ok {
		return nil, fmt.Errorf("shipping method %s has an unknown destination %q",
			ship.Code, ship.DestinationKind)
	}
	addr.To = to
	addr.ForDestination()

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

// lockedAvailableCredit returns the balance at checkout's linearization point.
// Guests own no account. Existing accounts remain locked until the order
// transaction commits, which is the same row every credit posting must lock.
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
// availability that become the order. The caller owns the surrounding
// transaction.
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

// orderParts is what hangs off an order header.
type orderParts struct {
	orderID       uuid.UUID
	lines         []db.CartLinesRow
	userID        uuid.NullUUID
	discountCents int64
	invoice       *Invoice
	coupon        *Coupon
	orderNumber   string
	address       *Address
	totalCents    int64
	creditCents   int64
}

// writeOrderParts writes the lines, the stock hold, the first history entry, the
// store-credit spend, the invoice choice, the redemption and the mail intent —
// all in the caller's transaction, because each is part of what the order IS.
func writeOrderParts(ctx context.Context, q *db.Queries, p *orderParts) error {
	if err := writeOrderLines(ctx, q, p.orderID, p.lines); err != nil {
		return err
	}

	// Without the hold two customers can order the last unit and both succeed;
	// record_inventory_movement's floor check is what refuses the second.
	if err := holdOrderStock(ctx, q, p.orderID, p.lines); err != nil {
		return err
	}

	// order_events is append-only, so an order whose history does not start with
	// 'placed' could never be repaired.
	if err := q.RecordPlacedEvent(ctx, p.orderID); err != nil {
		return fmt.Errorf("record placed event: %w", err)
	}

	// Spent here because orders_funded_to_leave_pending reads the ledger, so a
	// debit posted afterwards leaves a fully-credited order looking unpaid.
	// Spend exactly what the customer confirmed, never whatever balance happens
	// to exist now. p.totalCents bounds the request; the ledger guard catches a
	// balance that fell below it while checkout was waiting.
	if err := spendCredit(ctx, q, p.orderID, p.userID, p.totalCents, p.creditCents); err != nil {
		return err
	}

	if invErr := writeInvoicePreference(
		ctx, q, p.orderID, p.invoice, p.address,
	); invErr != nil {
		return invErr
	}

	// coupon_redemption_matches_order refuses a row that disagrees with
	// orders.discount_cents, so the two cannot drift.
	if couponErr := redeemCoupon(
		ctx, q, p.coupon, p.discountCents, p.orderID, p.userID,
	); couponErr != nil {
		return couponErr
	}

	// The confirmation email's INTENT, in the order's own transaction.
	if mailErr := enqueueOrderPlaced(ctx, q, p.orderID, p.orderNumber, p.address, p.totalCents); mailErr != nil {
		return mailErr
	}

	return nil
}

// writeInvoicePreference records the immutable filing identity alongside the
// customer's carrier choice. Delivery details can later be erased, while a
// committed sale and any refund against it still have a tax lifecycle.
func writeInvoicePreference(
	ctx context.Context,
	q *db.Queries,
	orderID uuid.UUID,
	inv *Invoice,
	addr *Address,
) error {
	if inv == nil {
		inv = &Invoice{Type: invoicepkg.PreferenceMember}
	}
	buyerName := addr.Name
	if inv.Type == invoicepkg.PreferenceCompany {
		buyerName = inv.CompanyName
	}
	if err := q.CreateInvoicePreference(ctx, db.CreateInvoicePreferenceParams{
		OrderID:       orderID,
		InvoiceType:   string(inv.Type),
		CarrierCode:   inv.Carrier,
		TaxID:         inv.TaxID,
		CustomerName:  buyerName,
		CustomerEmail: addr.Email,
	}); err != nil {
		return fmt.Errorf("record invoice preference: %w", err)
	}
	return nil
}

// priceOrder is what an order costs to deliver and what a coupon takes off it. A
// free-delivery coupon zeroes the BASE RATE and not the outlying-island
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

	// Applied against what the cart holds NOW: both the amount and minimum-spend
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

// finishOrder writes the delivery details, the idempotency record and the
// emptied cart, in the same transaction as the order.
func finishOrder(
	ctx context.Context,
	q *db.Queries,
	orderID, cartID uuid.UUID,
	addr *Address,
	attemptID checkoutAttemptID,
) error {
	if err := q.CreateOrderPrivateData(ctx, db.CreateOrderPrivateDataParams{
		OrderID:         orderID,
		Email:           text(addr.Email),
		RecipientName:   text(addr.Name),
		Phone:           text(addr.Phone),
		PostalCode:      addr.PostalCode,
		City:            addr.City,
		District:        addr.District,
		Street:          addr.Street,
		PickupBrand:     string(addr.PickupBrand),
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
	return nil
}

// claimCheckoutKey takes this checkout's idempotency key for the transaction and
// reports the order a concurrent request already placed on it. An advisory lock
// rather than an early INSERT, whose row would hold the key with order_id NULL.
func claimCheckoutKey(
	ctx context.Context, q *db.Queries, cartID uuid.UUID, attemptID checkoutAttemptID,
) (prior string, placed bool, err error) {
	key := attemptID.String()
	if lockErr := q.LockCheckoutKey(ctx, key); lockErr != nil {
		return "", false, fmt.Errorf("lock checkout key: %w", lockErr)
	}
	// Asked INSIDE the lock: the request that just waited is exactly the one any
	// earlier read would have missed.
	attempt, found, attemptErr := checkoutAttempt(ctx, q, attemptID)
	if attemptErr != nil || !found {
		return "", false, attemptErr
	}
	if !attempt.CartID.Valid || attempt.CartID.UUID != cartID {
		return "", false, errCheckoutKeyConflict
	}
	return checkoutAttemptOrder(ctx, q, attempt.OrderID)
}

// priorOrderTx is the same question asked through a given Queries.
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

// priorOrder reports the completed order produced by this cart and checkout
// key. The cart binding prevents an idempotency token from becoming cross-cart
// order access; Handler uses it only to make a lost-response retry converge.
func (s *Store) priorOrder(
	ctx context.Context,
	cartID uuid.UUID,
	attemptID checkoutAttemptID,
) (number string, found bool, err error) {
	return priorOrderTx(ctx, s.q, cartID, attemptID)
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

// Order reads a placed order for the confirmation page.
func (s *Store) Order(ctx context.Context, number string) (pages.OrderView, error) {
	o, err := s.q.OrderSummaryByNumber(ctx, number)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pages.OrderView{}, ErrNotFound
		}
		return pages.OrderView{}, fmt.Errorf("read order: %w", err)
	}
	lines, err := s.q.OrderLinesByOrder(ctx, o.ID)
	if err != nil {
		return pages.OrderView{}, fmt.Errorf("read order lines: %w", err)
	}

	view := pages.OrderView{
		Number:       o.OrderNumber,
		Status:       pages.FulfillmentStatus(o.FulfillmentStatus),
		Email:        o.Email,
		ShippingName: o.ShippingMethodName,
		Committed:    o.Committed,
		OwedCents:    o.OwedCents,
		DeliveryTo: pages.Delivery{
			PostalCode: o.PostalCode, City: o.City, District: o.District, Street: o.Street,
			PickupBrand: pickup.Brand(o.PickupBrand), PickupStoreCode: o.PickupStoreCode,
			PickupStoreName: o.PickupStoreName,
		}.Line(),
		SubtotalCents: o.SubtotalCents,
		ShippingCents: o.ShippingCents,
		DiscountCents: o.DiscountCents, DiscountReason: o.DiscountReason,
		CreditCents: o.CreditCents,
		TaxCents:    o.TaxCents,
		PlacedAt:    shoptime.Minute(o.PlacedAt),
	}
	for _, l := range lines {
		view.Lines = append(view.Lines, pages.OrderLine{
			SKU: l.SKU, Name: l.ProductName, Label: l.VariantLabel.String,
			UnitCents: l.UnitPriceCents, Quantity: l.Quantity,
		})
	}

	events, err := s.q.OrderTimeline(ctx, o.ID)
	if err != nil {
		return pages.OrderView{}, fmt.Errorf("read order timeline: %w", err)
	}
	for _, e := range events {
		view.Timeline = append(view.Timeline, pages.OrderEvent{
			Kind: e.Kind, Note: e.Note.String,
			At: shoptime.Minute(e.OccurredAt),
		})
	}

	shipments, err := s.q.OrderTracking(ctx, o.ID)
	if err != nil {
		return pages.OrderView{}, fmt.Errorf("read order tracking: %w", err)
	}
	for _, sh := range shipments {
		view.Shipments = append(view.Shipments, pages.OrderShipment{
			Carrier: sh.Carrier, Tracking: sh.TrackingNumber,
			ShippedAt:   shoptime.Minute(sh.ShippedAt),
			DeliveredAt: nullableTime(sh.DeliveredAt),
		})
	}
	return view, nil
}

// text wraps a string for a nullable text column, treating empty as NULL.
func text(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// holdOrderStock reserves every line's stock against the order. The idempotency
// key is derived from the order and the variant, so a retried checkout under the
// same order cannot double-hold.
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

// subtotalOf prices a cart, refusing anything that can no longer be supplied.
// Re-checked INSIDE the transaction, because the last one could have sold since
// the cart page rendered.
func subtotalOf(lines []db.CartLinesRow) (int64, error) {
	var subtotal int64
	for i := range lines {
		l := &lines[i]
		if l.ProductStatus != "active" || !l.IsActive || l.Quantity > l.SellableQuantity {
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

// writeOrderLines copies each cart line onto the order. The price and warranty
// promise are COPIED: a later catalogue change must not rewrite either one.
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
			return fmt.Errorf("create order line: %w", err)
		}
	}
	return nil
}

func nullableTime(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return shoptime.Minute(t.Time)
}

// spendCredit applies exactly the amount in the customer-confirmed quote. It is
// bounded by the order total and the ledger refuses a balance that fell while
// placement was waiting; a later increase is deliberately not auto-spent.
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

// AvailableCredit is what a signed-in customer has to spend. Guests have none.
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

// ItemCount is how many UNITS a cart holds, for the header badge.
func (s *Store) ItemCount(ctx context.Context, cartID uuid.UUID) (int, error) {
	n, err := s.q.CartItemCount(ctx, cartID)
	if err != nil {
		return 0, fmt.Errorf("count cart items: %w", err)
	}
	return int(n), nil
}
