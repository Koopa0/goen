package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/contact"
	"github.com/koopa0/goen/internal/i18n"
)

func TestContactSuccessOffersOnlyTheSignedInReadersOwnOrders(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, signedIn := range []bool{false, true} {
			t.Run(locale.Tag()+"/"+map[bool]string{false: "guest", true: "member"}[signedIn], func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				body := renderComponent(t, ctx, contact.ContactPanel(contact.ContactForm{Done: true, SignedIn: signedIn}))
				if !strings.Contains(body, `href="/"`) {
					t.Error("ContactPanel() success has no shop next step")
				}
				if got := strings.Contains(body, `href="/account#orders-heading"`); got != signedIn {
					t.Errorf("ContactPanel() own-order link = %t, want %t", got, signedIn)
				}
				if strings.Contains(body, `method="post"`) {
					t.Error("ContactPanel() success still presents the submitted form")
				}
			})
		}
	}
}
