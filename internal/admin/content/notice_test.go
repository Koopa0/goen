package content

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

func TestARedirectedNoticeIsShownAsItsOwnOutcome(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		query   string
		failure bool
	}{{"saved", false}, {"refused", true}} {
		ctx := i18n.WithLocale(t.Context(), i18n.En)
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/faq?"+tc.query+"=1", http.NoBody)
		var page strings.Builder
		if err := admin.FAQ(layouts.Page{Title: "faq"}, &admin.FAQView{Notice: web.Notice(req, notices)}).Render(ctx, &page); err != nil {
			t.Fatal(err)
		}
		got := page.String()
		m, ok := notices[tc.query]
		if !ok {
			t.Fatalf("notices has no entry %q", tc.query)
		}
		if sentence := i18n.T(ctx, m.Key); !strings.Contains(got, sentence) {
			t.Errorf("?%s=1: the page does not show %q", tc.query, sentence)
		}
		if danger := strings.Contains(got, "goen-notice--danger"); danger != tc.failure {
			t.Errorf("?%s=1: danger treatment = %t, want %t", tc.query, danger, tc.failure)
		}
	}
}

func TestARedirectedNoticeKeepsItsOutcome(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]components.Outcome{
		"already": components.OutcomeDone,
		"refused": components.OutcomeRefused,
	} {
		if got := notices[name].Outcome; got != want {
			t.Errorf("notices[%q].Outcome = %d, want %d", name, got, want)
		}
	}
}
