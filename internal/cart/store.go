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

// Store reads and writes carts and places orders.
//
// It holds the pool rather than a DBTX because placing an order spans several
// statements that must succeed or fail together, and that needs a transaction
// the store can open. Everything else runs on the pool directly.
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

// Add puts a variant in a cart.
//
// The variant is checked BEFORE the write so an inactive variant, a draft
// product or a quantity past what can be sold is a message rather than a
// foreign-key error or a hold the database will refuse later.
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

// View reads a cart for display.
//
// Prices and availability are read fresh every time. A cart line is not a
// promise: a variant can sell out or be repriced while it sits there, and a
// visitor must never be quoted a total the checkout would then refuse.
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
		// A line the catalogue can no longer honour: inactive, or short of
		// stock. The page says so per line and blocks checkout, rather than
		// letting the order fail at the hold.
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

// optionLabel renders a variant's options as "星霧藍 · 512GB".
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

// SavedAddresses is the delivery addresses an account has, for the checkout to
// offer instead of an empty form.
//
// A guest gets none, and the QUERY is what says so: a null owner is the zero
// uuid, which owns nothing. An `if !owner.Valid { return nil }` short-circuit
// here saves the round trip and costs the guarantee — it bypasses the one path
// TestAGuestHasNoAddressBook exists to hold, so the owner predicate could be
// deleted from the SQL with that test still green. A guard that makes a test
// unprovable costs more than the query it saves.
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
//
// Asked at quote time by the checkout page and again at write time by
// PlaceOrder. Two calls of one function, rather than the fee being computed
// twice — the second computation is the one that would drift, and it is the one
// that decides what the customer is charged.
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

// ShippingChoices is the shipping methods a checkout may offer FOR THIS CART.
//
// The cart is a parameter because the answer depends on it: a method whose
// carrier refuses a 27-inch monitor is not a choice for a basket with one in it.
// Offering every active method to every cart lets a customer pick 超商取貨 for
// something 7-ELEVEN will not take and pay for it, and the shop finds out at the
// counter with the parcel already packed.
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
			Carrier:         r.Carrier.String,
			FeeCents:        fee,
			Free:            fee == 0,
		})
	}
	return out, nil
}

// PlaceOrder turns a cart into an order, in one transaction.
//
// Everything the order needs — the header, its lines and its delivery details —
// is written together or not at all: the schema's deferred orders_have_lines
// and orders_have_delivery constraints mean a partial order is not a legal row,
// and a cart emptied against an order that failed to appear is the worst
// possible outcome.
//
// The shipping fee is recomputed from the version, never taken from the form. A
// hand-edited version id must not let an order be placed at a fee that was
// never offered.
//
// idempotencyKey makes a resubmitted checkout find its own order instead of
// placing a second one. The form carries it, so the back button and a
// double-click are the same request twice rather than two orders.
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
	// Nothing reads checkout_attempts on the pool before this transaction opens,
	// deliberately. A pre-check there would answer the cheap resubmit cases — a
	// reload, a back button, a retried request — without a transaction, and two
	// requests from one double-click both run it before either has one: both miss,
	// and both go on to place an order.
	//
	// claimCheckoutKey asks the same question under the lock, so it answers the
	// resubmit case as well. A read above it would be a second copy of one
	// decision, and the copy that can be wrong is the one made too early.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin checkout: %w", err)
	}
	// Rollback after a successful Commit is a no-op, so this is the safety net for
	// every early return below rather than an error worth reporting.
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

	// The destination is re-derived from the method goen just read, and the
	// address is trimmed to it. The handler set the same value from the same
	// source a moment ago; this is the authority, the way the shipping fee is
	// recomputed here rather than trusted from the form.
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
		// The language this order was placed in. Every message about it — the
		// confirmation now, the receipt from a webhook, the dispatch notice from
		// a back-office click — is sent in this and not in whatever language the
		// thing that triggered it happened to be reading.
		Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		return "", fmt.Errorf("create order: %w", err)
	}

	// Everything that hangs off the order header, in the same transaction.
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

// orderParts is what hangs off an order header. A struct rather than eight
// parameters, because the rule is five and the alternative is a signature
// nobody can read.
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

