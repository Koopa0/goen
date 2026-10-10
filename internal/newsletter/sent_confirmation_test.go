package newsletter

import (
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestNewsletterThanksReflectsOnlyAMaskedDestinationAndOffersTheLiveForm(t *testing.T) {
	t.Parallel()
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	for _, locale := range i18n.Locales() {
		for _, address := range []string{"n***@example.com", "news-private@example.com", "<script>***@example.com", ""} {
			t.Run(locale.Tag()+"/"+address, func(t *testing.T) {
				t.Parallel()
				req := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), locale), http.MethodGet, "/newsletter/thanks?"+url.Values{"address": {address}}.Encode(), http.NoBody)
				res := httptest.NewRecorder()
				h.Thanks(res, req)
				body := res.Body.String()
				if res.Code != http.StatusOK {
					t.Fatalf("Thanks() status = %d, want 200", res.Code)
				}
				for _, want := range []string{`href="/#newsletter-form"`, `id="newsletter-form"`, `action="/newsletter"`} {
					if !strings.Contains(body, want) {
						t.Errorf("Thanks() omits %q", want)
					}
				}
				if address == "n***@example.com" {
					if !strings.Contains(body, address) {
						t.Error("Thanks() lost the submitted masked address")
					}
					if locale == i18n.En && !strings.Contains(body, "If n***@example.com is not already subscribed") {
						t.Error("Thanks() unconditionally promises a letter to an existing subscriber")
					}
				} else if address != "" && strings.Contains(body, html.EscapeString(address)) {
					t.Error("Thanks() reflects an unmasked or malformed address")
				}
				if got := res.Header().Values("Set-Cookie"); len(got) != 0 {
					t.Errorf("Thanks() Set-Cookie = %q, want none", got)
				}
			})
		}
	}
}
