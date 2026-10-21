package checkout

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

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/discount/coupon"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/shipping"
)

// Store holds the pool rather than a DBTX because multi-statement operations
// open and own their transactions. carts reads the cart the page shows; what
// placement reads of the cart it reads in its own transaction.
type Store struct {
	pool  *pgxpool.Pool
	q     *db.Queries
	carts *cart.Store
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("checkout: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool), carts: cart.NewStore(pool)}
}

// SavedAddresses gives a guest none because the QUERY scopes to the owner,
// never a short-circuit here.
func (s *Store) SavedAddresses(ctx context.Context, owner uuid.NullUUID) ([]SavedAddress, error) {
	rows, err := s.q.SavedAddresses(ctx, owner.UUID)
	if err != nil {
		return nil, fmt.Errorf("read saved addresses: %w", err)
	}
	out := make([]SavedAddress, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, SavedAddress{
			ID: r.ID.String(), Label: r.Label.String, Name: r.RecipientName,
			Phone: r.Phone, PostalCode: r.PostalCode, City: r.City,
			District: r.District, Street: r.Street, Default: r.IsDefault,
		})
	}
	return out, nil
}

func (s *Store) QuoteShipping(ctx context.Context, versionID uuid.UUID, subtotalCents int64, postalCode string) (ShippingQuote, error) {
	v, err := s.q.ShippingVersion(ctx, db.ShippingVersionParams{
		ID: versionID, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ShippingQuote{}, cart.ErrNotFound
		}
		return ShippingQuote{}, fmt.Errorf("read shipping version: %w", err)
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
) (ShippingQuote, error) {
	zone, err := q.ShippingZoneFor(ctx, db.ShippingZoneForParams{
		VersionID: v.ID, PostalCode: postalCode,
		Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return ShippingQuote{}, fmt.Errorf("read shipping zone: %w", err)
	}
	return ShippingQuote{
		FeeCents:  cart.ShippingFee(v.FeeCents, v.FreeOverCents.Int64, subtotalCents),
		Surcharge: zone.SurchargeCents,
		ZoneName:  zone.ZoneName,
	}, nil
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
	addr *delivery.Details,
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
	defer db.Rollback(ctx, tx)
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
				return "", cart.ErrNotFound
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
	coupon      *coupon.Coupon
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
	addr *delivery.Details,
	couponCode string,
	shown checkoutQuoteID,
) (*checkoutTerms, error) {
	lines, err := checkoutCartSnapshot(ctx, q, cartID, i18n.FromContext(ctx))
	if err != nil {
		return nil, err
	}
	if len(lines) > issuer.MaxIssueProductLines {
		return nil, cart.ErrTooManyItems
	}

	ship, err := lockedShippingVersion(ctx, q, shippingVersionID)
	if err != nil {
		return nil, err
	}

	// The destination is re-derived from the method just read and the address
	// trimmed to it, as the fee is recomputed rather than trusted.
	to, ok := shipping.ParseDestination(ship.DestinationKind)
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

	canonicalCouponCode := coupon.Normalise(couponCode)
	var c *coupon.Coupon
	if canonicalCouponCode != "" {
		if lockErr := q.LockCouponForCheckout(ctx, canonicalCouponCode); lockErr != nil {
			return nil, fmt.Errorf("lock coupon %q: %w", canonicalCouponCode, lockErr)
		}
		c, err = couponByCode(ctx, q, canonicalCouponCode)
		if err != nil {
			return nil, err
		}
	}

	shippingFee, discount, err := priceOrder(ctx, q, &ship, subtotal, addr.PostalCode, c)
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
		lines: lines, ship: ship, coupon: c,
		shippingFee: shippingFee, discount: discount,
		gross: gross, creditCents: creditCents,
	}, nil
}

// lockedShippingVersion reads the version checkout prices delivery from. Its fee
// is append-only, but the zone surcharges and prefixes are edited in place, and
// orders_have_lines reads them again at COMMIT; the zone lock, held shared to
// the end of the transaction, keeps them as they were quoted.
func lockedShippingVersion(ctx context.Context, q *db.Queries, id uuid.UUID) (db.ShippingVersionRow, error) {
	if err := q.LockZoneRatesForCheckout(ctx); err != nil {
		return db.ShippingVersionRow{}, fmt.Errorf("lock zone rates: %w", err)
	}
	ship, err := q.ShippingVersion(ctx, db.ShippingVersionParams{
		ID: id, Locale: string(i18n.FromContext(ctx)),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.ShippingVersionRow{}, cart.ErrNotFound
	}
	if err != nil {
		return db.ShippingVersionRow{}, fmt.Errorf("read shipping version: %w", err)
	}
	return ship, nil
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
	if err := cart.Lock(ctx, q, cartID); err != nil {
		return nil, err
	}
	if err := q.LockCartCatalogue(ctx, cartID); err != nil {
		return nil, fmt.Errorf("lock cart catalogue: %w", err)
	}
	lines, err := cart.Lines(ctx, q, cartID, locale)
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
	coupon        *coupon.Coupon
	orderNumber   string
	address       *delivery.Details
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
		if dueErr := issuer.EnqueueDue(ctx, q, &outbox.InvoiceDue{
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
	addr *delivery.Details,
) error {
	if inv == nil {
		inv = &Invoice{Type: invoice.PreferenceMember}
	}
	buyerName := addr.RecipientName
	if inv.Type == invoice.PreferenceCompany {
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
	c *coupon.Coupon,
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
	if c != nil {
		var freeShipping bool
		discountCents, freeShipping, err = c.Apply(subtotal, quote.FeeCents == 0)
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
	addr *delivery.Details,
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
		Microseconds: int64(payment.HoldTTL / time.Microsecond),
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
				return cart.ErrUnavailable
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
		if product.Status(l.ProductStatus) != product.StatusActive || !l.IsActive || l.Quantity > l.SellableQuantity {
			return 0, cart.ErrUnavailable
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
			VariantLabel:   text(cart.OptionLabel(l.OptionNames, l.OptionValues)),
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
