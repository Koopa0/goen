package cart

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// Handler serves the cart and checkout.
type Handler struct {
	store *Store
	log   *slog.Logger
	// secure says whether cookies may claim the __Host- prefix. False in
	// development over plain HTTP, where a Secure cookie would never be sent
	// back and the cart would silently never persist.
	secure bool
	// findLimit bounds the order lookup. Half of that credential — the order
	// number — is guessable, so an unbounded endpoint is an oracle for the other
	// half, and the other half is somebody's delivery address.
	findLimit *ratelimit.Limiter
	// sessions closes a cancelled order's checkout at the payment provider. Nil
	// on a deployment with no Stripe key, where there is no session to close.
	sessions SessionCloser
}

// SessionCloser closes a checkout the customer may still have open at the
// payment provider.
//
// Defined HERE and satisfied by *payment.Gateway, because the consumer names
// what it needs: internal/cart already lends its store to internal/payment
// through an interface that package defines, and this is the same seam pointing
// the other way. One method, because cancelling an order asks the provider
// exactly one thing.
type SessionCloser interface {
	ExpireSession(ctx context.Context, sessionID string) error
}

// NewHandler returns a Handler writing through store.
//
// sessions may be nil, and that is the same shape [payment.Gateway] uses for a
// missing Stripe key: with no provider configured no session was ever opened, so
// there is nothing for a cancellation to close. A nil here is "the feature is
// off", never "somebody forgot" — an order cannot have a live session without
// the key that created it.
func NewHandler(store *Store, log *slog.Logger, secure bool, findLimit *ratelimit.Limiter,
	sessions SessionCloser,
) *Handler {
	if store == nil || log == nil || findLimit == nil {
		panic("cart: NewHandler requires a store, a logger and a lookup limiter")
	}
	return &Handler{
		store: store, log: log, secure: secure, findLimit: findLimit, sessions: sessions,
	}
}

// closeSessions expires the checkouts a cancelled order left open at Stripe.
//
// Post-commit and best effort, and both halves are deliberate. The order is
// already cancelled, its stock is back and its credit is returned; none of that
// is worth rolling back because a third party is slow. What a failure costs is
// bounded and already handled: the session dies with the stock hold within
// [HoldTTL] anyway, and money that beats it there arrives as
// payment.ErrOrderCancelled, which is recorded and refused rather than captured.
//
// So this turns "a human refunds it" into "it does not happen", and when it
// cannot, the log line names the order and the session so the human still can.
// A cancellation that reported failure because Stripe was unreachable would be
// worse: the customer would be told their order was not called off when it was.
func (h *Handler) closeSessions(ctx context.Context, number string, sessions []string) {
	if h.sessions == nil {
		return
	}
	for _, id := range sessions {
		if err := h.sessions.ExpireSession(ctx, id); err != nil {
			// Warn, not Error: Stripe REFUSES to expire a session that is not
			// open, and a customer who finished paying in the other tab a second
			// before cancelling produces exactly that refusal. It is the guard
			// working — goen must not decide for itself that a session is empty —
			// so it is worth seeing and is not an alarm.
			h.log.WarnContext(ctx, "expire checkout session of a cancelled order",
				"order_number", number, "session_id", id, "error", err)
		}
	}
}

// Page serves GET /cart.
func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	cartID, ok := h.existingCart(r)
	if !ok {
		web.Render(w, r, h.log, http.StatusOK, pages.Cart(pages.CartMeta(r.Context()), pages.CartView{}))
		return
	}
	view, err := h.store.View(r.Context(), cartID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read cart", "error", err)
		h.serverError(w, r)
		return
	}
	view.ReorderAdded, view.ReorderSkipped = reorderOutcome(r)
	web.Render(w, r, h.log, http.StatusOK, pages.Cart(pages.CartMeta(r.Context()), view))
}

