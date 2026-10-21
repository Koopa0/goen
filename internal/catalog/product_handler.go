package catalog

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/backinstock"
	"github.com/koopa0/goen/internal/cart/cartitem"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/siteurl"
)

func (h *Handler) Product(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	sel := ParseSelection(r.URL.Query())

	view, err := h.store.Product(r.Context(), slug, sel)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.productNotFound(w, r)
			return
		}
		if errors.Is(err, context.Canceled) {
			// The caller left; there is nobody to answer. A deadline stays an
			// error.
			return
		}
		h.log.ErrorContext(r.Context(), "load product", "error", err, "slug", slug)
		web.Render(w, r, h.log, http.StatusInternalServerError, layouts.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyCannotLoad)}, "",
			i18n.T(r.Context(), i18n.KeyCannotLoad),
			i18n.T(r.Context(), i18n.KeyCannotLoadProduct)))
		return
	}

	if u, signedIn := user.FromContext(r.Context()); signedIn {
		view.Saved = h.store.SavedByUser(r.Context(), u.ID, slug)
	}
	h.fillForViewer(r, slug, &view)
	view.NotifyOutcome = NotifyOutcome(r.URL.Query().Get("notify"))
	view.AskOutcome = r.URL.Query().Get("ask")
	view.ReviewPosted = r.URL.Query().Get("reviewed") == "1"
	view.AddedOutcome = cartitem.AddOutcome(r.URL.Query().Get("added"))
	view.Comparing = boundedSlugs(r.URL.Query()["p"])
	meta := ProductMeta(&view)
	meta.StructuredData = JSONLDSet(
		ProductJSONLD(&view, h.baseURL),
		BreadcrumbJSONLD(view.CategoryTrail(), view.Name, h.baseURL),
	)
	web.Render(w, r, h.log, http.StatusOK, Product(meta, &view))
}

func (h *Handler) productNotFound(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusNotFound, layouts.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyProductNotFound)}, "404",
		i18n.T(r.Context(), i18n.KeyProductNotFound),
		i18n.T(r.Context(), i18n.KeyProductNotFoundBody)))
}

// ProductFormPage is the product page a refused review, question or restock
// notice form re-rendered under: those POST-only addresses have no page of
// their own to return to.
func ProductFormPage(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/p/")
	if !ok {
		return "", false
	}
	for _, form := range []string{"/reviews", "/questions", "/notify"} {
		if slug, ok := strings.CutSuffix(rest, form); ok && siteurl.ValidSlug(slug) {
			return "/p/" + slug, true
		}
	}
	return "", false
}

func (h *Handler) Review(w http.ResponseWriter, r *http.Request) {
	u, signedIn := user.FromContext(r.Context())
	slug := r.PathValue("slug")
	if !signedIn {
		http.Redirect(w, r, "/signin?next=/p/"+url.PathEscape(slug), http.StatusSeeOther)
		return
	}
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}

	review := &Review{
		Rating: parseRating(r.PostFormValue("rating")),
		Title:  r.PostFormValue("title"),
		Body:   r.PostFormValue("body"),
	}
	draft := *review
	errs, err := h.store.AddReview(r.Context(), slug, u.ID, review)
	switch {
	case err == nil && len(errs) == 0:
		http.Redirect(w, r, "/p/"+url.PathEscape(slug)+"?reviewed=1#reviews", http.StatusSeeOther)
	case errors.Is(err, ErrAlreadyReviewed):
		h.rejectReview(w, r, slug, &draft, nil)
	case errors.Is(err, ErrNotDelivered):
		h.rejectReview(w, r, slug, &draft, map[string]i18n.Key{"body": i18n.KeyReviewNotDelivered})
	case errors.Is(err, ErrNotFound):
		h.productNotFound(w, r)
	case len(errs) > 0:
		h.rejectReview(w, r, slug, &draft, errs)
	default:
		h.log.ErrorContext(r.Context(), "add review", "error", err, "slug", slug)
		web.Render(w, r, h.log, http.StatusInternalServerError, layouts.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyLoggedTryAgain)))
	}
}

func (h *Handler) Notify(w http.ResponseWriter, r *http.Request) {
	if retryAfter, allowed := h.notifyLimit.Allow(ratelimit.ClientKey(r)); !allowed {
		ratelimit.Refuse(r.Context(), w, retryAfter)
		return
	}
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	addr := r.PostFormValue("email")
	variantID := r.PostFormValue("variant")

	var userID, accountEmail string
	if u, ok := user.FromContext(r.Context()); ok {
		userID, accountEmail = u.ID, u.Email
	}

	if variantID == "" {
		h.rejectNotify(w, r, slug, addr, NotifyNoOption)
		return
	}
	// Not a value the form can send: nothing on a page to re-render for.
	if _, parseErr := uuid.Parse(variantID); parseErr != nil {
		h.redirectNotified(w, r, slug, NotifyBadAddress)
		return
	}

	err := h.store.backInStock.Request(r.Context(), slug, variantID, addr, userID)
	switch {
	case err == nil && accountEmail != "" && strings.EqualFold(email.Clean(addr), accountEmail):
		// Never the typed address in the query string: anybody could put text
		// on the confirmation that way.
		h.redirectNotified(w, r, slug, NotifyRecordedForAccount)
	case err == nil:
		h.redirectNotified(w, r, slug, NotifyRecorded)
	case errors.Is(err, backinstock.ErrInvalid):
		// Two causes, two messages: an address goen will not mail, or a variant
		// that no longer needs a notice (back in stock, or gone).
		outcome := NotifyVariantUnavailable
		if !email.Valid(email.Clean(addr)) {
			outcome = NotifyBadAddress
		}
		h.rejectNotify(w, r, slug, addr, outcome)
	default:
		h.log.ErrorContext(r.Context(), "restock notice", "error", err, "slug", slug)
		web.Render(w, r, h.log, http.StatusInternalServerError, layouts.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyTryAgainBody)))
	}
}

