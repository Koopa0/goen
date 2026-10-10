package newsletter

import (
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	store *Store
	limit *ratelimit.Limiter
	log   *slog.Logger
}

// NewHandler takes limit for Submit, which spends two disjoint keys, the client
// IP and "newsletter:"+the address, since neither sees the attack the other
// bounds.
func NewHandler(store *Store, limit *ratelimit.Limiter, log *slog.Logger) *Handler {
	if store == nil || limit == nil || log == nil {
		panic("newsletter: NewHandler requires a store, a limiter and a logger")
	}
	return &Handler{store: store, limit: limit, log: log}
}

// Submit joins nobody to the list and answers every outcome identically.
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	// Per-IP lives here rather than in Guard so an HTMX refusal can still
	// replace the footer form; Guard's plain 429 would swap over it.
	if retryAfter, ok := h.limit.Allow(ratelimit.ClientKey(r)); !ok {
		h.log.WarnContext(r.Context(), "rate limited",
			"path", r.URL.Path, "retry_after_seconds", int(retryAfter.Seconds()+1))
		h.throttled(w, r, "", retryAfter)
		return
	}

	if err := web.ParseForm(w, r); err != nil {
		h.log.WarnContext(r.Context(), "parse newsletter form", "error", err)
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}

	addr := email.Clean(r.PostFormValue("email"))

	if k := Validate(addr); k != "" {
		msg := i18n.T(r.Context(), k)
		if k == i18n.KeyEmailTooLong {
			// Only this message carries a verb; Sprintf appends %!(EXTRA ...)
			// to one that has none.
			msg = fmt.Sprintf(msg, email.Max)
		}
		h.fail(w, r, http.StatusUnprocessableEntity, addr, msg)
		return
	}

	// Keyed on the address and BEFORE the write: unbounded, the form mails a
	// confirmation to whoever is typed into it, as often as the button is
	// pressed.
	if retryAfter, ok := h.limit.Allow("newsletter:" + addr); !ok {
		h.throttled(w, r, addr, retryAfter)
		return
	}

	if _, err := h.store.Request(r.Context(), addr); err != nil {
		h.log.ErrorContext(r.Context(), "request newsletter confirmation", "error", err)
		h.fail(w, r, http.StatusInternalServerError, addr, i18n.T(r.Context(), i18n.KeyNewsletterRetry))
		return
	}

	if web.IsHTMX(r) {
		web.Render(w, r, h.log, http.StatusOK, layouts.NewsletterForm(layouts.NewsletterState{Done: true}))
		return
	}
	http.Redirect(w, r, "/newsletter/thanks", http.StatusSeeOther)
}

// Thanks says a letter is on its way, not that the subscription is done.
func (h *Handler) Thanks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	meta := layouts.Page{
		Title:       i18n.T(ctx, i18n.KeyNewsletterSent),
		Description: i18n.T(ctx, i18n.KeyNewsletterSentMeta),
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Notice(
		meta,
		"",
		i18n.T(ctx, i18n.KeyNewsletterSent),
		i18n.T(ctx, i18n.KeyNewsletterSentBody),
	))
}

// ConfirmPage echoes the token into a form rather than acting on it: a
// confirmation a link scanner can complete confirms nothing.
func (h *Handler) ConfirmPage(w http.ResponseWriter, r *http.Request) {
	web.NoCompress(w)
	ctx := r.Context()
	if r.URL.Query().Get("done") == "1" {
		web.Render(w, r, h.log, http.StatusOK, pages.EmailLinkPage(
			pages.EmailLinkMeta(i18n.T(ctx, i18n.KeyNewsletterDone)),
			pages.EmailLinkView{
				Heading: i18n.T(ctx, i18n.KeyNewsletterDone),
				Body:    i18n.T(ctx, i18n.KeyNewsletterDoneBody),
			}))
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		web.Render(w, r, h.log, http.StatusOK, pages.EmailLinkPage(
			pages.EmailLinkMeta(i18n.T(ctx, i18n.KeyNewsletterConfirmTitle)),
			pages.EmailLinkView{
				Heading:  i18n.T(ctx, i18n.KeyNewsletterConfirmTitle),
				Body:     i18n.T(ctx, i18n.KeyEmailLinkIncomplete),
				Recovery: pages.EmailLinkSubscribe,
			}))
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.EmailLinkPage(
		pages.EmailLinkMeta(i18n.T(ctx, i18n.KeyNewsletterConfirmTitle)),
		pages.EmailLinkView{
			Heading: i18n.T(ctx, i18n.KeyNewsletterConfirmTitle),
			Body:    i18n.T(ctx, i18n.KeyNewsletterConfirmBody),
			Action:  "/newsletter/confirm",
			Submit:  i18n.T(ctx, i18n.KeyNewsletterConfirmSubmit),
			Token:   token,
		}))
}

func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	_, err := h.store.Confirm(ctx, r.PostFormValue("token"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.deadLink(w, r, i18n.T(ctx, i18n.KeyNewsletterConfirmDead), pages.EmailLinkSubscribe)
			return
		}
		h.log.ErrorContext(ctx, "confirm newsletter", "error", err)
		h.linkFailed(w, r, i18n.T(ctx, i18n.KeyTryAgainTitle), i18n.T(ctx, i18n.KeyTryAgainBody))
		return
	}

	http.Redirect(w, r, "/newsletter/confirm?done=1", http.StatusSeeOther)
}

