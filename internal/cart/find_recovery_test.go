package cart

import (
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/ratelimit"
)

func TestRefusedOrderLookupOffersContactAndKeepsInput(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		h := NewHandler(&Store{}, &orderaccess.Store{}, slog.New(slog.DiscardHandler), false,
			ratelimit.New(ratelimit.Config{Every: time.Hour, Burst: 10, TTL: time.Hour, MaxKeys: 10}), nil, nil)
		r := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), locale), http.MethodPost, "/orders/find", strings.NewReader("number=GO-260101-000001&email="))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.FindOrder(w, r)
		body := w.Body.String()
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(body, `value="GO-260101-000001"`) {
			t.Error("lookup refusal lost the number or the 422 response")
		}
		want := "Can't find your confirmation email? Contact us and include the email address you used at checkout."
		if locale == i18n.ZhHant {
			want = "找不到確認信？請聯絡我們，並附上下單時用的電子郵件。"
		}
		if !strings.Contains(body, `href="/contact">`+html.EscapeString(want)+`</a>`) {
			t.Error("lookup refusal has no confirmation-email recovery guidance")
		}
	}
}