// reorderOutcome reads what a 再買一次 redirect is reporting.
//
// Counts and nothing else. Values that will not parse are zero, which renders
// as no notice at all — a hand-edited URL must not be able to tell somebody
// their cart holds things it does not.
func reorderOutcome(r *http.Request) (added, skipped int) {
	q := r.URL.Query()
	// A value that will not parse is zero, which renders as no notice at all.
	// The errors are discarded deliberately rather than reported: a hand-edited
	// URL is not a fault to log, it is a visitor typing.
	added, _ = strconv.Atoi(q.Get("added"))     //nolint:errcheck // unparseable is zero, which renders nothing
	skipped, _ = strconv.Atoi(q.Get("skipped")) //nolint:errcheck // same
	return max(added, 0), max(skipped, 0)
}

// AddItem serves POST /cart/items.
//
// It answers 303 so a reload cannot resubmit — the write-face rule — and sends
// the visitor back to the product they were looking at, which is where they
// want to be to keep shopping.
func (h *Handler) AddItem(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}

	variantID, err := uuid.Parse(r.PostFormValue("variant"))
	if err != nil {
		h.backToProduct(w, r, "unknown")
		return
	}
	quantity := ParseQuantity(r.PostFormValue("quantity"))

	cartID, err := h.cartForWrite(w, r)
	if err != nil {
		h.log.ErrorContext(r.Context(), "open cart", "error", err)
		h.serverError(w, r)
		return
	}

	switch err := h.store.Add(r.Context(), cartID, variantID, quantity); {
	case err == nil:
		h.backToProduct(w, r, "added")
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrNotFound):
		h.backToProduct(w, r, "unavailable")
	default:
		h.log.ErrorContext(r.Context(), "add to cart", "error", err)
		h.serverError(w, r)
	}
}

// UpdateItem serves POST /cart/items/update, changing or removing one line.
func (h *Handler) UpdateItem(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	cartID, ok := h.existingCart(r)
	if !ok {
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
		return
	}
	variantID, err := uuid.Parse(r.PostFormValue("variant"))
	if err != nil {
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
		return
	}

	if r.PostFormValue("remove") != "" {
		if err := h.store.Remove(r.Context(), cartID, variantID); err != nil {
			h.log.ErrorContext(r.Context(), "remove cart item", "error", err)
			h.serverError(w, r)
			return
		}
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
		return
	}

	quantity, ok := ParseQuantityAllowingZero(r.PostFormValue("quantity"))
	if !ok {
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
		return
	}
	if err := h.store.SetQuantity(r.Context(), cartID, variantID, quantity); err != nil {
		h.log.ErrorContext(r.Context(), "update cart item", "error", err)
		h.serverError(w, r)
		return
	}
	http.Redirect(w, r, "/cart", http.StatusSeeOther)
}

// Checkout serves GET /checkout.
func (h *Handler) Checkout(w http.ResponseWriter, r *http.Request) {
	cartID, ok := h.existingCart(r)
	if !ok {
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
		return
	}
	view, err := h.checkoutView(r.Context(), cartID, ownerOf(r))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read checkout", "error", err)
		h.serverError(w, r)
		return
	}
	// A cart that cannot be supplied must not reach this form: the order would
	// fail at the inventory hold, after an address was typed.
	if !view.Cart.CanCheckout() {
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
		return
	}
	// The chosen method lives in the URL and nowhere else — no cookie, no cart
	// column, nothing to expire or disagree with the page. It is therefore a
	// LINK rather than a control, which is the same answer the product page's
	// variant picker gives: the choice works with scripting off because there
	// is nothing to script.
	//
	// Nothing personal is ever in that query string; the destination fields are
	// typed after the method is chosen, and they travel by POST.
	if ship := r.URL.Query().Get("ship"); ship != "" {
		for i := range view.Shipping {
			if view.Shipping[i].VersionID == ship {
				view.Chosen = ship
			}
		}
	}
	view.Destination = string(destinationOf(view.Shipping, view.Chosen))

	// A signed-in customer starts from the address they already gave us. The
	// book has existed since the account pages shipped and this page never read
	// it, so a repeat customer retyped a postal code every order.
	var prefill Address
	fillFromBook(&view, &prefill, r.URL.Query().Get("address"))
	view.Address = pages.CheckoutAddress{
		Email: emailOf(r), Name: prefill.Name, Phone: prefill.Phone,
		PostalCode: prefill.PostalCode, City: prefill.City,
		District: prefill.District, Street: prefill.Street,
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Checkout(pages.CheckoutMeta(r.Context()), &view))
}