// writeOrderParts writes the lines, the stock hold, the first history entry,
// the store-credit spend and the invoice choice.
//
// All of them in the caller's transaction, and all of them for the same reason:
// each is part of what the order IS, and an order that exists without any one
// of them is a state nothing downstream expects. Extracted from PlaceOrder to
// keep that function under the complexity limit — the grouping is presentation,
// the transaction is the guarantee.
func writeOrderParts(ctx context.Context, q *db.Queries, p *orderParts) error {
	if err := writeOrderLines(ctx, q, p.orderID, p.lines); err != nil {
		return err
	}

	// Reserve the stock, in the SAME transaction as the order.
	//
	// Without this two customers can order the last unit and both succeed: each
	// reads availability, each writes an order, and nothing between them ever
	// decrements anything. hold_inventory posts a movement, so the shelf and the
	// ledger move together and record_inventory_movement's floor check is what
	// refuses the second one.
	if err := holdOrderStock(ctx, q, p.orderID, p.lines); err != nil {
		return err
	}

	// The first history entry, in the same transaction. An order whose history
	// does not start with 'placed' is one whose timeline has a hole at the
	// beginning, and order_events is append-only, so it could never be repaired.
	if err := q.RecordPlacedEvent(ctx, p.orderID); err != nil {
		return fmt.Errorf("record placed event: %w", err)
	}

	// Store credit, in the same transaction. It is spent against the order the
	// moment the order exists, because orders_funded_to_leave_pending reads the
	// ledger to decide whether the order is funded — a debit posted afterwards
	// would leave a window in which a fully-credited order looks unpaid.
	//
	// A zero-owed order is the case this makes reachable: fully covered by
	// credit, it is committed with NO payment row at all, which is exactly why
	// order_is_committed exists rather than "EXISTS a succeeded payment".
	// p.totalCents, never subtotal+shipping: the discount is part of what the
	// order owes, and spending credit against the gross drives owed negative.
	if err := spendCredit(ctx, q, p.orderID, p.userID, p.totalCents); err != nil {
		return err
	}

	// The invoice choice, with the order. It is part of what was agreed, and
	// erase_user clears it alongside the delivery details for the same reason.
	if invErr := writeInvoicePreference(ctx, q, p.orderID, p.invoice); invErr != nil {
		return invErr
	}

	// The redemption, in the order's own transaction. It carries the discount
	// the order was actually given, and coupon_redemption_matches_order refuses
	// a row that disagrees with orders.discount_cents — so the two cannot drift.
	if couponErr := redeemCoupon(ctx, q, p.coupon, p.orderID, p.userID); couponErr != nil {
		return couponErr
	}

	// The confirmation email's INTENT, in the order's own transaction.
	//
	// Sending from here would be wrong in both orderings: before the commit
	// tells somebody about an order that may roll back, after it loses the
	// message if the process dies in between. The outbox removes the choice —
	// the order and the intent to email commit together, and delivery becomes
	// the separate retryable problem it actually is.
	if mailErr := enqueueOrderPlaced(ctx, q, p.orderID, p.orderNumber, p.address, p.totalCents); mailErr != nil {
		return mailErr
	}

	return nil
}

// writeInvoicePreference records the invoice choice, when there is one.
//
// Split out of PlaceOrder to keep it under the complexity limit. A nil
// preference is a caller that collected none — the tests, and any future path
// that places an order without a form behind it.
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

// priceOrder is what an order costs to deliver and what a coupon takes off it.
//
// Extracted from PlaceOrder to keep that function inside the complexity limit,
// and the two belong together anyway: a free-shipping coupon is priced against
// the fee, and the fee is priced against the address.
//
// A 免運 coupon zeroes the BASE RATE and not the 離島 surcharge, for the same
// reason the free-over threshold does not: both are the shop's own offer on its
// own rate, and the carrier still charges to cross the water. A coupon that ate
// the surcharge would be the shop paying NT$200 a parcel to honour a NT$0
// discount, which is not what anybody issuing it intends.
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

	// The coupon is re-priced against what the cart actually holds NOW, never
	// against the figure the checkout page showed. A cart can change between
	// rendering the form and submitting it, and a discount carried on the form
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

// finishOrder writes what remains once the order, its lines, its stock hold and
// its first history entry are down: the delivery details, the idempotency
// record, and the emptied cart.
//
// Extracted from PlaceOrder to keep that function readable, not because these
// three belong together — every one of them is still in the same transaction as
// the order, which is what matters. Emptying the cart afterwards would leave a
// window where the order exists and the cart still looks full, and a failure in
// that window loses the order or duplicates it.
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

// claimCheckoutKey takes this checkout's idempotency key for the length of the
// transaction, and reports the order a concurrent request already placed on it.
//
// It answers the case a read on the POOL cannot: two requests IN FLIGHT AT ONCE.
// A double-click sends them milliseconds apart, so both read before either has a
// transaction and both miss. Nothing further down catches them either — the
// attempt row that would collide is written second-to-last and its
// ON CONFLICT DO NOTHING is :exec, so the row count is discarded and the loser
// commits its own order regardless, while the hold key and the outbox dedupe key
// are both derived from the new order's own identity and so collide with
// nothing. With stock for two the customer gets two orders, two stock holds, two
// confirmation emails and two store-credit debits — the credit spend keys on the
// ORDER id, and there are two of those.
//
// An advisory lock rather than moving the INSERT up, and the difference is what
// happens to the LOSER. An early row holds the key while its order_id is still
// NULL, so the second request finds a claim it cannot answer with a number. The
// lock makes it WAIT instead, and by the time it looks the winner has committed
// and there IS a number to hand back — same order, one placement, no error shown
// to anybody. It is taken on the key's HASH because there is no row to lock yet,
// which is the whole problem, and it releases at commit or rollback with nothing
// to remember.
func claimCheckoutKey(
	ctx context.Context, q *db.Queries, key string,
) (prior string, placed bool, err error) {
	if _, lockErr := q.LockCheckoutKey(ctx, key); lockErr != nil {
		return "", false, fmt.Errorf("lock checkout key: %w", lockErr)
	}
	// Asked INSIDE the lock, because the request that just waited is exactly the
	// one any earlier read would have missed. This is the reader of the row the
	// winner wrote; without it the loser goes on to place a second order.
	number, ok := priorOrderTx(ctx, q, key)
	return number, ok, nil
}

