package faq

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/formtext"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("faq: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/faq", ac.RequireStaff(h.FAQ))
	mux.HandleFunc("POST /admin/faq", ac.RequireStaff(h.CreateFAQ))
	mux.HandleFunc("POST /admin/faq/{id}", ac.RequireStaff(h.EditFAQ))
	mux.HandleFunc("POST /admin/faq/categories", ac.RequireStaff(h.CreateFAQCategory))
	mux.HandleFunc("POST /admin/faq/categories/{id}", ac.RequireStaff(h.EditFAQCategory))
}

var notices = map[string]components.NoticeEntry{
	"ok":      components.NoticeDone(i18n.KeyAdminNoticeOK),
	"refused": components.NoticeRefused(i18n.KeyAdminNoticeRefused),
	"inuse":   components.NoticeRefused(i18n.KeyAdminFaqpCategoryInUse),
}

func (h *Handler) FAQ(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.FAQ(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read faq", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = components.NoticeFrom(r, notices)
	web.Render(w, r, h.log, http.StatusOK, FAQ(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageFAQ)}, &view))
}

func (h *Handler) CreateFAQ(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	f := faqFormOf(r)
	errs, err := h.store.CreateFAQEntry(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create faq entry", "error", err)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.rejectFAQ(w, r, f, errs)
	default:
		http.Redirect(w, r, "/admin/faq?ok=1", http.StatusSeeOther)
	}
}

func (h *Handler) EditFAQ(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	if r.PostFormValue("action") == "delete" {
		h.answerRowWrite(w, r, "delete faq entry", "/admin/faq", h.store.DeleteFAQEntry(r.Context(), id))
		return
	}

	f := faqFormOf(r)
	f.ID = id
	errs, err := h.store.UpdateFAQEntry(r.Context(), f)
	switch {
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	case err != nil:
		h.log.ErrorContext(r.Context(), "update faq entry", "error", err)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.rejectFAQ(w, r, f, errs)
	default:
		http.Redirect(w, r, "/admin/faq?ok=1", http.StatusSeeOther)
	}
}

func faqFormOf(r *http.Request) *Form {
	return &Form{
		CategoryID: r.PostFormValue("category_id"),
		Question:   r.PostFormValue("question"),
		Answer:     r.PostFormValue("answer"),
		QuestionEn: r.PostFormValue("question_en"),
		AnswerEn:   r.PostFormValue("answer_en"),
	}
}

func (h *Handler) rejectFAQ(
	w http.ResponseWriter, r *http.Request, f *Form, errs map[string]string,
) {
	view, err := h.store.FAQ(r.Context())
	if err != nil {
		access.ServerError(w, r, h.log)
		return
	}
	typed := Entry{
		ID: f.ID, CategoryID: r.PostFormValue("category_id"),
		Question: r.PostFormValue("question"), Answer: r.PostFormValue("answer"),
		QuestionEn: r.PostFormValue("question_en"), AnswerEn: r.PostFormValue("answer_en"),
	}
	// An edit goes back to the entry it was made on. The add form's draft is the
	// wrong place: its error would send the operator to press 新增 and publish a
	// second copy while the original stays unedited.
	if f.ID != "" {
		view.Edit, view.EditErrors = typed, errs
	} else {
		view.Draft, view.Errors = typed, errs
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, FAQ(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageFAQ)}, &view))
}

func (h *Handler) CreateFAQCategory(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	f := faqCategoryFormOf(r)
	errs, err := h.store.CreateFAQCategory(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create faq category", "error", err)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.rejectFAQCategory(w, r, f, errs)
	default:
		http.Redirect(w, r, "/admin/faq?ok=1", http.StatusSeeOther)
	}
}

func (h *Handler) EditFAQCategory(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	if r.PostFormValue("action") == "delete" {
		err := h.store.DeleteFAQCategory(r.Context(), id)
		if errors.Is(err, ErrInUse) {
			http.Redirect(w, r, "/admin/faq?inuse=1", http.StatusSeeOther)
			return
		}
		h.answerRowWrite(w, r, "delete faq category", "/admin/faq", err)
		return
	}

	f := faqCategoryFormOf(r)
	f.ID = id
	errs, err := h.store.RenameFAQCategory(r.Context(), f)
	switch {
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	case err != nil:
		h.log.ErrorContext(r.Context(), "rename faq category", "error", err)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.rejectFAQCategory(w, r, f, errs)
	default:
		http.Redirect(w, r, "/admin/faq?ok=1", http.StatusSeeOther)
	}
}

func faqCategoryFormOf(r *http.Request) *CategoryForm {
	return &CategoryForm{Name: r.PostFormValue("name"), NameEn: r.PostFormValue("name_en")}
}

// rejectFAQCategory answers a refused category form on the form it came from,
// with what was typed.
func (h *Handler) rejectFAQCategory(
	w http.ResponseWriter, r *http.Request, f *CategoryForm, errs map[string]string,
) {
	view, err := h.store.FAQ(r.Context())
	if err != nil {
		access.ServerError(w, r, h.log)
		return
	}
	view.CategoryDraft = FAQCategory{
		ID: f.ID, Name: r.PostFormValue("name"), NameEn: r.PostFormValue("name_en"),
	}
	view.CategoryErrors = errs
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, FAQ(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageFAQ)}, &view))
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
