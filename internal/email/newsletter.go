package email

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

func (n Notifier) SendNewsletterConfirm(ctx context.Context, p *outbox.NewsletterConfirm) error {
	if !Valid(p.Email) {
		return errors.New("a newsletter confirmation has no usable email address")
	}

	ctx = n.locale(ctx, p.Locale)
	link := strings.TrimRight(n.baseURL, "/") + "/newsletter/confirm?token=" + url.QueryEscape(p.Token)
	return n.send(ctx, &Message{
		To:      p.Email,
		Subject: i18n.T(ctx, i18n.KeyMailNewsConfirmSubject),
		Body:    n.letter(ctx, "", fmt.Sprintf(i18n.T(ctx, i18n.KeyMailNewsConfirmBody), link)),
	})
}

func (n Notifier) SendNewsletterWelcome(ctx context.Context, p *outbox.NewsletterWelcome) error {
	if !Valid(p.Email) {
		return errors.New("a newsletter welcome has no usable email address")
	}

	ctx = n.locale(ctx, p.Locale)
	link := strings.TrimRight(n.baseURL, "/") + "/newsletter/unsubscribe?token=" +
		url.QueryEscape(p.UnsubscribeToken)
	return n.send(ctx, &Message{
		To:      p.Email,
		Subject: i18n.T(ctx, i18n.KeyMailNewsWelcomeSubject),
		Body:    n.letter(ctx, "", fmt.Sprintf(i18n.T(ctx, i18n.KeyMailNewsWelcomeBody), link)),
	})
}

// SendNewsletterIssue delivers one copy. The unsubscribe link is appended here
// rather than left to whoever wrote the issue.
func (n Notifier) SendNewsletterIssue(ctx context.Context, p *outbox.NewsletterIssue) error {
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

	return n.send(ctx, &Message{
		To: p.Email, Subject: p.Subject, Body: body,
		OneClickUnsubscribe: OneClickUnsubscribeURL(link),
	})
}

func (n Notifier) SendAddressVerify(ctx context.Context, p *outbox.AddressVerify) error {
	if !Valid(p.Email) {
		return errors.New("an email verification has no usable address")
	}

	ctx = n.locale(ctx, p.Locale)
	base := strings.TrimRight(n.baseURL, "/")
	if p.Registration {
		link := base + "/register/complete?token=" + url.QueryEscape(p.Token)
		if p.Next != "" {
			link += "&next=" + url.QueryEscape(p.Next)
		}
		return n.send(ctx, &Message{
			To:      p.Email,
			Subject: i18n.T(ctx, i18n.KeyMailRegisterSubject),
			Body:    n.letter(ctx, "", fmt.Sprintf(i18n.T(ctx, i18n.KeyMailRegisterBody), link)),
		})
	}
	link := base + "/verify?token=" + url.QueryEscape(p.Token)
	return n.send(ctx, &Message{
		To:      p.Email,
		Subject: i18n.T(ctx, i18n.KeyMailVerifySubject),
		Body:    n.letter(ctx, "", fmt.Sprintf(i18n.T(ctx, i18n.KeyMailVerifyBody), p.Email, link)),
	})
}
