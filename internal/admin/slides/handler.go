package slides

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/web"
	"github.com/koopa0/goen/internal/web/formtext"
)

type Handler struct {
	store  *Store
	images *media.Handler
	log    *slog.Logger
}

func NewHandler(store *Store, images *media.Handler, log *slog.Logger) *Handler {
	if store == nil || images == nil || log == nil {
		panic("slides: NewHandler requires a store, a media handler and a logger")
	}
	return &Handler{store: store, images: images, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/slides", ac.RequireStaff(h.Slides))
	mux.HandleFunc("POST /admin/slides", ac.RequireStaff(h.CreateHero))
	mux.HandleFunc("POST /admin/slides/{id}/active", ac.RequireStaff(h.SetHeroActive))
	mux.HandleFunc("POST /admin/slides/{id}/promote", ac.RequireStaff(h.PromoteHero))
}

var notices = map[string]components.NoticeEntry{
	"ok":      components.NoticeDone(i18n.KeyAdminNoticeOK),
	"refused": components.NoticeRefused(i18n.KeyAdminNoticeRefused),
}

func (h *Handler) Slides(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.HeroSlides(r.Context(), r.URL.Query().Get(keyset.Param))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read hero slides", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = components.NoticeFrom(r, notices)
	web.Render(w, r, h.log, http.StatusOK, Slides(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageHero)}, &view))
}

func (h *Handler) answerRowWrite(w http.ResponseWriter, r *http.Request, what, back string, err error) {
	switch {
	case err == nil:
		http.Redirect(w, r, back+"?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), what, "error", err)
		access.NotFound(w, r, h.log)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), what, "error", err)
		http.Redirect(w, r, back+"?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), what, "error", err)
		access.ServerError(w, r, h.log)
	}
}

// CreateHero takes the artwork with the copy, multipart. The image is
// optional and a slide with none falls back to the built-in artwork; the copy is
// checked before the image is decoded, so a refused slide stores nothing.
func (h *Handler) CreateHero(w http.ResponseWriter, r *http.Request) {
	upload, err := h.images.OpenUpload(w, r, "image")

	f := &HeroForm{
		Eyebrow:        r.PostFormValue("eyebrow"),
		Headline:       r.PostFormValue("headline"),
		Body:           r.PostFormValue("body"),
		PrimaryLabel:   r.PostFormValue("primary_label"),
		PrimaryHref:    r.PostFormValue("primary_href"),
		SecondLabel:    r.PostFormValue("second_label"),
		SecondHref:     r.PostFormValue("second_href"),
		ImageChosen:    upload != nil,
		ImageAlt:       r.PostFormValue("alt"),
		EyebrowEn:      r.PostFormValue("eyebrow_en"),
		HeadlineEn:     r.PostFormValue("headline_en"),
		BodyEn:         r.PostFormValue("body_en"),
		PrimaryLabelEn: r.PostFormValue("primary_label_en"),
		SecondLabelEn:  r.PostFormValue("second_label_en"),
		ImageAltEn:     r.PostFormValue("alt_en"),
		Days:           formtext.ParseCountOrInvalid(r.PostFormValue("days")),
	}
	if err != nil {
		h.respondToUploadError(w, r, f, err)
		return
	}
	if upload != nil {
		defer upload.Close()
	}
	if errs := f.Validate(r.Context()); len(errs) > 0 {
		h.rejectHeroSlide(w, r, f, errs)
		return
	}
	if upload != nil {
		obj, storeErr := upload.Store(r.Context())
		if storeErr != nil {
			h.respondToUploadError(w, r, f, storeErr)
			return
		}
		f.ImageKey = obj.Digest
	}

	errs, err := h.store.CreateHeroSlide(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create hero slide", "error", err)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.rejectHeroSlide(w, r, f, errs)
	default:
		http.Redirect(w, r, "/admin/slides?ok=1", http.StatusSeeOther)
	}
}

func (h *Handler) respondToUploadError(w http.ResponseWriter, r *http.Request, f *HeroForm, err error) {
	switch {
	case errors.Is(err, formtext.ErrUnstorable):
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
	case errors.Is(err, media.ErrUploadAborted):
		// The browser went away mid-upload; there is no one to answer.
	case media.IsRefusal(err):
		h.log.WarnContext(r.Context(), "hero image", "error", err)
		h.rejectHeroSlide(w, r, f, map[string]string{"image": i18n.T(r.Context(), media.UploadNotice(err))})
	default:
		h.log.ErrorContext(r.Context(), "hero image", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) rejectHeroSlide(
	w http.ResponseWriter, r *http.Request, f *HeroForm, errs map[string]string,
) {
	view, err := h.store.HeroSlides(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read hero slides", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Errors = errs
	view.Draft = HeroDraft{
		Eyebrow: r.PostFormValue("eyebrow"), Headline: r.PostFormValue("headline"), Body: r.PostFormValue("body"),
		PrimaryLabel: r.PostFormValue("primary_label"), PrimaryHref: r.PostFormValue("primary_href"),
		SecondLabel: r.PostFormValue("second_label"), SecondHref: r.PostFormValue("second_href"),
		ImageKey: f.ImageKey, ImageAlt: r.PostFormValue("alt"), Days: r.PostFormValue("days"),
		EyebrowEn: r.PostFormValue("eyebrow_en"), HeadlineEn: r.PostFormValue("headline_en"), BodyEn: r.PostFormValue("body_en"),
		PrimaryLabelEn: r.PostFormValue("primary_label_en"), SecondLabelEn: r.PostFormValue("second_label_en"),
		ImageAltEn: r.PostFormValue("alt_en"),
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, Slides(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageHero)}, &view))
}

func (h *Handler) SetHeroActive(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	h.answerRowWrite(w, r, "toggle hero slide", "/admin/slides",
		h.store.SetHeroSlideActive(r.Context(), r.PathValue("id"), r.PostFormValue("active") == "true"))
}

func (h *Handler) PromoteHero(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	h.answerRowWrite(w, r, "promote hero slide", "/admin/slides", h.store.PromoteHeroSlide(r.Context(), r.PathValue("id")))
}