func (h *Handler) redirectNotified(w http.ResponseWriter, r *http.Request, slug string, outcome NotifyOutcome) {
	q := url.Values{}
	for k, v := range ParseSelection(r.URL.Query()) {
		q.Set(k, v)
	}
	q.Set("notify", string(outcome))
	http.Redirect(w, r, "/p/"+url.PathEscape(slug)+"?"+q.Encode(), http.StatusSeeOther)
}

func (h *Handler) rejectNotify(w http.ResponseWriter, r *http.Request, slug, addr string, outcome NotifyOutcome) {
	view, err := h.store.Product(r.Context(), slug, ParseSelection(r.URL.Query()))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.productNotFound(w, r)
			return
		}
		h.log.ErrorContext(r.Context(), "reload product", "error", err, "slug", slug)
		web.Render(w, r, h.log, http.StatusInternalServerError, layouts.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyTryAgainBody)))
		return
	}
	h.fillForViewer(r, slug, &view)
	view.NotifyOutcome = outcome
	view.NotifyEmail = addr
	web.Render(w, r, h.log, http.StatusUnprocessableEntity,
		Product(ProductMeta(&view), &view))
}

func (h *Handler) rejectReview(w http.ResponseWriter, r *http.Request, slug string, review *Review, errs map[string]i18n.Key) {
	view, err := h.store.Product(r.Context(), slug, ParseSelection(r.URL.Query()))
	if err != nil {
		h.log.ErrorContext(r.Context(), "reload product", "error", err, "slug", slug)
		web.Render(w, r, h.log, http.StatusInternalServerError, layouts.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyTryAgainBody)))
		return
	}
	h.fillForViewer(r, slug, &view)
	view.ReviewErrors = make(map[string]string, len(errs))
	for field, k := range errs {
		view.ReviewErrors[field] = i18n.T(r.Context(), k)
	}
	view.ReviewDraft = ReviewDraft{
		Rating: int(review.Rating), Title: review.Title, Body: review.Body,
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity,
		Product(ProductMeta(&view), &view))
}

func (h *Handler) fillForViewer(r *http.Request, slug string, view *ProductView) {
	u, signedIn := user.FromContext(r.Context())
	view.SignedIn, view.AccountEmail = signedIn, u.Email
	if !signedIn {
		return
	}
	standing, err := h.store.ReviewStanding(r.Context(), slug, u.ID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "check review eligibility", "error", err)
		return
	}
	view.ReviewStanding = standing
}

// parseRating returns 0 outside 1..5, which Validate reports as a missing
// rating.
func parseRating(s string) int16 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 16)
	if err != nil || n < 1 || n > 5 {
		return 0
	}
	return int16(n)
}

func (h *Handler) Ask(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin?next=/p/"+url.PathEscape(r.PathValue("slug")),
			http.StatusSeeOther)
		return
	}
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")

	if retryAfter, allowed := h.askLimit.Allow("ask:" + u.ID); !allowed {
		ratelimit.Refuse(r.Context(), w, retryAfter)
		return
	}

	body := r.PostFormValue("body")
	err := h.store.Ask(r.Context(), slug, u.ID, body)
	switch {
	case err == nil:
		http.Redirect(w, r, "/p/"+url.PathEscape(slug)+"?ask=1#questions",
			http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		h.productNotFound(w, r)
	case errors.Is(err, ErrQuestionTooLong):
		h.rejectAsk(w, r, slug, body, "long")
	case errors.Is(err, ErrQuestionInvalid):
		h.rejectAsk(w, r, slug, body, "bad")
	default:
		h.log.ErrorContext(r.Context(), "ask question", "error", err, "slug", slug)
		web.Render(w, r, h.log, http.StatusInternalServerError, layouts.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyLoggedTryAgain)))
	}
}

func (h *Handler) rejectAsk(w http.ResponseWriter, r *http.Request, slug, body, outcome string) {
	view, err := h.store.Product(r.Context(), slug, ParseSelection(r.URL.Query()))
	if err != nil {
		h.log.ErrorContext(r.Context(), "reload product", "error", err, "slug", slug)
		web.Render(w, r, h.log, http.StatusInternalServerError, layouts.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyTryAgainBody)))
		return
	}
	h.fillForViewer(r, slug, &view)
	view.AskOutcome = outcome
	view.AskDraft = body
	web.Render(w, r, h.log, http.StatusUnprocessableEntity,
		Product(ProductMeta(&view), &view))
}

func boundedSlugs(raw []string) []string {
	out := make([]string, 0, MaxCompare)
	for _, s := range raw {
		if !siteurl.ValidSlug(s) || slices.Contains(out, s) {
			continue
		}
		out = append(out, s)
		if len(out) == MaxCompare {
			break
		}
	}
	return out
}
