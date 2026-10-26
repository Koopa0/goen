// Package letter composes goen's transactional letters, in the language each
// was asked for, and delivers them over SMTP or, in development, to the log.
//
// It reads each letter from the payload its outbox topic stored, or from what
// was resolved at delivery, and only cmd/goen imports it: a feature queues a
// payload in internal/outbox and never composes mail itself. It does not hold
// the rules an address must meet, which are internal/email's.
package letter

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/outbox"
)

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
		panic("letter: New requires a sender and a base URL")
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
	return "\n\n" + fmt.Sprintf(i18n.T(ctx, i18n.KeyMailStatutoryDisclosure),
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

func (n Notifier) orderURL(number string) string {
	return strings.TrimRight(n.baseURL, "/") + "/orders/" + number
}

func (n Notifier) SendOrderPlaced(ctx context.Context, p *outbox.OrderPlaced) error {
	if !email.Valid(p.Email) {
		// Still an error though no retry can fix it: the outbox reschedules and
		// Stuck() shows it to a human.
		return fmt.Errorf("order %s has no usable email address", p.OrderNumber)
	}

	ctx = n.locale(ctx, p.Locale)
	body := placedLetterBody(ctx, p.OrderNumber, p.OwedCents, n.orderURL(p.OrderNumber))
	if p.Snapshot != nil {
		body = n.placedConfirmation(ctx, p, p.OwedCents, time.Now())
	}
	return n.send(ctx, &Message{
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

func (n Notifier) SendPasswordReset(ctx context.Context, p *outbox.PasswordReset) error {
	if !email.Valid(p.Email) {
		return errors.New("a password reset has no usable email address")
	}

	ctx = n.locale(ctx, p.Locale)
	link := strings.TrimRight(n.baseURL, "/") + "/reset?token=" + url.QueryEscape(p.Token)
	return n.send(ctx, &Message{
		To:      p.Email,
		Subject: i18n.T(ctx, i18n.KeyMailResetSubject),
		Body:    n.letter(ctx, "", fmt.Sprintf(i18n.T(ctx, i18n.KeyMailResetBody), link)),
	})
}

// AccountExists is the account to tell that somebody tried to take its address:
// its address and name as they are when the letter is sent, and the locale the
// attempt was made in.
type AccountExists struct {
	Locale string
	Email  string
	Name   string
	// Change is an attempt to move another account to the address, rather
	// than to register it again.
	Change bool
}

// SendAccountExists tells the owner of an address that somebody tried to
// register it again, or to move another account to it. Both forms answer every
// address the same, so this letter is the only place the answer "you already
// have an account" is given, and it goes to the mailbox rather than to whoever
// asked.
func (n Notifier) SendAccountExists(ctx context.Context, p *AccountExists) error {
	if !email.Valid(p.Email) {
		return errors.New("an account-exists notice has no usable recipient")
	}
	ctx = n.locale(ctx, p.Locale)
	base := strings.TrimRight(n.baseURL, "/")
	subject, body := i18n.KeyMailAccountExistsSubject, i18n.KeyMailAccountExistsBody
	if p.Change {
		subject, body = i18n.KeyMailAddressInUseSubject, i18n.KeyMailAddressInUseBody
	}
	return n.send(ctx, &Message{
		To:      p.Email,
		Subject: i18n.T(ctx, subject),
		Body:    n.letter(ctx, p.Name, fmt.Sprintf(i18n.T(ctx, body), base+"/signin", base+"/forgot")),
	})
}
