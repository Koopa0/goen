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
	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/email"

	"github.com/koopa0/goen/internal/i18n"
	invoicepkg "github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	store  *Store
	access *orderaccess.Store
	log    *slog.Logger
	secure bool
	// findLimit bounds the order lookup, otherwise an oracle for the secret
	// half of a credential whose other half is guessable.
	findLimit *ratelimit.Limiter
	// sessions is nil on a deployment with no Stripe key, where there is no
	// session to close.
	sessions payment.SessionCloser
	// storeMap is nil or disabled on a deployment with no carrier, where the
	// checkout offers no pickup.
	storeMap       *StoreMap
	barcodeChecker MobileBarcodeChecker
	// couponMisses bounds how many wrong coupon codes one shopper is told
	// about: a wrong code and a right one answer differently, so unbounded it
	// finds codes never handed out.
	couponMisses *ratelimit.Limiter
	// findPerAddress bounds the lookup per submitted address as well as per
	// client: the order number is guessable, so the address is the only secret,
	// and a per-client bound alone is lifted by changing client.
	findPerAddress *ratelimit.Limiter
}

type MobileBarcodeChecker interface {
	CheckBarcode(context.Context, string) (invoicepkg.BarcodeStatus, error)
}

// NewHandler reads a nil sessions as no provider configured, and a nil or
// disabled storeMap as no carrier, so the checkout offers no pickup; an
// omitted checker keeps local shape validation where there is no invoice gateway.
func NewHandler(store *Store, access *orderaccess.Store, log *slog.Logger, secure bool, findLimit *ratelimit.Limiter,
	sessions payment.SessionCloser, storeMap *StoreMap, checkers ...MobileBarcodeChecker,
) *Handler {
	if store == nil || access == nil || log == nil || findLimit == nil {
		panic("cart: NewHandler requires a store, an access check, a logger and a lookup limiter")
	}
	if len(checkers) > 1 {
		panic("cart: NewHandler accepts one mobile barcode checker")
	}
	var checker MobileBarcodeChecker
	if len(checkers) == 1 {
		checker = checkers[0]
	}
	return &Handler{
		barcodeChecker: checker,
		store:          store, access: access, log: log, secure: secure, findLimit: findLimit,
		sessions: sessions, storeMap: storeMap,
		// Twenty wrong codes before the first refusal, then one every two
		// minutes: more than a shopper retyping a code from a flyer needs.
		couponMisses: ratelimit.New(ratelimit.Config{
			Every: 2 * time.Minute, Burst: 20, TTL: time.Hour, MaxKeys: 65_536,
		}),
		// Five lookups of one address at once, then five an hour, from
		// anywhere: a customer finding their own order needs one or two.
		findPerAddress: ratelimit.New(ratelimit.Config{
			Every: 12 * time.Minute, Burst: 5, TTL: time.Hour, MaxKeys: 65_536,
		}),
	}
}

// freeDeliveryFor says only what is true for every method offered, since the
// cart does not know the destination: methods that turn free at different
// amounts are one sentence only at the highest, and a method that never does
// makes it none.
func freeDeliveryFor(choices []pages.ShippingChoice, subtotalCents int64) pages.FreeDelivery {
	if len(choices) == 0 {
		return pages.FreeDelivery{}
	}
	var allFreeAt, namedAt int64
	var zones []string
	reached := true
	for i := range choices {
		c := &choices[i]
		namedAt = max(namedAt, c.FreeOverCents)
		for _, z := range c.SurchargeZones {
			if !slices.Contains(zones, z) {
				zones = append(zones, z)
			}
		}
		if c.Free {
			continue
		}
		reached = false
		if c.FreeOverCents <= 0 {
			return pages.FreeDelivery{}
		}
		allFreeAt = max(allFreeAt, c.FreeOverCents)
	}
	if reached {
		return pages.FreeDelivery{Kind: pages.FreeDeliveryReached, ThresholdCents: namedAt, SurchargeZones: zones}
	}
	return pages.FreeDelivery{Kind: pages.FreeDeliveryShort, ShortfallCents: allFreeAt - subtotalCents, ThresholdCents: allFreeAt}
}

// TakesPayment holds because a session closer exists exactly where a payment key does.
func (h *Handler) TakesPayment() bool { return h.sessions != nil }

func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	cartID, found, ok := h.requestCart(w, r)
	if !ok {
		return
	}
	if !found {
		view := pages.CartView{Notice: cartPageNotice(r), ContinueURL: cartContinuation(r)}
		web.Render(w, r, h.log, http.StatusOK, pages.Cart(pages.CartMeta(r.Context()), view))
		return
	}
	view, err := h.store.View(r.Context(), cartID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read cart", "error", err)
		h.serverError(w, r)
		return
	}
	view.ContinueURL = cartContinuation(r)
	if choices, err := h.offeredShipping(r.Context(), cartID, view.SubtotalCents); err != nil {
		h.log.ErrorContext(r.Context(), "read shipping for the cart", "error", err)
	} else {
		view.FreeDelivery = freeDeliveryFor(choices, view.SubtotalCents)
		view.NoDelivery = len(choices) == 0
	}
	view.ReorderAdded, view.ReorderSkipped = reorderOutcome(r)
	view.ReorderAdjusted = view.FromReorder() && r.URL.Query().Get("qty") == "adjusted"
	if notice := cartPageNotice(r); notice != "" && !view.ReorderAdjusted {
		view.Notice = notice
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Cart(pages.CartMeta(r.Context()), view))
}

func cartContinuation(r *http.Request) string {
	if r.URL.Query().Get("qty") != "adjusted" {
		return ""
	}
	return web.SitePathOr(r.URL.Query().Get("next"), "")
}

func cartPageNotice(r *http.Request) string {
	switch r.URL.Query().Get("qty") {
	case "adjusted":
		return i18n.T(r.Context(), i18n.KeyCartQuantityAdjusted)
	case "unavailable":
		return i18n.T(r.Context(), i18n.KeyAddRefused)
	default:
		return ""
	}
}

// reorderOutcome renders no notice for anything that will not parse, so a
// hand-edited URL cannot tell somebody their cart holds things it does not.
func reorderOutcome(r *http.Request) (added, skipped int) {
	q := r.URL.Query()
	added, _ = strconv.Atoi(q.Get("added"))     //nolint:errcheck // unparseable is zero, which renders nothing
	skipped, _ = strconv.Atoi(q.Get("skipped")) //nolint:errcheck // same
	return max(added, 0), max(skipped, 0)
}

func (h *Handler) AddItem(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}

	variantID, err := uuid.Parse(r.PostFormValue("variant"))
	if err != nil {
		h.backToProduct(w, r, uuid.Nil, pages.AddOutcomeUnknown)
		return
	}
	quantity := ParseQuantity(r.PostFormValue("quantity"))

	cartID, err := h.cartForWrite(w, r)
	if err != nil {
		if errors.Is(r.Context().Err(), context.Canceled) {
			return
		}
		h.log.ErrorContext(r.Context(), "open cart", "error", err)
		h.serverError(w, r)
		return
	}

	switch err := h.store.Add(r.Context(), cartID, variantID, quantity); {
	case err == nil:
		h.backToProduct(w, r, variantID, pages.AddOutcomeAdded)
	case errors.Is(err, ErrQuantityAdjusted):
		h.backToProduct(w, r, variantID, pages.AddOutcomeAdjusted)
	case errors.Is(err, ErrTooManyItems):
		h.backToProduct(w, r, variantID, pages.AddOutcomeFull)
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrNotFound):
		h.backToProduct(w, r, variantID, pages.AddOutcomeUnavailable)
	default:
		h.log.ErrorContext(r.Context(), "add to cart", "error", err)
		h.serverError(w, r)
	}
}