// emailOf is the signed-in customer's address, or "". A guest types one; a
// customer should not have to give the shop an address it already mails them at.
func emailOf(r *http.Request) string {
	if u, ok := account.FromContext(r.Context()); ok {
		return u.Email
	}
	return ""
}

// rememberOrder gives this browser a token for an order it is allowed to see.
//
// A failure is logged and NOT fatal. The order exists and only the browser's proof
// of it failed: the customer still reaches it from the confirmation email or by
// signing in, and refusing a completed checkout over a cookie would cost more than
// the inconvenience.
func (h *Handler) rememberOrder(w http.ResponseWriter, r *http.Request, number string) {
	if err := h.store.RememberOrder(r.Context(), w, r, number, h.secure); err != nil {
		h.log.ErrorContext(r.Context(), "remember order", "error", err, "order", number)
	}
}

// PlaceOrder serves POST /checkout.
//
// A rejected form re-renders at 422 with the submitted values intact and
// aria-invalid on each control the server refused, which is the write-face
// rule's own wording. A successful order answers 303 to its confirmation.
func (h *Handler) PlaceOrder(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	cartID, ok := h.existingCart(r)
	if !ok {
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
		return
	}

	addr := &Address{
		Email:      r.PostFormValue("email"),
		Name:       r.PostFormValue("name"),
		Phone:      r.PostFormValue("phone"),
		PostalCode: r.PostFormValue("postal_code"),
		City:       r.PostFormValue("city"),
		District:   r.PostFormValue("district"),
		Street:     r.PostFormValue("street"),

		PickupBrand:     r.PostFormValue("pickup_brand"),
		PickupStoreCode: r.PostFormValue("pickup_store_code"),
		PickupStoreName: r.PostFormValue("pickup_store_name"),

		Note: r.PostFormValue("note"),
	}
	addr.Trim()

	owner := ownerOf(r)
	view, err := h.checkoutView(r.Context(), cartID, owner)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read checkout", "error", err)
		h.serverError(w, r)
		return
	}
	// What was SUBMITTED, echoed back — never re-filled from the book. A
	// rejected form that quietly reverted an edited address to the saved one
	// would overwrite the correction the customer just made.
	view.ChosenAddress = r.PostFormValue("address")
	view.Address = pages.CheckoutAddress{
		Email: addr.Email, Name: addr.Name, Phone: addr.Phone,
		PostalCode: addr.PostalCode, City: addr.City,
		District: addr.District, Street: addr.Street,
		PickupBrand: addr.PickupBrand, PickupStoreCode: addr.PickupStoreCode,
		PickupStoreName: addr.PickupStoreName, Note: addr.Note,
	}
	view.Chosen = r.PostFormValue("shipping")
	// WHERE this order goes is decided by the method the server offered, never
	// by the form. A hidden field naming the destination would let a hand-edited
	// submission attach a street address to a pickup order — and the page would
	// then ask for, and validate, the wrong half of the address.
	view.Destination = string(destinationOf(view.Shipping, view.Chosen))
	addr.To = Destination(view.Destination)
	view.Idempoten = r.PostFormValue("idempotency")

	inv := &Invoice{
		Type:    r.PostFormValue("invoice_type"),
		Carrier: r.PostFormValue("invoice_carrier"),
		TaxID:   r.PostFormValue("invoice_tax_id"),
	}
	view.Invoice = pages.CheckoutInvoice{
		Type: inv.Type, Carrier: inv.Carrier, TaxID: inv.TaxID,
	}

	// The coupon is looked up before the order is priced, so a bad code is a
	// message on the form rather than an order placed at the wrong total.
	coupon, couponErr := h.resolveCoupon(r, &view)

	shippingID, shipErr := uuid.Parse(view.Chosen)
	errs := checkoutErrors(r.Context(), addr, shipErr, inv)
	if shipErr == nil && errs == nil {
		// The address is valid, so the fee can be priced for real. Anything
		// earlier and the postal code is not trustworthy yet; anything later
		// and the customer has already committed to a figure.
		if quoteErr := h.requote(r, &view, shippingID, addr); quoteErr != "" {
			errs = map[string]string{"shipping": quoteErr}
		}
	}
	if couponErr != "" {
		// checkoutErrors returns a nil map when nothing was wrong, and writing
		// to a nil map panics — which turned a bad coupon code into a 500.
		if errs == nil {
			errs = map[string]string{}
		}
		errs["coupon"] = couponErr
	}
	if len(errs) > 0 {
		view.Errors = errs
		web.Render(w, r, h.log, http.StatusUnprocessableEntity,
			pages.Checkout(pages.CheckoutMeta(r.Context()), &view))
		return
	}

	key := view.Idempoten
	if key == "" {
		// A form that lost its key still gets one, so a retry at least cannot
		// duplicate itself within this request.
		key = newIdempotencyKey()
	}

	number, err := h.store.PlaceOrder(r.Context(), cartID, owner, shippingID, addr, inv, coupon, key)
	switch {
	case err == nil:
		// The browser remembers what it placed. Order numbers come off a per-day
		// counter and are therefore guessable, so this is what keeps the
		// confirmation page — which carries an email and an address — from being
		// enumerable.
		h.rememberOrder(w, r, number)
		// Straight to payment rather than to the confirmation. The order exists
		// and its stock is held, but neither is worth anything until it is
		// funded, and a confirmation page shown before payment reads as "done"
		// to a customer who then closes the tab.
		//
		// number comes from next_order_number() and matches orders_number_format.
		// Nothing the request supplied reaches it.
		http.Redirect(w, r, "/orders/"+number+"/pay", http.StatusSeeOther) //nolint:gosec // G710: server-generated order number
	case errors.Is(err, ErrEmpty):
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
	case errors.Is(err, ErrUnavailable):
		// Something sold out between the cart page and this write. Back to the
		// cart, which says which line and why.
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		view.Errors = map[string]string{"shipping": i18n.T(r.Context(), i18n.KeyChooseShipping)}
		web.Render(w, r, h.log, http.StatusUnprocessableEntity,
			pages.Checkout(pages.CheckoutMeta(r.Context()), &view))
	default:
		h.log.ErrorContext(r.Context(), "place order", "error", err)
		h.serverError(w, r)
	}
}