// priorOrderTx is the same question asked through a given Queries.
//
// It exists so the question can be asked a SECOND time inside the checkout's own
// transaction, after the advisory lock. The first ask runs on the pool and is
// the fast path for a resubmit; the second is the only thing that can see what a
// concurrent request committed while this one was waiting for the lock.
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
		TaxCents: o.TaxCents,
		PlacedAt: o.PlacedAt.Format("2006-01-02 15:04"),
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

// holdOrderStock reserves every line's stock against the order.
//
// The idempotency key is derived from the order and the variant, so a retried
// checkout under the same order cannot double-hold — record_inventory_movement
// keys on it. A refusal here is the stock floor: somebody else took the last
// one between the cart page and this write, which is exactly the race the hold
// exists to lose safely.
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
			return fmt.Errorf("%w: %s", ErrUnavailable, err.Error())
		}
	}
	return nil
}

// subtotalOf prices a cart, refusing anything that can no longer be supplied.
//
// This re-checks what the cart page already checked, INSIDE the transaction:
// between that render and this write the last one could have sold, and an order
// carrying a line the inventory hold will refuse is worse than a refusal here.
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

// writeOrderLines copies each cart line onto the order.
//
// The price is COPIED rather than referenced: an order is a record of what was
// agreed, and a later catalogue change must not rewrite it.
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
// order owes.
//
// A guest has none — there is no account to hold it against — so this is a
// no-op for guest checkout.
//
// The cap is the order's NET total, and the word "net" is the whole point.
// Capping at `subtotal + shipping` with the DISCOUNT left out lets a coupon and
// a credit balance on one order spend more credit than the order is worth: a
// NT$1,000 order with a NT$300 coupon takes NT$1,000 of credit and leaves
// order_amount_owed at MINUS 300.
//
// Nothing underneath refuses that. store_credit_never_negative guards the
// ACCOUNT balance, not the order, and no rule caps a spend at what its order
// owes — so the customer loses the NT$300 and the order is then unpayable
// forever: FullyFunded() sees a non-positive figure and keeps it away from
// Stripe, while orders_funded_to_leave_pending asks `owed <> 0`, which minus
// 300 satisfies. Unpaid, unshippable, uncancellable-by-payment.
//
// The figure comes from orderParts.totalCents, three lines above the call and
// the same one the confirmation email quotes. Two expressions for one fact is
// how the one that moves money comes to be the wrong one — mistake #13 in this
// repository's own list, a second time.
//
// store_credit_never_negative refuses an overdraft underneath, and the
// idempotency key is the order, so a retried checkout debits once.
func spendCredit(
	ctx context.Context,
	q *db.Queries,
	orderID uuid.UUID,
	userID uuid.NullUUID,
	owedCents int64,
) error {
	// A guest has no account, so AvailableCredit would return 0 anyway. This is
	// here to say so at the top rather than to enforce it — the query is what
	// makes it true, and a test removing this line stays green.
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
		// i18n-exempt: a persisted label whose only reader is /admin/credit, and
		// the back office is Chinese by decision. If a customer-facing credit
		// history is ever built, this has to become a code the page maps —
		// translating it at write time would put whichever language the writer
		// happened to have into a permanent ledger row.
		Reason:         "訂單折抵",
		OrderID:        orderID,
		IdempotencyKey: "order:" + orderID.String(),
	}); err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, err.Error())
	}
	return nil
}

// AvailableCredit is what a signed-in customer has to spend, for the checkout
// page. Guests have none.
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

// ItemCount is how many UNITS a cart holds, for the header badge. Units and not
// lines: a badge reading 1 over a cart holding three of something is wrong in
// the way a visitor notices at checkout.
func (s *Store) ItemCount(ctx context.Context, cartID uuid.UUID) (int, error) {
	n, err := s.q.CartItemCount(ctx, cartID)
	if err != nil {
		return 0, fmt.Errorf("count cart items: %w", err)
	}
	return int(n), nil
}
