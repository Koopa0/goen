package cart

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/cart/cartitem"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/shipping"
	"github.com/koopa0/goen/internal/shipping/storemap"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/respond"
	"github.com/koopa0/goen/internal/web/siteurl"
)

type Handler struct {
	store  *Store
	access *access.Store
	log    *slog.Logger
	secure bool
	// storeMap is nil or disabled on a deployment with no carrier, where no
	// delivery choice is pickup.
	storeMap *storemap.Client
}

// NewHandler reads a nil or disabled storeMap as no carrier, so no delivery
// choice the cart offers is pickup.
func NewHandler(store *Store, grants *access.Store, log *slog.Logger, secure bool, storeMap *storemap.Client) *Handler {
	if store == nil || grants == nil || log == nil {
		panic("cart: NewHandler requires a store, an access check and a logger")
	}
	return &Handler{store: store, access: grants, log: log, secure: secure, storeMap: storeMap}
}

// Routes registers the basket and reordering a past order into the basket.
// clearSpeculations wraps the writes that change the basket's count in the
// shared header.
func (h *Handler) Routes(mux *http.ServeMux, clearSpeculations func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /cart", h.Page)
	mux.HandleFunc("POST /cart/items", clearSpeculations(h.AddItem))
	mux.HandleFunc("POST /cart/items/update", clearSpeculations(h.UpdateItem))
	mux.HandleFunc("POST /orders/{number}/reorder", clearSpeculations(h.ReorderItems))
}

// freeDeliveryFor says only what is true for every method offered, since the
// cart does not know the destination: methods that turn free at different
// amounts are one sentence only at the highest, and a method that never does
// makes it none.
func freeDeliveryFor(choices []ShippingChoice, subtotalCents int64) FreeDelivery {
	if len(choices) == 0 {
		return FreeDelivery{}
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
			return FreeDelivery{}
		}
		allFreeAt = max(allFreeAt, c.FreeOverCents)
	}
	if reached {
		return FreeDelivery{Kind: FreeDeliveryReached, ThresholdCents: namedAt, SurchargeZones: zones}
	}
	return FreeDelivery{Kind: FreeDeliveryShort, ShortfallCents: allFreeAt - subtotalCents, ThresholdCents: allFreeAt}
}

func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	cartID, found, ok := h.requestCart(w, r)
	if !ok {
		return
	}
	if !found {
		view := View{Notice: cartPageNotice(r), ContinueURL: cartContinuation(r)}
		respond.Component(w, r, h.log, http.StatusOK, Page(Meta(r.Context()), view))
		return
	}
	view, err := h.store.View(r.Context(), cartID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read cart", "error", err)
		h.serverError(w, r)
		return
	}
	view.ContinueURL = cartContinuation(r)
	if choices, err := h.OfferedShipping(r.Context(), cartID, view.SubtotalCents); err != nil {
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
	respond.Component(w, r, h.log, http.StatusOK, Page(Meta(r.Context()), view))
}

func cartContinuation(r *http.Request) string {
	if r.URL.Query().Get("qty") != "adjusted" {
		return ""
	}
	return siteurl.PathOr(r.URL.Query().Get("next"), "")
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
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}

	variantID, err := uuid.Parse(r.PostFormValue("variant"))
	if err != nil {
		h.backToProduct(w, r, uuid.Nil, cartitem.AddOutcomeUnknown)
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
		h.backToProduct(w, r, variantID, cartitem.AddOutcomeAdded)
	case errors.Is(err, ErrQuantityAdjusted):
		h.backToProduct(w, r, variantID, cartitem.AddOutcomeAdjusted)
	case errors.Is(err, ErrTooManyItems):
		h.backToProduct(w, r, variantID, cartitem.AddOutcomeFull)
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrNotFound):
		h.backToProduct(w, r, variantID, cartitem.AddOutcomeUnavailable)
	default:
		h.log.ErrorContext(r.Context(), "add to cart", "error", err)
		h.serverError(w, r)
	}
}

func (h *Handler) UpdateItem(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	cartID, ok := h.Require(w, r)
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

func (h *Handler) ReorderItems(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !h.allows(r, number) {
		respond.Component(w, r, h.log, http.StatusNotFound, layouts.OrderNotFound(h.notFoundPage(r)))
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

func (h *Handler) allows(r *http.Request, number string) bool {
	ok, err := h.access.Allows(r, number)
	if err != nil {
		h.log.ErrorContext(r.Context(), "check order access", "order", number, "error", err)
	}
	return ok
}

// OfferedShipping is the delivery a cart is offered, on its page and at
// checkout. It drops pickup where the store map is not configured: a pickup
// order needs a store chosen on the map, so offering it would offer only a
// refusal.
func (h *Handler) OfferedShipping(
	ctx context.Context, cartID uuid.UUID, subtotalCents int64,
) ([]ShippingChoice, error) {
	choices, err := h.store.ShippingChoices(ctx, cartID, subtotalCents)
	if err != nil || h.storeMap.Enabled() {
		return choices, err
	}
	return slices.DeleteFunc(choices, func(c ShippingChoice) bool {
		to, ok := shipping.ParseDestination(c.DestinationKind)
		return ok && to == shipping.DestinationPickupPoint
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

// Require is the request's cart. Without one it redirects to /cart, and after
// a failed lookup it answers the error page; either way it reports false and
// the request is answered.
func (h *Handler) Require(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
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
	token := bearer.New()
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
func (h *Handler) backToProduct(w http.ResponseWriter, r *http.Request, variantID uuid.UUID, outcome cartitem.AddOutcome) {
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
	respond.Component(w, r, h.log, http.StatusInternalServerError, layouts.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
		i18n.T(r.Context(), i18n.KeyTryAgainTitle),
		i18n.T(r.Context(), i18n.KeyCartUnavailable)))
}

func (h *Handler) notFoundPage(r *http.Request) layouts.Page {
	return layouts.Page{Title: i18n.T(r.Context(), i18n.KeyOrderNotFound)}
}

// RecoveryContents reads the two carts a failed adoption leaves for the
// signed-in customer to compare: the one the request's cookie names, which no
// account owns, and the account's own. A cookie that names another account's
// cart reads as none. Without a signed-in customer it reads nothing and
// returns ErrNotFound; after any other error it reports neither cart.
func (h *Handler) RecoveryContents(ctx context.Context, r *http.Request) (beforeSignIn, account []cartitem.Item, err error) {
	u, ok := user.FromContext(ctx)
	if !ok {
		return nil, nil, ErrNotFound
	}
	accountID, err := h.store.ForUser(ctx, u.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, nil, err
	}
	if err == nil {
		account, err = h.store.Items(ctx, accountID)
		if err != nil {
			return nil, nil, err
		}
	}
	guestID, err := h.store.ByToken(ctx, ReadCookie(r, h.secure), uuid.NullUUID{})
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrNotYourCart) {
		return nil, account, nil
	}
	if err != nil {
		return nil, nil, err
	}
	beforeSignIn, err = h.store.Items(ctx, guestID)
	if err != nil {
		return nil, nil, err
	}
	return beforeSignIn, account, nil
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
		next.ServeHTTP(w, r.WithContext(layouts.WithCartCount(r.Context(), n)))
	})
}