func (h *Handler) UpdateItem(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	cartID, ok := h.requiredCart(w, r)
	if !ok {
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
		switch {
		case errors.Is(err, ErrQuantityAdjusted):
			http.Redirect(w, r, "/cart?qty=adjusted", http.StatusSeeOther)
		case errors.Is(err, ErrUnavailable), errors.Is(err, ErrNotFound):
			http.Redirect(w, r, "/cart?qty=unavailable", http.StatusSeeOther)
		default:
			h.log.ErrorContext(r.Context(), "update cart item", "error", err)
			h.serverError(w, r)
		}
		return
	}
	http.Redirect(w, r, "/cart", http.StatusSeeOther)
}

func (h *Handler) Checkout(w http.ResponseWriter, r *http.Request) {
	cartID, ok := h.requiredCart(w, r)
	if !ok {
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
	// A store returning from the carrier's map arrives on a cross-site POST
	// with no query of its own, so the three chosen keys ride in the pickup
	// cookie and are re-validated below by the code that validates the query.
	chosen := h.checkoutChoices(r)

	// On the way back from the carrier's map (or a start that could not open
	// it) what was typed comes back with the store. A plain visit restores
	// nothing.
	draft, restored := h.restoredDraft(r, cartID)
	if restored {
		chosen.Ship = firstOf(chosen.Ship, draft.Shipping)
		chosen.Invoice = firstOf(chosen.Invoice, draft.InvoiceType)
		chosen.Address = firstOf(chosen.Address, draft.SavedAddress)
	}

	// The chosen method lives in the URL only, so the choice works with
	// scripting off.
	if slices.ContainsFunc(view.Shipping, func(choice pages.ShippingChoice) bool {
		return choice.VersionID == chosen.Ship
	}) {
		view.Chosen = chosen.Ship
	}
	view.Destination = destinationOf(view.Shipping, view.Chosen)
	// The 發票 choice travels the same way: it decides which field the form asks
	// for, so a chooser only a script could act on would leave the two
	// disagreeing.
	if kind := invoicepkg.Preference(chosen.Invoice); kind.Known() {
		view.Invoice.Type = kind
	}

	var prefill order.Delivery
	fillFromBook(&view, &prefill, chosen.Address)
	if !restored {
		prefillRecipient(&view, &prefill)
	}
	view.Address = pages.CheckoutAddress{
		Email: emailOf(r), Name: prefill.RecipientName, Phone: prefill.Phone,
		PostalCode: prefill.PostalCode, City: prefill.City,
		District: prefill.District, Street: prefill.Street,
	}
	if restored {
		h.applyDraft(r, &view, &prefill, &draft)
		view.RecipientMe = view.OffersTheProfile() && matchesAccountRecipient(view.Profile, view.Address.Name, view.Address.Phone)
	}
	status := h.applyReturnedStore(r, &view)
	if !view.HasShipping() {
		h.renderCheckout(w, r, status, &view)
		return
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
	h.renderCheckout(w, r, status, &view)
}

func matchesAccountRecipient(p pages.CheckoutProfile, name, phone string) bool {
	if p.Name == "" && p.Phone == "" {
		return false
	}
	return (p.Name == "" || name == p.Name) && (p.Phone == "" || phone == p.Phone)
}

// A saved address brings its own recipient, which may be somebody else, and a
// restored draft brings what was typed, so only empty fields are filled.
func prefillRecipient(view *pages.CheckoutView, prefill *order.Delivery) {
	if !view.OffersTheProfile() {
		return
	}
	if prefill.RecipientName == "" {
		prefill.RecipientName = view.Profile.Name
	}
	if prefill.Phone == "" {
		prefill.Phone = view.Profile.Phone
	}
	view.RecipientMe = matchesAccountRecipient(view.Profile, prefill.RecipientName, prefill.Phone)
}

// Ticking overwrites on purpose and remembers what was there; unticking
// restores it only into a field still holding the account's value, so text
// typed since is never wiped. goen.js does the same with scripting on.
func applyRecipient(view *pages.CheckoutView, addr *order.Delivery) {
	if view.RecipientMe {
		takeAccountRecipient(view, addr)
	} else {
		returnToTypedRecipient(view, addr)
	}
	view.RecipientMe = matchesAccountRecipient(view.Profile, addr.RecipientName, addr.Phone)
}

func takeAccountRecipient(view *pages.CheckoutView, addr *order.Delivery) {
	p := view.Profile
	if p.Name != "" {
		if addr.RecipientName != p.Name {
			view.RecipientPrevName = addr.RecipientName
		}
		addr.RecipientName = p.Name
	}
	if p.Phone != "" {
		if addr.Phone != p.Phone {
			view.RecipientPrevPhone = addr.Phone
		}
		addr.Phone = p.Phone
	}
}

func returnToTypedRecipient(view *pages.CheckoutView, addr *order.Delivery) {
	p := view.Profile
	if p.Name != "" && addr.RecipientName == p.Name {
		addr.RecipientName, view.RecipientPrevName = view.RecipientPrevName, ""
	}
	if p.Phone != "" && addr.Phone == p.Phone {
		addr.Phone, view.RecipientPrevPhone = view.RecipientPrevPhone, ""
	}
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (h *Handler) restoredDraft(r *http.Request, cartID uuid.UUID) (checkoutDraft, bool) {
	q := r.URL.Query()
	if q.Get("pickup_n") == "" && q.Get("draft") != "1" {
		return checkoutDraft{}, false
	}
	d, ok, err := h.store.checkoutDraft(r.Context(), cartID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read checkout draft", "error", err)
		return checkoutDraft{}, false
	}
	return d, ok
}

// The draft is the whole form as it stood, so it replaces the account's and the
// address book's prefill; the signed-in customer's address stays the account's.
func (h *Handler) applyDraft(
	r *http.Request, view *pages.CheckoutView, prefill *order.Delivery, d *checkoutDraft,
) {
	address := d.Email
	if signedIn := emailOf(r); signedIn != "" {
		address = signedIn
	}
	prefill.RecipientName, prefill.Phone = d.Name, d.Phone
	prefill.PostalCode, prefill.City = d.PostalCode, d.City
	prefill.District, prefill.Street = d.District, d.Street
	view.Address = pages.CheckoutAddress{
		Email: address, Name: d.Name, Phone: d.Phone,
		PostalCode: d.PostalCode, City: d.City, District: d.District, Street: d.Street,
		PickupChain: d.Chain, Note: d.Note,
	}
	if d.SavedAddress != "" {
		view.ChosenAddress = d.SavedAddress
	}
	view.Invoice = pages.CheckoutInvoice{
		Type: invoicepkg.Preference(d.InvoiceType), MobileBarcode: d.MobileBarcode,
		DonationCode: d.DonationCode, CompanyName: d.CompanyName, TaxID: d.TaxID,
	}
	if d.Coupon != "" {
		if msg, _ := h.applyCoupon(r, view, d.Coupon); msg != "" {
			view.Errors = map[string]string{"coupon": msg}
		}
	}
}

// PickupStart receives the button that submits the checkout form to goen, so what the shopper
// typed is saved before they leave; the carrier gets only the map form on the
// page this answers, which holds none of it.
func (h *Handler) PickupStart(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	cartID, ok := h.requiredCart(w, r)
	if !ok {
		return
	}
	guess, ok := h.reserveCouponGuess(w, r, cartID)
	if !ok {
		return
	}
	defer guess.settle(false)

	// The same reading of the form as an order's: the store is dropped unless
	// this browser vouches for it, and the coupon is checked behind the same
	// limiter.
	submission, ok := h.checkoutSubmission(w, r, cartID, ownerOf(r), checkoutAttemptID{}, false)
	if !ok {
		return
	}
	guess.settle(submission.couponMissed)
	view, addr, inv := &submission.view, &submission.address, &submission.invoice

	draft := checkoutDraft{
		Email: addr.Email, Name: addr.RecipientName, Phone: addr.Phone,
		PostalCode: addr.PostalCode, City: addr.City, District: addr.District, Street: addr.Street,
		Note: addr.Note, Chain: addr.PickupChain,
		Shipping: view.Chosen, SavedAddress: view.ChosenAddress,
		InvoiceType: string(inv.Type), MobileBarcode: inv.MobileBarcode, DonationCode: inv.DonationCode,
		CompanyName: inv.CompanyName, TaxID: inv.TaxID,
	}
	if submission.couponErr == "" {
		draft.Coupon = view.CouponCode
	}
	if err := h.store.saveCheckoutDraft(r.Context(), cartID, &draft); err != nil {
		h.log.ErrorContext(r.Context(), "keep the checkout draft", "error", err)
		h.serverError(w, r)
		return
	}

	back := "/checkout?draft=1"
	if !h.storeMap.Enabled() || destinationOf(view.Shipping, view.Chosen) != destination.PickupPoint {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if _, ok := h.storeMap.Subtype(addr.PickupChain); !ok {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if _, ok := h.pickupSession(w, r, view.Chosen, string(inv.Type), view.ChosenAddress); !ok {
		h.serverError(w, r)
		return
	}
	// A GET hand-off, so the Back button from the carrier's map lands on an
	// ordinary page with nothing to repost.
	http.Redirect(w, r, pages.PickupMapPath, http.StatusSeeOther)
}

// PickupMap serves a page with nothing the shopper typed on it or in its URL.
func (h *Handler) PickupMap(w http.ResponseWriter, r *http.Request) {
	const back = "/checkout?draft=1"
	cartID, ok := h.requiredCart(w, r)
	if !ok {
		return
	}
	draft, found, err := h.store.checkoutDraft(r.Context(), cartID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read checkout draft", "error", err)
		h.serverError(w, r)
		return
	}
	state, known := readPickupCookie(r, h.secure)
	if !found || !known {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	form, ok := h.mapRequest(r, draft.Chain, state.Nonce)
	if !ok {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	web.Render(w, r, h.log, http.StatusOK, pages.PickupStart(form, back))
}

// checkoutChoices reads the pickup cookie ONLY on the one hop back from the
// map: a plain visit keeps today's defaults rather than silently restoring an
// older choice.
func (h *Handler) checkoutChoices(r *http.Request) pickupState {
	q := r.URL.Query()
	chosen := pickupState{
		Ship: q.Get("ship"), Invoice: q.Get("invoice"), Address: q.Get("address"),
	}
	if q.Get("pickup_n") == "" {
		return chosen
	}
	saved, ok := readPickupCookie(r, h.secure)
	if !ok {
		return chosen
	}
	if chosen.Ship == "" {
		chosen.Ship = saved.Ship
	}
	if chosen.Invoice == "" {
		chosen.Invoice = saved.Invoice
	}
	if chosen.Address == "" {
		chosen.Address = saved.Address
	}
	return chosen
}

// applyReturnedStore echoes nothing that arrived on a refusal: the page says a
// store could not be confirmed and asks again.
func (h *Handler) applyReturnedStore(r *http.Request, view *pages.CheckoutView) int {
	// Without a carrier no store can have been chosen, so these keys mean
	// nothing.
	if !h.storeMap.Enabled() {
		return http.StatusOK
	}
	q := r.URL.Query()
	posted := PostedStore{
		Chain: pickup.Chain(q.Get("pickup_chain")),
		Code:  q.Get("pickup_store_code"),
		Name:  q.Get("pickup_store_name"),
		Nonce: q.Get("pickup_n"),
	}
	if posted.Empty() {
		return http.StatusOK
	}
	state, known := readPickupCookie(r, h.secure)
	if !honourPickupStore(posted, state, known) {
		h.logPickupRefusal(r, posted)
		view.PickupRefused = true
		return http.StatusUnprocessableEntity
	}
	view.Address.PickupChain = posted.Chain
	view.Address.PickupStoreCode = posted.Code
	view.Address.PickupStoreName = posted.Name
	// Display only: the address is never posted back or stored, so it cannot
	// outlive the page it is read on.
	view.PickupStoreAddr = q.Get("pickup_store_addr")
	return http.StatusOK
}

// PickupReturn is the one route exempt from goen's cross-origin defence, so it
// is worth nothing to an attacker who drives it: it validates SHAPE only, reads
// and sets no cookie, touches no database, and answers a page that sends the
// browser to a fixed local path with the store as encoded query parameters.
// Whether the store is honoured is decided at /checkout, against a nonce this
// browser alone holds.
func (h *Handler) PickupReturn(w http.ResponseWriter, r *http.Request) {
	// Smaller than web.MaxFormBytes: the callback is under 300 bytes and this
	// endpoint takes unauthenticated traffic.
	r.Body = http.MaxBytesReader(w, r.Body, maxCallbackFieldBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	store, ok := h.storeMap.readCallback(r)
	if !ok {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyPickupStoreUnreadable), http.StatusBadRequest)
		return
	}
	// Not cacheable: the body carries a store a third party chose for whoever
	// holds this browser.
	w.Header().Set("Cache-Control", "no-store")
	web.Render(w, r, h.log, http.StatusOK, pages.PickupReturn(store.refreshTarget()))
}

// dropUnvouchedStore is the placement half of the same rule: a store number or
// name must arrive with a nonce this browser's own cookie matches, or both
// halves are blanked and the page says a store could not be confirmed.
//
// It also drops a store belonging to the OTHER chain: a parcel waiting at a
// 7-ELEVEN is not waiting at a 全家. That comparison is consistency, not
// security, since both halves come from the same form.
func (h *Handler) dropUnvouchedStore(r *http.Request, addr *order.Delivery) bool {
	if !h.storeMap.Enabled() {
		// With no picker there is nothing to vouch for; the only writer of
		// these fields is the back office through its own form.
		return false
	}
	if addr.PickupStoreCode == "" && addr.PickupStoreName == "" {
		return false
	}
	posted := PostedStore{
		Chain: addr.PickupChain,
		Code:  addr.PickupStoreCode,
		Name:  addr.PickupStoreName,
		Nonce: r.PostFormValue("pickup_n"),
	}
	state, known := readPickupCookie(r, h.secure)
	sameChain := r.PostFormValue("pickup_store_chain") == string(addr.PickupChain)
	if sameChain && honourPickupStore(posted, state, known) {
		return false
	}
	addr.PickupStoreCode, addr.PickupStoreName = "", ""
	if sameChain {
		h.logPickupRefusal(r, posted)
	}
	return sameChain
}

func (h *Handler) logPickupRefusal(r *http.Request, posted PostedStore) {
	// The nonce authorizes a selection; diagnostics must not disclose it or the
	// customer's destination to anyone who can read logs.
	h.log.WarnContext(r.Context(), "pickup store refused",
		"method", r.Method,
		"pickup_cookie_count", len(r.CookiesNamed(pickupCookieName(h.secure))),
		"nonce_valid", validNonce(posted.Nonce),
		"nonce_matched", pickupNonceMatched(r, h.secure, posted.Nonce),
		"chain_offered", offeredAtCheckout(posted.Chain))
}

// renderCheckout refreshes the pickup cookie first: it must be written before
// the body, and every render is one the shopper may leave for the map from.
func (h *Handler) renderCheckout(
	w http.ResponseWriter, r *http.Request, status int, view *pages.CheckoutView,
) {
	if !view.HasShipping() {
		view.Chosen = ""
		view.QuoteID = ""
		view.Destination = ""
	}
	h.offerTheStoreMap(w, r, view)
	web.Render(w, r, h.log, status, pages.Checkout(pages.CheckoutMeta(r.Context()), view))
}

// offerTheStoreMap keeps the nonce across renders, since reload and Back must
// keep working, while everything else is rebuilt from this request so an
// invoice or address choice never comes back stale.
//
// The button it enables submits the checkout form to PickupStart; the carrier's
// own form is built there, on a page that holds nothing the shopper typed.
func (h *Handler) offerTheStoreMap(
	w http.ResponseWriter, r *http.Request, view *pages.CheckoutView,
) {
	if !h.storeMap.Enabled() || !view.ToPickupPoint() {
		return
	}
	nonce, ok := h.pickupSession(w, r, view.Chosen, string(view.Invoice.Type), view.ChosenAddress)
	if !ok {
		return
	}
	view.PickupNonce = nonce

	_, view.MapOffered = h.storeMap.Subtype(view.Address.PickupChain)
}

func (h *Handler) pickupSession(
	w http.ResponseWriter, r *http.Request, ship, invoice, address string,
) (nonce string, ok bool) {
	state, ok := readPickupCookie(r, h.secure)
	if !ok {
		n, err := newPickupNonce()
		if err != nil {
			h.log.ErrorContext(r.Context(), "open a pickup nonce", "error", err)
			return "", false
		}
		state = pickupState{Nonce: n}
	}
	state.Ship, state.Invoice, state.Address = ship, invoice, address
	writePickupCookie(w, state, h.secure)
	return state.Nonce, true
}

func (h *Handler) mapRequest(
	r *http.Request, chain pickup.Chain, nonce string,
) (pages.CheckoutMapForm, bool) {
	tradeNo, err := NewMerchantTradeNo()
	if err != nil {
		h.log.ErrorContext(r.Context(), "open a map correlation number", "error", err)
		return pages.CheckoutMapForm{}, false
	}
	return h.storeMap.Request(chain, tradeNo, nonce, wantsTheMobileMap(r))
}

// 7-ELEVEN serves a different map to a phone; 全家's is responsive and ignores
// the field.
func wantsTheMobileMap(r *http.Request) bool {
	return strings.Contains(r.Header.Get("User-Agent"), "Mobile")
}

func emailOf(r *http.Request) string {
	if u, ok := user.FromContext(r.Context()); ok {
		return u.Email
	}
	return ""
}

func (h *Handler) rememberOrder(w http.ResponseWriter, r *http.Request, number string) error {
	if err := h.access.Grant(w, r, number); err != nil {
		h.log.ErrorContext(r.Context(), "remember order", "error", err, "order", number)
		return err
	}
	return nil
}

func (h *Handler) PlaceOrder(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	cartID, ok := h.requiredCart(w, r)
	if !ok {
		return
	}
	attemptID, attemptErr := parseCheckoutAttemptID(r.PostFormValue("idempotency"))
	if attemptErr == nil && h.answerPriorCheckout(w, r, cartID, attemptID) {
		return
	}

	guess, ok := h.reserveCouponGuess(w, r, cartID)
	if !ok {
		return
	}
	defer guess.settle(false)

	owner := ownerOf(r)
	submission, ok := h.checkoutSubmission(w, r, cartID, owner, attemptID, attemptErr == nil)
	if !ok {
		return
	}
	guess.settle(submission.couponMissed)

	// A CHOOSER CHANGE, not an order: the delivery method, saved address and 發票
	// type each decide which fields the form asks for, so it re-renders with
	// the submitted values and validates nothing.
	if r.PostFormValue("update") != "" {
		h.renderCheckout(w, r, http.StatusOK, &submission.view)
		return
	}

	shown, ok := h.validateCheckoutSubmission(w, r, cartID, submission)
	if !ok {
		return
	}

	if attemptErr != nil {
		// Only a canonical server identity can name a retry; otherwise keep the
		// fresh identity and require confirmation before it can write anything.
		submission.view.Repriced = i18n.T(r.Context(), i18n.KeyCheckoutChanged)
		h.renderCheckout(w, r, http.StatusUnprocessableEntity, &submission.view)
		return
	}

	if !h.checkMobileBarcode(w, r, &submission.invoice, &submission.view) {
		return
	}

	number, err := h.store.placeOrder(
		r.Context(), cartID, owner, submission.shippingID, &submission.address,
		&submission.invoice, submission.view.CouponCode,
		shown, attemptID,
	)
	h.answerPlacement(w, r, cartID, &submission.address, &submission.view, number, err)
}

// answerPriorCheckout handles the lost-response retry before examining the cart
// a successful checkout deliberately emptied.
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
	if err := h.rememberOrder(w, r, prior); err != nil {
		h.placementGrantFailed(w, r)
		return true
	}
	http.Redirect(w, r, "/orders/"+prior+"/pay", http.StatusSeeOther) //nolint:gosec // server-generated order number
	return true
}

// checkoutSubmission rebuilds server-owned checkout state and keeps the
// customer's submitted fields; ok is false only after this method has answered
// the request.
type checkoutSubmission struct {
	view         pages.CheckoutView
	address      order.Delivery
	invoice      Invoice
	shippingID   uuid.UUID
	shippingErr  error
	couponErr    string
	couponMissed bool
}

func (h *Handler) checkoutSubmission(
	w http.ResponseWriter,
	r *http.Request,
	cartID uuid.UUID,
	owner uuid.NullUUID,
	attemptID checkoutAttemptID,
	attemptOK bool,
) (*checkoutSubmission, bool) {
	addr := order.Delivery{
		Email:           r.PostFormValue("email"),
		RecipientName:   r.PostFormValue("name"),
		Phone:           r.PostFormValue("phone"),
		PostalCode:      r.PostFormValue("postal_code"),
		City:            r.PostFormValue("city"),
		District:        r.PostFormValue("district"),
		Street:          r.PostFormValue("street"),
		PickupChain:     pickup.Chain(r.PostFormValue("pickup_chain")),
		PickupStoreCode: r.PostFormValue("pickup_store_code"),
		PickupStoreName: r.PostFormValue("pickup_store_name"),
		Note:            r.PostFormValue("note"),
	}
	addr.Trim()
	// The same rule as the GET: without it a 422 re-render would hand a dropped
	// store back in its hidden inputs and the second submit would place the
	// order with it.
	storeRefused := h.dropUnvouchedStore(r, &addr)

	view, err := h.checkoutView(r.Context(), cartID, owner)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read checkout", "error", err)
		h.serverError(w, r)
		return nil, false
	}
	view.PickupRefused = storeRefused
	// Another tab may already have emptied or invalidated this cart; Store
	// keeps the same rule transactionally.
	if !view.Cart.CanCheckout() {
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
		return nil, false
	}

	view.ChosenAddress = r.PostFormValue("address")
	view.RecipientMe = r.PostFormValue("recipient_me") == "1"
	view.RecipientPrevName = clip(r.PostFormValue("recipient_prev_name"))
	view.RecipientPrevPhone = clip(r.PostFormValue("recipient_prev_phone"))
	switch r.PostFormValue("update") {
	case "address":
		fillFromBook(&view, &addr, view.ChosenAddress)
		if view.ChosenAddress == pages.OtherAddress {
			addr.PostalCode, addr.City, addr.District, addr.Street = "", "", "", ""
		}
	case "recipient":
		applyRecipient(&view, &addr)
	}
	if view.OffersTheProfile() {
		view.RecipientMe = matchesAccountRecipient(view.Profile, addr.RecipientName, addr.Phone)
	}
	view.Address = pages.CheckoutAddress{
		Email: addr.Email, Name: addr.RecipientName, Phone: addr.Phone,
		PostalCode: addr.PostalCode, City: addr.City,
		District: addr.District, Street: addr.Street,
		PickupChain: addr.PickupChain, PickupStoreCode: addr.PickupStoreCode,
		PickupStoreName: addr.PickupStoreName, Note: addr.Note,
	}
	view.Chosen = r.PostFormValue("shipping")
	to := destinationOf(view.Shipping, view.Chosen)
	view.Destination = to
	addr.To = to
	// Invalid form text never survives as internal state; a valid retry keeps
	// its exact identity.
	if attemptOK {
		view.IdempotencyKey = attemptID.String()
	}

	inv := Invoice{
		Type:          invoicepkg.Preference(r.PostFormValue("invoice_type")),
		MobileBarcode: r.PostFormValue("invoice_carrier"),
		DonationCode:  r.PostFormValue("invoice_donation_code"),
		CompanyName:   r.PostFormValue("invoice_company_name"),
		TaxID:         r.PostFormValue("invoice_tax_id"),
	}
	view.Invoice = pages.CheckoutInvoice{
		Type: inv.Type, MobileBarcode: inv.MobileBarcode, DonationCode: inv.DonationCode,
		CompanyName: inv.CompanyName, TaxID: inv.TaxID,
	}

	couponErr, missed := h.resolveCoupon(r, &view)
	if couponErr != "" {
		view.Errors = map[string]string{"coupon": couponErr}
	}
	if !view.HasShipping() {
		return &checkoutSubmission{
			view: view, address: addr, invoice: inv,
			couponErr: couponErr, couponMissed: missed,
		}, true
	}
	shippingID, shipErr := uuid.Parse(view.Chosen)
	if shipErr == nil {
		if quoteErr := h.quoteCheckoutShipping(
			r.Context(), &view, shippingID, addr.PostalCode,
		); quoteErr != nil {
			h.log.ErrorContext(r.Context(), "quote shipping", "error", quoteErr)
			view.Repriced = i18n.T(r.Context(), i18n.KeyShippingUnpriceable)
		}
	}
	if shipErr == nil && view.Repriced == "" {
		if quoteErr := setCheckoutQuoteID(cartID, &view); quoteErr != nil {
			h.log.ErrorContext(r.Context(), "build refreshed checkout quote", "error", quoteErr)
			h.serverError(w, r)
			return nil, false
		}
	}
	return &checkoutSubmission{
		view: view, address: addr, invoice: inv,
		shippingID: shippingID, shippingErr: shipErr, couponErr: couponErr,
		couponMissed: missed,
	}, true
}

func (h *Handler) validateCheckoutSubmission(
	w http.ResponseWriter,
	r *http.Request,
	cartID uuid.UUID,
	submission *checkoutSubmission,
) (checkoutQuoteID, bool) {
	if !submission.view.HasShipping() {
		if submission.couponErr != "" {
			submission.view.Errors = map[string]string{"coupon": submission.couponErr}
		}
		h.renderCheckout(w, r, http.StatusUnprocessableEntity, &submission.view)
		return checkoutQuoteID{}, false
	}
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
	if submission.shippingErr == nil && submission.view.Repriced == "" && currentErr != nil {
		h.log.ErrorContext(r.Context(), "build submitted checkout quote", "error", currentErr)
		h.serverError(w, r)
		return checkoutQuoteID{}, false
	}
	if shownErr != nil || currentErr != nil || shown != current {
		submission.view.Repriced = i18n.T(r.Context(), i18n.KeyCheckoutChanged)
	}
	if len(errs) > 0 || submission.view.Repriced != "" {
		submission.view.Errors = errs
		h.renderCheckout(w, r, http.StatusUnprocessableEntity, &submission.view)
		return checkoutQuoteID{}, false
	}
	return shown, true
}

func (h *Handler) answerPlacement(
	w http.ResponseWriter, r *http.Request,
	cartID uuid.UUID, addr *order.Delivery, view *pages.CheckoutView, number string, err error,
) {
	switch {
	case err == nil:
		if rememberErr := h.rememberOrder(w, r, number); rememberErr != nil {
			h.placementGrantFailed(w, r)
			return
		}
		// Left behind, the nonce would still vouch for a store in the next
		// checkout this browser starts.
		clearPickupCookie(w, h.secure)
		// Straight to payment: a confirmation shown before payment reads as
		// done to a customer who then closes the tab.
		http.Redirect(w, r, "/orders/"+number+"/pay", http.StatusSeeOther) //nolint:gosec // G710: server-generated order number
	case errors.Is(err, ErrEmpty):
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
	case errors.Is(err, ErrUnavailable):
		// Something sold out between the cart page and this write; the cart
		// page says which line.
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
	case errors.Is(err, ErrTooManyItems):
		view.Repriced = i18n.T(r.Context(), i18n.KeyCartLineLimit)
		h.renderCheckout(w, r, http.StatusUnprocessableEntity, view)
	case errors.Is(err, ErrMixedTaxTypes):
		view.Repriced = i18n.T(r.Context(), i18n.KeyCartMixedTaxTypes)
		h.renderCheckout(w, r, http.StatusUnprocessableEntity, view)
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
	case errors.Is(err, ErrBusy):
		// Warn: a lock wait that outlived statement_timeout is a busy shop, not
		// a fault.
		h.log.WarnContext(r.Context(), "checkout timed out waiting for a lock", "error", err)
		view.Repriced = i18n.T(r.Context(), i18n.KeyCheckoutBusy)
		h.renderCheckout(w, r, http.StatusUnprocessableEntity, view)
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
	h.renderCheckout(w, r, http.StatusUnprocessableEntity, view)
}

func (h *Handler) answerCheckoutChanged(
	w http.ResponseWriter,
	r *http.Request,
	cartID uuid.UUID,
	addr *order.Delivery,
	view *pages.CheckoutView,
) {
	if err := h.refreshCheckoutState(r.Context(), cartID, ownerOf(r), addr, view); err != nil {
		h.log.ErrorContext(r.Context(), "refresh changed checkout quote", "error", err)
		h.serverError(w, r)
		return
	}
	if couponErr, _ := h.resolveCoupon(r, view); couponErr != "" {
		view.Errors = map[string]string{"coupon": couponErr}
	}
	view.Repriced = i18n.T(r.Context(), i18n.KeyCheckoutChanged)
	if err := setCheckoutQuoteID(cartID, view); err != nil {
		h.log.ErrorContext(r.Context(), "build changed checkout quote", "error", err)
		h.serverError(w, r)
		return
	}
	h.renderCheckout(w, r, http.StatusUnprocessableEntity, view)
}

func (h *Handler) answerCheckoutKeyConflict(
	w http.ResponseWriter, r *http.Request, cartID uuid.UUID, view *pages.CheckoutView,
) {
	// The key is a retry identity, not order access: do not reveal which cart
	// owns a collision.
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
	h.renderCheckout(w, r, http.StatusUnprocessableEntity, view)
}

func (h *Handler) answerMissingShipping(
	w http.ResponseWriter,
	r *http.Request,
	cartID uuid.UUID,
	addr *order.Delivery,
	view *pages.CheckoutView,
) {
	if err := h.refreshCheckoutState(r.Context(), cartID, ownerOf(r), addr, view); err != nil {
		h.log.ErrorContext(r.Context(), "refresh checkout after missing shipping", "error", err)
		h.serverError(w, r)
		return
	}
	view.Errors = nil
	if view.HasShipping() {
		view.Errors = map[string]string{"shipping": i18n.T(r.Context(), i18n.KeyChooseShipping)}
	}
	if err := setCheckoutQuoteID(cartID, view); err != nil {
		h.log.ErrorContext(r.Context(), "build replacement shipping quote", "error", err)
		h.serverError(w, r)
		return
	}
	h.renderCheckout(w, r, http.StatusUnprocessableEntity, view)
}

func (h *Handler) answerCouponRefusal(
	w http.ResponseWriter,
	r *http.Request,
	cartID uuid.UUID,
	addr *order.Delivery,
	view *pages.CheckoutView,
	refusal error,
) {
	// Coupon eligibility is authoritative inside the transaction: reload the
	// cart and quote without the rejected coupon.
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
	h.renderCheckout(w, r, http.StatusUnprocessableEntity, view)
}

// refreshCheckoutState keeps the typed address, invoice, coupon code and
// idempotency key; catalogue, shipping and credit are replaced.
func (h *Handler) refreshCheckoutState(
	ctx context.Context,
	cartID uuid.UUID,
	owner uuid.NullUUID,
	addr *order.Delivery,
	view *pages.CheckoutView,
) error {
	cartView, err := h.store.View(ctx, cartID)
	if err != nil {
		return fmt.Errorf("read current cart: %w", err)
	}
	choices, err := h.offeredShipping(ctx, cartID, cartView.SubtotalCents)
	if err != nil {
		return err
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
	if !view.HasShipping() {
		view.Chosen = ""
		view.QuoteID = ""
		view.Destination = ""
		return nil
	}

	if !slices.ContainsFunc(choices, func(choice pages.ShippingChoice) bool {
		return choice.VersionID == view.Chosen
	}) {
		view.Chosen = choices[0].VersionID
	}
	view.Destination = destinationOf(choices, view.Chosen)
	addr.To = view.Destination
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

// resolveCoupon's missed is a code looked up and refused, which is what
// couponMisses charges; a failed lookup is the shop's fault, not a guess.
func (h *Handler) resolveCoupon(r *http.Request, view *pages.CheckoutView) (message string, missed bool) {
	return h.applyCoupon(r, view, r.PostFormValue("coupon"))
}

// applyCoupon is reached by the posted form through resolveCoupon, behind the
// guess limiter; a draft restored after the map reaches it directly, which is
// safe because a draft only holds a code accepted when saved.
func (h *Handler) applyCoupon(r *http.Request, view *pages.CheckoutView, raw string) (message string, missed bool) {
	view.CouponCode = NormaliseCode(raw)
	if view.CouponCode == "" {
		return "", false
	}

	subtotal := view.Cart.SubtotalCents
	c, err := h.store.CouponByCode(r.Context(), view.CouponCode)
	if err == nil {
		discountCents, freeShipping, applyErr := c.Apply(subtotal)
		if applyErr == nil {
			view.CouponApplied = c.description
			view.CouponDiscountCents = discountCents
			view.CouponFreeShipping = freeShipping
			return "", false
		}
		err = applyErr
	}
	switch {
	case errors.Is(err, ErrCouponExpired):
		return i18n.T(r.Context(), i18n.KeyCouponExpired), true
	case errors.Is(err, ErrCouponMinimum):
		return i18n.T(r.Context(), i18n.KeyCouponBelowMinimum), true
	case errors.Is(err, ErrNoSuchCoupon):
		return i18n.T(r.Context(), i18n.KeyCouponUnknown), true
	default:
		h.log.ErrorContext(r.Context(), "resolve coupon", "error", err)
		return i18n.T(r.Context(), i18n.KeyCouponUnavailable), false
	}
}

// couponGuess holds nothing when the request carries no code.
type couponGuess []*ratelimit.Reservation

// reserveCouponGuess sets tokens aside BEFORE the lookup, because a refusal
// that came only after a miss would itself say the code was wrong; set aside
// rather than checked, because every request that passed while one token was
// left would be told about its code.
func (h *Handler) reserveCouponGuess(w http.ResponseWriter, r *http.Request, cartID uuid.UUID) (couponGuess, bool) {
	if NormaliseCode(r.PostFormValue("coupon")) == "" {
		return nil, true
	}
	keys := couponKeys(r, cartID)
	guess := make(couponGuess, 0, len(keys))
	for _, key := range keys {
		reservation, retryAfter, ok := h.couponMisses.Reserve(key)
		if !ok {
			guess.settle(false)
			ratelimit.Refuse(r.Context(), w, retryAfter)
			return nil, false
		}
		guess = append(guess, reservation)
	}
	return guess, true
}

// settle keeps the tokens when the code was looked up and refused and gives
// them back otherwise: a code that applies is never charged, nor a failed
// lookup. Only the first call counts.
func (g couponGuess) settle(missed bool) {
	for _, reservation := range g {
		if missed {
			reservation.Keep()
		} else {
			reservation.Refund()
		}
	}
}

func couponKeys(r *http.Request, cartID uuid.UUID) [2]string {
	holder := "cart:" + cartID.String()
	if owner := ownerOf(r); owner.Valid {
		holder = "account:" + owner.UUID.String()
	}
	return [2]string{"client:" + ratelimit.ClientKey(r), holder}
}

// checkoutErrors: a pickup order needs a store chosen on one of the two
// official maps; the schema only bounds the values, so a submission that
// skipped the map is refused here, whether or not this deployment can open it.
func checkoutErrors(
	ctx context.Context, addr *order.Delivery, shipErr error, inv *Invoice,
) map[string]string {
	refusals := addr.Validate()
	if shipErr != nil {
		refusals = append(refusals,
			web.FieldRefusal{Field: "shipping", MessageKey: i18n.KeyChooseShipping})
	}
	if addr.To == destination.PickupPoint {
		if !offeredAtCheckout(addr.PickupChain) {
			refusals = append(refusals,
				web.FieldRefusal{Field: "pickup_chain", MessageKey: i18n.KeyPickupChainRequired})
		} else if addr.PickupStoreCode == "" || addr.PickupStoreName == "" {
			refusals = append(refusals,
				web.FieldRefusal{Field: "pickup_store", MessageKey: i18n.KeyPickupStoreRequired})
		}
	}
	refusals = append(refusals, inv.Validate()...)
	return account.FieldMessages(ctx, refusals)
}

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

// offeredShipping drops pickup where the store map is not configured: a pickup
// order needs a store chosen on the map, so offering it would offer only a
// refusal.
func (h *Handler) offeredShipping(
	ctx context.Context, cartID uuid.UUID, subtotalCents int64,
) ([]pages.ShippingChoice, error) {
	choices, err := h.store.ShippingChoices(ctx, cartID, subtotalCents)
	if err != nil || h.storeMap.Enabled() {
		return choices, err
	}
	return slices.DeleteFunc(choices, func(c pages.ShippingChoice) bool {
		to, ok := destination.For(c.DestinationKind)
		return ok && to == destination.PickupPoint
	}), nil
}

func ownerOf(r *http.Request) uuid.NullUUID {
	u, ok := user.FromContext(r.Context())
	if !ok {
		return uuid.NullUUID{}
	}
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: id, Valid: true}
}

// An id that names nothing falls through to the default rather than an error
// page.
func fillFromBook(view *pages.CheckoutView, addr *order.Delivery, wanted string) {
	if len(view.SavedAddresses) == 0 {
		return
	}
	if wanted == pages.OtherAddress {
		view.ChosenAddress = pages.OtherAddress
		return
	}
	chosen := &view.SavedAddresses[0] // is_default first, then oldest
	for i := range view.SavedAddresses {
		if view.SavedAddresses[i].ID == wanted {
			chosen = &view.SavedAddresses[i]
		}
	}
	view.ChosenAddress = chosen.ID
	addr.RecipientName, addr.Phone = chosen.Name, chosen.Phone
	addr.PostalCode, addr.City = chosen.PostalCode, chosen.City
	addr.District, addr.Street = chosen.District, chosen.Street
}

// destinationOf reads the choices the server built, not the form.
func destinationOf(choices []pages.ShippingChoice, versionID string) destination.Kind {
	for i := range choices {
		if choices[i].VersionID == versionID {
			if d, ok := destination.For(choices[i].DestinationKind); ok {
				return d
			}
			return ""
		}
	}
	return ""
}

func (h *Handler) OrderPage(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")

	// Anything but the browser that placed the order or the account that owns
	// it gets the same answer as an order that does not exist.
	if !h.allows(r, number) {
		if orderaccess.ReloadSameSite(w, r, h.log) {
			return
		}
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
	view.PaymentRefreshURL = paymentReturnRefresh(r, &view)
	if view.PaymentRefreshURL != "" {
		w.Header().Set("Refresh", strconv.Itoa(paymentReturnRefreshSeconds)+"; url="+view.PaymentRefreshURL)
		view.PaymentRefreshSeconds = paymentReturnRefreshSeconds
		view.PaymentRefreshChecks = paymentReturnRefreshChecks
	}
	view.ShowWarrantyLink = h.ownedBySignedInUser(r, number)
	view.PaymentsEnabled = h.TakesPayment()
	web.Render(w, r, h.log, http.StatusOK, pages.Order(pages.OrderMeta(r.Context(), view.Number), &view))
}

const (
	paymentReturnRefreshSeconds = 5
	paymentReturnRefreshChecks  = 3
)

// A browser return is not payment evidence. Keep that hint when bounded checks
// end so a delayed webhook cannot turn it into an invitation to pay again.
func paymentReturnRefresh(r *http.Request, view *pages.OrderView) string {
	view.PaymentReturnHint = r.URL.Query().Get("paid") == "1"
	if !view.AwaitingPayment() || !view.PaymentReturnHint {
		return ""
	}
	attempt := 0
	if raw := r.URL.Query().Get("confirmation"); raw != "" {
		if raw == "done" {
			view.PaymentConfirmationPending = true
			return ""
		}
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed >= paymentReturnRefreshChecks {
			return ""
		}
		attempt = parsed
	}
	next := "/orders/" + url.PathEscape(view.Number) + "?paid=1&confirmation="
	if attempt < paymentReturnRefreshChecks-1 {
		next += strconv.Itoa(attempt + 1)
	} else {
		next += "done"
	}
	return next
}

func (h *Handler) ReorderItems(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !h.allows(r, number) {
		web.Render(w, r, h.log, http.StatusNotFound, pages.OrderNotFound(h.notFoundPage(r)))
		return
	}

	// The cart is CREATED if there is none: somebody reordering usually has an
	// empty one.
	cartID, err := h.cartForWrite(w, r)
	if err != nil {
		if errors.Is(r.Context().Err(), context.Canceled) {
			return
		}
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
	outcome := url.Values{
		"added":   {strconv.Itoa(result.Added)},
		"skipped": {strconv.Itoa(len(result.Skipped))},
	}
	if result.Adjusted {
		outcome.Set("qty", "adjusted")
	}
	http.Redirect(w, r, "/cart?"+outcome.Encode(), http.StatusSeeOther)
}

func (h *Handler) CancelOrder(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !h.allows(r, number) {
		web.Render(w, r, h.log, http.StatusNotFound, pages.OrderNotFound(h.notFoundPage(r)))
		return
	}

	sessions, err := h.store.CancelOrder(r.Context(), number)
	switch {
	case err == nil:
		payment.CloseSessions(r.Context(), h.sessions, h.log, number, sessions)
		http.Redirect(w, r, "/orders/"+url.PathEscape(number)+"?cancelled=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotCancellable):
		// 422, not a redirect: nothing was written.
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyCancelRefusedTitle)}, "",
			i18n.T(r.Context(), i18n.KeyCancelRefusedTitle),
			i18n.T(r.Context(), i18n.KeyCancelRefusedBody), pages.NoticeActions{
				Primary:   pages.NoticeLink{Href: "/orders/" + url.PathEscape(number), Label: i18n.KeyBackToOrder},
				Secondary: pages.NoticeLink{Href: "/contact", Label: i18n.KeyContact},
			}))
	default:
		h.log.ErrorContext(r.Context(), "cancel order", "error", err)
		h.serverError(w, r)
	}
}

func (h *Handler) allows(r *http.Request, number string) bool {
	ok, err := h.access.Allows(r, number)
	if err != nil {
		h.log.ErrorContext(r.Context(), "check order access", "order", number, "error", err)
	}
	return ok
}

func (h *Handler) ownedBySignedInUser(r *http.Request, number string) bool {
	owns, err := h.access.OwnedBySignedInUser(r, number)
	if err != nil {
		h.log.ErrorContext(r.Context(), "check order ownership", "error", err)
	}
	return owns
}

func (h *Handler) checkoutView(ctx context.Context, cartID uuid.UUID, owner uuid.NullUUID) (pages.CheckoutView, error) {
	cartView, err := h.store.View(ctx, cartID)
	if err != nil {
		return pages.CheckoutView{}, err
	}
	choices, err := h.offeredShipping(ctx, cartID, cartView.SubtotalCents)
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
	name, phone, err := h.store.customerProfile(ctx, owner)
	if err != nil {
		return pages.CheckoutView{}, err
	}
	view := pages.CheckoutView{
		Cart: cartView, Shipping: choices,
		InvoiceChoices: invoiceChoices(ctx),
		PickupChains:   pages.CheckoutPickupChainChoices(),
		SavedAddresses: saved,
		IdempotencyKey: attemptID.String(),
	}
	balance, err := h.store.AvailableCredit(ctx, owner)
	if err != nil {
		return pages.CheckoutView{}, err
	}
	view.AvailableCreditCents = balance
	if u, ok := user.FromContext(ctx); ok {
		view.Profile = pages.CheckoutProfile{Email: u.Email, Name: name, Phone: phone}
	}
	if len(choices) > 0 {
		view.Chosen = choices[0].VersionID
		view.Destination = destinationOf(choices, view.Chosen)
	}
	return view, nil
}

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
	// A refused code is typed text only; hashing it would make the page look
	// changed after the shopper clears it.
	couponCode := ""
	if view.HasCoupon() {
		couponCode = NormaliseCode(view.CouponCode)
	}
	return (checkoutQuote{
		CartID:            cartID,
		Lines:             lines,
		ShippingVersionID: shippingID,
		ShippingCents:     shippingCents,
		CouponCode:        couponCode,
		DiscountCents:     view.CouponDiscountCents,
		CreditCents:       creditCents,
	}).ID()
}

func setCheckoutQuoteID(cartID uuid.UUID, view *pages.CheckoutView) error {
	if !view.HasShipping() {
		view.QuoteID = ""
		return nil
	}
	id, err := checkoutQuoteIDForView(cartID, view)
	if err != nil {
		view.QuoteID = ""
		return err
	}
	view.QuoteID = id.String()
	return nil
}

// invoiceChoices comes from the issuer-owned closed set so rendering,
// validation, storage and ECPay cannot drift.
func invoiceChoices(ctx context.Context) []pages.InvoiceChoice {
	types := invoicepkg.OfferedPreferences()
	out := make([]pages.InvoiceChoice, 0, len(types))
	for _, t := range types {
		out = append(out, pages.InvoiceChoice{Value: t, Label: i18n.T(ctx, invoiceTypeLabelKey(t))})
	}
	return out
}

// A GET must not create a cart: a crawler would leave a row per visit.
func (h *Handler) existingCart(r *http.Request) (uuid.UUID, bool, error) {
	id, ok, _, err := h.lookupCart(r.Context(), r)
	return id, ok, err
}

func (h *Handler) requestCart(w http.ResponseWriter, r *http.Request) (id uuid.UUID, found, ok bool) {
	id, found, err := h.existingCart(r)
	if err == nil {
		return id, found, true
	}
	if errors.Is(r.Context().Err(), context.Canceled) {
		return uuid.Nil, false, false
	}
	h.log.ErrorContext(r.Context(), "read request cart", "error", err)
	h.serverError(w, r)
	return uuid.Nil, false, false
}

func (h *Handler) requiredCart(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, found, ok := h.requestCart(w, r)
	if !ok {
		return uuid.Nil, false
	}
	if !found {
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
		return uuid.Nil, false
	}
	return id, true
}

// A signed-in create attaches user_id so the next add does not mint an unowned
// cart the account can never see through the cookie. Create recovers a
// carts_one_per_user collision by rereading the winner.
func (h *Handler) cartForWrite(w http.ResponseWriter, r *http.Request) (uuid.UUID, error) {
	id, ok, err := h.existingCart(r)
	if err != nil {
		return uuid.Nil, err
	}
	if ok {
		return id, nil
	}
	token, err := NewToken()
	if err != nil {
		return uuid.Nil, err
	}
	id, err = h.store.Create(r.Context(), token, ownerOf(r))
	if err != nil {
		return uuid.Nil, err
	}
	SetCookie(w, token, h.secure)
	return id, nil
}

// lookupCart is the one place a request's cart is decided. The cookie reaches
// an unowned cart or the requester's own; a cart another account owns is no
// cart, and stale reports a cookie naming one. After a merge-adopt the cookie
// names the deleted guest row, so a signed-in miss falls through to the account
// cart.
func (h *Handler) lookupCart(ctx context.Context, r *http.Request) (id uuid.UUID, ok, stale bool, lookupErr error) {
	owner := ownerOf(r)
	if token := ReadCookie(r, h.secure); token != "" {
		tokenCart, err := h.store.ByToken(ctx, token, owner)
		if err == nil {
			return tokenCart, true, false, nil
		}
		if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrNotYourCart) {
			return uuid.Nil, false, false, err
		}
		stale = errors.Is(err, ErrNotYourCart)
	}
	if !owner.Valid {
		return uuid.Nil, false, stale, nil
	}
	accountCart, err := h.store.ForUser(ctx, owner.UUID.String())
	if errors.Is(err, ErrNotFound) {
		return uuid.Nil, false, stale, nil
	}
	if err != nil {
		return uuid.Nil, false, stale, err
	}
	return accountCart, true, stale, nil
}

// wishlistPath is the one page besides a product an add-to-cart form may send
// the shopper back to, matched exactly and never by a request value.
const wishlistPath = "/account/wishlist"

// backToProduct takes the slug from the form's own field rather than the
// Referer, which a request controls; the selection query is rebuilt from the
// variant the server accepted, not from anything the form named.
func (h *Handler) backToProduct(w http.ResponseWriter, r *http.Request, variantID uuid.UUID, outcome pages.AddOutcome) {
	if r.PostFormValue("return") == wishlistPath {
		http.Redirect(w, r, wishlistPath+"?added="+url.QueryEscape(string(outcome)), http.StatusSeeOther)
		return
	}
	slug := r.PostFormValue("back")
	if !isSlug(slug) {
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
		return
	}
	target, err := h.store.productReturnURL(r.Context(), slug, variantID, string(outcome))
	if err != nil {
		h.log.ErrorContext(r.Context(), "build product return url", "error", err)
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther) //nolint:gosec // G710: slug and selection validated server-side
}

// isSlug is what stops the "back" field from becoming an open redirect.
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

func (h *Handler) placementGrantFailed(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusInternalServerError, pages.PlacementGrantFailed(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyPlacementGrantFailedTitle)}))
}

