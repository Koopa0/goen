package campaigns

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	store  *Store
	images *media.Handler
	log    *slog.Logger
}

func NewHandler(store *Store, images *media.Handler, log *slog.Logger) *Handler {
	if store == nil || images == nil || log == nil {
		panic("campaigns: NewHandler requires a store, a media handler and a logger")
	}
	return &Handler{store: store, images: images, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/campaigns", ac.RequireStaff(h.Page))
	mux.HandleFunc("POST /admin/campaigns", ac.RequireStaff(h.Create))
	mux.HandleFunc("GET /admin/campaigns/{slug}", ac.RequireStaff(h.Edit))
	mux.HandleFunc("POST /admin/campaigns/{slug}/products", ac.RequireStaff(h.FeatureProduct))
	mux.HandleFunc("POST /admin/campaigns/{slug}/tone", ac.RequireStaff(h.SetTone))
	mux.HandleFunc("POST /admin/campaigns/{slug}/image", ac.RequireStaff(h.SetImage))
	mux.HandleFunc("POST /admin/campaigns/{slug}/image/remove", ac.RequireStaff(h.RemoveImage))
	mux.HandleFunc("POST /admin/campaigns/{slug}/active", ac.RequireStaff(h.SetActive))
	mux.HandleFunc("POST /admin/campaigns/{slug}/window", ac.RequireStaff(h.SetWindow))
}

var notices = map[string]web.NoticeEntry{
	"ok":         web.Done(i18n.KeyAdminNoticeOK),
	"refused":    web.Refused(i18n.KeyAdminNoticeRefused),
	"nodiscount": web.Refused(i18n.KeyAdminNoticeNoDiscount),
}

func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.List(r.Context(), r.URL.Query().Get(web.KeysetParam))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read campaigns", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = web.Notice(r, notices)
	web.Render(w, r, h.log, http.StatusOK, admin.Campaigns(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageCampaigns)}, view))
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	f := &Form{
		Slug:    r.PostFormValue("slug"),
		Title:   r.PostFormValue("title"),
		TitleEn: r.PostFormValue("title_en"),
		Days:    web.ParseCountOrInvalid(r.PostFormValue("days")),
		Tone:    r.PostFormValue("tone"),
	}
	errs, err := h.store.Create(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create campaign", "error", err)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		view, readErr := h.store.List(r.Context(), r.URL.Query().Get(web.KeysetParam))
		if readErr != nil {
			access.ServerError(w, r, h.log)
			return
		}
		view.Errors = errs
		view.Draft = admin.CampaignDraft{
			Slug: f.Slug, Title: f.Title, TitleEn: f.TitleEn, Days: r.PostFormValue("days"), Tone: f.Tone,
		}
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Campaigns(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageCampaigns)}, view))
	default:
		//nolint:gosec // G710: slug matched slugFormat in Validate
		http.Redirect(w, r, "/admin/campaigns/"+f.Slug+"?ok=1", http.StatusSeeOther)
	}
}

func (h *Handler) Edit(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, web.Notice(r, notices), nil)
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, notice components.Result, errs map[string]string) {
	slug := r.PathValue("slug")
	products, err := h.store.Products(r.Context(), slug)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read campaign products", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	image, tone, err := h.store.Image(r.Context(), slug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			web.Render(w, r, h.log, http.StatusNotFound, admin.MissingRecord(
				layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminMissingCampaign)},
				admin.MissingRecordView{Section: "campaigns", Heading: i18n.T(r.Context(), i18n.KeyAdminMissingCampaign), Body: i18n.T(r.Context(), i18n.KeyAdminNotFoundBody), BackLabel: i18n.KeyAdminBackCampaigns}))
			return
		}
		h.log.ErrorContext(r.Context(), "read campaign image", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	detail, err := h.store.Detail(r.Context(), slug)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read campaign", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	if errs["window"] != "" {
		detail.StartsAtInput, detail.EndsAtInput = r.PostFormValue("starts_at"), r.PostFormValue("ends_at")
	}
	term := web.SearchTerm(r.URL.Query().Get("find"))
	matches, err := h.store.SearchProducts(r.Context(), slug, term)
	if err != nil {
		h.log.ErrorContext(r.Context(), "search campaign products", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	var altDraft, altEnDraft string
	if errs["image"] != "" || errs["alt"] != "" || errs["alt_en"] != "" {
		altDraft, altEnDraft = r.PostFormValue("alt"), r.PostFormValue("alt_en")
	}
	view := admin.CampaignView{
		Slug: slug, CampaignDetail: detail, Term: term, Matches: matches,
		Products: products, Notice: notice, Image: image, Tone: tone, Errors: errs,
		ImageAltDraft: altDraft, ImageAltEnDraft: altEnDraft,
	}
	// The results are one figure of the page: failing to read them must not
	// take the editor with them.
	view.Results, err = h.store.Results(r.Context(), slug, detail, len(products), time.Now())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read campaign results", "error", err, "slug", slug)
		view.ResultsUnavailable = true
	}
	web.Render(w, r, h.log, status, admin.CampaignForm(layouts.Page{Title: detail.Title}, view))
}

