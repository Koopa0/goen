package checkout

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/discount/coupon"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/shipping"
	"github.com/koopa0/goen/internal/shipping/storemap"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/ratelimit"
	"github.com/koopa0/goen/internal/web/respond"
)

type Handler struct {
	store *Store
	// carts is the cart a request names and the delivery it is offered.
	carts  *cart.Handler
	access *access.Store
	log    *slog.Logger
	secure bool
	// payments is nil or disabled on a deployment with no Stripe key, where the
	// checkout takes no payment.
	payments *payment.Gateway
	// storeMap is nil or disabled on a deployment with no carrier, where the
	// checkout offers no pickup.
	storeMap *storemap.Client
	// invoiceGateway is nil or disabled on a deployment with no 加值中心, where a
	// mobile barcode is checked for its shape only.
	invoiceGateway *issuer.Gateway
	// couponMisses bounds how many wrong coupon codes one shopper is told
	// about: a wrong code and a right one answer differently, so unbounded it
	// finds codes never handed out.
	couponMisses *ratelimit.Limiter
}

// NewHandler reads a nil or disabled payments as no provider configured, and a
// nil or disabled storeMap as no carrier, so the checkout offers no pickup, and
// a nil or disabled invoiceGateway as no 加值中心, so a mobile barcode keeps
// local shape validation.
func NewHandler(store *Store, carts *cart.Handler, grants *access.Store, log *slog.Logger, secure bool,
	payments *payment.Gateway, storeMap *storemap.Client, invoiceGateway *issuer.Gateway,
) *Handler {
	if store == nil || carts == nil || grants == nil || log == nil {
		panic("checkout: NewHandler requires a store, the cart, an access check and a logger")
	}
	return &Handler{
		store: store, carts: carts, access: grants, log: log, secure: secure,
		payments: payments, storeMap: storeMap, invoiceGateway: invoiceGateway,
		// Twenty wrong codes before the first refusal, then one every two
		// minutes: more than a shopper retyping a code from a flyer needs.
		couponMisses: ratelimit.New(ratelimit.Config{
			Every: 2 * time.Minute, Burst: 20, TTL: time.Hour, MaxKeys: 65_536,
		}),
	}
}

// Routes registers the checkout and the store-pickup round trip where a
// carrier is configured. submissions bounds each checkout submission per
// client; the caller owns that budget.
func (h *Handler) Routes(mux *http.ServeMux, submissions *ratelimit.Limiter) {
	mux.HandleFunc("GET /checkout", h.Page)
	mux.HandleFunc("POST /checkout", ratelimit.Guard(submissions, h.log, h.PlaceOrder))
	if h.storeMap.Enabled() {
		// The carrier's page posts the chosen store here from the SHOPPER'S
		// browser, so it arrives cross-site with none of goen's cookies. It
		// exists only where a carrier is configured: an unconfigured goen
		// answers 404 here and its cross-origin defence keeps no bypass.
		mux.HandleFunc("POST "+storemap.ReturnPath, h.PickupReturn)
		// The checkout form is posted here by 「選擇門市」: same-origin, so under
		// the cross-origin defence like every other form, and it keeps what was
		// typed before the browser is handed to the carrier.
		mux.HandleFunc("POST "+PickupStartAction, ratelimit.Guard(submissions, h.log, h.PickupStart))
		mux.HandleFunc("GET "+PickupMapPath, h.PickupMap)
	}
}

// TakesPayment holds where a payment key is configured.
func (h *Handler) TakesPayment() bool { return h.payments.Enabled() }

