package cart

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"

	"github.com/koopa0/goen/internal/i18n"
	invoicepkg "github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/pickup"
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

	// The database cancellation has already committed. Keep request values for
	// tracing, but do not let a client disconnect turn provider cleanup into a
	// no-op. One short budget bounds the whole best-effort batch.
	const timeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

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
	case errors.Is(err, ErrTooManyItems):
		h.backToProduct(w, r, "full")
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
	if kind := invoicepkg.Preference(r.URL.Query().Get("invoice")); kind.Known() {
		view.Invoice.Type = kind
	}

	var prefill Address
	fillFromBook(&view, &prefill, r.URL.Query().Get("address"))
	view.Address = pages.CheckoutAddress{
		Email: emailOf(r), Name: prefill.Name, Phone: prefill.Phone,
		PostalCode: prefill.PostalCode, City: prefill.City,
		District: prefill.District, Street: prefill.Street,
	}
	shippingID, err := uuid.Parse(view.Chosen)
	if err != nil {
		h.log.ErrorContext(r.Context(), "checkout has no valid shipping choice", "error", err)
		h.serverError(w, r)
		return
	}
	if err := h.quoteCheckoutShipping(r.Context(), &view, shippingID, prefill.PostalCode); err != nil {
		h.log.ErrorContext(r.Context(), "quote checkout shipping", "error", err)
		h.serverError(w, r)
		return
	}
	if err := setCheckoutQuoteID(cartID, &view); err != nil {
		h.log.ErrorContext(r.Context(), "build checkout quote", "error", err)
		h.serverError(w, r)
		return
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
	attemptID, attemptErr := parseCheckoutAttemptID(r.PostFormValue("idempotency"))
	if attemptErr == nil && h.answerPriorCheckout(w, r, cartID, attemptID) {
		return
	}

	owner := ownerOf(r)
	submission, ok := h.checkoutSubmission(w, r, cartID, owner, attemptID, attemptErr == nil)
	if !ok {
		return
	}

	// A CHOOSER CHANGE, not an order. The delivery method, the saved address and
	// the 發票 type each decide which fields the form asks for, so changing one
	// re-renders with the submitted values intact; nothing is validated, because
	// nobody has finished.
	if r.PostFormValue("update") != "" {
		web.Render(w, r, h.log, http.StatusOK,
			pages.Checkout(pages.CheckoutMeta(r.Context()), &submission.view))
		return
	}

	shown, ok := h.validateCheckoutSubmission(w, r, cartID, submission)
	if !ok {
		return
	}

	if attemptErr != nil {
		// Only a canonical server identity can name a retry. checkoutSubmission
		// retained checkoutView's fresh identity instead of echoing malformed form
		// text; require confirmation before that new identity can write anything.
		submission.view.Repriced = i18n.T(r.Context(), i18n.KeyCheckoutChanged)
		web.Render(w, r, h.log, http.StatusUnprocessableEntity,
			pages.Checkout(pages.CheckoutMeta(r.Context()), &submission.view))
		return
	}

	number, err := h.store.placeOrder(
		r.Context(), cartID, owner, submission.shippingID, &submission.address,
		&submission.invoice, submission.view.CouponCode,
		shown, attemptID,
	)
	h.answerPlacement(w, r, cartID, &submission.address, &submission.view, number, err)
}

// answerPriorCheckout handles the lost-response retry before examining the
// cart that a successful checkout deliberately emptied.
func (h *Handler) answerPriorCheckout(
	w http.ResponseWriter, r *http.Request, cartID uuid.UUID, attemptID checkoutAttemptID,
) bool {
	prior, found, err := h.store.priorOrder(
		r.Context(), cartID, attemptID,
	)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read prior checkout attempt", "error", err)
		h.serverError(w, r)
		return true
	}
	if !found {
		return false
	}
	h.rememberOrder(w, r, prior)
	http.Redirect(w, r, "/orders/"+prior+"/pay", http.StatusSeeOther) //nolint:gosec // server-generated order number
	return true
}

