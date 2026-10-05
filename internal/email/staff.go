package email

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

// StaffInvitation identifies a newly invited account without retaining its PII.
type StaffInvitation struct {
	UserID string `json:"user_id"`
	Locale string `json:"locale"`
}

// SendStaffInvitation uses an address resolved at delivery, never a queued copy.
func (n Notifier) SendStaffInvitation(ctx context.Context, p *StaffInvitation, address, name string) error {
	if !Valid(address) {
		return errors.New("staff invitation has no usable recipient")
	}
	ctx = n.locale(ctx, p.Locale)
	link := strings.TrimRight(n.baseURL, "/") + "/forgot"
	return n.send(ctx, &Message{To: address, Subject: i18n.T(ctx, i18n.KeyMailStaffInvitationSubject), Body: n.letter(ctx, name, fmt.Sprintf(i18n.T(ctx, i18n.KeyMailStaffInvitationBody), link))})
}

// StaffEnrolment carries the code that finishes a staff member's second-factor
// enrolment to the account's address.
type StaffEnrolment struct {
	Locale string `json:"locale"`
	// Tagged "email" so erase_user's sweep of an erased address finds it.
	Email string `json:"email"`
	Code  string `json:"code"`
}

func (n Notifier) SendStaffEnrolment(ctx context.Context, p *StaffEnrolment) error {
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