// resolveCoupon looks up the typed code and prices it, or reports why not.
//
// An empty field is not an error: most checkouts have no coupon. The view is
// updated either way so the page shows what was applied — a smaller total with
// no explanation is worse than no discount.
func (h *Handler) resolveCoupon(r *http.Request, view *pages.CheckoutView) (coupon *Coupon, formError string) {
	raw := r.PostFormValue("coupon")
	view.CouponCode = NormaliseCode(raw)
	if view.CouponCode == "" {
		return nil, ""
	}

	subtotal := view.Cart.SubtotalCents
	shipping := view.ShippingFeeCents()
	c, err := h.store.FindCoupon(r.Context(), view.CouponCode, subtotal, shipping)
	switch {
	case err == nil:
		view.CouponApplied = c.Description
		view.CouponDiscountCents = c.DiscountCents
		view.CouponFreeShipping = c.FreeShipping
		return c, ""
	case errors.Is(err, ErrCouponExpired):
		return nil, i18n.T(r.Context(), i18n.KeyCouponExpired)
	case errors.Is(err, ErrCouponMinimum):
		return nil, i18n.T(r.Context(), i18n.KeyCouponBelowMinimum)
	case errors.Is(err, ErrNoSuchCoupon):
		return nil, i18n.T(r.Context(), i18n.KeyCouponUnknown)
	default:
		h.log.ErrorContext(r.Context(), "resolve coupon", "error", err)
		return nil, i18n.T(r.Context(), i18n.KeyCouponUnavailable)
	}
}