// checkoutSubmission rebuilds server-owned checkout state while preserving the
// customer's submitted fields. ok is false only after this method has answered
// the request.
type checkoutSubmission struct {
	view        pages.CheckoutView
	address     Address
	invoice     Invoice
	shippingID  uuid.UUID
	shippingErr error
	couponErr   string
}

func (h *Handler) checkoutSubmission(
	w http.ResponseWriter,
	r *http.Request,
	cartID uuid.UUID,
	owner uuid.NullUUID,
	attemptID checkoutAttemptID,
	attemptOK bool,
) (*checkoutSubmission, bool) {
	addr := Address{
		Email:           r.PostFormValue("email"),
		Name:            r.PostFormValue("name"),
		Phone:           r.PostFormValue("phone"),
		PostalCode:      r.PostFormValue("postal_code"),
		City:            r.PostFormValue("city"),
		District:        r.PostFormValue("district"),
		Street:          r.PostFormValue("street"),
		PickupBrand:     pickup.Brand(r.PostFormValue("pickup_brand")),
		PickupStoreCode: r.PostFormValue("pickup_store_code"),
		PickupStoreName: r.PostFormValue("pickup_store_name"),
		Note:            r.PostFormValue("note"),
	}
	addr.Trim()

	view, err := h.checkoutView(r.Context(), cartID, owner)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read checkout", "error", err)
		h.serverError(w, r)
		return nil, false
	}
	// Another tab may already have emptied or invalidated this cart. The cart
	// page explains the current state; Store keeps the same rule transactionally.
	if !view.Cart.CanCheckout() {
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
		return nil, false
	}

	// Echo what was submitted, except when the address chooser itself asks to
	// replace those fields with one saved address.
	view.ChosenAddress = r.PostFormValue("address")
	if r.PostFormValue("update") == "address" {
		fillFromBook(&view, &addr, view.ChosenAddress)
	}
	view.Address = pages.CheckoutAddress{
		Email: addr.Email, Name: addr.Name, Phone: addr.Phone,
		PostalCode: addr.PostalCode, City: addr.City,
		District: addr.District, Street: addr.Street,
		PickupBrand: addr.PickupBrand, PickupStoreCode: addr.PickupStoreCode,
		PickupStoreName: addr.PickupStoreName, Note: addr.Note,
	}
	view.Chosen = r.PostFormValue("shipping")
	// Resolved once: a round trip through string would be an unchecked
	// conversion of a value DestinationFor has already vouched for.
	destination := destinationOf(view.Shipping, view.Chosen)
	view.Destination = string(destination)
	addr.To = destination
	// Invalid form text never survives as internal state. checkoutView already
	// owns a fresh identity for that case; a valid retry keeps its exact identity.
	if attemptOK {
		view.IdempotencyKey = attemptID.String()
	}

	inv := Invoice{
		Type:        invoicepkg.Preference(r.PostFormValue("invoice_type")),
		Carrier:     r.PostFormValue("invoice_carrier"),
		CompanyName: r.PostFormValue("invoice_company_name"),
		TaxID:       r.PostFormValue("invoice_tax_id"),
	}
	view.Invoice = pages.CheckoutInvoice{
		Type: inv.Type, Carrier: inv.Carrier,
		CompanyName: inv.CompanyName, TaxID: inv.TaxID,
	}

	couponErr := h.resolveCoupon(r, &view)
	shippingID, shipErr := uuid.Parse(view.Chosen)
	if shipErr == nil {
		if quoteErr := h.quoteCheckoutShipping(
			r.Context(), &view, shippingID, addr.PostalCode,
		); quoteErr != nil {
			h.log.ErrorContext(r.Context(), "quote shipping", "error", quoteErr)
			view.Repriced = i18n.T(r.Context(), i18n.KeyShippingUnpriceable)
		}
	}
	if couponErr != "" {
		view.Errors = map[string]string{"coupon": couponErr}
	}
	if shipErr == nil && view.Repriced == "" && couponErr == "" {
		if quoteErr := setCheckoutQuoteID(cartID, &view); quoteErr != nil {
			h.log.ErrorContext(r.Context(), "build refreshed checkout quote", "error", quoteErr)
			h.serverError(w, r)
			return nil, false
		}
	}
	return &checkoutSubmission{
		view: view, address: addr, invoice: inv,
		shippingID: shippingID, shippingErr: shipErr, couponErr: couponErr,
	}, true
}

