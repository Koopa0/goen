package loyalty

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	nethtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestPointsNoticeLooksLikeItsOutcome: a redemption that went through is
// announced in the accent style and a refused one in the danger style.
func TestPointsNoticeLooksLikeItsOutcome(t *testing.T) {
	t.Parallel()
	const owner = "a7e579d0-16ed-4994-bc10-b6c8324ecdf7"
	h := &Handler{confirmationKey: "notice-outcome-key"}
	token := encodeRedemptionConfirmation(h.confirmationKey, owner, redemptionConfirmation{
		OperationID: uuid.MustParse("240cc90f-2f42-485c-9cf5-52d21ba4bf75"), Points: 100, CreditCents: 1000, IssuedAt: time.Now().Unix(),
	})
	for _, tt := range []struct {
		name  string
		query string
		class string
		role  string
	}{
		{"redeemed", "redeemed=" + token, "goen-notice--accent", "status"},
		{"short", "short=1", "goen-notice--danger", "alert"},
	} {
		for _, locale := range i18n.Locales() {
			t.Run(tt.name+"/"+locale.Tag(), func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/account/points?"+tt.query, http.NoBody)
				notice := h.noticeFor(req, owner)
				if notice.Text == "" {
					t.Fatalf("?%s says nothing", tt.query)
				}
				assertPointsNotice(t, renderComponent(t, ctx, Points(frame.Page{}, PointsView{Notice: notice})), tt.class, tt.role)
			})
		}
	}
}

func assertPointsNotice(t *testing.T, body, class, role string) {
	t.Helper()
	doc, err := nethtml.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	n := findDescendant(doc, func(n *nethtml.Node) bool {
		return n.Type == nethtml.ElementNode && attrValue(n, "id") == "points-notice"
	})
	if n == nil {
		t.Fatal("the page renders no points-notice")
	}
	if !strings.Contains(" "+className(n)+" ", " "+class+" ") || attrValue(n, "role") != role {
		t.Errorf("points-notice has class %q and role %q, want %s and %s", className(n), attrValue(n, "role"), class, role)
	}
}
