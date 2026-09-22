//go:build integration

package payment_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/payment"
)

func TestPayPageShowsTestCardGuidanceOnlyForSandboxKeys(t *testing.T) {
	for _, key := range []string{"sk_test_example", "rk_test_example", "rkcs_test_example", "sk_live_example", "rk_live_example", "unrecognized", ""} {
		for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			t.Run(key+"/"+string(locale), func(t *testing.T) {
				number, _ := order(t, 50000)
				gateway, err := payment.NewGateway(key, "whsec_example", "https://goen.example")
				if err != nil {
					t.Fatal(err)
				}
				h := payment.NewHandler(payment.NewStore(pool), gateway, alwaysPlacedHere{}, slog.New(slog.DiscardHandler), false)
				req := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), locale), http.MethodGet, "/orders/"+number+"/pay", http.NoBody)
				req.SetPathValue("number", number)
				res := httptest.NewRecorder()
				h.Page(res, req)
				if res.Code != http.StatusOK {
					t.Fatalf("payment page = %d: %s", res.Code, res.Body.String())
				}
				body := res.Body.String()
				want := strings.Contains(key, "_test_")
				if got := strings.Contains(body, `id="pay-sandbox"`); got != want {
					t.Errorf("sandbox notice = %t, want %t", got, want)
				}
				if got := strings.Contains(body, "4242 4242 4242 4242"); got != want {
					t.Errorf("test card instructions = %t, want %t", got, want)
				}
				if want {
					words := []string{"不會收取真實款項", "請勿輸入真實卡號", "未來到期日", "3 位數安全碼"}
					if locale == i18n.En {
						words = []string{"no real money", "Do not enter a real card", "future expiry", "three-digit CVC"}
					}
					for _, word := range words {
						if !strings.Contains(body, word) {
							t.Errorf("sandbox guidance omits %q", word)
						}
					}
				}
			})
		}
	}
}
