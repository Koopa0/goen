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
	"slices"
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
	// secure says whether cookies may claim the __Host- prefix.
	secure bool
	// findLimit bounds the order lookup, which is otherwise an oracle for the
	// secret half of a credential whose other half is guessable.
	findLimit *ratelimit.Limiter
	// sessions closes a cancelled order's checkout at the payment provider. Nil
	// on a deployment with no Stripe key, where there is no session to close.
	sessions SessionCloser
}

// SessionCloser closes a checkout the customer may still have open at the
// payment provider.
type SessionCloser interface {
	ExpireSession(ctx context.Context, sessionID string) error
}

// NewHandler returns a Handler writing through store. A nil sessions means no
// provider is configured, so no session was ever opened to close.
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

// closeSessions expires the checkouts a cancelled order left open at Stripe,
// post-commit and best effort: the cancellation has already committed, and money
// that beats this there arrives as payment.ErrOrderCancelled.
func (h *Handler) closeSessions(ctx context.Context, number string, sessions []string) {
	if h.sessions == nil {
		return
	}
	for _, id := range sessions {
		if err := h.sessions.ExpireSession(ctx, id); err != nil {
			// Warn, not Error: Stripe refuses to expire a session that is not
			// open, which is what a customer who paid in the other tab produces.
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

// reorderOutcome reads the counts a reorder redirect is reporting. A
// hand-edited URL must not be able to tell somebody their cart holds things it
// does not, so anything that will not parse renders as no notice at all.
func reorderOutcome(r *http.Request) (added, skipped int) {
	q := r.URL.Query()
	added, _ = strconv.Atoi(q.Get("added"))     //nolint:errcheck // unparseable is zero, which renders nothing
	skipped, _ = strconv.Atoi(q.Get("skipped")) //nolint:errcheck // same
	return max(added, 0), max(skipped, 0)
}

// AddItem serves POST /cart/items, answering 303 to the product it came from.
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
	// The chosen method lives in the URL and nowhere else, so it is a link and
	// the choice works with scripting off.
	if ship := r.URL.Query().Get("ship"); ship != "" {
		for i := range view.Shipping {
			if view.Shipping[i].VersionID == ship {
				view.Chosen = ship
			}
		}
	}
	view.Destination = string(destinationOf(view.Shipping, view.Chosen))
	// The 發票 choice travels the same way, and for the same reason: it decides
	// which field the form asks for, so a chooser that only a script could act
	// on would leave the two disagreeing.
	if kind := r.URL.Query().Get("invoice"); slices.Contains(InvoiceTypes, kind) {
		view.Invoice.Type = kind
	}

	var prefill Address
	fillFromBook(&view, &prefill, r.URL.Query().Get("address"))
	view.Address = pages.CheckoutAddress{
		Email: emailOf(r), Name: prefill.Name, Phone: prefill.Phone,
		PostalCode: prefill.PostalCode, City: prefill.City,
		District: prefill.District, Street: prefill.Street,
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Checkout(pages.CheckoutMeta(r.Context()), &view))
}

// emailOf is the signed-in customer's address, or "".
func emailOf(r *http.Request) string {
	if u, ok := account.FromContext(r.Context()); ok {
		return u.Email
	}
	return ""
}

// rememberOrder gives this browser a token for an order it is allowed to see. A
// failure is logged and NOT fatal: only the browser's proof of it failed.
func (h *Handler) rememberOrder(w http.ResponseWriter, r *http.Request, number string) {
	if err := h.store.RememberOrder(r.Context(), w, r, number, h.secure); err != nil {
		h.log.ErrorContext(r.Context(), "remember order", "error", err, "order", number)
	}
}

// PlaceOrder serves POST /checkout, re-rendering at 422 with the submitted
// values intact or answering 303 to payment.
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
	// What was SUBMITTED, echoed back — never re-filled from the book, which
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
	// by the form.
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

	coupon, couponErr := h.resolveCoupon(r, &view)

	// A CHOOSER CHANGE, not an order. The delivery method, the saved address and
	// the 發票 type each decide which fields the form asks for, so changing one
	// has to re-render — and it used to do that through a link carrying only the
	// choice, which discarded everything already typed. The values come back
	// because this handler has already rebuilt the whole view from the
	// submission; nothing is validated, because nobody has finished.
	if r.PostFormValue("update") != "" {
		web.Render(w, r, h.log, http.StatusOK,
			pages.Checkout(pages.CheckoutMeta(r.Context()), &view))
		return
	}

	shippingID, shipErr := uuid.Parse(view.Chosen)
	errs := checkoutErrors(r.Context(), addr, shipErr, inv)
	if shipErr == nil && errs == nil {
		// The address is valid, so the fee can be priced for real: earlier and
		// the postal code is not trustworthy, later and the customer has
		// already committed to a figure.
		if quoteErr := h.requote(r, &view, shippingID, addr); quoteErr != "" {
			view.Repriced = quoteErr
		}
	}
	if couponErr != "" {
		// checkoutErrors returns a nil map when nothing was wrong, and writing
		// to a nil map panics.
		if errs == nil {
			errs = map[string]string{}
		}
		errs["coupon"] = couponErr
	}
	if len(errs) > 0 || view.Repriced != "" {
		view.Errors = errs
		web.Render(w, r, h.log, http.StatusUnprocessableEntity,
			pages.Checkout(pages.CheckoutMeta(r.Context()), &view))
		return
	}

	key := view.Idempoten
	if key == "" {
		// A form that lost its key still gets one.
		key = newIdempotencyKey()
	}

	number, err := h.store.PlaceOrder(r.Context(), cartID, owner, shippingID, addr, inv, coupon, key)
	h.answerPlacement(w, r, &view, number, err)
}

// answerPlacement turns the outcome of a checkout write into a response.
func (h *Handler) answerPlacement(
	w http.ResponseWriter, r *http.Request,
	view *pages.CheckoutView, number string, err error,
) {
	switch {
	case err == nil:
		h.rememberOrder(w, r, number)
		// Straight to payment rather than to the confirmation, which shown
		// before payment reads as "done" to a customer who then closes the tab.
		http.Redirect(w, r, "/orders/"+number+"/pay", http.StatusSeeOther) //nolint:gosec // G710: server-generated order number
	case errors.Is(err, ErrEmpty):
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
	case errors.Is(err, ErrUnavailable):
		// Something sold out between the cart page and this write; the cart page
		// says which line and why.
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		view.Errors = map[string]string{"shipping": i18n.T(r.Context(), i18n.KeyChooseShipping)}
		web.Render(w, r, h.log, http.StatusUnprocessableEntity,
			pages.Checkout(pages.CheckoutMeta(r.Context()), view))
	case errors.Is(err, ErrCouponUsedUp), errors.Is(err, ErrCouponExpired):
		// The coupon was fine when the form was validated and is refused inside
		// the transaction, where redeem_coupon counts the limits under its lock.
		// Not a rare race: the pre-check reads no limit at all.
		reason := i18n.KeyCouponUsedUp
		if errors.Is(err, ErrCouponExpired) {
			reason = i18n.KeyCouponExpired
		}
		view.Errors = map[string]string{"coupon": i18n.T(r.Context(), reason)}
		web.Render(w, r, h.log, http.StatusUnprocessableEntity,
			pages.Checkout(pages.CheckoutMeta(r.Context()), view))
	default:
		h.log.ErrorContext(r.Context(), "place order", "error", err)
		h.serverError(w, r)
	}
}

// resolveCoupon looks up the typed code and prices it, or reports why not. An
// empty field is not an error.
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
// and refuses the order when the figure has moved. The fee shown when the method
// was chosen is a mainland fee, because no postal code had been typed yet.
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

	// The form carries what the customer was last shown.
	shown, err := strconv.ParseInt(r.PostFormValue("quoted_shipping"), 10, 64)
	if err == nil && shown == quote.Total() {
		return ""
	}
	return fmt.Sprintf(i18n.T(r.Context(), i18n.KeyShippingRepriced),
		quote.ZoneName, pages.TWD(quote.Total()))
}

