package content

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	store   *Store
	images  *media.Handler
	letters *newsletter.Store
	log     *slog.Logger
}

func NewHandler(store *Store, images *media.Handler, letters *newsletter.Store, log *slog.Logger) *Handler {
	if store == nil || images == nil || letters == nil || log == nil {
		panic("content: NewHandler requires a store, a media handler, a newsletter store and a logger")
	}
	return &Handler{store: store, images: images, letters: letters, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/faq", ac.RequireStaff(h.FAQ))
	mux.HandleFunc("POST /admin/faq", ac.RequireStaff(h.CreateFAQ))
	mux.HandleFunc("POST /admin/faq/{id}", ac.RequireStaff(h.EditFAQ))
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

var notices = map[string]web.NoticeEntry{
	"ok":      web.Done(i18n.KeyAdminNoticeOK),
	"refused": web.Refused(i18n.KeyAdminNoticeRefused),
	"saved":   web.Done(i18n.KeyAdminNoticeSaved),
	"sent":    web.Done(i18n.KeyAdminNoticeSent),
	"already": web.Done(i18n.KeyAdminNoticeAlready),
}

func (h *Handler) FAQ(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.FAQ(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read faq", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = web.Notice(r, notices)
	web.Render(w, r, h.log, http.StatusOK, admin.FAQ(
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
		Category:   r.PostFormValue("category"),
		Question:   r.PostFormValue("question"),
		Answer:     r.PostFormValue("answer"),
		CategoryEn: r.PostFormValue("category_en"),
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
	typed := admin.FAQEntry{
		ID:       f.ID,
		Category: f.Category, Question: f.Question, Answer: f.Answer,
		CategoryEn: f.CategoryEn, QuestionEn: f.QuestionEn, AnswerEn: f.AnswerEn,
	}
	// An edit goes back to the entry it was made on. The add form's draft is the
	// wrong place: its error would send the operator to press 新增 and publish a
	// second copy while the original stays unedited.
	if f.ID != "" {
		view.Edit, view.EditErrors = typed, errs
	} else {
		view.Draft, view.Errors = typed, errs
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.FAQ(
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
		heroAfter = r.URL.Query().Get(web.KeysetParam)
	case bannerQueue:
		bannerAfter = r.URL.Query().Get(web.KeysetParam)
	}
	view, err := h.homeView(r.Context(), heroAfter, bannerAfter)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read home editor", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = web.Notice(r, notices)
	web.Render(w, r, h.log, http.StatusOK, admin.Home(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageHero)}, &view))
}

func (h *Handler) homeView(ctx context.Context, heroAfter, bannerAfter string) (admin.HeroView, error) {
	view, err := h.store.HeroSlides(ctx, heroAfter)
	if err != nil {
		return admin.HeroView{}, err
	}
	banners, err := h.store.Banners(ctx, bannerAfter)
	if err != nil {
		return admin.HeroView{}, err
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
		h.rejectBanner(w, r, f, errs)
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
	w http.ResponseWriter, r *http.Request, f *BannerForm, errs map[string]string,
) {
	view, err := h.homeView(r.Context(), "", "")
	if err != nil {
		h.log.ErrorContext(r.Context(), "read home editor", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Errors = errs
	view.BannerDraft = admin.BannerDraft{
		Message: f.Message, Short: f.Short, Code: f.Code,
		CTALabel: f.CTALabel, CTAHref: f.CTAHref, Days: r.PostFormValue("days"),
		MessageEn: f.MessageEn, ShortEn: f.ShortEn, CTALabelEn: f.CTALabelEn,
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Home(
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
	view.Draft = admin.HeroDraft{
		Eyebrow: r.PostFormValue("eyebrow"), Headline: r.PostFormValue("headline"), Body: r.PostFormValue("body"),
		PrimaryLabel: r.PostFormValue("primary_label"), PrimaryHref: r.PostFormValue("primary_href"),
		SecondLabel: r.PostFormValue("second_label"), SecondHref: r.PostFormValue("second_href"),
		ImageKey: f.ImageKey, ImageAlt: r.PostFormValue("alt"), Days: r.PostFormValue("days"),
		EyebrowEn: r.PostFormValue("eyebrow_en"), HeadlineEn: r.PostFormValue("headline_en"), BodyEn: r.PostFormValue("body_en"),
		PrimaryLabelEn: r.PostFormValue("primary_label_en"), SecondLabelEn: r.PostFormValue("second_label_en"),
		ImageAltEn: r.PostFormValue("alt_en"),
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Home(
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
	view.Notice = web.Notice(r, notices)
	web.Render(w, r, h.log, http.StatusOK, admin.Newsletter(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageNewsletter)}, view))
}

// ComposeNewsletter writes a DRAFT and sends nothing: the irreversible step gets its own button.
func (h *Handler) ComposeNewsletter(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseLongTextForm(w, r, newsletter.MaxIssueSubjectRunes+newsletter.MaxIssueBodyRunes); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	subject, body := r.PostFormValue("subject"), r.PostFormValue("body")

	if keys := newsletter.ValidateIssue(subject, body); len(keys) > 0 {
		view, err := h.newsletterView(r)
		if err != nil {
			h.log.ErrorContext(r.Context(), "read the newsletter", "error", err)
			access.ServerError(w, r, h.log)
			return
		}
		view.Draft = admin.NewsletterDraft{Subject: subject, Body: body}
		view.Errors = make(map[string]string, len(keys))
		for field, k := range keys {
			view.Errors[field] = i18n.T(r.Context(), k)
		}
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Newsletter(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageNewsletter)}, view))
		return
	}

	if _, err := h.letters.Compose(r.Context(), subject, body, audit.ActorID(r.Context())); err != nil {
		h.log.ErrorContext(r.Context(), "compose a newsletter issue", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	http.Redirect(w, r, "/admin/newsletter?saved=1", http.StatusSeeOther)
}

// SendNewsletter relies on the store refusing a second send in the UPDATE's own
// WHERE clause, because a reload is not the only way two of these arrive.
func (h *Handler) SendNewsletter(w http.ResponseWriter, r *http.Request) {
	switch _, err := h.letters.Send(r.Context(), r.PathValue("id"), audit.ActorID(r.Context())); {
	case err == nil:
		http.Redirect(w, r, "/admin/newsletter?sent=1", http.StatusSeeOther)
	case errors.Is(err, newsletter.ErrAlreadySent):
		http.Redirect(w, r, "/admin/newsletter?already=1", http.StatusSeeOther)
	case errors.Is(err, newsletter.ErrNoSuchIssue):
		access.NotFound(w, r, h.log)
	default:
		h.log.ErrorContext(r.Context(), "send a newsletter issue", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) newsletterView(r *http.Request) (admin.NewsletterView, error) {
	counts, err := h.letters.Counts(r.Context())
	if err != nil {
		return admin.NewsletterView{}, err
	}
	issues, err := h.letters.Issues(r.Context(), NewsletterIssueLimit)
	if err != nil {
		return admin.NewsletterView{}, err
	}
	view := admin.NewsletterView{
		Active: counts.Active, Unsubscribed: counts.Unsubscribed, Awaiting: counts.Awaiting,
		Issues: make([]admin.NewsletterIssue, 0, len(issues)),
	}
	for i := range issues {
		it := &issues[i]
		view.Issues = append(view.Issues, admin.NewsletterIssue{
			ID: it.ID, Subject: it.Subject, Body: it.Body, Sent: it.Sent,
			SentAt: it.SentAt, Recipients: it.Recipients, SentBy: it.SentBy,
		})
	}
	return view, nil
}