// checkoutErrors collects everything wrong with a submission, keeping the first
// message per field so a control shows one reason rather than a pile.
func checkoutErrors(ctx context.Context, addr *Address, shipErr error, inv *Invoice) map[string]string {
	fieldErrs := addr.Validate()
	if shipErr != nil {
		fieldErrs = append(fieldErrs, FieldError{Field: "shipping", MessageKey: i18n.KeyChooseShipping})
	}
	fieldErrs = append(fieldErrs, inv.Validate()...)
	if len(fieldErrs) == 0 {
		return nil
	}
	out := make(map[string]string, len(fieldErrs))
	for _, e := range fieldErrs {
		if _, seen := out[e.Field]; !seen {
			out[e.Field] = i18n.T(ctx, e.MessageKey)
		}
	}
	return out
}

// requote prices the chosen method against the address that was actually typed,
// and refuses to place the order when the figure has moved.
//
// The fee shown when the method was chosen is a MAINLAND fee: the customer had
// not typed a postal code yet, because the method chooser is a link and the
// address is a form below it. An order to 金門 costs more, and charging that
// silently on submit is the customer paying a number they were never shown.
//
// So the first submission that finds a different figure is refused, the page
// re-renders with the surcharge named, and the second submission goes through.
// One extra round trip, no scripting, and nobody is charged a surprise.
func (h *Handler) requote(r *http.Request, view *pages.CheckoutView, versionID uuid.UUID, addr *Address) string {
	quote, err := h.store.QuoteShipping(r.Context(), versionID, view.Cart.SubtotalCents, addr.PostalCode)
	if err != nil {
		h.log.ErrorContext(r.Context(), "quote shipping", "error", err)
		return i18n.T(r.Context(), i18n.KeyShippingUnpriceable)
	}
	view.QuotedShippingCents = quote.Total()
	view.SurchargeCents = quote.Surcharge
	view.ZoneName = quote.ZoneName
	if quote.Surcharge == 0 {
		return ""
	}

	// The form carries what the customer was last shown. Equal means they have
	// seen this number; different means they have not, and they see it now.
	shown, err := strconv.ParseInt(r.PostFormValue("quoted_shipping"), 10, 64)
	if err == nil && shown == quote.Total() {
		return ""
	}
	return fmt.Sprintf(i18n.T(r.Context(), i18n.KeyShippingRepriced),
		quote.ZoneName, pages.TWD(quote.Total()))
}

// ownerOf is the signed-in customer, or a null id for a guest.
//
// A guest order has no owner, which is what guest checkout means; a signed-in
// one belongs to the account, so it shows in their history and is reachable
// without the placed-order cookie.
func ownerOf(r *http.Request) uuid.NullUUID {
	u, ok := account.FromContext(r.Context())
	if !ok {
		return uuid.NullUUID{}
	}
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: id, Valid: true}
}

// fillFromBook fills the form from a saved address, and says which one.
//
// The choice travels as an id in the query string — the customer's own address,
// reachable only through their session, and no personal data in the URL itself.
// An id that names nothing (a guest's guess, or an address since deleted) falls
// through to the default one, because the alternative is an error page in the
// middle of a checkout over a field nobody typed.
func fillFromBook(view *pages.CheckoutView, addr *Address, wanted string) {
	if len(view.SavedAddresses) == 0 {
		return
	}
	chosen := &view.SavedAddresses[0] // is_default first, then oldest
	for i := range view.SavedAddresses {
		if view.SavedAddresses[i].ID == wanted {
			chosen = &view.SavedAddresses[i]
		}
	}
	view.ChosenAddress = chosen.ID
	addr.Name, addr.Phone = chosen.Name, chosen.Phone
	addr.PostalCode, addr.City = chosen.PostalCode, chosen.City
	addr.District, addr.Street = chosen.District, chosen.Street
}

// destinationOf is the destination of the chosen method, or "" if the form named
// one that is not on offer.
//
// It reads the CHOICES the server just built rather than the form, which is
// what makes the destination the shop's answer rather than the customer's.
func destinationOf(choices []pages.ShippingChoice, versionID string) Destination {
	for i := range choices {
		if choices[i].VersionID == versionID {
			if d, ok := DestinationFor(choices[i].DestinationKind); ok {
				return d
			}
			return ""
		}
	}
	return ""
}

