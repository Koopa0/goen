package email

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

// Notifier turns outbox messages into mail.
//
// It lives here rather than in internal/outbox because what an order.placed
// message SAYS is an email concern; that it gets delivered at all is the
// outbox's. The outbox knows nothing about mail, and this knows nothing about
// retries.
type Notifier struct {
	Sender Sender
	// BaseURL is where a link in an email points. Required: a receipt whose
	// "view your order" link goes to 127.0.0.1 is a receipt nobody can use.
	BaseURL string
}

// locale returns a context that speaks the language the message was recorded in.
//
// The worker's own context has no locale — it is not serving anybody — so the
// language has to come from the payload, and the payload gets it from whoever
// produced the message. Two of them are produced with no visitor present: the
// receipt comes from a Stripe webhook and the dispatch notice from a
// back-office click, and both read orders.locale rather than the request they
// happen to be running in.
//
// An empty or unknown tag falls back to the default rather than failing. A row
// written before this column existed would otherwise be a message nobody can
// send, and the fallback is the language the shop is written in.
func (n Notifier) locale(ctx context.Context, tag string) context.Context {
	return i18n.WithLocale(ctx, i18n.Parse(tag))
}

// letter wraps a body in the greeting and the sign-off every message shares.
//
// One function, so a new message cannot arrive without them and none of them can
// drift. name may be empty — a restock notice goes to an address nobody has
// necessarily named — and the greeting drops the placeholder rather than
// addressing somebody as an empty string.
func (n Notifier) letter(ctx context.Context, name, body string) string {
	greeting := i18n.T(ctx, i18n.KeyMailHello)
	if name != "" {
		greeting = fmt.Sprintf(i18n.T(ctx, i18n.KeyMailGreeting), name)
	}
	return strings.Join([]string{
		greeting, "", body, "", i18n.T(ctx, i18n.KeyMailNoReply), "— goen", "",
	}, "\n")
}

// orderURL is where a message points a customer at their own order.
func (n Notifier) orderURL(number string) string {
	return strings.TrimRight(n.BaseURL, "/") + "/orders/" + number
}

// OrderPlaced is the payload of an order.placed message. Declared again here
// rather than imported from internal/cart, because a consumer that imports the
// producer's types is a consumer that cannot be deployed a version behind.
type OrderPlaced struct {
	// Locale is the language to send in, recorded by the producer. See
	// [Notifier.locale].
	Locale      string `json:"locale"`
	OrderNumber string `json:"order_number"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	TotalCents  int64  `json:"total_cents"`
}

// SendOrderPlaced sends the confirmation.
func (n Notifier) SendOrderPlaced(ctx context.Context, p *OrderPlaced) error {
	if !Valid(p.Email) {
		// A message with no usable address will never succeed however often it
		// is retried, but it is still an error: the outbox reschedules it and
		// Stuck() shows it to a human, which is the right amount of noise for
		// "an order was placed and we cannot tell anyone".
		return fmt.Errorf("order %s has no usable email address", p.OrderNumber)
	}

	ctx = n.locale(ctx, p.Locale)
	return n.Sender.Send(ctx, &Message{
		To:      p.Email,
		Subject: fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedSubject), p.OrderNumber),
		Body: n.letter(ctx, p.Name, fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedBody),
			p.OrderNumber, twd(p.TotalCents), n.orderURL(p.OrderNumber))),
	})
}

// twd formats cents as New Taiwan dollars.
//
// Duplicated from the UI's own formatter on purpose: an email is not a page,
// and importing the template package here would drag the whole rendering layer
// into a worker that renders nothing.
func twd(cents int64) string {
	whole := cents / 100
	s := strconv.FormatInt(whole, 10)
	// Thousands separators, inserted from the right.
	var out strings.Builder
	out.WriteString("NT$")
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(r)
	}
	return out.String()
}

// PasswordReset is what a reset message needs.
//
// The TOKEN travels in the payload, which means it is written to
// outbox_messages and lives there until the row is cleaned up. That is a real
// exposure and it is bounded on purpose: the token expires in an hour, it is
// single-use, and using it ends every session — so a token recovered from an
// old outbox row is a token that no longer opens anything.
//
// The alternative — sending from the handler to keep it out of the database —
// loses the message when the process dies between the write and the send, and
// a reset nobody receives is a customer permanently locked out. Of the two, an
// hour-long window on a spent credential is the smaller one.
type PasswordReset struct {
	// Locale is the language to send in, recorded by the producer. See
	// [Notifier.locale].
	Locale string `json:"locale"`
	Email  string
	Token  string
}

// SendPasswordReset mails somebody a link back into their account.
func (n Notifier) SendPasswordReset(ctx context.Context, p *PasswordReset) error {
	if !Valid(p.Email) {
		return errors.New("a password reset has no usable email address")
	}

	ctx = n.locale(ctx, p.Locale)
	link := strings.TrimRight(n.BaseURL, "/") + "/reset?token=" + url.QueryEscape(p.Token)
	return n.Sender.Send(ctx, &Message{
		To:      p.Email,
		Subject: i18n.T(ctx, i18n.KeyMailResetSubject),
		Body:    n.letter(ctx, "", fmt.Sprintf(i18n.T(ctx, i18n.KeyMailResetBody), link)),
	})
}
