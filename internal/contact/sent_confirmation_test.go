package contact_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/contact"
	"github.com/koopa0/goen/internal/i18n"
)

// TestPanelDoneOwnOrdersLink: the contact success panel offers the own-orders
// link only to a signed-in reader.
func TestPanelDoneOwnOrdersLink(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, signedIn := range []bool{false, true} {
			t.Run(locale.Tag()+"/"+map[bool]string{false: "guest", true: "member"}[signedIn], func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				body := renderComponent(t, ctx, contact.Panel(contact.Form{Done: true, SignedIn: signedIn}))
				if !strings.Contains(body, `href="/"`) {
					t.Error("contact.Panel() success has no shop next step")
				}
				if got := strings.Contains(body, `href="/account#orders-heading"`); got != signedIn {
					t.Errorf("contact.Panel() own-order link = %t, want %t", got, signedIn)
				}
				if strings.Contains(body, `method="post"`) {
					t.Error("contact.Panel() success still presents the submitted form")
				}
			})
		}
	}
}
