package newsletter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"time"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/web/compress"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/ratelimit"
	"github.com/koopa0/goen/internal/web/respond"
)

type Handler struct {
	store *Store
	limit *ratelimit.Limiter
	log   *slog.Logger
}

// NewHandler takes limit for Submit, which spends two disjoint keys, the client
// IP and "newsletter:"+the address, since neither sees the attack the other
// bounds. The address key must earn a token back well within [ConfirmTTL]; see
// [Handler.requestConfirmation].
func NewHandler(store *Store, limit *ratelimit.Limiter, log *slog.Logger) *Handler {
	if store == nil || limit == nil || log == nil {
		panic("newsletter: NewHandler requires a store, a limiter and a logger")
	}
	return &Handler{store: store, limit: limit, log: log}
}

// Routes registers signing up and the two mailed links.
func (h *Handler) Routes(mux *http.ServeMux) {
	// Submit applies the per-IP bound itself: Guard would answer plain text and
	// htmx would swap that over the footer form.
	mux.HandleFunc("POST /newsletter", h.Submit)
	mux.HandleFunc("GET /newsletter/thanks", h.Thanks)
	// Both links open a page carrying a form; a GET that confirmed or
	// unsubscribed would be completed by every link scanner that reads the mail
	// before its owner does.
	mux.HandleFunc("GET /newsletter/confirm", h.ConfirmPage)
	mux.HandleFunc("POST /newsletter/confirm", h.Confirm)
	mux.HandleFunc("GET /newsletter/unsubscribe", h.UnsubscribePage)
	mux.HandleFunc("POST /newsletter/unsubscribe", h.Unsubscribe)
}

// Submit joins nobody to the list and answers every outcome identically.
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	// Per-IP lives here rather than in Guard so an HTMX refusal can still
	// replace the footer form; Guard's plain 429 would swap over it.
	if retryAfter, ok := h.limit.Allow(ratelimit.ClientKey(r)); !ok {
		h.log.WarnContext(r.Context(), "rate limited",
			"path", r.URL.Path, "retry_after_seconds", int(retryAfter.Seconds()+1))
		h.throttled(w, r, retryAfter)
		return
	}

	if err := formtext.ParseForm(w, r); err != nil {
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
			msg = fmt.Sprintf(msg, email.MaxBytes)
		}
		h.fail(w, r, http.StatusUnprocessableEntity, addr, msg)
		return
	}

	if err := h.requestConfirmation(r.Context(), addr); err != nil {
		h.log.ErrorContext(r.Context(), "request newsletter confirmation", "error", err)
		h.fail(w, r, http.StatusInternalServerError, addr, i18n.T(r.Context(), i18n.KeyNewsletterRetry))
		return
	}

	if respond.IsHTMX(r) {
		respond.Component(w, r, h.log, http.StatusOK, frame.NewsletterForm(frame.NewsletterState{Done: true, Email: email.Mask(addr)}))
		return
	}
	http.Redirect(w, r, "/newsletter/thanks?"+url.Values{"address": {email.Mask(addr)}}.Encode(), http.StatusSeeOther)
}

// requestConfirmation queues a confirmation for addr unless the address has had
// its allowance of them, and either way the caller answers that the mail is on
// its way. The allowance bounds the mail and never the request, and only the
// confirmations it queues spend it: a request that queues nothing, because the
// address is subscribed or the write failed, gives its token back. Every
// confirmation that spent it went to the address, the newest replacing the
// rest, and an empty allowance was last spent within one interval of the limit,
// which is shorter than ConfirmTTL, so whoever asks then already has a live
// link. That is the rule the ratelimit package states for a shared key that
// bounds what is sent, and the one /forgot follows.
func (h *Handler) requestConfirmation(ctx context.Context, addr string) error {
	// Reserved BEFORE the write: unbounded, the form mails a confirmation to
	// whoever is typed into it, as often as the button is pressed.
	allowance, _, ok := h.limit.Reserve("newsletter:" + addr)
	if !ok {
		h.log.WarnContext(ctx, "newsletter confirmation not queued: the address has had its allowance")
		return nil
	}
	outcome, err := h.store.Request(ctx, addr)
	if err != nil || outcome != Requested {
		allowance.Refund()
		return err
	}
	allowance.Keep()
	return nil
}

// Thanks answers the plain POST. Active subscriptions enqueue no letter; the
// acknowledgement must not reveal whether this address subscribed before.
func (h *Handler) Thanks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	meta := frame.Page{
		Title:       i18n.T(ctx, i18n.KeyCheckInbox),
		Description: i18n.T(ctx, i18n.KeyNewsletterSentMeta),
	}
	respond.Component(w, r, h.log, http.StatusOK, NewsletterSent(meta, email.ReadMasked(r.URL.Query().Get("address"))))
}

