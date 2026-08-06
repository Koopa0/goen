package newsletter

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// Handler serves the footer's signup form and the two links it leads to.
type Handler struct {
	store *Store
	limit *ratelimit.Limiter
	log   *slog.Logger
}

// NewHandler returns a Handler writing through store.
//
// limit bounds submissions per ADDRESS. Per-IP is middleware in cmd/goen, and
// neither alone is enough: filling somebody else's mailbox takes one request per
// machine, which per-IP limiting cannot see, and a distributed script walking
// addresses is invisible to a per-address bucket.
func NewHandler(store *Store, limit *ratelimit.Limiter, log *slog.Logger) *Handler {
	if store == nil || limit == nil || log == nil {
		panic("newsletter: NewHandler requires a store, a limiter and a logger")
	}
	return &Handler{store: store, limit: limit, log: log}
}

// Submit serves POST /newsletter.
//
// The form lives in the footer of every page, so a plain browser cannot be
// answered with "the same page again" — it is redirected to the acknowledgement
// instead.
//
// Nothing joins the list here. The answer is the same whether a link was sent,
// whether the address was already on the list, and whether it belongs to the
// person who typed it: a form that says "you are already subscribed" reports
// membership of a mailing list to anybody who cares to type an address in.
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		h.log.WarnContext(r.Context(), "parse newsletter form", "error", err)
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}

	addr := email.Clean(r.PostFormValue("email"))

	if k := Validate(addr); k != "" {
		// The length message carries the limit. Sprintf on a message with no
		// verb is a no-op, so one call covers all three rather than a switch
		// that has to be kept in step with the validator.
		h.fail(w, r, http.StatusUnprocessableEntity, addr,
			fmt.Sprintf(i18n.T(r.Context(), k), email.Max))
		return
	}

	// Keyed on the address, and BEFORE the write: without it the form mails a
	// confirmation to whoever is typed into it, as often as somebody presses the
	// button. That is the abuse this endpoint has, and it is not credential
	// guessing — it is using goen to deliver mail to a stranger.
	if retryAfter, ok := h.limit.Allow("newsletter:" + addr); !ok {
		ratelimit.Refuse(w, retryAfter)
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

// Thanks serves GET /newsletter/thanks, the landing point of the plain form's
// redirect.
//
// It says a letter is on its way, not that the subscription is done, because it
// is not: the mailbox has to answer. Saying "已訂閱" here is what the page used
// to do, and it was a promise the shop had no way to keep.
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

// ConfirmPage serves GET /newsletter/confirm.
//
// The token is echoed into a form rather than acted on. Following a link must
// not write anything: mail clients and security gateways fetch the URLs in a
// message before a human sees it, and a confirmation a scanner can complete is
// not a confirmation of anything.
func (h *Handler) ConfirmPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	web.Render(w, r, h.log, http.StatusOK, pages.NewsletterAction(
		pages.NewsletterMeta(i18n.T(ctx, i18n.KeyNewsletterConfirmTitle)),
		pages.NewsletterActionView{
			Heading: i18n.T(ctx, i18n.KeyNewsletterConfirmTitle),
			Body:    i18n.T(ctx, i18n.KeyNewsletterConfirmBody),
			Action:  "/newsletter/confirm",
			Submit:  i18n.T(ctx, i18n.KeyNewsletterConfirmSubmit),
			Token:   r.URL.Query().Get("token"),
		}))
}

// Confirm serves POST /newsletter/confirm.
func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	addr, err := h.store.Confirm(ctx, r.PostFormValue("token"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.linkFailed(w, r, i18n.T(ctx, i18n.KeyNewsletterLinkDead),
				i18n.T(ctx, i18n.KeyNewsletterConfirmDead))
			return
		}
		h.log.ErrorContext(ctx, "confirm newsletter", "error", err)
		h.linkFailed(w, r, i18n.T(ctx, i18n.KeyTryAgainTitle), i18n.T(ctx, i18n.KeyTryAgainBody))
		return
	}

	web.Render(w, r, h.log, http.StatusOK, pages.NewsletterAction(
		pages.NewsletterMeta(i18n.T(ctx, i18n.KeyNewsletterDone)),
		pages.NewsletterActionView{
			Heading: i18n.T(ctx, i18n.KeyNewsletterDone),
			Body:    fmt.Sprintf(i18n.T(ctx, i18n.KeyNewsletterDoneBody), addr),
		}))
}

// UnsubscribePage serves GET /newsletter/unsubscribe, for the same reason
// ConfirmPage exists: a scanner that follows the link must not take somebody off
// the list without their knowing.
func (h *Handler) UnsubscribePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	web.Render(w, r, h.log, http.StatusOK, pages.NewsletterAction(
		pages.NewsletterMeta(i18n.T(ctx, i18n.KeyNewsletterLeaveTitle)),
		pages.NewsletterActionView{
			Heading: i18n.T(ctx, i18n.KeyNewsletterLeaveTitle),
			Body:    i18n.T(ctx, i18n.KeyNewsletterLeaveBody),
			Action:  "/newsletter/unsubscribe",
			Submit:  i18n.T(ctx, i18n.KeyNewsletterLeaveSubmit),
			Token:   r.URL.Query().Get("token"),
		}))
}

// Unsubscribe serves POST /newsletter/unsubscribe.
//
// A second click answers the same success as the first. Only a token matching
// nothing is a failure worth showing, because only there is the reader's address
// still on the list.
func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	addr, err := h.store.Unsubscribe(ctx, r.PostFormValue("token"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.linkFailed(w, r, i18n.T(ctx, i18n.KeyNewsletterLinkDead),
				i18n.T(ctx, i18n.KeyNewsletterLeaveDead))
			return
		}
		h.log.ErrorContext(ctx, "unsubscribe newsletter", "error", err)
		h.linkFailed(w, r, i18n.T(ctx, i18n.KeyTryAgainTitle), i18n.T(ctx, i18n.KeyTryAgainBody))
		return
	}

	web.Render(w, r, h.log, http.StatusOK, pages.NewsletterAction(
		pages.NewsletterMeta(i18n.T(ctx, i18n.KeyNewsletterLeft)),
		pages.NewsletterActionView{
			Heading: i18n.T(ctx, i18n.KeyNewsletterLeft),
			Body:    fmt.Sprintf(i18n.T(ctx, i18n.KeyNewsletterLeftBody), addr),
		}))
}

// linkFailed answers a link that cannot be acted on. 422 rather than 404: the
// route exists and the request was understood — what failed is the token in it.
func (h *Handler) linkFailed(w http.ResponseWriter, r *http.Request, heading, body string) {
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.NewsletterAction(
		pages.NewsletterMeta(heading),
		pages.NewsletterActionView{Heading: heading, Body: body}))
}

// fail answers a rejected submission: the form itself for htmx, and a standalone
// page for a browser that has left the page the form was on.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, status int, addr, msg string) {
	if web.IsHTMX(r) {
		state := layouts.NewsletterState{Email: addr, Error: msg}
		web.Render(w, r, h.log, status, layouts.NewsletterForm(state))
		return
	}
	title := i18n.T(r.Context(), i18n.KeyNewsletterFailed)
	web.Render(w, r, h.log, status, pages.Notice(layouts.Page{Title: title}, "", title, msg))
}
