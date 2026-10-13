package stock

import (
	"bytes"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

func TestReceiptRefusalKeepsTheCorrectableForm(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, tt := range []struct {
			name string
			raw  string
			key  i18n.Key
		}{
			{name: "above the bound", raw: "10001", key: i18n.KeyAdminNoticeBadQty},
			{name: "malformed", raw: ` 12<x> `, key: i18n.KeyAdminNoticeBadQty},
			{name: "empty", key: i18n.KeyAdminNoticeBadQty},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				form := url.Values{"quantity": {tt.raw}, "idempotency": {`rcv:SKU:original<&>`}}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/stock/receive", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				if err := req.ParseForm(); err != nil {
					t.Fatal(err)
				}
				view := admin.MovementsView{SKU: "SKU", Slug: "product", FormID: "new-page"}
				rec := httptest.NewRecorder()
				h := Handler{log: slog.New(slog.DiscardHandler)}
				h.renderReceiptRefusal(rec, req, &view, tt.key)
				if rec.Code != http.StatusUnprocessableEntity || rec.Header().Get("Location") != "" {
					t.Errorf("receipt refusal = %d %q, want 422 without redirect", rec.Code, rec.Header().Get("Location"))
				}
				body := rec.Body.String()
				input := regexp.MustCompile(`<input[^>]*id="receive-qty"[^>]*>`).FindString(body)
				for _, want := range []string{
					`type="text"`, `inputmode="numeric"`, `value="` + html.EscapeString(tt.raw) + `"`,
					`aria-invalid="true"`, `aria-describedby="receive-qty-error"`,
				} {
					if !strings.Contains(input, want) {
						t.Errorf("receipt quantity control is missing %q", want)
					}
				}
				for _, want := range []string{
					`name="idempotency" value="rcv:SKU:original&lt;&amp;&gt;"`,
					`id="receive-qty-error" role="alert">` + html.EscapeString(i18n.T(ctx, tt.key)) + `</p>`,
				} {
					if !strings.Contains(body, want) {
						t.Errorf("receipt refusal page is missing %q", want)
					}
				}
			})
		}
	}
}

func TestFreshReceiptFormHasItsOwnKeyAndNoRefusal(t *testing.T) {
	t.Parallel()
	view := admin.MovementsView{SKU: "SKU", Slug: "product", FormID: "first-page"}
	var body bytes.Buffer
	if err := admin.Movements(layouts.Page{}, &view).Render(t.Context(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.String(), `name="idempotency" value="rcv:SKU:first-page"`) {
		t.Error("fresh receipt form lost its own operation identity")
	}
	if strings.Contains(body.String(), `aria-invalid="true"`) || strings.Contains(body.String(), `id="receive-qty-error"`) {
		t.Error("fresh receipt form reports a refusal before submission")
	}
}
