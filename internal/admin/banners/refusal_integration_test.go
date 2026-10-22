//go:build integration

package banners_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/banners"
	"github.com/koopa0/goen/internal/i18n"
)

// TestHandler_CreateBannerRefused: a refused strip keeps the list.
func TestHandler_CreateBannerRefused(t *testing.T) {
	p := admintest.Pool(t)
	staffCtx, _ := admintest.StaffContext(t, p)
	bannerID := seedStripList(t, staffCtx, p)
	state := func() []byte {
		t.Helper()
		var rows []byte
		if err := p.QueryRow(staffCtx, `SELECT row_to_json(b) FROM promo_banners b WHERE b.id = $1`, bannerID).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	before := state()
	mux := http.NewServeMux()
	banners.NewHandler(banners.NewStore(admintest.AdminRolePool(t, p)), slog.New(slog.DiscardHandler)).Routes(mux, admintest.BackOffice)
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(staffCtx, locale)
		t.Run(locale.Tag(), func(t *testing.T) {
			get := httptest.NewRecorder()
			mux.ServeHTTP(get, httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/banners", nil))
			if get.Code != http.StatusOK {
				t.Fatalf("banners GET = %d, want 200", get.Code)
			}
			assertStripList(t, ctx, get.Body.String(), bannerID)
			res := httptest.NewRecorder()
			mux.ServeHTTP(res, refusedStripRequest(ctx))
			if res.Code != http.StatusUnprocessableEntity || res.Header().Get("Location") != "" {
				t.Fatalf("refused strip = %d to %q, want 422 without Location", res.Code, res.Header().Get("Location"))
			}
			assertStripList(t, ctx, res.Body.String(), bannerID)
			assertExampleRefusal(t, res.Body.String(), "b-message")
			draft := admintest.InputElementByID(t, res.Body.String(), "b-short")
			if got := admintest.InputAttribute(t, draft, "value"); got != "Draft strip" {
				t.Errorf("refused strip draft = %q, want %q", got, "Draft strip")
			}
			if diff := cmp.Diff(before, state()); diff != "" {
				t.Errorf("refusal changed the existing strip (-want +got):\n%s", diff)
			}
		})
	}
}

func assertExampleRefusal(t *testing.T, body, id string) {
	t.Helper()
	input := admintest.InputElementByID(t, body, id)
	type inputFacts struct{ Value, Invalid, Descriptions string }
	got := inputFacts{
		Value:        admintest.InputAttribute(t, input, "value"),
		Invalid:      admintest.InputAttribute(t, input, "aria-invalid"),
		Descriptions: admintest.InputAttribute(t, input, "aria-describedby"),
	}
	want := inputFacts{Invalid: "true", Descriptions: id + "-example " + id + "-error"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("refused field (-want +got):\n%s", diff)
	}
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for _, description := range strings.Fields(want.Descriptions) {
		var visible int
		for n := range doc.Descendants() {
			var nodeID, class string
			var hidden bool
			for _, a := range n.Attr {
				switch a.Key {
				case "id":
					nodeID = a.Val
				case "class":
					class = a.Val
				case "hidden":
					hidden = true
				}
			}
			if nodeID != description {
				continue
			}
			var text strings.Builder
			for child := range n.Descendants() {
				if child.Type == html.TextNode {
					text.WriteString(child.Data)
				}
			}
			if n.Data == "p" && !hidden && !strings.Contains(class, "sr-only") && strings.TrimSpace(text.String()) != "" {
				visible++
			}
		}
		if visible != 1 {
			t.Errorf("field %q description %q has %d visible text elements, want one", id, description, visible)
		}
	}
}

// seedStripList leaves exactly one strip on the list.
func seedStripList(t *testing.T, ctx context.Context, p *pgxpool.Pool) (bannerID string) {
	t.Helper()
	bannerID = uuid.NewString()
	if _, err := p.Exec(ctx, `DELETE FROM promo_banners`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `
		INSERT INTO promo_banners (id, message, message_en, is_active)
		VALUES ($1, 'Existing strip', 'Existing strip', true)`, bannerID); err != nil {
		t.Fatal(err)
	}
	return bannerID
}

// assertStripList holds that the page still lists the seeded strip with its
// toggle, and does not call the list empty.
func assertStripList(t *testing.T, ctx context.Context, body, bannerID string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	want := "/admin/banners/" + bannerID + "/active"
	found := false
	for node := range doc.Descendants() {
		if node.Type != html.ElementNode || node.Data != "form" {
			continue
		}
		var method, action string
		for _, attr := range node.Attr {
			switch attr.Key {
			case "method":
				method = attr.Val
			case "action":
				action = attr.Val
			}
		}
		if method == "post" && action == want {
			found = true
		}
	}
	if !found {
		t.Errorf("the existing strip has no toggle form %s", want)
	}
	if strings.Contains(body, i18n.T(ctx, i18n.KeyAdminHomeBannerEmpty)) {
		t.Errorf("nonempty list shows %q", i18n.T(ctx, i18n.KeyAdminHomeBannerEmpty))
	}
}

// refusedStripRequest is a strip form with no message.
func refusedStripRequest(ctx context.Context) *http.Request {
	fields := url.Values{"message": {""}, "short": {"Draft strip"}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/banners", strings.NewReader(fields.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}
