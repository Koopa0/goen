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
type NewsletterConfirm struct {
	Locale string `json:"locale"`
	Email  string `json:"email"`
	Token  string `json:"token"`
}

// SendNewsletterConfirm asks a mailbox whether it wants the newsletter.
func (n Notifier) SendNewsletterConfirm(ctx context.Context, p *NewsletterConfirm) error {
	if !Valid(p.Email) {
		return errors.New("a newsletter confirmation has no usable email address")
	}

	ctx = n.locale(ctx, p.Locale)
	link := strings.TrimRight(n.baseURL, "/") + "/newsletter/confirm?token=" + url.QueryEscape(p.Token)
	return n.sender.Send(ctx, &Message{
		To:      p.Email,
		Subject: i18n.T(ctx, i18n.KeyMailNewsConfirmSubject),
		Body:    n.letter(ctx, "", fmt.Sprintf(i18n.T(ctx, i18n.KeyMailNewsConfirmBody), link)),
	})
}

// NewsletterWelcome is what a newsletter.welcome message carries. Its
// unsubscribe token never expires, because an email sent a year ago still has to
// take somebody off the list.
type NewsletterWelcome struct {
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
	link := strings.TrimRight(n.baseURL, "/") + "/newsletter/unsubscribe?token=" +
		url.QueryEscape(p.UnsubscribeToken)
	return n.sender.Send(ctx, &Message{
		To:      p.Email,
		Subject: i18n.T(ctx, i18n.KeyMailNewsWelcomeSubject),
		Body:    n.letter(ctx, "", fmt.Sprintf(i18n.T(ctx, i18n.KeyMailNewsWelcomeBody), link)),
	})
}

// NewsletterIssue is one copy of one newsletter, addressed to one person. The
// subject and body are the shop's own words and go out as written; only the
// unsubscribe footer is translated.
type NewsletterIssue struct {
	Locale           string `json:"locale"`
	Email            string `json:"email"`
	Subject          string `json:"subject"`
	Body             string `json:"body"`
	UnsubscribeToken string `json:"unsubscribe_token"`
}

// SendNewsletterIssue delivers one copy. The unsubscribe link is appended here
// rather than left to whoever wrote the issue.
func (n Notifier) SendNewsletterIssue(ctx context.Context, p *NewsletterIssue) error {
	if !Valid(p.Email) {
		return errors.New("a newsletter issue has no usable email address")
	}
	if p.UnsubscribeToken == "" {
		return errors.New("a newsletter issue has no unsubscribe token")
	}

	ctx = n.locale(ctx, p.Locale)
	link := strings.TrimRight(n.baseURL, "/") + "/newsletter/unsubscribe?token=" +
		url.QueryEscape(p.UnsubscribeToken)
	body := strings.Join([]string{
		p.Body, "",
		fmt.Sprintf(i18n.T(ctx, i18n.KeyMailIssueFooter), link),
		"", i18n.T(ctx, i18n.KeyMailNoReply), "— goen", "",
	}, "\n")

	return n.sender.Send(ctx, &Message{To: p.Email, Subject: p.Subject, Body: body})
}

// AddressVerify is what an account.email_verify message carries. The address is
// in the payload as well as being the recipient, because a CHANGE sends to the
// new address while the account still holds the old one.
type AddressVerify struct {
	Locale string `json:"locale"`
	Email  string `json:"email"`
	Token  string `json:"token"`
}

// SendAddressVerify asks somebody to prove an address is theirs.
func (n Notifier) SendAddressVerify(ctx context.Context, p *AddressVerify) error {
	if !Valid(p.Email) {
		return errors.New("an email verification has no usable address")
	}

	ctx = n.locale(ctx, p.Locale)
	link := strings.TrimRight(n.baseURL, "/") + "/verify?token=" + url.QueryEscape(p.Token)
	return n.sender.Send(ctx, &Message{
		To:      p.Email,
		Subject: i18n.T(ctx, i18n.KeyMailVerifySubject),
		Body:    n.letter(ctx, "", fmt.Sprintf(i18n.T(ctx, i18n.KeyMailVerifyBody), p.Email, link)),
	})
}
