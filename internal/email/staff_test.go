package email

import (
	"strings"
	"testing"
)

func TestStaffInvitationPointsToMailboxProofWithoutACapability(t *testing.T) {
	t.Parallel()
	for _, locale := range []string{"en", "zh-TW"} {
		n, sink := notifier(t)
		if err := n.SendStaffInvitation(t.Context(), &StaffInvitation{UserID: "account-id", Locale: locale}, "colleague@example.com", "Colleague"); err != nil {
			t.Fatal(err)
		}
		if sink.msg == nil || sink.msg.To != "colleague@example.com" || !strings.Contains(sink.msg.Body, "https://goen.test/forgot") || strings.Contains(sink.msg.Body, "token=") {
			t.Fatalf("invalid onboarding notice: %+v", sink.msg)
		}
		if hasHan(sink.msg.Body) != (locale == "zh-TW") {
			t.Fatalf("invitation ignored locale %q", locale)
		}
	}
}
