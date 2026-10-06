package pages

import (
	"html"
	"strings"
	"testing"

	htmlparse "golang.org/x/net/html"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestRegistrationDeadOffersAnotherRegistrationLink(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		body   string
		link   string
		wrong  string
	}{
		{i18n.ZhHant, "這個註冊連結已失效（已經用過，或超過兩天）。請重新寄一封註冊信。", "重新寄註冊信", "請到會員中心重新寄一次。"},
		{i18n.En, "This sign-up link no longer works (used already, or more than two days old). Ask for a new one.", "Send a new link", "from your account page."},
	} {
		t.Run(string(tt.locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			page := html.UnescapeString(renderComponent(t, ctx, RegistrationDead(layouts.Page{Title: "Registration"})))
			if !strings.Contains(page, tt.body) || strings.Contains(page, tt.wrong) {
				t.Errorf("registration refusal lacks %q or still gives account-page directions", tt.body)
			}
			var recovery struct{ Href, Text, Class string }
			z := htmlparse.NewTokenizer(strings.NewReader(page))
			for z.Next() != htmlparse.ErrorToken {
				token := z.Token()
				if token.Data != "a" || token.Type != htmlparse.StartTagToken {
					continue
				}
				for _, attr := range token.Attr {
					if attr.Key == "href" {
						recovery.Href = attr.Val
					}
					if attr.Key == "class" {
						recovery.Class = attr.Val
					}
				}
				if !strings.Contains(recovery.Class, "--primary") {
					continue
				}
				if z.Next() == htmlparse.TextToken {
					recovery.Text = strings.TrimSpace(string(z.Text()))
				}
				break
			}
			if diff := cmp.Diff(struct{ Href, Text string }{"/register?resend=1", tt.link}, struct{ Href, Text string }{recovery.Href, recovery.Text}); diff != "" {
				t.Errorf("registration recovery link (-want +got):\n%s", diff)
			}
			if !strings.Contains(recovery.Class, "--primary") {
				t.Errorf("recovery link class = %q, want the primary button", recovery.Class)
			}
		})
	}
}
