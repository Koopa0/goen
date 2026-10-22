package banners

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/widget"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/respond"
)

type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("banners: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/banners", ac.RequireStaff(h.Banners))
	mux.HandleFunc("POST /admin/banners", ac.RequireStaff(h.CreateBanner))
	mux.HandleFunc("POST /admin/banners/{id}/active", ac.RequireStaff(h.SetBannerActive))
}

var notices = map[string]widget.NoticeEntry{
	"ok":      widget.NoticeDone(i18n.KeyAdminNoticeOK),
	"refused": widget.NoticeRefused(i18n.KeyAdminNoticeRefused),
}

func (h *Handler) Banners(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Banners(r.Context(), r.URL.Query().Get(keyset.Param))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read promo banners", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = widget.NoticeFrom(r, notices)
	respond.Component(w, r, h.log, http.StatusOK, Banners(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminHomeBanner)}, &view))
}

func (h *Handler) CreateBanner(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	f := &BannerForm{
		Message:    r.PostFormValue("message"),
		Short:      r.PostFormValue("short"),
		Code:       r.PostFormValue("code"),
		CTALabel:   r.PostFormValue("cta_label"),
		CTAHref:    r.PostFormValue("cta_href"),
		MessageEn:  r.PostFormValue("message_en"),
		ShortEn:    r.PostFormValue("short_en"),
		CTALabelEn: r.PostFormValue("cta_label_en"),
		Days:       formtext.ParseCountOrInvalid(r.PostFormValue("days")),
	}
	errs, err := h.store.CreateBanner(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create promo banner", "error", err)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.rejectBanner(w, r, errs)
	default:
		http.Redirect(w, r, "/admin/banners?ok=1", http.StatusSeeOther)
	}
}

func (h *Handler) SetBannerActive(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	h.answerRowWrite(w, r, "toggle promo banner", "/admin/banners",
		h.store.SetBannerActive(r.Context(), r.PathValue("id"), r.PostFormValue("active") == "1"))
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

func (h *Handler) rejectBanner(
	w http.ResponseWriter, r *http.Request, errs map[string]string,
) {
	view, err := h.store.Banners(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read promo banners", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Errors = errs
	view.Draft = BannerDraft{
		Message: r.PostFormValue("message"), Short: r.PostFormValue("short"), Code: r.PostFormValue("code"),
		CTALabel: r.PostFormValue("cta_label"), CTAHref: r.PostFormValue("cta_href"), Days: r.PostFormValue("days"),
		MessageEn: r.PostFormValue("message_en"), ShortEn: r.PostFormValue("short_en"), CTALabelEn: r.PostFormValue("cta_label_en"),
	}
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity, Banners(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminHomeBanner)}, &view))
}
