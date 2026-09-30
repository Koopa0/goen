package email

import (
	"strings"
	"testing"
)

// TestTheAccountExistsLetterCarriesNoCapability: a second registration of an
// address is answered in this letter, to its owner, and whoever registered can
// make it arrive at will. So it points at the two doors the owner already has
// and carries nothing that opens one.
func TestTheAccountExistsLetterCarriesNoCapability(t *testing.T) {
	t.Parallel()
	for _, locale := range []string{"en", "zh-TW"} {
		n, sink := notifier(t)
		if err := n.SendAccountExists(t.Context(), locale, "owner@example.com", "Owner"); err != nil {
			t.Fatal(err)
		}
		if sink.msg == nil || sink.msg.To != "owner@example.com" {
			t.Fatalf("the letter went to %+v, want the account's own address", sink.msg)
		}
		for _, link := range []string{"https://goen.test/signin", "https://goen.test/forgot"} {
			if !strings.Contains(sink.msg.Body, link) {
				t.Errorf("the %s letter does not point at %s:\n%s", locale, link, sink.msg.Body)
			}
		}
		if strings.Contains(sink.msg.Body, "token=") {
			t.Errorf("the %s letter carries a token:\n%s", locale, sink.msg.Body)
		}
		if hasHan(sink.msg.Body) != (locale == "zh-TW") {
			t.Errorf("the letter ignored locale %q", locale)
		}
	}
	n, _ := notifier(t)
	if err := n.SendAccountExists(t.Context(), "en", "", "Owner"); err == nil {
		t.Error("a letter with no recipient was sent")
	}
}

// TestARegistrationLetterLeadsToThePasswordNotStraightIn: the mailbox alone
// does not say who registered, so a registration's link goes to the page that
// asks for the password chosen at registration, never to the page that proves
// an address on the link alone.
func TestARegistrationLetterLeadsToThePasswordNotStraightIn(t *testing.T) {
	t.Parallel()
	for _, locale := range []string{"en", "zh-TW"} {
		n, sink := notifier(t)
		if err := n.SendAddressVerify(t.Context(), &AddressVerify{
			Locale: locale, Email: "new@example.com", Token: "tok", Registration: true, Next: "/cart",
		}); err != nil {
			t.Fatal(err)
		}
		if sink.msg == nil || sink.msg.To != "new@example.com" {
			t.Fatalf("the letter went to %+v, want the registered address", sink.msg)
		}
		if !strings.Contains(sink.msg.Body, "https://goen.test/register/complete?token=tok&next=%2Fcart") {
			t.Errorf("the %s letter does not link to the password page:\n%s", locale, sink.msg.Body)
		}
		if strings.Contains(sink.msg.Body, "/verify?") {
			t.Errorf("the %s letter links to the page that proves an address on the link alone:\n%s",
				locale, sink.msg.Body)
		}
		if hasHan(sink.msg.Body) != (locale == "zh-TW") {
			t.Errorf("the letter ignored locale %q", locale)
		}
	}
}

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
		for _, line := range strings.Split(sink.msg.Body, "\n") {
			if strings.Contains(line, "/forgot") && (strings.Contains(line, "two-factor") || strings.Contains(line, "兩階段")) {
				t.Fatalf("the reset link is labelled as two-factor setup: %q", line)
			}
		}
		for _, banned := range []string{"already have a password", "已有密碼", "walk you through", "引導你完成兩階段"} {
			if strings.Contains(sink.msg.Body, banned) {
				t.Fatalf("invitation makes a claim that is false for some invitees: %q", banned)
			}
		}
		if !strings.Contains(sink.msg.Body, "Forgot password") && !strings.Contains(sink.msg.Body, "忘記密碼") {
			t.Fatal("invitation does not point at the forgot-password flow")
		}
		if hasHan(sink.msg.Body) != (locale == "zh-TW") {
			t.Fatalf("invitation ignored locale %q", locale)
		}
	}
}