func (h *Handler) UnsubscribePage(w http.ResponseWriter, r *http.Request) {
	web.NoCompress(w)
	ctx := r.Context()
	if r.URL.Query().Get("done") == "1" {
		web.Render(w, r, h.log, http.StatusOK, pages.EmailLinkPage(
			pages.EmailLinkMeta(i18n.T(ctx, i18n.KeyNewsletterLeft)),
			pages.EmailLinkView{
				Heading: i18n.T(ctx, i18n.KeyNewsletterLeft),
				Body:    i18n.T(ctx, i18n.KeyNewsletterLeftBody),
			}))
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		web.Render(w, r, h.log, http.StatusOK, pages.EmailLinkPage(
			pages.EmailLinkMeta(i18n.T(ctx, i18n.KeyNewsletterLeaveTitle)),
			pages.EmailLinkView{
				Heading:  i18n.T(ctx, i18n.KeyNewsletterLeaveTitle),
				Body:     i18n.T(ctx, i18n.KeyEmailLinkIncomplete),
				Recovery: pages.EmailLinkContact,
			}))
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.EmailLinkPage(
		pages.EmailLinkMeta(i18n.T(ctx, i18n.KeyNewsletterLeaveTitle)),
		pages.EmailLinkView{
			Heading: i18n.T(ctx, i18n.KeyNewsletterLeaveTitle),
			Body:    i18n.T(ctx, i18n.KeyNewsletterLeaveBody),
			Action:  "/newsletter/unsubscribe",
			Submit:  i18n.T(ctx, i18n.KeyNewsletterLeaveSubmit),
			Token:   token,
		}))
}

// Unsubscribe answers a second click with the same success as the first; only a
// token matching nothing is shown as a failure, because only there is the
// reader still on the list.
func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	if err := parseUnsubscribeForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	token, oneClick, err := unsubscribeToken(r)
	if err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	_, err = h.store.Unsubscribe(ctx, token)
	if err == nil {
		if oneClick {
			w.WriteHeader(http.StatusOK)
		} else {
			http.Redirect(w, r, "/newsletter/unsubscribe?done=1", http.StatusSeeOther)
		}
		return
	}
	if !errors.Is(err, ErrNotFound) {
		h.log.ErrorContext(ctx, "unsubscribe newsletter", "error", err)
	}
	if oneClick {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrNotFound) {
			status = http.StatusUnprocessableEntity
		}
		w.WriteHeader(status)
		return
	}
	if errors.Is(err, ErrNotFound) {
		h.deadLink(w, r, i18n.T(ctx, i18n.KeyNewsletterLeaveDead), pages.EmailLinkContact)
		return
	}
	h.linkFailed(w, r, i18n.T(ctx, i18n.KeyTryAgainTitle), i18n.T(ctx, i18n.KeyTryAgainBody))
}

func parseUnsubscribeForm(w http.ResponseWriter, r *http.Request) error {
	if err := web.ParseForm(w, r); err != nil {
		return err
	}
	if r.Header.Get("Content-Type") == "" {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return err
	}
	if mediaType != "multipart/form-data" {
		return nil
	}
	// RFC 8058 receivers may use either form encoding. ParseForm has already
	// bounded the body, but it leaves multipart fields unread.
	err = r.ParseMultipartForm(web.MaxFormBytes)
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }() //nolint:errcheck // Best-effort temporary-file cleanup.
	}
	if err != nil {
		return err
	}
	return web.CheckFormText(r.Form)
}

func unsubscribeToken(r *http.Request) (token string, oneClick bool, err error) {
	marker, oneClick := r.PostForm["List-Unsubscribe"]
	if !oneClick {
		return r.PostFormValue("token"), false, nil
	}
	tokens := r.URL.Query()["token"]
	if len(r.PostForm) != 1 || len(marker) != 1 || marker[0] != "One-Click" || len(tokens) != 1 || tokens[0] == "" {
		return "", true, errors.New("newsletter: invalid one-click unsubscribe form")
	}
	return tokens[0], true, nil
}

func (h *Handler) deadLink(w http.ResponseWriter, r *http.Request, body string, recovery pages.EmailLinkRecovery) {
	heading := i18n.T(r.Context(), i18n.KeyEmailLinkDeadTitle)
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.EmailLinkPage(
		pages.EmailLinkMeta(heading),
		pages.EmailLinkView{Heading: heading, Body: body, Recovery: recovery}))
}

func (h *Handler) linkFailed(w http.ResponseWriter, r *http.Request, heading, body string) {
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.EmailLinkPage(
		pages.EmailLinkMeta(heading),
		pages.EmailLinkView{Heading: heading, Body: body}))
}

// throttled: HTMX swaps the body into the footer form, so it must stay a form;
// a plain request can stay text.
func (h *Handler) throttled(w http.ResponseWriter, r *http.Request, addr string, retryAfter time.Duration) {
	if web.IsHTMX(r) {
		ratelimit.SetRetryAfter(w, retryAfter)
		h.fail(w, r, http.StatusTooManyRequests, addr, i18n.T(r.Context(), i18n.KeyTooManyRequests))
		return
	}
	ratelimit.Refuse(r.Context(), w, retryAfter)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, status int, addr, msg string) {
	state := layouts.NewsletterState{Email: addr}
	if status == http.StatusUnprocessableEntity {
		state.FieldRefusal = msg
	} else {
		state.Notice = msg
	}
	if web.IsHTMX(r) {
		web.Render(w, r, h.log, status, layouts.NewsletterForm(state))
		return
	}
	title := i18n.T(r.Context(), i18n.KeyNewsletterFailed)
	web.Render(w, r, h.log, status, pages.Notice(layouts.Page{Title: title, Newsletter: state}, "", title, i18n.T(r.Context(), i18n.KeyNewsletterReviewForm)))
}