func (h *Handler) SetWindow(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	errs, err := h.store.SetWindow(r.Context(), slug,
		r.PostFormValue("starts_at"), r.PostFormValue("ends_at"))
	switch {
	case err == nil && len(errs) > 0:
		h.render(w, r, http.StatusUnprocessableEntity, components.Result{}, errs)
	case err == nil:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/campaigns/"+slug+"?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	default:
		h.log.ErrorContext(r.Context(), "set campaign window", "error", err, "slug", slug)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) SetTone(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	switch err := h.store.SetTone(r.Context(), slug, r.PostFormValue("tone")); {
	case err == nil:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/campaigns/"+slug+"?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	case errors.Is(err, ErrInvalid):
		h.render(w, r, http.StatusUnprocessableEntity, components.Result{}, map[string]string{"tone": i18n.T(r.Context(), i18n.KeyFormToneUnknown)})
	default:
		h.log.ErrorContext(r.Context(), "set campaign tone", "error", err, "slug", slug)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) SetImage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	obj, err := h.images.StoreUpload(w, r, "image")
	if err != nil {
		h.respondToUploadError(w, r, err)
		return
	}
	err = h.store.SetImage(r.Context(), slug, obj.Digest, r.PostFormValue("alt"), r.PostFormValue("alt_en"))
	switch {
	case err == nil:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/campaigns/"+slug+"?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	case errors.Is(err, ErrInvalid):
		field, reason := "alt", i18n.KeyFormHeroAlt
		if utf8.RuneCountInString(strings.TrimSpace(r.PostFormValue("alt_en"))) > MaxAltRunes {
			field, reason = "alt_en", i18n.KeyFormCampaignAltEnLong
		}
		h.render(w, r, http.StatusUnprocessableEntity, components.Result{}, map[string]string{field: i18n.T(r.Context(), reason)})
	default:
		h.log.ErrorContext(r.Context(), "set campaign image", "error", err, "slug", slug)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) RemoveImage(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	switch err := h.store.ClearImage(r.Context(), slug); {
	case err == nil:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/campaigns/"+slug+"?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	default:
		h.log.ErrorContext(r.Context(), "remove campaign image", "error", err, "slug", slug)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) FeatureProduct(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	var err error
	if r.PostFormValue("action") == "remove" {
		err = h.store.UnfeatureProduct(r.Context(), slug, r.PostFormValue("product"))
	} else {
		err = h.store.FeatureProduct(r.Context(), slug, r.PostFormValue("product"))
	}
	back := "/admin/campaigns/" + slug
	switch {
	case err == nil:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, back+"?ok=1", http.StatusSeeOther)
	case pgerr.IsConstraint(err, "sale_campaign_needs_discount"):
		h.log.WarnContext(r.Context(), "feature product", "campaign", slug, "error", err)
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, back+"?nodiscount=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "feature product", "campaign", slug, "error", err)
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, back+"?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "feature product", "campaign", slug, "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) SetActive(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	back := "/admin/campaigns"
	if r.PostFormValue("back") == "detail" {
		back += "/" + slug
	}
	switch err := h.store.SetActive(r.Context(), slug, r.PostFormValue("active") == "true"); {
	case err == nil:
		http.Redirect(w, r, back+"?ok=1", http.StatusSeeOther) //nolint:gosec // G710: slug is the route's own path value
	case errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "set campaign active", "campaign", slug, "error", err)
		access.NotFound(w, r, h.log)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "set campaign active", "campaign", slug, "error", err)
		http.Redirect(w, r, back+"?refused=1", http.StatusSeeOther) //nolint:gosec // G710: slug is the route's own path value
	default:
		h.log.ErrorContext(r.Context(), "set campaign active", "campaign", slug, "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) respondToUploadError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, web.ErrFormText):
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
	case media.IsRefusal(err):
		h.log.WarnContext(r.Context(), "campaign image upload", "error", err, "slug", r.PathValue("slug"))
		h.render(w, r, http.StatusUnprocessableEntity, components.Result{}, map[string]string{"image": i18n.T(r.Context(), media.UploadNotice(err))})
	default:
		h.log.ErrorContext(r.Context(), "campaign image upload", "error", err, "slug", r.PathValue("slug"))
		access.ServerError(w, r, h.log)
	}
}