// ConfirmPage echoes the token into a form rather than acting on it: a
// confirmation a link scanner can complete confirms nothing.
func (h *Handler) ConfirmPage(w http.ResponseWriter, r *http.Request) {
	compress.Skip(w)
	ctx := r.Context()
	if r.URL.Query().Get("done") == "1" {
		respond.Component(w, r, h.log, http.StatusOK, frame.EmailLinkPage(
			frame.EmailLinkMeta(i18n.T(ctx, i18n.KeyNewsletterDone)),
			frame.EmailLinkView{
				Heading: i18n.T(ctx, i18n.KeyNewsletterDone),
				Body:    i18n.T(ctx, i18n.KeyNewsletterDoneBody),
			}))
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		respond.Component(w, r, h.log, http.StatusOK, frame.EmailLinkPage(
			frame.EmailLinkMeta(i18n.T(ctx, i18n.KeyNewsletterConfirmTitle)),
			frame.EmailLinkView{
				Heading:  i18n.T(ctx, i18n.KeyNewsletterConfirmTitle),
				Body:     i18n.T(ctx, i18n.KeyEmailLinkIncomplete),
				Recovery: frame.EmailLinkSubscribe,
			}))
		return
	}
	respond.Component(w, r, h.log, http.StatusOK, frame.EmailLinkPage(
		frame.EmailLinkMeta(i18n.T(ctx, i18n.KeyNewsletterConfirmTitle)),
		frame.EmailLinkView{
			Heading: i18n.T(ctx, i18n.KeyNewsletterConfirmTitle),
			Body:    i18n.T(ctx, i18n.KeyNewsletterConfirmBody),
			Action:  "/newsletter/confirm",
			Submit:  i18n.T(ctx, i18n.KeyNewsletterConfirmSubmit),
			Token:   token,
		}))
}

func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	_, err := h.store.Confirm(ctx, r.PostFormValue("token"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.deadLink(w, r, i18n.T(ctx, i18n.KeyNewsletterConfirmDead), frame.EmailLinkSubscribe)
			return
		}
		h.log.ErrorContext(ctx, "confirm newsletter", "error", err)
		h.linkFailed(w, r, i18n.T(ctx, i18n.KeyTryAgainTitle), i18n.T(ctx, i18n.KeyTryAgainBody))
		return
	}

	http.Redirect(w, r, "/newsletter/confirm?done=1", http.StatusSeeOther)
}

func (h *Handler) UnsubscribePage(w http.ResponseWriter, r *http.Request) {
	compress.Skip(w)
	ctx := r.Context()
	if r.URL.Query().Get("done") == "1" {
		respond.Component(w, r, h.log, http.StatusOK, frame.EmailLinkPage(
			frame.EmailLinkMeta(i18n.T(ctx, i18n.KeyNewsletterLeft)),
			frame.EmailLinkView{
				Heading: i18n.T(ctx, i18n.KeyNewsletterLeft),
				Body:    i18n.T(ctx, i18n.KeyNewsletterLeftBody),
			}))
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		respond.Component(w, r, h.log, http.StatusOK, frame.EmailLinkPage(
			frame.EmailLinkMeta(i18n.T(ctx, i18n.KeyNewsletterLeaveTitle)),
			frame.EmailLinkView{
				Heading:  i18n.T(ctx, i18n.KeyNewsletterLeaveTitle),
				Body:     i18n.T(ctx, i18n.KeyEmailLinkIncomplete),
				Recovery: frame.EmailLinkContact,
			}))
		return
	}
	respond.Component(w, r, h.log, http.StatusOK, frame.EmailLinkPage(
		frame.EmailLinkMeta(i18n.T(ctx, i18n.KeyNewsletterLeaveTitle)),
		frame.EmailLinkView{
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
		h.deadLink(w, r, i18n.T(ctx, i18n.KeyNewsletterLeaveDead), frame.EmailLinkContact)
		return
	}
	h.linkFailed(w, r, i18n.T(ctx, i18n.KeyTryAgainTitle), i18n.T(ctx, i18n.KeyTryAgainBody))
}

func parseUnsubscribeForm(w http.ResponseWriter, r *http.Request) error {
	if err := formtext.ParseForm(w, r); err != nil {
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
	err = r.ParseMultipartForm(formtext.MaxFormBytes) //nolint:gosec // G120: formtext.ParseForm above bounded the body with http.MaxBytesReader
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }() //nolint:errcheck // Best-effort temporary-file cleanup.
	}
	if err != nil {
		return err
	}
	return formtext.CheckStorable(r.Form)
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

func (h *Handler) deadLink(w http.ResponseWriter, r *http.Request, body string, recovery frame.EmailLinkRecovery) {
	heading := i18n.T(r.Context(), i18n.KeyEmailLinkDeadTitle)
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity, frame.EmailLinkPage(
		frame.EmailLinkMeta(heading),
		frame.EmailLinkView{Heading: heading, Body: body, Recovery: recovery}))
}

func (h *Handler) linkFailed(w http.ResponseWriter, r *http.Request, heading, body string) {
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity, frame.EmailLinkPage(
		frame.EmailLinkMeta(heading),
		frame.EmailLinkView{Heading: heading, Body: body}))
}

// throttled: HTMX swaps the body into the footer form, so it must stay a form;
// a plain request can stay text. The form arrives empty: the client is refused
// before its form is read.
func (h *Handler) throttled(w http.ResponseWriter, r *http.Request, retryAfter time.Duration) {
	if respond.IsHTMX(r) {
		ratelimit.SetRetryAfter(w, retryAfter)
		h.fail(w, r, http.StatusTooManyRequests, "", i18n.T(r.Context(), i18n.KeyTooManyRequests))
		return
	}
	ratelimit.Refuse(r.Context(), w, retryAfter)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, status int, addr, msg string) {
	state := frame.NewsletterState{Email: addr}
	if status == http.StatusUnprocessableEntity {
		state.FieldRefusal = msg
	} else {
		state.Notice = msg
	}
	if respond.IsHTMX(r) {
		respond.Component(w, r, h.log, status, frame.NewsletterForm(state))
		return
	}
	title := i18n.T(r.Context(), i18n.KeyNewsletterFailed)
	respond.Component(w, r, h.log, status, frame.Notice(frame.Page{Title: title, Newsletter: state}, "", title, i18n.T(r.Context(), i18n.KeyNewsletterReviewForm)))
}