// ownerOf is the signed-in customer, or a null id for a guest.
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

// fillFromBook fills the form from a saved address, and says which one. An id
// that names nothing falls through to the default rather than to an error page.
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
// one that is not on offer. It reads the choices the server built, not the form.
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

	// Shown to the browser that placed the order or to the account that owns it,
	// and anything else is the same 404 as an order that does not exist.
	if !h.store.PlacedHere(r.Context(), r, number, h.secure) && !h.ownedBySignedInUser(r, number) {
		// Its own page rather than a bare Notice: this is the one 404 with a way
		// through, and the reader is either a guest or a signed-out customer.
		web.Render(w, r, h.log, http.StatusNotFound, pages.OrderNotFound(h.notFoundPage(r)))
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

// ReorderItems serves POST /orders/{number}/reorder, under the same access rule
// as the page it is posted from.
func (h *Handler) ReorderItems(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !h.store.PlacedHere(r.Context(), r, number, h.secure) && !h.ownedBySignedInUser(r, number) {
		web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
			h.notFoundPage(r), "404", i18n.T(r.Context(), i18n.KeyOrderNotFound),
			i18n.T(r.Context(), i18n.KeyOrderNotYoursShort)))
		return
	}

	// The cart is CREATED if there is none: somebody reordering usually has an
	// empty one.
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

	// Counts, never product names: a query string is logged.
	http.Redirect(w, r, "/cart?"+url.Values{
		"added":   {strconv.Itoa(result.Added)},
		"skipped": {strconv.Itoa(len(result.Skipped))},
	}.Encode(), http.StatusSeeOther)
}