// OrderPage serves GET /orders/{number}, the confirmation.
func (h *Handler) OrderPage(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")

	// Order numbers come off a per-day counter, so they are guessable. The page
	// carries an email and a delivery address, which makes "reachable by number"
	// an enumeration hole. It is shown to the browser that placed the order, or
	// to the account that owns it — and anything else is the same 404 as an
	// order that does not exist.
	if !h.store.PlacedHere(r.Context(), r, number, h.secure) && !h.ownedBySignedInUser(r, number) {
		web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
			h.notFoundPage(r), "404", i18n.T(r.Context(), i18n.KeyOrderNotFound),
			i18n.T(r.Context(), i18n.KeyOrderNotYours)))
		return
	}

	view, err := h.store.Order(r.Context(), number)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
				h.notFoundPage(r), "404", i18n.T(r.Context(), i18n.KeyOrderNotFound),
				i18n.T(r.Context(), i18n.KeyOrderGone)))
			return
		}
		h.log.ErrorContext(r.Context(), "read order", "error", err)
		h.serverError(w, r)
		return
	}
	view.Cancelled = r.URL.Query().Get("cancelled") == "1"
	web.Render(w, r, h.log, http.StatusOK, pages.Order(pages.OrderMeta(r.Context(), view.Number), &view))
}

// ReorderItems serves POST /orders/{number}/reorder.
//
// The same access rule as the page it is posted from: the browser that placed
// the order, or the account that owns it. Order numbers come off a guessable
// counter, and without it filling a stranger's cart is one form submission.
func (h *Handler) ReorderItems(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !h.store.PlacedHere(r.Context(), r, number, h.secure) && !h.ownedBySignedInUser(r, number) {
		web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
			h.notFoundPage(r), "404", i18n.T(r.Context(), i18n.KeyOrderNotFound),
			i18n.T(r.Context(), i18n.KeyOrderNotYoursShort)))
		return
	}

	// The cart is CREATED if there is none: somebody reordering has an empty
	// cart far more often than not, and refusing because of that would be
	// refusing the whole point.
	cartID, err := h.cartForWrite(w, r)
	if err != nil {
		h.log.ErrorContext(r.Context(), "cart for reorder", "error", err)
		h.serverError(w, r)
		return
	}

	result, err := h.store.Reorder(r.Context(), cartID, number)
	if err != nil {
		h.log.ErrorContext(r.Context(), "reorder", "error", err, "order", number)
		h.serverError(w, r)
		return
	}

	// The outcome travels as counts, never as product names: a query string is
	// logged, and what somebody bought is theirs.
	http.Redirect(w, r, "/cart?"+url.Values{
		"added":   {strconv.Itoa(result.Added)},
		"skipped": {strconv.Itoa(len(result.Skipped))},
	}.Encode(), http.StatusSeeOther)
}

// CancelOrder serves POST /orders/{number}/cancel.
//
// The same access rule as the page it is posted from: the browser that placed
// the order, or the account that owns it. Without it, order numbers come off a
// guessable counter and cancelling somebody else's order is a form submission.
func (h *Handler) CancelOrder(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !h.store.PlacedHere(r.Context(), r, number, h.secure) && !h.ownedBySignedInUser(r, number) {
		web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
			h.notFoundPage(r), "404", i18n.T(r.Context(), i18n.KeyOrderNotFound),
			i18n.T(r.Context(), i18n.KeyOrderNotYoursShort)))
		return
	}

	sessions, err := h.store.Cancel(r.Context(), number)
	switch {
	case err == nil:
		h.closeSessions(r.Context(), number, sessions)
		http.Redirect(w, r, "/orders/"+url.PathEscape(number)+"?cancelled=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotCancellable):
		// 422 and not a redirect: nothing was written, and the page has to say
		// why rather than showing a cancelled order that is not cancelled.
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyCancelRefusedTitle)}, "",
			i18n.T(r.Context(), i18n.KeyCancelRefusedTitle),
			i18n.T(r.Context(), i18n.KeyCancelRefusedBody)))
	default:
		h.log.ErrorContext(r.Context(), "cancel order", "error", err)
		h.serverError(w, r)
	}
}

