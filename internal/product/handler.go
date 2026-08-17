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
	askLimit *ratelimit.Limiter
	baseURL  string
	store    *Store
	log      *slog.Logger
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
	view.Comparing = boundedSlugs(r.URL.Query()["p"])
	meta := pages.ProductMeta(&view)
	meta.StructuredData = pages.JSONLDSet(
		pages.ProductJSONLD(&view, h.baseURL),
		pages.BreadcrumbJSONLD(view.Crumbs, view.Name, h.baseURL),
	)
	web.Render(w, r, h.log, http.StatusOK, pages.Product(meta, &view))
}

// Review serves POST /p/{slug}/reviews.
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
		http.Redirect(w, r, "/p/"+url.PathEscape(slug)+"#reviews", http.StatusSeeOther)
	case errors.Is(err, ErrAlreadyReviewed):
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

// parseRating returns 0 outside 1..5, which Validate reports as a missing rating.
func parseRating(s string) int16 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 16)
	if err != nil || n < 1 || n > 5 {
		return 0
	}
	return int16(n)
}

// Ask serves POST /p/{slug}/questions.
func (h *Handler) Ask(w http.ResponseWriter, r *http.Request) {
	u, ok := account.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin?next=/p/"+url.PathEscape(r.PathValue("slug")),
			http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")

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

var slugFormat = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
