package email

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

// SendStaffInvitation uses an address resolved at delivery, never a queued copy.
func (n Notifier) SendStaffInvitation(ctx context.Context, p *outbox.StaffInvitation, address, name string) error {
	if !Valid(address) {
		return errors.New("staff invitation has no usable recipient")
	}
	ctx = n.locale(ctx, p.Locale)
	link := strings.TrimRight(n.baseURL, "/") + "/forgot"
	return n.send(ctx, &Message{To: address, Subject: i18n.T(ctx, i18n.KeyMailStaffInvitationSubject), Body: n.letter(ctx, name, fmt.Sprintf(i18n.T(ctx, i18n.KeyMailStaffInvitationBody), link))})
}

func (n Notifier) SendStaffEnrolment(ctx context.Context, p *outbox.StaffEnrolment) error {
	if !Valid(p.Email) {
		return errors.New("a staff enrolment code has no usable recipient")
	}
	ctx = n.locale(ctx, p.Locale)
	reset := strings.TrimRight(n.baseURL, "/") + "/forgot"
	return n.send(ctx, &Message{
		To:      p.Email,
		Subject: i18n.T(ctx, i18n.KeyMailStaffEnrolmentSubject),
		Body:    n.letter(ctx, "", fmt.Sprintf(i18n.T(ctx, i18n.KeyMailStaffEnrolmentBody), p.Code, reset)),
	})
}

func (n Notifier) SendStaffFactorFailure(ctx context.Context, p *outbox.StaffFactorFailure) error {
	if !Valid(p.Email) {
		return errors.New("a staff second-factor warning has no usable recipient")
	}
	ctx = n.locale(ctx, p.Locale)
	reset := strings.TrimRight(n.baseURL, "/") + "/forgot"
	subject, body := i18n.KeyMailStaffFactorFailureSubject, i18n.KeyMailStaffFactorFailureBody
	if p.Locked {
		subject, body = i18n.KeyMailStaffFactorLockedSubject, i18n.KeyMailStaffFactorLockedBody
	}
	return n.send(ctx, &Message{
		To:      p.Email,
		Subject: i18n.T(ctx, subject),
		Body:    n.letter(ctx, "", fmt.Sprintf(i18n.T(ctx, body), p.Limit, reset)),
	})
}