func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	cartID, ok := h.carts.Require(w, r)
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
	if slices.ContainsFunc(view.Shipping, func(choice cart.ShippingChoice) bool {
		return choice.VersionID == chosen.Ship
	}) {
		view.Chosen = chosen.Ship
	}
	view.Destination = destinationOf(view.Shipping, view.Chosen)
	// The 發票 choice can be named in the URL too, so a link opens the form with
	// that card chosen.
	if kind := invoice.Preference(chosen.Invoice); kind.Known() {
		view.Invoice.Type = kind
	}

	var prefill delivery.Details
	fillFromBook(&view, &prefill, chosen.Address)
	if !restored {
		prefillRecipient(&view, &prefill)
	}
	view.Address = Address{
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

func matchesAccountRecipient(p Profile, name, phone string) bool {
	if p.Name == "" && p.Phone == "" {
		return false
	}
	return (p.Name == "" || name == p.Name) && (p.Phone == "" || phone == p.Phone)
}

// A saved address brings its own recipient, which may be somebody else, and a
// restored draft brings what was typed, so only empty fields are filled.
func prefillRecipient(view *View, prefill *delivery.Details) {
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
func applyRecipient(view *View, addr *delivery.Details) {
	if view.RecipientMe {
		takeAccountRecipient(view, addr)
	} else {
		returnToTypedRecipient(view, addr)
	}
	view.RecipientMe = matchesAccountRecipient(view.Profile, addr.RecipientName, addr.Phone)
}

func takeAccountRecipient(view *View, addr *delivery.Details) {
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

func returnToTypedRecipient(view *View, addr *delivery.Details) {
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
	r *http.Request, view *View, prefill *delivery.Details, d *checkoutDraft,
) {
	address := d.Email
	if signedIn := emailOf(r); signedIn != "" {
		address = signedIn
	}
	prefill.RecipientName, prefill.Phone = d.Name, d.Phone
	prefill.PostalCode, prefill.City = d.PostalCode, d.City
	prefill.District, prefill.Street = d.District, d.Street
	view.Address = Address{
		Email: address, Name: d.Name, Phone: d.Phone,
		PostalCode: d.PostalCode, City: d.City, District: d.District, Street: d.Street,
		PickupChain: d.Chain, Note: d.Note,
	}
	if d.SavedAddress != "" {
		view.ChosenAddress = d.SavedAddress
	}
	view.Invoice = InvoiceView{
		Type: invoice.Preference(d.InvoiceType), MobileBarcode: d.MobileBarcode,
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
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	cartID, ok := h.carts.Require(w, r)
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

	// Every invoice type's fields are in the page, so the form carries what was typed under
	// the types not chosen. The draft is personal data and keeps the chosen type's only; the
	// refusals are for a placement, not for a trip to the map.
	inv.Validate()
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
	if !h.storeMap.Enabled() || destinationOf(view.Shipping, view.Chosen) != shipping.DestinationPickupPoint {
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
	http.Redirect(w, r, PickupMapPath, http.StatusSeeOther)
}

// PickupMap serves a page with nothing the shopper typed on it or in its URL.
func (h *Handler) PickupMap(w http.ResponseWriter, r *http.Request) {
	const back = "/checkout?draft=1"
	cartID, ok := h.carts.Require(w, r)
	if !ok {
		return
	}
	draft, found, err := h.store.checkoutDraft(r.Context(), cartID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read checkout draft", "error", err)
		h.serverError(w, r)
		return
	}
	state, known := storemap.ReadCookie(r, h.secure)
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
	respond.Component(w, r, h.log, http.StatusOK, storemap.StartPage(form, back))
}

// checkoutChoices reads the pickup cookie ONLY on the one hop back from the
// map: a plain visit keeps today's defaults rather than silently restoring an
// older choice.
func (h *Handler) checkoutChoices(r *http.Request) storemap.State {
	q := r.URL.Query()
	chosen := storemap.State{
		Ship: q.Get("ship"), Invoice: q.Get("invoice"), Address: q.Get("address"),
	}
	if q.Get("pickup_n") == "" {
		return chosen
	}
	saved, ok := storemap.ReadCookie(r, h.secure)
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
func (h *Handler) applyReturnedStore(r *http.Request, view *View) int {
	// Without a carrier no store can have been chosen, so these keys mean
	// nothing.
	if !h.storeMap.Enabled() {
		return http.StatusOK
	}
	q := r.URL.Query()
	posted := storemap.PostedStore{
		Chain: shipping.Chain(q.Get("pickup_chain")),
		Code:  q.Get("pickup_store_code"),
		Name:  q.Get("pickup_store_name"),
		Nonce: q.Get("pickup_n"),
	}
	if posted.Empty() {
		return http.StatusOK
	}
	state, known := storemap.ReadCookie(r, h.secure)
	if !storemap.Honoured(posted, state, known) {
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
	// Smaller than formtext.MaxFormBytes: the callback is under 300 bytes and this
	// endpoint takes unauthenticated traffic.
	r.Body = http.MaxBytesReader(w, r.Body, storemap.MaxCallbackBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	store, ok := h.storeMap.ReadCallback(r)
	if !ok {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyPickupStoreUnreadable), http.StatusBadRequest)
		return
	}
	// Not cacheable: the body carries a store a third party chose for whoever
	// holds this browser.
	w.Header().Set("Cache-Control", "no-store")
	respond.Component(w, r, h.log, http.StatusOK, storemap.ReturnPage(store.RefreshTarget()))
}

// dropUnvouchedStore is the placement half of the same rule: a store number or
// name must arrive with a nonce this browser's own cookie matches, or both
// halves are blanked and the page says a store could not be confirmed.
//
// It also drops a store belonging to the OTHER chain: a parcel waiting at a
// 7-ELEVEN is not waiting at a 全家. That comparison is consistency, not
// security, since both halves come from the same form.
func (h *Handler) dropUnvouchedStore(r *http.Request, addr *delivery.Details) bool {
	if !h.storeMap.Enabled() {
		// With no picker there is nothing to vouch for; the only writer of
		// these fields is the back office through its own form.
		return false
	}
	if addr.PickupStoreCode == "" && addr.PickupStoreName == "" {
		return false
	}
	posted := storemap.PostedStore{
		Chain: addr.PickupChain,
		Code:  addr.PickupStoreCode,
		Name:  addr.PickupStoreName,
		Nonce: r.PostFormValue("pickup_n"),
	}
	state, known := storemap.ReadCookie(r, h.secure)
	sameChain := r.PostFormValue("pickup_store_chain") == string(addr.PickupChain)
	if sameChain && storemap.Honoured(posted, state, known) {
		return false
	}
	addr.PickupStoreCode, addr.PickupStoreName = "", ""
	if sameChain {
		h.logPickupRefusal(r, posted)
	}
	return sameChain
}

func (h *Handler) logPickupRefusal(r *http.Request, posted storemap.PostedStore) {
	// The nonce authorizes a selection; diagnostics must not disclose it or the
	// customer's destination to anyone who can read logs.
	h.log.WarnContext(r.Context(), "pickup store refused",
		"method", r.Method,
		"pickup_cookie_count", len(r.CookiesNamed(storemap.CookieNameFor(h.secure))),
		"nonce_valid", storemap.ValidNonce(posted.Nonce),
		"nonce_matched", storemap.NonceMatched(r, h.secure, posted.Nonce),
		"chain_offered", posted.Chain.AtCheckout())
}

// renderCheckout refreshes the pickup cookie first: it must be written before
// the body, and every render is one the shopper may leave for the map from.
func (h *Handler) renderCheckout(
	w http.ResponseWriter, r *http.Request, status int, view *View,
) {
	if !view.HasShipping() {
		view.Chosen = ""
		view.QuoteID = ""
		view.Destination = ""
	}
	h.offerTheStoreMap(w, r, view)
	view.Partial = respond.IsHTMX(r)
	respond.Component(w, r, h.log, status, Page(Meta(r.Context()), view))
}

// offerTheStoreMap keeps the nonce across renders, since reload and Back must
// keep working, while everything else is rebuilt from this request so an
// invoice or address choice never comes back stale.
//
// The button it enables submits the checkout form to PickupStart; the carrier's
// own form is built there, on a page that holds nothing the shopper typed.
func (h *Handler) offerTheStoreMap(
	w http.ResponseWriter, r *http.Request, view *View,
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
	w http.ResponseWriter, r *http.Request, ship, invoiceChoice, address string,
) (nonce string, ok bool) {
	state, ok := storemap.ReadCookie(r, h.secure)
	if !ok {
		n, err := storemap.NewNonce()
		if err != nil {
			h.log.ErrorContext(r.Context(), "open a pickup nonce", "error", err)
			return "", false
		}
		state = storemap.State{Nonce: n}
	}
	state.Ship, state.Invoice, state.Address = ship, invoiceChoice, address
	storemap.WriteCookie(w, state, h.secure)
	return state.Nonce, true
}

func (h *Handler) mapRequest(
	r *http.Request, chain shipping.Chain, nonce string,
) (storemap.Form, bool) {
	tradeNo, err := storemap.NewMerchantTradeNo()
	if err != nil {
		h.log.ErrorContext(r.Context(), "open a map correlation number", "error", err)
		return storemap.Form{}, false
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
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	cartID, ok := h.carts.Require(w, r)
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

	// A CHOOSER CHANGE, not an order: the delivery method and the saved address
	// each decide which fields the form asks for, so it re-renders with the
	// submitted values and validates nothing.
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
// a successful checkout deliberately emptied. A double click's second submit
// can look before the first commits and then read the emptied cart or the
// spent credit after it does; that commit also recorded the attempt, so
// PlaceOrder asks again before each refusal such a read can cause.
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
	view         View
	address      delivery.Details
	invoice      Invoice
	shippingID   uuid.UUID
	shippingErr  error
	couponErr    string
	couponMissed bool
	attemptID    checkoutAttemptID
	attemptOK    bool
}

func (h *Handler) checkoutSubmission(
	w http.ResponseWriter,
	r *http.Request,
	cartID uuid.UUID,
	owner uuid.NullUUID,
	attemptID checkoutAttemptID,
	attemptOK bool,
) (*checkoutSubmission, bool) {
	addr := delivery.Details{
		Email:           r.PostFormValue("email"),
		RecipientName:   r.PostFormValue("name"),
		Phone:           r.PostFormValue("phone"),
		PostalCode:      r.PostFormValue("postal_code"),
		City:            r.PostFormValue("city"),
		District:        r.PostFormValue("district"),
		Street:          r.PostFormValue("street"),
		PickupChain:     shipping.Chain(r.PostFormValue("pickup_chain")),
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
		h.answerCartRefusal(w, r, cartID, attemptID, attemptOK)
		return nil, false
	}

	view.ChosenAddress = r.PostFormValue("address")
	view.RecipientMe = r.PostFormValue("recipient_me") == "1"
	view.RecipientPrevName = clip(r.PostFormValue("recipient_prev_name"))
	view.RecipientPrevPhone = clip(r.PostFormValue("recipient_prev_phone"))
	switch r.PostFormValue("update") {
	case "address":
		fillFromBook(&view, &addr, view.ChosenAddress)
		if view.ChosenAddress == OtherAddress {
			addr.PostalCode, addr.City, addr.District, addr.Street = "", "", "", ""
		}
	case "recipient":
		applyRecipient(&view, &addr)
	}
	if view.OffersTheProfile() {
		view.RecipientMe = matchesAccountRecipient(view.Profile, addr.RecipientName, addr.Phone)
	}
	view.Address = Address{
		Email: addr.Email, Name: addr.RecipientName, Phone: addr.Phone,
		PostalCode: addr.PostalCode, City: addr.City,
		District: addr.District, Street: addr.Street,
		PickupChain: addr.PickupChain, PickupStoreCode: addr.PickupStoreCode,
		PickupStoreName: addr.PickupStoreName, Note: addr.Note,
	}
	view.Chosen = chosenShipping(view.Shipping, r.PostFormValue("shipping"))
	to := destinationOf(view.Shipping, view.Chosen)
	view.Destination = to
	addr.To = to
	// Invalid form text never survives as internal state; a valid retry keeps
	// its exact identity.
	if attemptOK {
		view.IdempotencyKey = attemptID.String()
	}

	inv := Invoice{
		Type:          invoice.Preference(r.PostFormValue("invoice_type")),
		MobileBarcode: r.PostFormValue("invoice_carrier"),
		DonationCode:  r.PostFormValue("invoice_donation_code"),
		CompanyName:   r.PostFormValue("invoice_company_name"),
		TaxID:         r.PostFormValue("invoice_tax_id"),
	}
	view.Invoice = InvoiceView(inv)

	couponErr, missed := h.resolveCoupon(r, &view)
	if couponErr != "" {
		view.Errors = map[string]string{"coupon": couponErr}
	}
	if !view.HasShipping() {
		return &checkoutSubmission{
			view: view, address: addr, invoice: inv,
			couponErr: couponErr, couponMissed: missed,
			attemptID: attemptID, attemptOK: attemptOK,
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
		couponMissed: missed, attemptID: attemptID, attemptOK: attemptOK,
	}, true
}

// answerCartRefusal sends a cart that cannot check out back to the cart page,
// unless this attempt's own order emptied it after PlaceOrder's first look.
func (h *Handler) answerCartRefusal(
	w http.ResponseWriter, r *http.Request, cartID uuid.UUID,
	attemptID checkoutAttemptID, attemptOK bool,
) {
	if attemptOK && h.answerPriorCheckout(w, r, cartID, attemptID) {
		return
	}
	http.Redirect(w, r, "/cart", http.StatusSeeOther)
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
	changed := quoteMoved(shown, current, shownErr, currentErr)
	if changed {
		submission.view.noteQuoteChanged(r.Context(), r)
	}
	if len(errs) > 0 || changed || submission.view.Repriced != "" {
		if submission.attemptOK && h.answerPriorCheckout(w, r, cartID, submission.attemptID) {
			return checkoutQuoteID{}, false
		}
		submission.view.Errors = errs
		h.renderCheckout(w, r, http.StatusUnprocessableEntity, &submission.view)
		return checkoutQuoteID{}, false
	}
	return shown, true
}

// quoteMoved says the form was not looking at the quote this request would show.
func quoteMoved(shown, current checkoutQuoteID, shownErr, currentErr error) bool {
	return shownErr != nil || currentErr != nil || shown != current
}

func (h *Handler) answerPlacement(
	w http.ResponseWriter, r *http.Request,
	cartID uuid.UUID, addr *delivery.Details, view *View, number string, err error,
) {
	switch {
	case err == nil:
		if rememberErr := h.rememberOrder(w, r, number); rememberErr != nil {
			h.placementGrantFailed(w, r)
			return
		}
		// Left behind, the nonce would still vouch for a store in the next
		// checkout this browser starts.
		storemap.ClearCookie(w, h.secure)
		// Straight to payment: a confirmation shown before payment reads as
		// done to a customer who then closes the tab.
		http.Redirect(w, r, "/orders/"+number+"/pay", http.StatusSeeOther) //nolint:gosec // G710: server-generated order number
	case errors.Is(err, ErrEmpty):
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
	case errors.Is(err, cart.ErrUnavailable):
		// Something sold out between the cart page and this write; the cart
		// page says which line.
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
	case errors.Is(err, cart.ErrTooManyItems):
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
	case errors.Is(err, cart.ErrNotFound):
		h.answerMissingShipping(w, r, cartID, addr, view)
	case errors.Is(err, coupon.ErrUsedUp), errors.Is(err, coupon.ErrExpired),
		errors.Is(err, coupon.ErrBelowMinimum), errors.Is(err, coupon.ErrNotNeeded),
		errors.Is(err, coupon.ErrNotFound):
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
	w http.ResponseWriter, r *http.Request, cartID uuid.UUID, view *View,
) {
	balance, err := h.store.AvailableCredit(r.Context(), ownerOf(r))
	if err != nil {
		h.log.ErrorContext(r.Context(), "refresh store credit after checkout refusal", "error", err)
		h.serverError(w, r)
		return
	}
	view.AvailableCreditCents = balance
	view.CreditChanged = fmt.Sprintf(i18n.T(r.Context(), i18n.KeyCreditChanged), money.TWD(balance))
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
	addr *delivery.Details,
	view *View,
) {
	if err := h.refreshCheckoutState(r.Context(), cartID, ownerOf(r), addr, view); err != nil {
		h.log.ErrorContext(r.Context(), "refresh changed checkout quote", "error", err)
		h.serverError(w, r)
		return
	}
	if couponErr, _ := h.resolveCoupon(r, view); couponErr != "" {
		view.Errors = map[string]string{"coupon": couponErr}
	}
	view.noteQuoteChanged(r.Context(), r)
	if err := setCheckoutQuoteID(cartID, view); err != nil {
		h.log.ErrorContext(r.Context(), "build changed checkout quote", "error", err)
		h.serverError(w, r)
		return
	}
	h.renderCheckout(w, r, http.StatusUnprocessableEntity, view)
}

func (h *Handler) answerCheckoutKeyConflict(
	w http.ResponseWriter, r *http.Request, cartID uuid.UUID, view *View,
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
	addr *delivery.Details,
	view *View,
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
	addr *delivery.Details,
	view *View,
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
	case errors.Is(refusal, coupon.ErrExpired):
		reason = i18n.KeyCouponExpired
	case errors.Is(refusal, coupon.ErrBelowMinimum):
		reason = i18n.KeyCouponBelowMinimum
	case errors.Is(refusal, coupon.ErrNotNeeded):
		reason = i18n.KeyCouponNotNeeded
	case errors.Is(refusal, coupon.ErrNotFound):
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
	addr *delivery.Details,
	view *View,
) error {
	cartView, err := h.store.carts.View(ctx, cartID)
	if err != nil {
		return fmt.Errorf("read current cart: %w", err)
	}
	choices, err := h.carts.OfferedShipping(ctx, cartID, cartView.SubtotalCents)
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
	view.Moved = nil
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

	if !slices.ContainsFunc(choices, func(choice cart.ShippingChoice) bool {
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
func (h *Handler) resolveCoupon(r *http.Request, view *View) (message string, missed bool) {
	return h.applyCoupon(r, view, r.PostFormValue("coupon"))
}

// applyCoupon is reached by the posted form through resolveCoupon, behind the
// guess limiter; a draft restored after the map reaches it directly, which is
// safe because a draft only holds a code accepted when saved.
func (h *Handler) applyCoupon(r *http.Request, view *View, raw string) (message string, missed bool) {
	view.CouponCode = coupon.Normalise(raw)
	if view.CouponCode == "" {
		return "", false
	}

	subtotal := view.Cart.SubtotalCents
	c, err := h.store.CouponByCode(r.Context(), view.CouponCode)
	if err == nil {
		discountCents, freeShipping, applyErr := c.Apply(
			subtotal, view.HasShipping() && view.BaseShippingCents() == 0,
		)
		if applyErr == nil {
			view.CouponApplied = c.Description
			view.CouponDiscountCents = discountCents
			view.CouponFreeShipping = freeShipping
			return "", false
		}
		err = applyErr
	}
	switch {
	case errors.Is(err, coupon.ErrExpired):
		return i18n.T(r.Context(), i18n.KeyCouponExpired), true
	case errors.Is(err, coupon.ErrBelowMinimum):
		return i18n.T(r.Context(), i18n.KeyCouponBelowMinimum), true
	case errors.Is(err, coupon.ErrNotNeeded):
		return i18n.T(r.Context(), i18n.KeyCouponNotNeeded), true
	case errors.Is(err, coupon.ErrNotFound):
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
	if coupon.Normalise(r.PostFormValue("coupon")) == "" {
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
	ctx context.Context, addr *delivery.Details, shipErr error, inv *Invoice,
) map[string]string {
	refusals := addr.Validate()
	if shipErr != nil {
		refusals = append(refusals,
			formtext.FieldRefusal{Field: "shipping", MessageKey: i18n.KeyChooseShipping})
	}
	if addr.To == shipping.DestinationPickupPoint {
		if !addr.PickupChain.AtCheckout() {
			refusals = append(refusals,
				formtext.FieldRefusal{Field: "pickup_chain", MessageKey: i18n.KeyPickupChainRequired})
		} else if addr.PickupStoreCode == "" || addr.PickupStoreName == "" {
			refusals = append(refusals,
				formtext.FieldRefusal{Field: "pickup_store", MessageKey: i18n.KeyPickupStoreRequired})
		}
	}
	refusals = append(refusals, inv.Validate()...)
	return formtext.FieldMessages(ctx, refusals)
}

func (h *Handler) quoteCheckoutShipping(
	ctx context.Context,
	view *View,
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
func fillFromBook(view *View, addr *delivery.Details, wanted string) {
	if len(view.SavedAddresses) == 0 {
		return
	}
	if wanted == OtherAddress {
		view.ChosenAddress = OtherAddress
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

// chosenShipping is the method the form named, except that a shop offering one has
// nothing to choose: the form is answered with it, as the page was first shown, rather
// than refused for naming a method the shop has since replaced or no method at all.
func chosenShipping(choices []cart.ShippingChoice, posted string) string {
	if len(choices) == 1 {
		return choices[0].VersionID
	}
	return posted
}

// destinationOf reads the choices the server built, not the form.
func destinationOf(choices []cart.ShippingChoice, versionID string) shipping.Destination {
	for i := range choices {
		if choices[i].VersionID == versionID {
			if d, ok := shipping.ParseDestination(choices[i].DestinationKind); ok {
				return d
			}
			return ""
		}
	}
	return ""
}

func (h *Handler) checkoutView(ctx context.Context, cartID uuid.UUID, owner uuid.NullUUID) (View, error) {
	cartView, err := h.store.carts.View(ctx, cartID)
	if err != nil {
		return View{}, err
	}
	choices, err := h.carts.OfferedShipping(ctx, cartID, cartView.SubtotalCents)
	if err != nil {
		return View{}, err
	}
	saved, err := h.store.SavedAddresses(ctx, owner)
	if err != nil {
		return View{}, err
	}
	attemptID, err := newCheckoutAttemptID()
	if err != nil {
		return View{}, err
	}
	name, phone, err := h.store.customerProfile(ctx, owner)
	if err != nil {
		return View{}, err
	}
	view := View{
		Cart: cartView, Shipping: choices,
		InvoiceChoices: invoiceChoices(ctx),
		PickupChains:   pickupChains(),
		SavedAddresses: saved,
		IdempotencyKey: attemptID.String(),
	}
	balance, err := h.store.AvailableCredit(ctx, owner)
	if err != nil {
		return View{}, err
	}
	view.AvailableCreditCents = balance
	if u, ok := user.FromContext(ctx); ok {
		view.Profile = Profile{Email: u.Email, Name: name, Phone: phone}
	}
	if len(choices) > 0 {
		view.Chosen = choices[0].VersionID
		view.Destination = destinationOf(choices, view.Chosen)
	}
	return view, nil
}

func checkoutQuoteIDForView(cartID uuid.UUID, view *View) (checkoutQuoteID, error) {
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
		couponCode = coupon.Normalise(view.CouponCode)
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

func setCheckoutQuoteID(cartID uuid.UUID, view *View) error {
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
func invoiceChoices(ctx context.Context) []InvoiceChoice {
	types := invoice.OfferedPreferences()
	out := make([]InvoiceChoice, 0, len(types))
	for _, t := range types {
		out = append(out, InvoiceChoice{Value: t, Label: i18n.T(ctx, invoiceTypeLabelKey(t))})
	}
	return out
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	respond.Component(w, r, h.log, http.StatusInternalServerError, frame.Notice(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
		i18n.T(r.Context(), i18n.KeyTryAgainTitle),
		i18n.T(r.Context(), i18n.KeyCartUnavailable)))
}

func (h *Handler) placementGrantFailed(w http.ResponseWriter, r *http.Request) {
	respond.Component(w, r, h.log, http.StatusInternalServerError, PlacementGrantFailed(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyPlacementGrantFailedTitle)}))
}