func (h *Handler) validateCheckoutSubmission(
	w http.ResponseWriter,
	r *http.Request,
	cartID uuid.UUID,
	submission *checkoutSubmission,
) (checkoutQuoteID, bool) {
	errs := checkoutErrors(
		r.Context(), &submission.address, submission.shippingErr, &submission.invoice,
	)
	if submission.couponErr != "" {
		if errs == nil {
			errs = map[string]string{}
		}
		errs["coupon"] = submission.couponErr
	}

	shown, shownErr := parseCheckoutQuoteID(r.PostFormValue("checkout_quote"))
	current, currentErr := checkoutQuoteIDForView(cartID, &submission.view)
	if submission.shippingErr == nil && submission.couponErr == "" &&
		submission.view.Repriced == "" && currentErr != nil {
		h.log.ErrorContext(r.Context(), "build submitted checkout quote", "error", currentErr)
		h.serverError(w, r)
		return checkoutQuoteID{}, false
	}
	if shownErr != nil || currentErr != nil || shown != current {
		submission.view.Repriced = i18n.T(r.Context(), i18n.KeyCheckoutChanged)
	}
	if len(errs) > 0 || submission.view.Repriced != "" {
		submission.view.Errors = errs
		web.Render(w, r, h.log, http.StatusUnprocessableEntity,
			pages.Checkout(pages.CheckoutMeta(r.Context()), &submission.view))
		return checkoutQuoteID{}, false
	}
	return shown, true
}

// answerPlacement turns the outcome of a checkout write into a response.
func (h *Handler) answerPlacement(
	w http.ResponseWriter, r *http.Request,
	cartID uuid.UUID, addr *Address, view *pages.CheckoutView, number string, err error,
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
	case errors.Is(err, ErrTooManyItems):
		view.Repriced = i18n.T(r.Context(), i18n.KeyCartLineLimit)
		web.Render(w, r, h.log, http.StatusUnprocessableEntity,
			pages.Checkout(pages.CheckoutMeta(r.Context()), view))
	case errors.Is(err, ErrCreditChanged):
		h.answerCreditChanged(w, r, cartID, view)
	case errors.Is(err, errCheckoutChanged):
		h.answerCheckoutChanged(w, r, cartID, addr, view)
	case errors.Is(err, errCheckoutKeyConflict):
		h.answerCheckoutKeyConflict(w, r, cartID, view)
	case errors.Is(err, ErrNotFound):
		h.answerMissingShipping(w, r, cartID, addr, view)
	case errors.Is(err, ErrCouponUsedUp), errors.Is(err, ErrCouponExpired),
		errors.Is(err, ErrCouponMinimum), errors.Is(err, ErrNoSuchCoupon):
		h.answerCouponRefusal(w, r, cartID, addr, view, err)
	default:
		h.log.ErrorContext(r.Context(), "place order", "error", err)
		h.serverError(w, r)
	}
}