// ownedBySignedInUser reports whether the signed-in account owns this order.
func (h *Handler) ownedBySignedInUser(r *http.Request, number string) bool {
	u, ok := account.FromContext(r.Context())
	if !ok {
		return false
	}
	owns, err := h.store.OrderBelongsTo(r.Context(), number, u.ID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "check order ownership", "error", err)
		return false
	}
	return owns
}

// checkoutView assembles the form's data.
func (h *Handler) checkoutView(ctx context.Context, cartID uuid.UUID, owner uuid.NullUUID) (pages.CheckoutView, error) {
	cartView, err := h.store.View(ctx, cartID)
	if err != nil {
		return pages.CheckoutView{}, err
	}
	choices, err := h.store.ShippingChoices(ctx, cartView.SubtotalCents)
	if err != nil {
		return pages.CheckoutView{}, err
	}
	saved, err := h.store.SavedAddresses(ctx, owner)
	if err != nil {
		return pages.CheckoutView{}, err
	}
	view := pages.CheckoutView{
		Cart: cartView, Shipping: choices,
		InvoiceChoices: invoiceChoices(ctx),
		PickupBrands:   pages.PickupBrandChoices(),
		SavedAddresses: saved,
		Idempoten:      newIdempotencyKey(),
	}
	if len(choices) > 0 {
		view.Chosen = choices[0].VersionID
		view.Destination = string(destinationOf(choices, view.Chosen))
	}
	return view, nil
}

// invoiceChoices is what the form offers. Built from InvoiceTypes and
// InvoiceTypeLabelKey so the page and the validator cannot list different
// things — adding a type in one place and forgetting the other is how a form
// comes to offer something the server refuses.
func invoiceChoices(ctx context.Context) []pages.InvoiceChoice {
	out := make([]pages.InvoiceChoice, 0, len(InvoiceTypes))
	for _, t := range InvoiceTypes {
		out = append(out, pages.InvoiceChoice{Value: t, Label: i18n.T(ctx, InvoiceTypeLabelKey(t))})
	}
	return out
}

// existingCart returns the cart the request's cookie names, without creating
// one. A GET must not create a cart: a crawler would leave a row per visit.
func (h *Handler) existingCart(r *http.Request) (uuid.UUID, bool) {
	token := ReadCookie(r, h.secure)
	if token == "" {
		return uuid.Nil, false
	}
	id, err := h.store.Find(r.Context(), token)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// cartForWrite returns the request's cart, opening one if this is the visitor's
// first item. Only a POST reaches this.
func (h *Handler) cartForWrite(w http.ResponseWriter, r *http.Request) (uuid.UUID, error) {
	if id, ok := h.existingCart(r); ok {
		return id, nil
	}
	token, err := NewToken()
	if err != nil {
		return uuid.Nil, err
	}
	id, err := h.store.Create(r.Context(), token, uuid.NullUUID{})
	if err != nil {
		return uuid.Nil, err
	}
	SetCookie(w, token, h.secure)
	return id, nil
}

// backToProduct answers 303 to the product the form came from, carrying an
// outcome the page can show. The slug is checked against the form's own field
// rather than the Referer, which a request controls.
func (h *Handler) backToProduct(w http.ResponseWriter, r *http.Request, outcome string) {
	slug := r.PostFormValue("back")
	if !isSlug(slug) {
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
		return
	}
	// slug passed isSlug, so it is lowercase letters, digits and hyphens only —
	// it cannot start with "//" or carry a scheme, which is what would make this
	// an open redirect. outcome is one of this file's own literals.
	http.Redirect(w, r, "/p/"+slug+"?added="+outcome, http.StatusSeeOther) //nolint:gosec // G710: slug validated by isSlug
}

// isSlug reports whether s is a product slug and nothing else. This is what
// stops a form field from becoming an open redirect: without it, "back" could
// name any URL and the 303 would send the visitor there.
func isSlug(s string) bool {
	if s == "" || len(s) > 120 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return true
}

// newIdempotencyKey returns a fresh checkout key.
func newIdempotencyKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// A key that cannot be generated must not silently become a constant,
		// which would make every checkout look like a repeat of the first.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
		i18n.T(r.Context(), i18n.KeyTryAgainTitle),
		i18n.T(r.Context(), i18n.KeyCartUnavailable)))
}

