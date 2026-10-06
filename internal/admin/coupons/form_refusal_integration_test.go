//go:build integration

package coupons_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/coupons"
	"github.com/koopa0/goen/internal/coupon"
	"github.com/koopa0/goen/internal/i18n"
)

func TestCouponLimitRefusalRetainsDraftAndAllowsCorrection(t *testing.T) {
	owner := admintest.Pool(t)
	staff, _ := admintest.StaffContext(t, owner)
	desk := coupons.NewStore(admintest.AdminRolePool(t, owner))
	mux := http.NewServeMux()
	coupons.NewHandler(desk, slog.New(slog.DiscardHandler)).Routes(mux, admintest.BackOffice)
	for _, locale := range i18n.Locales() {
		for _, tt := range []struct {
			field     string
			refused   string
			corrected string
			message   i18n.Key
			label     i18n.Key
		}{
			{field: "min", refused: "100000001", corrected: "00012", message: i18n.KeyFormCouponMinSpend, label: i18n.KeyAdminCoupMinSpend},
			{field: "days", refused: "1000001", corrected: "0007", message: i18n.KeyFormCouponDays, label: i18n.KeyAdminCoupDays},
			{field: "max", refused: "1000001", corrected: "0002", message: i18n.KeyFormCouponMaxUses, label: i18n.KeyAdminCoupMaxRedeem},
		} {
			t.Run(locale.Tag()+"/"+tt.field, func(t *testing.T) {
				ctx := i18n.WithLocale(staff, locale)
				code := "REFUSAL-" + strings.ToUpper(uuid.NewString()[:8])
				form := url.Values{
					"code":        {code},
					"description": {"Coupon limit refusal"},
					"kind":        {string(coupon.Amount)},
					"value":       {"00100"},
					"cap":         {""},
					"min":         {"00012"},
					"days":        {"0007"},
					"max":         {"0002"},
					"percustomer": {"01"},
				}
				form.Set(tt.field, tt.refused)
				post := func() *httptest.ResponseRecorder {
					req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/coupons", strings.NewReader(form.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					response := httptest.NewRecorder()
					mux.ServeHTTP(response, req)
					return response
				}
				response := post()
				if response.Code != http.StatusUnprocessableEntity || response.Header().Get("Location") != "" {
					t.Fatalf("refused POST = %d, location %q, want 422 without redirect; body: %s", response.Code, response.Header().Get("Location"), response.Body.String())
				}
				doc, err := html.Parse(strings.NewReader(response.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				want := make(map[string]couponRefusalControl)
				got := make(map[string]couponRefusalControl)
				for _, field := range []string{"code", "description", "value", "cap", "min", "days", "max", "percustomer"} {
					want[field] = couponRefusalControl{Controls: 1, Tag: "input", Value: form.Get(field)}
					got[field] = couponRefusalFacts(doc, field)
				}
				facts := want[tt.field]
				facts.Invalid, facts.DescribedBy = "true", "c-"+tt.field+"-error"
				facts.Errors, facts.ErrorTag, facts.ErrorClass = 1, "p", "ui-error-text"
				facts.ErrorText = i18n.T(ctx, tt.message)
				want[tt.field] = facts
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("refused POST draft and error relation (-want +got):\n%s", diff)
				}
				var labels []string
				for node := range doc.Descendants() {
					if node.Type == html.ElementNode && node.Data == "label" && couponRefusalAttribute(node, "for") == "c-"+tt.field {
						labels = append(labels, couponRefusalText(node))
					}
				}
				if diff := cmp.Diff([]string{i18n.T(ctx, tt.label)}, labels); diff != "" {
					t.Errorf("refused field label (-want +got):\n%s", diff)
				}
				var count int
				if err := owner.QueryRow(ctx, `SELECT count(*) FROM coupons WHERE code=$1`, code).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("refused POST inserted %d coupons, want zero", count)
				}
				form.Set(tt.field, tt.corrected)
				response = post()
				if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/coupons?ok=1" {
					t.Fatalf("corrected POST = %d, location %q, want 303 to success; body: %s", response.Code, response.Header().Get("Location"), response.Body.String())
				}
				if err := owner.QueryRow(ctx, `SELECT count(*) FROM coupons WHERE code=$1`, code).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 1 {
					t.Errorf("corrected POST inserted %d coupons, want one", count)
				}
			})
		}
	}
}

type couponRefusalControl struct {
	Controls    int
	Tag         string
	Value       string
	Invalid     string
	DescribedBy string
	Errors      int
	ErrorTag    string
	ErrorClass  string
	ErrorText   string
}

func couponRefusalFacts(doc *html.Node, field string) couponRefusalControl {
	var facts couponRefusalControl
	id := "c-" + field
	for node := range doc.Descendants() {
		if node.Type != html.ElementNode {
			continue
		}
		switch couponRefusalAttribute(node, "id") {
		case id:
			facts.Controls++
			facts.Tag, facts.Value = node.Data, couponRefusalAttribute(node, "value")
			facts.Invalid, facts.DescribedBy = couponRefusalAttribute(node, "aria-invalid"), couponRefusalAttribute(node, "aria-describedby")
		case id + "-error":
			facts.Errors++
			facts.ErrorTag, facts.ErrorClass, facts.ErrorText = node.Data, couponRefusalAttribute(node, "class"), couponRefusalText(node)
		}
	}
	return facts
}

func couponRefusalAttribute(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func couponRefusalText(node *html.Node) string {
	var parts []string
	for child := range node.Descendants() {
		if child.Type == html.TextNode {
			parts = append(parts, child.Data)
		}
	}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}
