package email

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
)

// Notifier turns outbox messages into mail.
type Notifier struct {
	sender  Sender
	baseURL string
	// seller and sellerContact are Consumer Protection Act §18 I item 1 — who
	// the trader is, and how to reach them. They travel with the confirmation
	// because §18 II wants the disclosure in a form the consumer can STORE.
	seller        string
	sellerContact string
}

// New returns a Notifier sending through sender, with baseURL as the origin of
// every link it mails. seller and sellerContact are Consumer Protection Act
// §18 I item 1 and are optional; with either empty the confirmation omits the
// disclosure rather than naming nobody.
func New(sender Sender, baseURL, seller, sellerContact string) Notifier {
	if sender == nil || baseURL == "" {
		panic("email: New requires a sender and a base URL")
	}
	return Notifier{
		sender: sender, baseURL: baseURL, seller: seller, sellerContact: sellerContact,
	}
}

// statutoryDisclosure is Consumer Protection Act §18 I. Empty when the shop is
// unconfigured: a disclosure naming nobody makes the letter merely look compliant.
func (n Notifier) statutoryDisclosure(ctx context.Context) string {
	if n.seller == "" || n.sellerContact == "" {
		return ""
	}
	return "\n" + fmt.Sprintf(i18n.T(ctx, i18n.KeyMailStatutoryDisclosure),
		n.seller, n.sellerContact)
}

// locale returns a context in the language the message was recorded in; the
// worker's own context has none.
func (n Notifier) locale(ctx context.Context, tag string) context.Context {
	return i18n.WithLocale(ctx, i18n.Parse(tag))
}

// letter wraps a body in the greeting and the sign-off every message shares.
// An empty name gets the bare greeting rather than "Hello ,".
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
	return strings.TrimRight(n.baseURL, "/") + "/orders/" + number
}

// OrderPlaced is the payload of an order.placed message. Declared again here
// rather than imported from internal/cart, because a consumer that imports the
// producer's types cannot be deployed a version behind.
type OrderPlaced struct {
	Locale      string `json:"locale"`
	OrderNumber string `json:"order_number"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	TotalCents  int64  `json:"total_cents"`
	// OwedCents is what is still payable after store credit. A missing
	// field (already-queued rows) falls back to TotalCents so an in-flight
	// letter does not flip to the funded copy.
	OwedCents *int64 `json:"owed_cents,omitempty"`
}

// SendOrderPlaced sends the confirmation.
func (n Notifier) SendOrderPlaced(ctx context.Context, p *OrderPlaced) error {
	if !Valid(p.Email) {
		// Still an error though no retry can fix it: the outbox reschedules and
		// Stuck() shows it to a human.
		return fmt.Errorf("order %s has no usable email address", p.OrderNumber)
	}

	ctx = n.locale(ctx, p.Locale)
	owed := p.TotalCents
	if p.OwedCents != nil {
		owed = *p.OwedCents
	}
	body := placedLetterBody(ctx, p.OrderNumber, owed, n.orderURL(p.OrderNumber))
	return n.sender.Send(ctx, &Message{
		To:      p.Email,
		Subject: fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedSubject), p.OrderNumber),
		Body:    n.letter(ctx, p.Name, body+n.statutoryDisclosure(ctx)),
	})
}

// placedLetterBody quotes the amount still owed, not the order total. A
// credit-funded order has nothing to collect.
func placedLetterBody(ctx context.Context, number string, owedCents int64, orderURL string) string {
	if owedCents == 0 {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedFundedBody), number, orderURL)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPlacedBody), number, twd(owedCents), orderURL)
}

// twd formats cents through the same renderer the page uses, so a letter and
// the page it links to cannot disagree about a figure.
func twd(cents int64) string { return money.TWD(cents) }

// PasswordReset is what a reset message needs. The token travels in the payload
// and lives in outbox_messages until the row is swept.
type PasswordReset struct {
	Locale string `json:"locale"`
	// Tagged, like every other payload here: erase_user matches the exact
	// recipient field and keeps the old "Email" key only for already-queued rows.
	Email string `json:"email"`
	Token string `json:"token"`
}

// SendPasswordReset mails somebody a link back into their account.
func (n Notifier) SendPasswordReset(ctx context.Context, p *PasswordReset) error {
	if !Valid(p.Email) {
		return errors.New("a password reset has no usable email address")
	}

	ctx = n.locale(ctx, p.Locale)
	link := strings.TrimRight(n.baseURL, "/") + "/reset?token=" + url.QueryEscape(p.Token)
	return n.sender.Send(ctx, &Message{
		To:      p.Email,
		Subject: i18n.T(ctx, i18n.KeyMailResetSubject),
		Body:    n.letter(ctx, "", fmt.Sprintf(i18n.T(ctx, i18n.KeyMailResetBody), link)),
	})
}
