package account

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

func TestForgotConfirmationNamesOnlyTheMaskedSubmittedAddress(t *testing.T) {
	t.Parallel()
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	for _, locale := range i18n.Locales() {
		for _, address := range []string{"a***@example.com", "ada@example.com", "", "<script>***@example.com"} {
			t.Run(locale.Tag()+"/"+address, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/forgot?"+url.Values{"sent": {"1"}, "address": {address}}.Encode(), http.NoBody)
				res := httptest.NewRecorder()
				h.ForgotPage(res, req)
				body := res.Body.String()
				if res.Code != http.StatusOK {
					t.Fatalf("ForgotPage() status = %d, want 200", res.Code)
				}
				for _, want := range []string{`id="forgot-sent"`, `href="/forgot"`, `id="forgot-resend"`, `action="/forgot"`} {
					if !strings.Contains(body, want) {
						t.Errorf("ForgotPage() omits %q", want)
					}
				}
				if address == "a***@example.com" {
					if !strings.Contains(body, address) {
						t.Error("ForgotPage() lost the submitted masked destination")
					}
				} else if address != "" && strings.Contains(body, html.EscapeString(address)) {
					t.Error("ForgotPage() reflected an unmasked or malformed destination")
				}
				if got := res.Header().Values("Set-Cookie"); len(got) != 0 {
					t.Errorf("ForgotPage() Set-Cookie = %q, want none", got)
				}
			})
		}
	}
}

func TestEmailConfirmationNoticeKeepsTheSubmittedDestinationAndConditionalPromise(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ address, want string }{
		{address: "n***@example.com", want: "If n***@example.com can be used for your account, check that inbox for the confirmation link. It takes effect after you follow the link."},
		{address: "old@example.com", want: "If that address can be used for your account, check its inbox for the confirmation link. It takes effect after you follow the link."},
		{want: "If that address can be used for your account, check its inbox for the confirmation link. It takes effect after you follow the link."},
	} {
		t.Run(tt.address, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), i18n.En), http.MethodGet, "/account?"+url.Values{"email": {"sent"}, "address": {tt.address}}.Encode(), http.NoBody)
			if got := accountNotice(req); got != tt.want {
				t.Errorf("accountNotice() = %q, want %q", got, tt.want)
			}
		})
	}
}
