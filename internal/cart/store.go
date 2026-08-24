package cart

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Store reads and writes carts and places orders. It holds the pool rather than
// a DBTX because placing an order needs a transaction the store itself opens.
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

// Find returns the cart a token names, or ErrNotFound.
func (s *Store) Find(ctx context.Context, token string) (uuid.UUID, error) {
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

// Create opens a new cart and returns its id.
func (s *Store) Create(ctx context.Context, token string, userID uuid.NullUUID) (uuid.UUID, error) {
	id, err := s.q.CreateCart(ctx, db.CreateCartParams{TokenHash: HashToken(token), UserID: userID})
	if err != nil {
		return uuid.Nil, fmt.Errorf("create cart: %w", err)
	}
	return id, nil
}

// Add puts a variant in a cart, checked before the write so an inactive one is a
// message rather than a foreign-key error.
func (s *Store) Add(ctx context.Context, cartID, variantID uuid.UUID, quantity int32) error {
	v, err := s.q.VariantForCart(ctx, variantID)
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
	if err := s.q.AddCartItem(ctx, db.AddCartItemParams{
		CartID: cartID, VariantID: variantID, Quantity: quantity,
	}); err != nil {
		return fmt.Errorf("add cart item: %w", err)
	}
	return nil
}

// SetQuantity changes a line, removing it at zero.
func (s *Store) SetQuantity(ctx context.Context, cartID, variantID uuid.UUID, quantity int32) error {
	if quantity <= 0 {
		if err := s.q.RemoveCartItem(ctx, db.RemoveCartItemParams{CartID: cartID, VariantID: variantID}); err != nil {
			return fmt.Errorf("remove cart item: %w", err)
		}
		return nil
	}
	if err := s.q.SetCartItemQuantity(ctx, db.SetCartItemQuantityParams{
		CartID: cartID, VariantID: variantID, Quantity: quantity,
	}); err != nil {
		return fmt.Errorf("set cart quantity: %w", err)
	}
	return nil
}

// Remove drops a line.
func (s *Store) Remove(ctx context.Context, cartID, variantID uuid.UUID) error {
	if err := s.q.RemoveCartItem(ctx, db.RemoveCartItemParams{CartID: cartID, VariantID: variantID}); err != nil {
		return fmt.Errorf("remove cart item: %w", err)
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
		}
		line.Unavailable = !r.IsActive || r.SellableQuantity <= 0
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

// SavedAddresses is the delivery addresses an account has. A guest gets none and
// the QUERY is what says so: an `if !owner.Valid` short-circuit here would leave
// TestAGuestHasNoAddressBook green with the owner predicate deleted from the SQL.
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
	return s.quoteFor(ctx, &v, subtotalCents, postalCode)
}

// quoteFor prices a version already read.
func (s *Store) quoteFor(ctx context.Context, v *db.ShippingVersionRow, subtotalCents int64, postalCode string) (Quote, error) {
	zone, err := s.q.ShippingZoneFor(ctx, db.ShippingZoneForParams{
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

// PlaceOrder turns a cart into an order in ONE transaction, recomputing the
// shipping fee from the version rather than taking it from the form.
// idempotencyKey makes a resubmitted checkout find its own order.
func (s *Store) PlaceOrder(
	ctx context.Context,
	cartID uuid.UUID,
	userID uuid.NullUUID,
	shippingVersionID uuid.UUID,
	addr *Address,
	inv *Invoice,
	coupon *Coupon,
	idempotencyKey string,
) (number string, err error) {
	// Nothing reads checkout_attempts on the pool first: claimCheckoutKey asks the
	// same question under the lock, and the copy that can be wrong is the early one.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin checkout: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	prior, taken, err := claimCheckoutKey(ctx, q, idempotencyKey)
	if err != nil {
		return "", err
	}
	if taken {
		return prior, nil
	}

	lines, err := q.CartLines(ctx, db.CartLinesParams{
		CartID: cartID, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return "", fmt.Errorf("read cart for checkout: %w", err)
	}
	if len(lines) == 0 {
		return "", ErrEmpty
	}

	ship, err := q.ShippingVersion(ctx, db.ShippingVersionParams{
		ID: shippingVersionID, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("read shipping version: %w", err)
	}

	// The destination is re-derived from the method just read and the address is
	// trimmed to it, the way the shipping fee is recomputed rather than trusted.
	to, ok := DestinationFor(ship.DestinationKind)
	if !ok {
		return "", fmt.Errorf("shipping method %s has an unknown destination %q",
			ship.Code, ship.DestinationKind)
	}
	addr.To = to
	addr.ForDestination()

	subtotal, err := subtotalOf(lines)
	if err != nil {
		return "", err
	}

	shippingFee, discount, err := s.priceOrder(ctx, &ship, subtotal, addr.PostalCode, coupon)
	if err != nil {
		return "", err
	}

	order, err := q.CreateOrder(ctx, db.CreateOrderParams{
		UserID:             userID,
		ShippingVersionID:  ship.ID,
		ShippingMethodCode: ship.Code,
		ShippingMethodName: ship.Name,
		ShippingCents:      shippingFee,
		DiscountCents:      discount,
		CustomerNote:       text(addr.Note),
		// Every later message about this order is sent in this language, never in
		// whatever the thing that triggered it happened to be reading.
		Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		return "", fmt.Errorf("create order: %w", err)
	}

	if err := writeOrderParts(ctx, q, &orderParts{
		orderID:     order.ID,
		lines:       lines,
		userID:      userID,
		subtotal:    subtotal,
		shippingFee: shippingFee,
		invoice:     inv,
		coupon:      coupon,
		orderNumber: order.OrderNumber,
		address:     addr,
		totalCents:  subtotal + shippingFee - discount,
	}); err != nil {
		return "", err
	}

	if err := finishOrder(ctx, q, order.ID, cartID, addr, idempotencyKey); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit checkout: %w", err)
	}
	return order.OrderNumber, nil
}

// orderParts is what hangs off an order header.
type orderParts struct {
	orderID     uuid.UUID
	lines       []db.CartLinesRow
	userID      uuid.NullUUID
	subtotal    int64
	shippingFee int64
	invoice     *Invoice
	coupon      *Coupon
	orderNumber string
	address     *Address
	totalCents  int64
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
	// p.totalCents, never subtotal+shipping: the gross drives owed negative.
	if err := spendCredit(ctx, q, p.orderID, p.userID, p.totalCents); err != nil {
		return err
	}

	if invErr := writeInvoicePreference(ctx, q, p.orderID, p.invoice); invErr != nil {
		return invErr
	}

	// coupon_redemption_matches_order refuses a row that disagrees with
	// orders.discount_cents, so the two cannot drift.
	if couponErr := redeemCoupon(ctx, q, p.coupon, p.orderID, p.userID); couponErr != nil {
		return couponErr
	}

	// The confirmation email's INTENT, in the order's own transaction.
	if mailErr := enqueueOrderPlaced(ctx, q, p.orderID, p.orderNumber, p.address, p.totalCents); mailErr != nil {
		return mailErr
	}

	return nil
}

// writeInvoicePreference records the invoice choice, when there is one.
func writeInvoicePreference(ctx context.Context, q *db.Queries, orderID uuid.UUID, inv *Invoice) error {
	if inv == nil {
		return nil
	}
	if err := q.CreateInvoicePreference(ctx, db.CreateInvoicePreferenceParams{
		OrderID:     orderID,
		InvoiceType: inv.Type,
		CarrierCode: inv.Carrier,
		TaxID:       inv.TaxID,
	}); err != nil {
		return fmt.Errorf("record invoice preference: %w", err)
	}
	return nil
}

// priceOrder is what an order costs to deliver and what a coupon takes off it. A
// free-delivery coupon zeroes the BASE RATE and not the outlying-island
// surcharge, the same line the free-over threshold is held to.
func (s *Store) priceOrder(
	ctx context.Context,
	ship *db.ShippingVersionRow,
	subtotal int64,
	postalCode string,
	coupon *Coupon,
) (shippingCents, discountCents int64, err error) {
	quote, err := s.quoteFor(ctx, ship, subtotal, postalCode)
	if err != nil {
		return 0, 0, err
	}
	shippingCents = quote.Total()

	// Re-priced against what the cart holds NOW: a discount carried on the form
	// would be a discount the customer chose.
	if coupon != nil {
		coupon.Price(subtotal, shippingCents)
		discountCents = coupon.DiscountCents
		if coupon.FreeShipping {
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
	idempotencyKey string,
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
		PickupBrand:     addr.PickupBrand,
		PickupStoreCode: addr.PickupStoreCode,
		PickupStoreName: addr.PickupStoreName,
	}); err != nil {
		return fmt.Errorf("create order private data: %w", err)
	}

	if err := q.RecordCheckoutAttempt(ctx, db.RecordCheckoutAttemptParams{
		IdempotencyKey: idempotencyKey,
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
	ctx context.Context, q *db.Queries, key string,
) (prior string, placed bool, err error) {
	if _, lockErr := q.LockCheckoutKey(ctx, key); lockErr != nil {
		return "", false, fmt.Errorf("lock checkout key: %w", lockErr)
	}
	// Asked INSIDE the lock: the request that just waited is exactly the one any
	// earlier read would have missed.
	number, ok := priorOrderTx(ctx, q, key)
	return number, ok, nil
}

// priorOrderTx is the same question asked through a given Queries.
func priorOrderTx(ctx context.Context, q *db.Queries, key string) (string, bool) {
	id, err := q.CheckoutAttempt(ctx, key)
	if err != nil || !id.Valid {
		return "", false
	}
	number, err := q.OrderNumberByID(ctx, id.UUID)
	if err != nil {
		return "", false
	}
	return number, true
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
		Status:       o.FulfillmentStatus,
		Email:        o.Email,
		ShippingName: o.ShippingMethodName,
		Committed:    o.Committed,
		OwedCents:    o.OwedCents,
		DeliveryTo: pages.Delivery{
			PostalCode: o.PostalCode, City: o.City, District: o.District, Street: o.Street,
			PickupBrand: o.PickupBrand, PickupStoreCode: o.PickupStoreCode,
			PickupStoreName: o.PickupStoreName,
		}.Line(),
		SubtotalCents: o.SubtotalCents,
		ShippingCents: o.ShippingCents,
		DiscountCents: o.DiscountCents, DiscountReason: o.DiscountReason,
		CreditCents: o.CreditCents,
		TaxCents:    o.TaxCents,
		PlacedAt:    o.PlacedAt.Format("2006-01-02 15:04"),
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
			At: e.OccurredAt.Format("2006-01-02 15:04"),
		})
	}

	shipments, err := s.q.OrderTracking(ctx, o.ID)
	if err != nil {
		return pages.OrderView{}, fmt.Errorf("read order tracking: %w", err)
	}
	for _, sh := range shipments {
		view.Shipments = append(view.Shipments, pages.OrderShipment{
			Carrier: sh.Carrier, Tracking: sh.TrackingNumber,
			ShippedAt:   sh.ShippedAt.Format("2006-01-02 15:04"),
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
	expires := time.Now().Add(HoldTTL)
	for i := range lines {
		l := &lines[i]
		if _, err := q.HoldForOrder(ctx, db.HoldForOrderParams{
			OrderID:        orderID,
			VariantID:      l.VariantID,
			Quantity:       l.Quantity,
			ExpiresAt:      expires,
			IdempotencyKey: "hold:" + orderID.String() + ":" + l.VariantID.String(),
		}); err != nil {
			return fmt.Errorf("%w: %w", ErrUnavailable, err)
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
		if !l.IsActive || l.Quantity > l.SellableQuantity {
			return 0, ErrUnavailable
		}
		subtotal += l.PriceCents * int64(l.Quantity)
	}
	return subtotal, nil
}

// writeOrderLines copies each cart line onto the order. The price is COPIED
// rather than referenced: a later catalogue change must not rewrite it.
func writeOrderLines(ctx context.Context, q *db.Queries, orderID uuid.UUID, lines []db.CartLinesRow) error {
	for i := range lines {
		l := &lines[i]
		if err := q.CreateOrderLine(ctx, db.CreateOrderLineParams{
			OrderID:        orderID,
			VariantID:      uuid.NullUUID{UUID: l.VariantID, Valid: true},
			SKU:            l.SKU,
			ProductName:    l.Name,
			VariantLabel:   text(optionLabel(l.OptionNames, l.OptionValues)),
			UnitPriceCents: l.PriceCents,
			Quantity:       l.Quantity,
			Position:       int32(i),
		}); err != nil {
			return fmt.Errorf("create order line: %w", err)
		}
	}
	return nil
}

// nullableTime formats a timestamp that may be absent.
func nullableTime(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.Format("2006-01-02 15:04")
}

// spendCredit applies whatever store credit the customer has, up to what the
// order owes. The cap is the NET total: at subtotal + shipping a coupon and a
// balance spend more than the order is worth, and nothing underneath refuses it.
func spendCredit(
	ctx context.Context,
	q *db.Queries,
	orderID uuid.UUID,
	userID uuid.NullUUID,
	owedCents int64,
) error {
	if !userID.Valid {
		return nil
	}
	balance, err := q.AvailableCredit(ctx, userID)
	if err != nil {
		return fmt.Errorf("read store credit: %w", err)
	}
	if balance <= 0 {
		return nil
	}
	spend := min(balance, owedCents)
	if spend <= 0 {
		return nil
	}
	if _, err := q.SpendCredit(ctx, db.SpendCreditParams{
		UserID:      userID.UUID,
		AmountCents: -spend,
		// i18n-exempt: a persisted label whose only reader is /admin/credit.
		Reason:         "訂單折抵",
		OrderID:        orderID,
		IdempotencyKey: "order:" + orderID.String(),
	}); err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
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