func (h *Handler) findOrderGrantFailed(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusInternalServerError, pages.FindOrderGrantFailed(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyFindOrderGrantFailedTitle)}))
}

func (h *Handler) notFoundPage(r *http.Request) layouts.Page {
	return layouts.Page{Title: i18n.T(r.Context(), i18n.KeyOrderNotFound)}
}

func (h *Handler) IDForRequest(ctx context.Context, r *http.Request) (uuid.UUID, bool, error) {
	id, ok, _, err := h.lookupCart(ctx, r)
	return id, ok, err
}

// ForgetCart keeps the cookie only for a guest cart no account owns: an
// account's cart must not stay reachable for the next person at the browser,
// but a guest cart's cookie is the only way back to it.
func (h *Handler) ForgetCart(w http.ResponseWriter, r *http.Request) {
	token := ReadCookie(r, h.secure)
	if token == "" {
		return
	}
	// Asked on behalf of nobody, the lookup answers only for a cart no account
	// owns; anything else, a failed read included, forgets the cookie.
	_, err := h.store.ByToken(r.Context(), token, uuid.NullUUID{})
	if err == nil {
		return
	}
	if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrNotYourCart) {
		h.log.ErrorContext(r.Context(), "read the cart at sign-out", "error", err)
	}
	expireCookie(w, cookieName(h.secure), h.secure)
}

