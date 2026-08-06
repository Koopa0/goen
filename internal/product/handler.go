package product

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// Handler serves the product detail page.
type Handler struct {
	// askLimit bounds how fast one account can post questions. The surface is
	// public, so it is a spam target the moment it exists.
	askLimit *ratelimit.Limiter
	// baseURL is the origin structured data must name. A JSON-LD offer whose
	// url is a relative path is one a crawler discards.
	baseURL string
	store   *Store
	log     *slog.Logger
}

// NewHandler returns a Handler reading through store.
func NewHandler(store *Store, log *slog.Logger, baseURL string) *Handler {
	if store == nil || log == nil {
		panic("product: NewHandler requires a store and a logger")
	}
	return &Handler{
		store: store, log: log, baseURL: baseURL,
		askLimit: ratelimit.New(ratelimit.Config{
			Every: 6 * time.Second, Burst: 10, TTL: time.Hour,
		}),
	}
}

// Detail serves GET /p/{slug}.
func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	sel := ParseSelection(r.URL.Query())

	view, err := h.store.Load(r.Context(), slug, sel)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
				layouts.Page{Title: i18n.T(r.Context(), i18n.KeyProductNotFound)}, "404",
				i18n.T(r.Context(), i18n.KeyProductNotFound),
				i18n.T(r.Context(), i18n.KeyProductNotFoundBody)))
			return
		}
		h.log.ErrorContext(r.Context(), "load product", "error", err, "slug", slug)
		web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyCannotLoad)}, "",
			i18n.T(r.Context(), i18n.KeyCannotLoad),
			i18n.T(r.Context(), i18n.KeyCannotLoadProduct)))
		return
	}

	if u, signedIn := account.FromContext(r.Context()); signedIn {
		view.Saved = h.store.SavedByUser(r.Context(), u.ID, slug)
	}
	h.fillReviewForm(r, slug, &view)
	view.NotifyOutcome = r.URL.Query().Get("notify")
	view.AskOutcome = r.URL.Query().Get("ask")
	// The comparison set travels in the query string, so a product page reached
	// from a comparison knows what is already in it. Bounded here because it
	// reaches a URL builder and, from there, a query.
	view.Comparing = boundedSlugs(r.URL.Query()["p"])
	meta := pages.ProductMeta(&view)
	// What a search result SHOWS. A title alone is a link; a title with a
	// price, a currency and "in stock" is a decision somebody can make before
	// they click.
	//
	// The breadcrumb goes with it: a result showing 首頁 › 手機 › Pixelight
	// tells somebody where the page sits, and a bare URL does not. Both travel
	// in one block because schema.org accepts an array and layouts.Page carries
	// one string.
	meta.StructuredData = pages.JSONLDSet(
		pages.ProductJSONLD(&view, h.baseURL),
		pages.BreadcrumbJSONLD(view.Crumbs, view.Name, h.baseURL),
	)
	web.Render(w, r, h.log, http.StatusOK, pages.Product(meta, &view))
}

// Review serves POST /p/{slug}/reviews.
//
// A plain form. On refusal the page re-renders at 422 with what was typed still
// in it, which is the write-face rule's requirement and also the difference
// between fixing a typo and retyping a paragraph.
func (h *Handler) Review(w http.ResponseWriter, r *http.Request) {
	u, signedIn := account.FromContext(r.Context())
	slug := r.PathValue("slug")
	if !signedIn {
		http.Redirect(w, r, "/signin?next=/p/"+url.PathEscape(slug), http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}

	review := &Review{
		Rating: parseRating(r.PostFormValue("rating")),
		Title:  r.PostFormValue("title"),
		Body:   r.PostFormValue("body"),
	}
	errs, err := h.store.AddReview(r.Context(), slug, u.ID, review)
	switch {
	case err == nil && len(errs) == 0:
		// 303 and back to the reviews, so a reload cannot post it twice.
		// url.PathEscape makes the slug a single path segment whatever it
		// contained, so it cannot steer the redirect.
		http.Redirect(w, r, "/p/"+url.PathEscape(slug)+"#reviews", http.StatusSeeOther)
	case errors.Is(err, ErrAlreadyReviewed):
		// No field errors: fillReviewForm will find CanReview false and render
		// the explanation instead of the form, so a message pinned to a field
		// would point at an input that is not on the page.
		h.rejectReview(w, r, slug, review, nil)
	case len(errs) > 0:
		h.rejectReview(w, r, slug, review, errs)
	default:
		h.log.ErrorContext(r.Context(), "add review", "error", err, "slug", slug)
		web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyLoggedTryAgain)))
	}
}