// notFoundPage is the shell for "no such order", which four handlers answer with
// the same title and three different reasons.
func (h *Handler) notFoundPage(r *http.Request) layouts.Page {
	return layouts.Page{Title: i18n.T(r.Context(), i18n.KeyOrderNotFound)}
}

// CartIDForRequest returns the cart a request's cookie names, without creating
// one. It exists for the account package's sign-in path, which adopts a guest
// cart — expressed as a one-method interface there rather than an import of
// this type.
func (h *Handler) CartIDForRequest(ctx context.Context, r *http.Request) (uuid.UUID, bool) {
	token := ReadCookie(r, h.secure)
	if token == "" {
		return uuid.Nil, false
	}
	id, err := h.store.Find(ctx, token)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// WithCount puts the visitor's cart size into the request context, for the
// header badge.
//
// Middleware rather than a line in every handler: layouts.Page has carried a
// CartCount field since the header was built and NOTHING ever assigned it, so
// the badge showed 0 to every visitor whatever was in their cart. A field each
// handler must remember to fill is a field that goes unfilled.
//
// A visitor with no cart cookie costs no query. One with a cookie costs a
// single indexed sum, which is the price of a header that tells the truth.
func (h *Handler) WithCount(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.CartIDForRequest(r.Context(), r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		n, err := h.store.ItemCount(r.Context(), id)
		if err != nil {
			// A badge is not worth failing a page for. Logged, and the header
			// shows nothing rather than a wrong number.
			h.log.ErrorContext(r.Context(), "count cart items", "error", err)
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(web.WithCartCount(r.Context(), n)))
	})
}

// FindOrderPage serves GET /orders/find.
func (h *Handler) FindOrderPage(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusOK, pages.FindOrder(
		pages.FindOrderMeta(r.Context()), pages.FindOrderView{}))
}

// FindOrder serves POST /orders/find.
//
// On success it writes the SAME cookie placing an order writes, and redirects to
// the order page. Reusing that mechanism rather than inventing a second one is the
// point: there is one answer to "may this browser see this order", and adding a
// parallel path would be a second place for it to be wrong.
//
// It answers IDENTICALLY on every failure. Order numbers come off a per-day
// counter and are guessable, so "that number exists but the address is wrong" is a
// sentence that hands somebody half a credential.
func (h *Handler) FindOrder(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	number := strings.ToUpper(strings.TrimSpace(r.PostFormValue("number")))
	addr := r.PostFormValue("email")

	// Bounded per IP, and BEFORE the read. The pair is guessable in one half, so
	// this endpoint is an oracle for the other half if it can be asked without
	// limit — and the limiter is the only thing standing between a script and
	// somebody's delivery address.
	if retryAfter, ok := h.findLimit.Allow("findorder:" + ratelimit.ClientIP(r)); !ok {
		ratelimit.Refuse(w, retryAfter)
		return
	}

	found, err := h.store.FindOrder(r.Context(), number, addr)
	if err != nil {
		h.log.ErrorContext(r.Context(), "find order", "error", err)
		h.serverError(w, r)
		return
	}
	if !found {
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.FindOrder(
			pages.FindOrderMeta(r.Context()), pages.FindOrderView{
				Number: number, Email: addr,
				Error: i18n.T(r.Context(), i18n.KeyFindOrderRefused),
			}))
		return
	}

	h.rememberOrder(w, r, number)
	http.Redirect(w, r, "/orders/"+url.PathEscape(number), http.StatusSeeOther)
}
