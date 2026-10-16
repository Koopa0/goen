package content

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	store  *Store
	images *media.Handler
	log    *slog.Logger
}

func NewHandler(store *Store, images *media.Handler, log *slog.Logger) *Handler {
	if store == nil || images == nil || log == nil {
		panic("content: NewHandler requires a store, a media handler and a logger")
	}
	return &Handler{store: store, images: images, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/faq", ac.RequireStaff(h.FAQ))
	mux.HandleFunc("POST /admin/faq", ac.RequireStaff(h.CreateFAQ))
	mux.HandleFunc("POST /admin/faq/{id}", ac.RequireStaff(h.EditFAQ))
	mux.HandleFunc("POST /admin/faq/categories", ac.RequireStaff(h.CreateFAQCategory))
	mux.HandleFunc("POST /admin/faq/categories/{id}", ac.RequireStaff(h.EditFAQCategory))
	mux.HandleFunc("GET /admin/newsletter", ac.RequireStaff(h.Newsletter))
	mux.HandleFunc("POST /admin/newsletter", ac.RequireStaff(h.ComposeNewsletter))
	mux.HandleFunc("POST /admin/newsletter/{id}/send", ac.RequireStaff(h.SendNewsletter))
	mux.HandleFunc("GET /admin/home", ac.RequireStaff(h.Home))
	mux.HandleFunc("POST /admin/home", ac.RequireStaff(h.CreateHero))
	mux.HandleFunc("POST /admin/home/banner", ac.RequireStaff(h.CreateBanner))
	mux.HandleFunc("POST /admin/home/banner/{id}/active", ac.RequireStaff(h.SetBannerActive))
	mux.HandleFunc("POST /admin/home/{id}/active", ac.RequireStaff(h.SetHeroActive))
	mux.HandleFunc("POST /admin/home/{id}/promote", ac.RequireStaff(h.PromoteHero))
}

var notices = map[string]components.NoticeEntry{
	"ok":      components.NoticeDone(i18n.KeyAdminNoticeOK),
	"refused": components.NoticeRefused(i18n.KeyAdminNoticeRefused),
	"saved":   components.NoticeDone(i18n.KeyAdminNoticeSaved),
	"sent":    components.NoticeDone(i18n.KeyAdminNoticeSent),
	"already": components.NoticeDone(i18n.KeyAdminNoticeAlready),
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
	if err := web.ParseForm(w, r); err != nil {
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
	if err := web.ParseForm(w, r); err != nil {
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

func faqFormOf(r *http.Request) *FAQForm {
	return &FAQForm{
		CategoryID: r.PostFormValue("category_id"),
		Question:   r.PostFormValue("question"),
		Answer:     r.PostFormValue("answer"),
		QuestionEn: r.PostFormValue("question_en"),
		AnswerEn:   r.PostFormValue("answer_en"),
	}
}

func (h *Handler) rejectFAQ(
	w http.ResponseWriter, r *http.Request, f *FAQForm, errs map[string]string,
) {
	view, err := h.store.FAQ(r.Context())
	if err != nil {
		access.ServerError(w, r, h.log)
		return
	}
	typed := FAQEntry{
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
	if err := web.ParseForm(w, r); err != nil {
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
	if err := web.ParseForm(w, r); err != nil {
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

func faqCategoryFormOf(r *http.Request) *FAQCategoryForm {
	return &FAQCategoryForm{Name: r.PostFormValue("name"), NameEn: r.PostFormValue("name_en")}
}

// rejectFAQCategory answers a refused category form on the form it came from,
// with what was typed.
func (h *Handler) rejectFAQCategory(
	w http.ResponseWriter, r *http.Request, f *FAQCategoryForm, errs map[string]string,
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

type homeQueue string

const (
	heroQueue   homeQueue = "slides"
	bannerQueue homeQueue = "banners"
)

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	var heroAfter, bannerAfter string
	switch homeQueue(r.URL.Query().Get("queue")) {
	case heroQueue:
		heroAfter = r.URL.Query().Get(keyset.Param)
	case bannerQueue:
		bannerAfter = r.URL.Query().Get(keyset.Param)
	}
	view, err := h.homeView(r.Context(), heroAfter, bannerAfter)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read home editor", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = components.NoticeFrom(r, notices)
	web.Render(w, r, h.log, http.StatusOK, Home(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageHero)}, &view))
}

func (h *Handler) homeView(ctx context.Context, heroAfter, bannerAfter string) (HeroView, error) {
	view, err := h.store.HeroSlides(ctx, heroAfter)
	if err != nil {
		return HeroView{}, err
	}
	banners, err := h.store.Banners(ctx, bannerAfter)
	if err != nil {
		return HeroView{}, err
	}
	view.Banners, view.BannerBound = banners.Rows, banners.Bound
	return view, nil
}

func (h *Handler) CreateBanner(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
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
		Days:       web.ParseCountOrInvalid(r.PostFormValue("days")),
	}
	errs, err := h.store.CreateBanner(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create promo banner", "error", err)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.rejectBanner(w, r, errs)
	default:
		http.Redirect(w, r, "/admin/home?ok=1", http.StatusSeeOther)
	}
}

func (h *Handler) SetBannerActive(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	h.answerRowWrite(w, r, "toggle promo banner", "/admin/home",
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
	view, err := h.homeView(r.Context(), "", "")
	if err != nil {
		h.log.ErrorContext(r.Context(), "read home editor", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Errors = errs
	view.BannerDraft = BannerDraft{
		Message: r.PostFormValue("message"), Short: r.PostFormValue("short"), Code: r.PostFormValue("code"),
		CTALabel: r.PostFormValue("cta_label"), CTAHref: r.PostFormValue("cta_href"), Days: r.PostFormValue("days"),
		MessageEn: r.PostFormValue("message_en"), ShortEn: r.PostFormValue("short_en"), CTALabelEn: r.PostFormValue("cta_label_en"),
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, Home(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageHero)}, &view))
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
		Days:           web.ParseCountOrInvalid(r.PostFormValue("days")),
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
		http.Redirect(w, r, "/admin/home?ok=1", http.StatusSeeOther)
	}
}

func (h *Handler) respondToUploadError(w http.ResponseWriter, r *http.Request, f *HeroForm, err error) {
	switch {
	case errors.Is(err, web.ErrFormText):
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
	view, err := h.homeView(r.Context(), "", "")
	if err != nil {
		h.log.ErrorContext(r.Context(), "read home editor", "error", err)
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
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, Home(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageHero)}, &view))
}

func (h *Handler) SetHeroActive(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	h.answerRowWrite(w, r, "toggle hero slide", "/admin/home",
		h.store.SetHeroSlideActive(r.Context(), r.PathValue("id"), r.PostFormValue("active") == "true"))
}

func (h *Handler) PromoteHero(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	h.answerRowWrite(w, r, "promote hero slide", "/admin/home", h.store.PromoteHeroSlide(r.Context(), r.PathValue("id")))
}

const NewsletterIssueLimit = 50

func (h *Handler) Newsletter(w http.ResponseWriter, r *http.Request) {
	view, err := h.newsletterView(r)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read the newsletter", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = components.NoticeFrom(r, notices)
	web.Render(w, r, h.log, http.StatusOK, Newsletter(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageNewsletter)}, view))
}

// ComposeNewsletter writes a DRAFT and sends nothing: the irreversible step gets its own button.
func (h *Handler) ComposeNewsletter(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseLongTextForm(w, r, MaxNewsletterSubjectRunes+MaxNewsletterBodyRunes); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	subject, body := r.PostFormValue("subject"), r.PostFormValue("body")

	if keys := validateNewsletter(subject, body); len(keys) > 0 {
		view, err := h.newsletterView(r)
		if err != nil {
			h.log.ErrorContext(r.Context(), "read the newsletter", "error", err)
			access.ServerError(w, r, h.log)
			return
		}
		view.Draft = NewsletterDraft{Subject: subject, Body: body}
		view.Errors = make(map[string]string, len(keys))
		for field, k := range keys {
			view.Errors[field] = i18n.T(r.Context(), k)
		}
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, Newsletter(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageNewsletter)}, view))
		return
	}

	if _, err := h.store.ComposeNewsletter(r.Context(), subject, body, audit.ActorID(r.Context())); err != nil {
		h.log.ErrorContext(r.Context(), "compose a newsletter issue", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	http.Redirect(w, r, "/admin/newsletter?saved=1", http.StatusSeeOther)
}

// SendNewsletter relies on the store refusing a second send in the UPDATE's own
// WHERE clause, because a reload is not the only way two of these arrive.
func (h *Handler) SendNewsletter(w http.ResponseWriter, r *http.Request) {
	switch _, err := h.store.SendNewsletter(r.Context(), r.PathValue("id"), audit.ActorID(r.Context())); {
	case err == nil:
		http.Redirect(w, r, "/admin/newsletter?sent=1", http.StatusSeeOther)
	case errors.Is(err, ErrAlreadySent):
		http.Redirect(w, r, "/admin/newsletter?already=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	default:
		h.log.ErrorContext(r.Context(), "send a newsletter issue", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) newsletterView(r *http.Request) (NewsletterView, error) {
	counts, err := h.store.SubscriberCounts(r.Context())
	if err != nil {
		return NewsletterView{}, err
	}
	issues, err := h.store.NewsletterIssues(r.Context(), NewsletterIssueLimit)
	if err != nil {
		return NewsletterView{}, err
	}
	return NewsletterView{
		Active: counts.Active, Unsubscribed: counts.Unsubscribed, Awaiting: counts.Awaiting,
		EnglishActive: counts.EnglishActive,
		Issues:        issues,
	}, nil
}
