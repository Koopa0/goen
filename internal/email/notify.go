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
type Notifier struct {
	Sender Sender
	// BaseURL is where a link in an email points. Required.
	BaseURL string
	// Seller and SellerContact are Consumer Protection Act §18 I item 1 — who
	// the trader is, and how to reach them. They travel with the confirmation
	// because §18 II wants the disclosure in a form the consumer can STORE.
	Seller        string
	SellerContact string
}

// statutoryDisclosure is Consumer Protection Act §18 I. Empty when the shop is
// unconfigured: a disclosure naming nobody makes the letter merely look compliant.
func (n Notifier) statutoryDisclosure(ctx context.Context) string {
	if n.Seller == "" || n.SellerContact == "" {
		return ""
	}
	return "\n" + fmt.Sprintf(i18n.T(ctx, i18n.KeyMailStatutoryDisclosure),
		n.Seller, n.SellerContact)
}

// locale returns a context speaking the language the message was recorded in.
// The worker's own context has none; an unknown tag falls back to the default.
func (n Notifier) locale(ctx context.Context, tag string) context.Context {
	return i18n.WithLocale(ctx, i18n.Parse(tag))
}

// letter wraps a body in the greeting and the sign-off every message shares.
// name may be empty, and the greeting then drops the placeholder rather than
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
// producer's types cannot be deployed a version behind.
type OrderPlaced struct {
	// Locale is the language to send in, recorded by the producer.
	Locale      string `json:"locale"`
	OrderNumber string `json:"order_number"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	TotalCents  int64  `json:"total_cents"`
}

// SendOrderPlaced sends the confirmation.
func (n Notifier) SendOrderPlaced(ctx context.Context, p *OrderPlaced) error {
	if !Valid(p.Email) {
		// Still an error though no retry can fix it: the outbox reschedules and
		// Stuck() shows it to a human.
		return fmt.Errorf("order %s has no usable email address", p.OrderNumber)
	}

	ctx = n.locale(ctx, p.Locale)
	return n.Sender.Send(ctx, &Message{
		To:      p.Email,
		Subject: fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedSubject), p.OrderNumber),
		Body: n.letter(ctx, p.Name, fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedBody),
			p.OrderNumber, twd(p.TotalCents), n.orderURL(p.OrderNumber))+
			n.statutoryDisclosure(ctx)),
	})
}

// twd formats cents as New Taiwan dollars.
func twd(cents int64) string {
	whole := cents / 100
	s := strconv.FormatInt(whole, 10)
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

// PasswordReset is what a reset message needs. The TOKEN travels in the payload
// and lives in outbox_messages until the row is swept; it expires in an hour, is
// single-use, and using it ends every session.
type PasswordReset struct {
	// Locale is the language to send in, recorded by the producer.
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