// Notify serves POST /p/{slug}/notify.
//
// A plain form on the sold-out state of the product page. The address is typed
// rather than taken from the session, because the visitor most worth reaching
// here is the one who has not signed in.
func (h *Handler) Notify(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	addr := r.PostFormValue("email")
	variantID := r.PostFormValue("variant")

	var userID string
	if u, ok := account.FromContext(r.Context()); ok {
		userID = u.ID
	}

	err := h.store.RequestRestockNotice(r.Context(), variantID, addr, userID)
	switch {
	case err == nil:
		// Back to the variant that was asked about, not to the product's
		// default: a visitor who picked 256GB and asked about it should land on
		// 256GB.
		//nolint:gosec // G710: slug is the route's own path value, escaped
		http.Redirect(w, r, "/p/"+url.PathEscape(slug)+"?"+r.URL.RawQuery+"&notify=1",
			http.StatusSeeOther)
	case errors.Is(err, ErrNotifyInvalid):
		//nolint:gosec // G710: slug is the route's own path value, escaped
		http.Redirect(w, r, "/p/"+url.PathEscape(slug)+"?"+r.URL.RawQuery+"&notify=bad",
			http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "restock notice", "error", err, "slug", slug)
		web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyTryAgainBody)))
	}
}

// rejectReview re-renders the product page with the form's own values.
func (h *Handler) rejectReview(w http.ResponseWriter, r *http.Request, slug string, review *Review, errs map[string]i18n.Key) {
	view, err := h.store.Load(r.Context(), slug, ParseSelection(r.URL.Query()))
	if err != nil {
		h.log.ErrorContext(r.Context(), "reload product", "error", err, "slug", slug)
		web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyTryAgainBody)))
		return
	}
	h.fillReviewForm(r, slug, &view)
	// Rendered HERE: the validator returns keys, and this is the first place
	// that knows which locale is reading.
	view.ReviewErrors = make(map[string]string, len(errs))
	for field, k := range errs {
		view.ReviewErrors[field] = i18n.T(r.Context(), k)
	}
	view.ReviewDraft = pages.ReviewDraft{
		Rating: int(review.Rating), Title: review.Title, Body: review.Body,
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity,
		pages.Product(pages.ProductMeta(&view), &view))
}

// fillReviewForm decides what the review form offers this visitor.
//
// A failure to read it is logged and swallowed: the form disappearing is a
// smaller harm than the product page failing to load over it.
func (h *Handler) fillReviewForm(r *http.Request, slug string, view *pages.ProductView) {
	u, signedIn := account.FromContext(r.Context())
	view.SignedIn = signedIn
	if !signedIn {
		return
	}
	allowed, verified, err := h.store.CanReview(r.Context(), slug, u.ID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "check review eligibility", "error", err)
		return
	}
	view.CanReview, view.WouldVerify = allowed, verified
}

// parseRating reads the star field, refusing anything outside 1..5 by
// returning 0 — which Validate then reports as a missing rating.
//
// It returns int16 rather than an int the caller converts: gosec cannot see
// that the 1..5 bound makes the conversion safe, and a //nolint would assert
// the bound where the signature can express it.
func parseRating(s string) int16 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 16)
	if err != nil || n < 1 || n > 5 {
		return 0
	}
	return int16(n)
}

// Ask serves POST /p/{slug}/questions.
//
// Signed in only. An anonymous public writing surface is a spam target with
// nobody to hold responsible, and the sign-in is what makes the per-account
// rate limit mean anything.
func (h *Handler) Ask(w http.ResponseWriter, r *http.Request) {
	u, ok := account.FromContext(r.Context())
	if !ok {
		// PathEscape, so a slug carrying anything odd cannot start a new path
		// segment or a query in the target.
		http.Redirect(w, r, "/signin?next=/p/"+url.PathEscape(r.PathValue("slug")),
			http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")

	// Bounded per ACCOUNT, not per IP: the writing surface is public and the
	// account is what a flood would be attributed to. Ten a minute is far more
	// than a person asks and far less than a script wants.
	if retryAfter, allowed := h.askLimit.Allow("ask:" + u.ID); !allowed {
		ratelimit.Refuse(w, retryAfter)
		return
	}

	outcome := "1"
	if err := h.store.Ask(r.Context(), slug, u.ID, r.PostFormValue("body")); err != nil {
		h.log.WarnContext(r.Context(), "ask question", "error", err, "slug", slug)
		outcome = "bad"
	}
	http.Redirect(w, r, "/p/"+url.PathEscape(slug)+"?ask="+outcome+"#questions",
		http.StatusSeeOther)
}

// boundedSlugs is the comparison set a URL carried, bounded and sanitised.
//
// The values reach an href, so anything that is not a slug is dropped rather
// than escaped — a comparison link is a place somebody would try to put a
// second query parameter, and there is no legitimate slug that looks like one.
func boundedSlugs(raw []string) []string {
	const maxCompare = 4
	out := make([]string, 0, maxCompare)
	for _, s := range raw {
		if !slugFormat.MatchString(s) || slices.Contains(out, s) {
			continue
		}
		out = append(out, s)
		if len(out) == maxCompare {
			break
		}
	}
	return out
}

// slugFormat is the shape of every slug goen mints.
var slugFormat = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
