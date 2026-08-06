package email

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

// NewsletterConfirm is what a newsletter.confirm message carries.
//
// The TOKEN travels in the payload, so it is written to outbox_messages and
// lives there for as long as that row does. The same trade as a password reset,
// with far less at stake: the link joins a mailing list, expires in two days,
// and is spent by the first use. What it must not become is a way to confirm
// somebody else's address, and it cannot — a leaked one can only subscribe the
// address it was issued for, which then holds a working unsubscribe link.
type NewsletterConfirm struct {
	// Locale is the language to send in, recorded by the producer. See
	// [Notifier.locale].
	Locale string `json:"locale"`
	Email  string `json:"email"`
	Token  string `json:"token"`
}

// SendNewsletterConfirm asks a mailbox whether it wants the newsletter.
//
// The message is deliberately quiet about goen and loud about the question. It
// is sent to an address that somebody typed into a form, and the person reading
// it may not be the person who typed it — so the first thing it has to say is
// how to ignore it.
func (n Notifier) SendNewsletterConfirm(ctx context.Context, p *NewsletterConfirm) error {
	if !Valid(p.Email) {
		return errors.New("a newsletter confirmation has no usable email address")
	}

	ctx = n.locale(ctx, p.Locale)
	link := strings.TrimRight(n.BaseURL, "/") + "/newsletter/confirm?token=" + url.QueryEscape(p.Token)
	return n.Sender.Send(ctx, &Message{
		To:      p.Email,
		Subject: i18n.T(ctx, i18n.KeyMailNewsConfirmSubject),
		Body:    n.letter(ctx, "", fmt.Sprintf(i18n.T(ctx, i18n.KeyMailNewsConfirmBody), link)),
	})
}

// NewsletterWelcome is what a newsletter.welcome message carries.
//
// It exists to deliver the unsubscribe link. That link never expires — an email
// sent a year ago still has to be able to take somebody off the list — so this
// token sits in outbox_messages indefinitely, which is the one place goen's
// token exposure is not bounded by an expiry. Recovering it from the database
// buys somebody the ability to unsubscribe one address from a newsletter, and
// pruning delivered messages is queued work rather than a mitigation claimed
// here.
type NewsletterWelcome struct {
	// Locale is the language to send in, recorded by the producer. See
	// [Notifier.locale].
	Locale           string `json:"locale"`
	Email            string `json:"email"`
	UnsubscribeToken string `json:"unsubscribe_token"`
}

// SendNewsletterWelcome confirms a subscription and says how to end it.
func (n Notifier) SendNewsletterWelcome(ctx context.Context, p *NewsletterWelcome) error {
	if !Valid(p.Email) {
		return errors.New("a newsletter welcome has no usable email address")
	}

	ctx = n.locale(ctx, p.Locale)
	link := strings.TrimRight(n.BaseURL, "/") + "/newsletter/unsubscribe?token=" +
		url.QueryEscape(p.UnsubscribeToken)
	return n.Sender.Send(ctx, &Message{
		To:      p.Email,
		Subject: i18n.T(ctx, i18n.KeyMailNewsWelcomeSubject),
		Body:    n.letter(ctx, "", fmt.Sprintf(i18n.T(ctx, i18n.KeyMailNewsWelcomeBody), link)),
	})
}

// NewsletterIssue is one copy of one newsletter, addressed to one person.
//
// The subject and body are the shop's own words, authored in the back office and
// sent as written: they are content, like a product description, and a lookup
// table is not where a monthly letter belongs. What IS translated is the
// unsubscribe footer, which is chrome and which every copy has to carry.
type NewsletterIssue struct {
	Locale           string `json:"locale"`
	Email            string `json:"email"`
	Subject          string `json:"subject"`
	Body             string `json:"body"`
	UnsubscribeToken string `json:"unsubscribe_token"`
}

// SendNewsletterIssue delivers one copy.
//
// The unsubscribe link is appended by the sender rather than left to whoever
// wrote the issue. A newsletter without one is the thing this whole feature was
// built to make impossible, and "remember to paste the link" is not a mechanism.
func (n Notifier) SendNewsletterIssue(ctx context.Context, p *NewsletterIssue) error {
	if !Valid(p.Email) {
		return errors.New("a newsletter issue has no usable email address")
	}
	if p.UnsubscribeToken == "" {
		// Refused rather than sent without one. The outbox reschedules it and
		// Stuck() shows a human, which is the right amount of noise for "we were
		// about to send mail nobody could opt out of".
		return errors.New("a newsletter issue has no unsubscribe token")
	}

	ctx = n.locale(ctx, p.Locale)
	link := strings.TrimRight(n.BaseURL, "/") + "/newsletter/unsubscribe?token=" +
		url.QueryEscape(p.UnsubscribeToken)
	body := strings.Join([]string{
		p.Body, "",
		fmt.Sprintf(i18n.T(ctx, i18n.KeyMailIssueFooter), link),
		"", i18n.T(ctx, i18n.KeyMailNoReply), "— goen", "",
	}, "\n")

	return n.Sender.Send(ctx, &Message{To: p.Email, Subject: p.Subject, Body: body})
}

// EmailVerify is what an account.email_verify message carries.
//
// The address is in the payload as well as being the recipient, because a CHANGE
// sends to the new address while the account still holds the old one — the letter
// has to name which address it is about, or somebody with two accounts cannot tell
// what they are confirming.
type EmailVerify struct {
	Locale string `json:"locale"`
	Email  string `json:"email"`
	Token  string `json:"token"`
}

// SendEmailVerify asks somebody to prove an address is theirs.
func (n Notifier) SendEmailVerify(ctx context.Context, p *EmailVerify) error {
	if !Valid(p.Email) {
		return errors.New("an email verification has no usable address")
	}

	ctx = n.locale(ctx, p.Locale)
	link := strings.TrimRight(n.BaseURL, "/") + "/verify?token=" + url.QueryEscape(p.Token)
	return n.Sender.Send(ctx, &Message{
		To:      p.Email,
		Subject: i18n.T(ctx, i18n.KeyMailVerifySubject),
		Body:    n.letter(ctx, "", fmt.Sprintf(i18n.T(ctx, i18n.KeyMailVerifyBody), p.Email, link)),
	})
}
