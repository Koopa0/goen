package taxonomy

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
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
		panic("taxonomy: NewHandler requires a store, a media handler and a logger")
	}
	return &Handler{store: store, images: images, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/taxonomy", ac.RequireStaff(h.Page))
	mux.HandleFunc("POST /admin/taxonomy/{kind}", ac.RequireStaff(h.Create))
	mux.HandleFunc("POST /admin/taxonomy/{kind}/{slug}", ac.RequireStaff(h.Edit))
	mux.HandleFunc("GET /admin/categories/{slug}", ac.RequireStaff(h.EditCategory))
	mux.HandleFunc("POST /admin/categories/{slug}/image", ac.RequireStaff(h.SetCategoryImage))
	mux.HandleFunc("POST /admin/categories/{slug}/image/remove", ac.RequireStaff(h.RemoveCategoryImage))
}

var notices = map[string]web.NoticeEntry{
	"ok":      web.Done(i18n.KeyAdminNoticeOK),
	"refused": web.Refused(i18n.KeyAdminNoticeRefused),
	"inuse":   web.Refused(i18n.KeyAdminNoticeInUse),
}

func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.List(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read taxonomy", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = web.Notice(r, notices)
	web.Render(w, r, h.log, http.StatusOK, admin.Taxonomy(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageTaxonomy)}, &view))
}

// Create takes the kind from the path, which the router constrains, never from a form.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	kind := r.PathValue("kind")
	f := &Form{
		Slug:    r.PostFormValue("slug"),
		Name:    r.PostFormValue("name"),
		NameEn:  r.PostFormValue("name_en"),
		Parent:  r.PostFormValue("parent"),
		IconKey: r.PostFormValue("icon_key"),
		Tone:    r.PostFormValue("tone"),
		// An unticked box posts nothing, which is the answer "no".
		Comparable: r.PostFormValue("comparable") != "",
	}

	var errs map[string]string
	var err error
	if kind == "categories" {
		errs, err = h.store.CreateCategory(r.Context(), f)
	} else {
		errs, err = h.store.CreateBrand(r.Context(), f)
	}
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create taxon", "error", err, "kind", kind)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		view, readErr := h.store.List(r.Context())
		if readErr != nil {
			access.ServerError(w, r, h.log)
			return
		}
		view.Which, view.Errors = kind, errs
		view.Draft = admin.TaxonDraft{
			Slug: f.Slug, Name: f.Name, NameEn: f.NameEn, Parent: f.Parent,
			IconKey: f.IconKey, Tone: f.Tone, Comparable: f.Comparable,
		}
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Taxonomy(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageTaxonomy)}, &view))
	default:
		http.Redirect(w, r, "/admin/taxonomy?ok=1", http.StatusSeeOther)
	}
}

func (h *Handler) Edit(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	kind := "brand"
	if r.PathValue("kind") == "categories" {
		kind = "category"
	}
	slug := r.PathValue("slug")

	var err error
	if r.PostFormValue("action") == "delete" {
		err = h.store.Delete(r.Context(), kind, slug)
	} else {
		err = h.store.Rename(r.Context(), kind, slug,
			r.PostFormValue("name"), r.PostFormValue("name_en"),
			r.PostFormValue("icon_key"), r.PostFormValue("tone"),
			r.PostFormValue("comparable") != "")
	}
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/taxonomy?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrInUse):
		http.Redirect(w, r, "/admin/taxonomy?inuse=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrInvalid):
		http.Redirect(w, r, "/admin/taxonomy?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "edit taxon", "error", err, "kind", kind)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) EditCategory(w http.ResponseWriter, r *http.Request) {
	h.renderCategory(w, r, http.StatusOK, web.Notice(r, notices), nil)
}

func (h *Handler) renderCategory(w http.ResponseWriter, r *http.Request, status int, notice components.Result, errs map[string]string) {
	slug := r.PathValue("slug")
	view, err := h.store.CategoryHeader(r.Context(), slug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			access.NotFound(w, r, h.log)
			return
		}
		h.log.ErrorContext(r.Context(), "read category header", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice, view.Errors = notice, errs
	if errs["image"] != "" || errs["alt"] != "" || errs["alt_en"] != "" {
		view.ImageAltDraft, view.ImageAltEnDraft = r.PostFormValue("alt"), r.PostFormValue("alt_en")
	}
	web.Render(w, r, h.log, status, admin.CategoryForm(layouts.Page{Title: view.Name}, view))
}

func (h *Handler) SetCategoryImage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	obj, err := h.images.StoreUpload(w, r, "image")
	if err != nil {
		h.respondToUploadError(w, r, err)
		return
	}
	err = h.store.SetCategoryImage(r.Context(), slug, obj.Digest, r.PostFormValue("alt"), r.PostFormValue("alt_en"))
	switch {
	case err == nil:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/categories/"+slug+"?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	case errors.Is(err, ErrInvalid):
		field, reason := "alt", i18n.KeyFormHeroAlt
		if utf8.RuneCountInString(strings.TrimSpace(r.PostFormValue("alt_en"))) > MaxAltRunes {
			field, reason = "alt_en", i18n.KeyFormCampaignAltEnLong
		}
		h.renderCategory(w, r, http.StatusUnprocessableEntity, components.Result{}, map[string]string{field: i18n.T(r.Context(), reason)})
	default:
		h.log.ErrorContext(r.Context(), "set category image", "error", err, "slug", slug)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) RemoveCategoryImage(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	switch err := h.store.ClearCategoryImage(r.Context(), slug); {
	case err == nil:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/categories/"+slug+"?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	default:
		h.log.ErrorContext(r.Context(), "remove category image", "error", err, "slug", slug)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) respondToUploadError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, web.ErrFormText):
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
	case media.IsRefusal(err):
		h.log.WarnContext(r.Context(), "category image upload", "error", err, "slug", r.PathValue("slug"))
		h.renderCategory(w, r, http.StatusUnprocessableEntity, components.Result{}, map[string]string{"image": i18n.T(r.Context(), media.UploadNotice(err))})
	default:
		h.log.ErrorContext(r.Context(), "category image upload", "error", err, "slug", r.PathValue("slug"))
		access.ServerError(w, r, h.log)
	}
}