// ForgetOrders revokes the grants as well as the cookie, because a client can
// ignore an expiry. A failure to revoke is logged rather than stopping the
// session's end.
func (h *Handler) ForgetOrders(w http.ResponseWriter, r *http.Request) {
	if err := h.access.Revoke(w, r); err != nil {
		h.log.ErrorContext(r.Context(), "forget this browser's orders", "error", err)
	}
}

// WithCount resolves the cart on every visitor request, so it is also where a
// cookie naming another account's cart is expired; a handler that opens a new
// cart sets its own cookie after this one.
func (h *Handler) WithCount(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok, stale, lookupErr := h.lookupCart(r.Context(), r)
		if lookupErr != nil {
			if !errors.Is(r.Context().Err(), context.Canceled) {
				h.log.ErrorContext(r.Context(), "read cart for the item count", "error", lookupErr)
			}
			next.ServeHTTP(w, r)
			return
		}
		if stale {
			expireCookie(w, cookieName(h.secure), h.secure)
		}
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		n, err := h.store.ItemCount(r.Context(), id)
		if err != nil {
			if !errors.Is(r.Context().Err(), context.Canceled) {
				h.log.ErrorContext(r.Context(), "count cart items", "error", err)
			}
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(web.WithCartCount(r.Context(), n)))
	})
}