// CancelOrder serves POST /orders/{number}/cancel, under the same access rule as
// the page it is posted from.
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
		// 422 and not a redirect: nothing was written.
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
	choices, err := h.store.ShippingChoices(ctx, cartID, cartView.SubtotalCents)
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

// invoiceChoices is what the form offers, built from InvoiceTypes so the page
// and the validator cannot list different things.
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
// first item.
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
// outcome the page can show. The slug comes from the form's own field rather
// than the Referer, which a request controls.
func (h *Handler) backToProduct(w http.ResponseWriter, r *http.Request, outcome string) {
	slug := r.PostFormValue("back")
	if !isSlug(slug) {
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/p/"+slug+"?added="+outcome, http.StatusSeeOther) //nolint:gosec // G710: slug validated by isSlug
}

// isSlug reports whether s is a product slug and nothing else, which is what
// stops the "back" field from becoming an open redirect.
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
		// A key that cannot be generated must not become a constant, which would
		// make every checkout look like a repeat of the first.
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

// notFoundPage is the shell for "no such order".
func (h *Handler) notFoundPage(r *http.Request) layouts.Page {
	return layouts.Page{Title: i18n.T(r.Context(), i18n.KeyOrderNotFound)}
}

// CartIDForRequest returns the cart a request's cookie names, without creating
// one.
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
// header badge. Middleware rather than a line in every handler, because a field
// each handler must remember to fill is a field that goes unfilled.
func (h *Handler) WithCount(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.CartIDForRequest(r.Context(), r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		n, err := h.store.ItemCount(r.Context(), id)
		if err != nil {
			// A badge is not worth failing a page for.
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

// FindOrder serves POST /orders/find. On success it writes the SAME cookie
// placing an order writes, and it answers IDENTICALLY on every failure, since
// "that number exists but the address is wrong" hands over half a credential.
func (h *Handler) FindOrder(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	number := strings.ToUpper(strings.TrimSpace(r.PostFormValue("number")))
	addr := r.PostFormValue("email")

	// Bounded per IP, and BEFORE the read: unbounded, this endpoint is an oracle
	// for the secret half of the pair.
	if retryAfter, ok := h.findLimit.Allow("findorder:" + ratelimit.ClientIP(r)); !ok {
		ratelimit.Refuse(r.Context(), w, retryAfter)
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