func (h *Handler) answerCreditChanged(
	w http.ResponseWriter, r *http.Request, cartID uuid.UUID, view *pages.CheckoutView,
) {
	balance, err := h.store.AvailableCredit(r.Context(), ownerOf(r))
	if err != nil {
		h.log.ErrorContext(r.Context(), "refresh store credit after checkout refusal", "error", err)
		h.serverError(w, r)
		return
	}
	view.AvailableCreditCents = balance
	view.CreditChanged = fmt.Sprintf(i18n.T(r.Context(), i18n.KeyCreditChanged), pages.TWD(balance))
	if err := setCheckoutQuoteID(cartID, view); err != nil {
		h.log.ErrorContext(r.Context(), "build refreshed credit quote", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity,
		pages.Checkout(pages.CheckoutMeta(r.Context()), view))
}

func (h *Handler) answerCheckoutChanged(
	w http.ResponseWriter,
	r *http.Request,
	cartID uuid.UUID,
	addr *Address,
	view *pages.CheckoutView,
) {
	if err := h.refreshCheckoutState(r.Context(), cartID, ownerOf(r), addr, view); err != nil {
		h.log.ErrorContext(r.Context(), "refresh changed checkout quote", "error", err)
		h.serverError(w, r)
		return
	}
	if couponErr := h.resolveCoupon(r, view); couponErr != "" {
		view.Errors = map[string]string{"coupon": couponErr}
	}
	view.Repriced = i18n.T(r.Context(), i18n.KeyCheckoutChanged)
	if err := setCheckoutQuoteID(cartID, view); err != nil {
		h.log.ErrorContext(r.Context(), "build changed checkout quote", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity,
		pages.Checkout(pages.CheckoutMeta(r.Context()), view))
}

func (h *Handler) answerCheckoutKeyConflict(
	w http.ResponseWriter, r *http.Request, cartID uuid.UUID, view *pages.CheckoutView,
) {
	// The key is a retry identity, not order access. Do not reveal which cart
	// owns a collision; replace it and require confirmation.
	attemptID, err := newCheckoutAttemptID()
	if err != nil {
		h.log.ErrorContext(r.Context(), "replace conflicting checkout identity", "error", err)
		h.serverError(w, r)
		return
	}
	view.IdempotencyKey = attemptID.String()
	view.Repriced = i18n.T(r.Context(), i18n.KeyCheckoutChanged)
	if err := setCheckoutQuoteID(cartID, view); err != nil {
		h.log.ErrorContext(r.Context(), "build checkout quote after key collision", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity,
		pages.Checkout(pages.CheckoutMeta(r.Context()), view))
}

func (h *Handler) answerMissingShipping(
	w http.ResponseWriter,
	r *http.Request,
	cartID uuid.UUID,
	addr *Address,
	view *pages.CheckoutView,
) {
	if err := h.refreshCheckoutState(r.Context(), cartID, ownerOf(r), addr, view); err != nil {
		h.log.ErrorContext(r.Context(), "refresh checkout after missing shipping", "error", err)
		h.serverError(w, r)
		return
	}
	view.Errors = map[string]string{"shipping": i18n.T(r.Context(), i18n.KeyChooseShipping)}
	if err := setCheckoutQuoteID(cartID, view); err != nil {
		h.log.ErrorContext(r.Context(), "build replacement shipping quote", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity,
		pages.Checkout(pages.CheckoutMeta(r.Context()), view))
}

func (h *Handler) answerCouponRefusal(
	w http.ResponseWriter,
	r *http.Request,
	cartID uuid.UUID,
	addr *Address,
	view *pages.CheckoutView,
	refusal error,
) {
	// Coupon eligibility is authoritative inside the transaction. Reload the
	// current cart/quote and remove the rejected coupon's old effect.
	if err := h.refreshCheckoutState(r.Context(), cartID, ownerOf(r), addr, view); err != nil {
		h.log.ErrorContext(r.Context(), "refresh checkout after coupon refusal", "error", err)
		h.serverError(w, r)
		return
	}
	reason := i18n.KeyCouponUsedUp
	switch {
	case errors.Is(refusal, ErrCouponExpired):
		reason = i18n.KeyCouponExpired
	case errors.Is(refusal, ErrCouponMinimum):
		reason = i18n.KeyCouponBelowMinimum
	case errors.Is(refusal, ErrNoSuchCoupon):
		reason = i18n.KeyCouponUnknown
	}
	view.Errors = map[string]string{"coupon": i18n.T(r.Context(), reason)}
	if err := setCheckoutQuoteID(cartID, view); err != nil {
		h.log.ErrorContext(r.Context(), "build coupon-refusal quote", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity,
		pages.Checkout(pages.CheckoutMeta(r.Context()), view))
}

// refreshCheckoutState re-prices the server-owned parts of a submitted
// checkout after a transactional refusal. Typed address, invoice, coupon code
// and idempotency key remain; catalogue, shipping and credit are replaced.
func (h *Handler) refreshCheckoutState(
	ctx context.Context,
	cartID uuid.UUID,
	owner uuid.NullUUID,
	addr *Address,
	view *pages.CheckoutView,
) error {
	cartView, err := h.store.View(ctx, cartID)
	if err != nil {
		return fmt.Errorf("read current cart: %w", err)
	}
	choices, err := h.store.ShippingChoices(ctx, cartID, cartView.SubtotalCents)
	if err != nil {
		return err
	}
	if len(choices) == 0 {
		return errors.New("cart: checkout has no current shipping choice")
	}

	view.Cart = cartView
	view.Shipping = choices
	view.CouponApplied = ""
	view.CouponDiscountCents = 0
	view.CouponFreeShipping = false
	view.QuotedShippingCents = 0
	view.SurchargeCents = 0
	view.ZoneName = ""
	view.Repriced = ""
	balance, err := h.store.AvailableCredit(ctx, owner)
	if err != nil {
		return fmt.Errorf("read current store credit: %w", err)
	}
	view.AvailableCreditCents = balance

	if !slices.ContainsFunc(choices, func(choice pages.ShippingChoice) bool {
		return choice.VersionID == view.Chosen
	}) {
		view.Chosen = choices[0].VersionID
	}
	view.Destination = string(destinationOf(choices, view.Chosen))
	addr.To = Destination(view.Destination)
	shippingID, err := uuid.Parse(view.Chosen)
	if err != nil {
		return fmt.Errorf("parse current shipping choice: %w", err)
	}
	quote, err := h.store.QuoteShipping(ctx, shippingID, cartView.SubtotalCents, addr.PostalCode)
	if err != nil {
		return err
	}
	shippingCents, err := quote.Total()
	if err != nil {
		return fmt.Errorf("total refreshed shipping quote: %w", err)
	}
	view.QuotedShippingCents = shippingCents
	view.SurchargeCents = quote.Surcharge
	view.ZoneName = quote.ZoneName
	return nil
}

// resolveCoupon looks up the typed code and applies it to this view, or reports
// why not. An empty field is not an error.
func (h *Handler) resolveCoupon(r *http.Request, view *pages.CheckoutView) string {
	raw := r.PostFormValue("coupon")
	view.CouponCode = NormaliseCode(raw)
	if view.CouponCode == "" {
		return ""
	}

	subtotal := view.Cart.SubtotalCents
	c, err := h.store.CouponByCode(r.Context(), view.CouponCode)
	if err == nil {
		discountCents, freeShipping, applyErr := c.Apply(subtotal)
		if applyErr == nil {
			view.CouponApplied = c.description
			view.CouponDiscountCents = discountCents
			view.CouponFreeShipping = freeShipping
			return ""
		}
		err = applyErr
	}
	switch {
	case errors.Is(err, ErrCouponExpired):
		return i18n.T(r.Context(), i18n.KeyCouponExpired)
	case errors.Is(err, ErrCouponMinimum):
		return i18n.T(r.Context(), i18n.KeyCouponBelowMinimum)
	case errors.Is(err, ErrNoSuchCoupon):
		return i18n.T(r.Context(), i18n.KeyCouponUnknown)
	default:
		h.log.ErrorContext(r.Context(), "resolve coupon", "error", err)
		return i18n.T(r.Context(), i18n.KeyCouponUnavailable)
	}
}

// checkoutErrors collects everything wrong with a submission.
func checkoutErrors(ctx context.Context, addr *Address, shipErr error, inv *Invoice) map[string]string {
	fieldErrs := addr.Validate()
	if shipErr != nil {
		fieldErrs = append(fieldErrs,
			account.FieldError{Field: "shipping", MessageKey: i18n.KeyChooseShipping})
	}
	fieldErrs = append(fieldErrs, inv.Validate()...)
	return account.FieldMessages(ctx, fieldErrs)
}

// quoteCheckoutShipping prices the chosen method against the current postal
// code and puts that exact server-owned result into the rendered view.
func (h *Handler) quoteCheckoutShipping(
	ctx context.Context,
	view *pages.CheckoutView,
	versionID uuid.UUID,
	postalCode string,
) error {
	quote, err := h.store.QuoteShipping(ctx, versionID, view.Cart.SubtotalCents, postalCode)
	if err != nil {
		return err
	}
	shippingCents, err := quote.Total()
	if err != nil {
		return fmt.Errorf("total checkout shipping quote: %w", err)
	}
	view.QuotedShippingCents = shippingCents
	view.SurchargeCents = quote.Surcharge
	view.ZoneName = quote.ZoneName
	return nil
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
	attemptID, err := newCheckoutAttemptID()
	if err != nil {
		return pages.CheckoutView{}, err
	}
	view := pages.CheckoutView{
		Cart: cartView, Shipping: choices,
		InvoiceChoices: invoiceChoices(ctx),
		PickupBrands:   pages.PickupBrandChoices(),
		SavedAddresses: saved,
		IdempotencyKey: attemptID.String(),
	}
	balance, err := h.store.AvailableCredit(ctx, owner)
	if err != nil {
		return pages.CheckoutView{}, err
	}
	view.AvailableCreditCents = balance
	if len(choices) > 0 {
		view.Chosen = choices[0].VersionID
		view.Destination = string(destinationOf(choices, view.Chosen))
	}
	return view, nil
}

// checkoutQuoteIDForView builds the identity of exactly what CheckoutView renders.
func checkoutQuoteIDForView(cartID uuid.UUID, view *pages.CheckoutView) (checkoutQuoteID, error) {
	shippingID, err := uuid.Parse(view.Chosen)
	if err != nil {
		return checkoutQuoteID{}, fmt.Errorf("parse quoted shipping version: %w", err)
	}
	lines := make([]checkoutQuoteLine, 0, len(view.Cart.Lines))
	for i := range view.Cart.Lines {
		line := &view.Cart.Lines[i]
		variantID, parseErr := uuid.Parse(line.VariantID)
		if parseErr != nil {
			return checkoutQuoteID{}, fmt.Errorf("parse quoted variant: %w", parseErr)
		}
		lines = append(lines, checkoutQuoteLine{
			VariantID: variantID,
			Quantity:  line.Quantity,
			UnitCents: line.UnitCents,
		})
	}
	shippingCents := view.ChargedShippingCents()
	gross, err := checkoutGross(
		view.Cart.SubtotalCents, shippingCents, view.CouponDiscountCents,
	)
	if err != nil {
		return checkoutQuoteID{}, fmt.Errorf("total rendered checkout quote: %w", err)
	}
	creditCents := min(max(view.AvailableCreditCents, 0), gross)
	return (checkoutQuote{
		CartID:            cartID,
		Lines:             lines,
		ShippingVersionID: shippingID,
		ShippingCents:     shippingCents,
		CouponCode:        NormaliseCode(view.CouponCode),
		DiscountCents:     view.CouponDiscountCents,
		CreditCents:       creditCents,
	}).ID()
}

func setCheckoutQuoteID(cartID uuid.UUID, view *pages.CheckoutView) error {
	id, err := checkoutQuoteIDForView(cartID, view)
	if err != nil {
		view.QuoteID = ""
		return err
	}
	view.QuoteID = id.String()
	return nil
}

// invoiceChoices is what the form offers, built from the issuer-owned closed
// set so rendering, checkout validation, storage and ECPay cannot drift.
func invoiceChoices(ctx context.Context) []pages.InvoiceChoice {
	types := invoicepkg.OfferedPreferences()
	out := make([]pages.InvoiceChoice, 0, len(types))
	for _, t := range types {
		out = append(out, pages.InvoiceChoice{Value: t, Label: i18n.T(ctx, invoiceTypeLabelKey(t))})
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
	id, err := h.store.CartByToken(r.Context(), token)
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
	id, err := h.store.CartByToken(ctx, token)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// WithCount puts the visitor's cart size into the request context for the
// header badge, rather than a line each handler must remember to write.
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

	found, err := h.store.OrderBelongsToEmail(r.Context(), number, addr)
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
