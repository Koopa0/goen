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
	return n.sender.Send(ctx, &Message{To: address, Subject: i18n.T(ctx, i18n.KeyMailStaffInvitationSubject), Body: n.letter(ctx, name, fmt.Sprintf(i18n.T(ctx, i18n.KeyMailStaffInvitationBody), link))})
}