func (h *Handler) FindOrderPage(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusOK, pages.FindOrder(
		pages.FindOrderMeta(r.Context()), pages.FindOrderView{}))
}

// FindOrder writes the SAME cookie placing an order writes, and answers
// IDENTICALLY on every failure, since "that number exists but the address is
// wrong" hands over half a credential.
func (h *Handler) FindOrder(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	number := strings.ToUpper(strings.TrimSpace(r.PostFormValue("number")))
	addr := r.PostFormValue("email")

	// Bounded per IP and BEFORE the read: unbounded, this endpoint is an oracle
	// for the secret half of the pair.
	if retryAfter, ok := h.findLimit.Allow("findorder:" + ratelimit.ClientKey(r)); !ok {
		ratelimit.Refuse(r.Context(), w, retryAfter)
		return
	}
	// Keyed on the address as the lookup reads it, so case or spacing changes
	// are the same address; one longer than the policy names no order and is
	// not kept as a key.
	if key := email.Clean(addr); key != "" && len(key) <= email.Max {
		if retryAfter, ok := h.findPerAddress.Allow("findorder:" + key); !ok {
			ratelimit.Refuse(r.Context(), w, retryAfter)
			return
		}
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

	if err := h.rememberOrder(w, r, number); err != nil {
		h.findOrderGrantFailed(w, r)
		return
	}
	http.Redirect(w, r, "/orders/"+url.PathEscape(number), http.StatusSeeOther)
}
