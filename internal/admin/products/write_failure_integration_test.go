//go:build integration

package products_test

import (
	"bytes"
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

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/user"
)

func TestProductWritesReportOnlyCommittedSuccess(t *testing.T) {
	staff, _ := admintest.StaffContext(t, pool)
	p := admintest.AdminRolePool(t, pool)
	s := products.NewStore(p)
	for _, locale := range i18n.Locales() {
		for _, scenario := range []string{
			"image-success", "image-missing", "image-rollback",
			"spec-success", "spec-missing", "spec-rollback",
			"option-success", "option-missing", "option-refused", "option-rollback",
			"value-success", "value-missing", "value-refused", "value-rollback",
		} {
			t.Run(locale.Tag()+"/"+scenario, func(t *testing.T) {
				ctx := i18n.WithLocale(staff, locale)
				slug := admintest.DraftProduct(t, ctx, pool, s)
				digest := storeMedia(t)
				if err := s.AttachImage(ctx, slug, digest, "Write result photograph", "", "", 800, 600); err != nil {
					t.Fatal(err)
				}
				if fields, err := s.AddSpec(ctx, slug, products.SpecDraft{Label: "Capacity", Value: "Original value"}); err != nil || len(fields) > 0 {
					t.Fatalf("spec fixture = %v/%v", fields, err)
				}
				if fields, err := s.AddOption(ctx, slug, products.OptionDraft{Name: "Colour"}); err != nil || len(fields) > 0 {
					t.Fatalf("option fixture = %v/%v", fields, err)
				}
				view, err := s.Product(ctx, slug)
				if err != nil {
					t.Fatal(err)
				}
				want := productWriteRows(t, ctx, pool, slug)
				path := "/images/remove"
				form := url.Values{"digest": {digest}}
				status, location := http.StatusSeeOther, "/admin/products/"+slug+"?ok=1"
				requestSlug := slug
				switch {
				case strings.HasPrefix(scenario, "spec-"):
					path, form = "/specs/remove", url.Values{"spec": {view.Specs[0].ID}}
				case strings.HasPrefix(scenario, "option-"):
					path, form = "/options", url.Values{"name": {"Size"}, "name_en": {"Typed option"}}
				case strings.HasPrefix(scenario, "value-"):
					path, form = "/options/values", url.Values{"option": {view.Options[0].ID}, "value": {"Blue"}, "value_en": {"Typed value"}, "swatch_hex": {"#123456"}}
				}
				switch scenario {
				case "image-success":
					want[0]--
				case "spec-success":
					want[1]--
				case "option-success":
					want[2]++
				case "value-success":
					want[3]++
				case "image-missing":
					form.Set("digest", "missing-image")
					status, location = http.StatusNotFound, ""
				case "spec-missing":
					form.Set("spec", uuid.NewString())
					status, location = http.StatusNotFound, ""
				case "option-missing":
					requestSlug = "missing-" + uuid.NewString()
					status, location = http.StatusNotFound, ""
				case "value-missing":
					form.Set("option", uuid.NewString())
					status, location = http.StatusUnprocessableEntity, ""
				case "option-refused":
					form.Set("name", "")
					status, location = http.StatusUnprocessableEntity, ""
				case "value-refused":
					form.Set("swatch_hex", "not a colour")
					status, location = http.StatusUnprocessableEntity, ""
				}
				if strings.HasSuffix(scenario, "-success") {
					want[4]++
				}
				if strings.HasSuffix(scenario, "-rollback") {
					// The write reaches audit insertion, whose actor foreign key rolls it back.
					ctx = user.NewContext(ctx, user.User{ID: uuid.NewString(), Role: user.RoleAdmin})
					status, location = http.StatusInternalServerError, ""
				}
				var diagnostics bytes.Buffer
				logger := slog.New(slog.NewJSONHandler(&diagnostics, nil))
				h := products.NewHandler(s, media.NewHandler(media.NewStore(p), logger), logger)
				mux := http.NewServeMux()
				h.Routes(mux, admintest.BackOffice)
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/products/"+requestSlug+path, strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				res := httptest.NewRecorder()
				mux.ServeHTTP(res, req)
				if res.Code != status || res.Header().Get("Location") != location {
					t.Errorf("product write = %d Location %q, want %d %q", res.Code, res.Header().Get("Location"), status, location)
				}
				if diff := cmp.Diff(want, productWriteRows(t, ctx, pool, slug)); diff != "" {
					t.Errorf("committed product and audit rows (-want +got):\n%s", diff)
				}
				if status == http.StatusInternalServerError {
					body := res.Body.String()
					if !strings.Contains(body, i18n.T(ctx, i18n.KeyAdminErrorBody)) || strings.Contains(body, "audit_events_actor_user_id_fkey") || strings.Contains(body, "SQLSTATE") {
						t.Error("rolled-back write must show the generic server error without its database cause")
					}
					if !strings.Contains(diagnostics.String(), "audit_events_actor_user_id_fkey") {
						t.Errorf("audit rollback diagnostics = %q, want the actual foreign-key failure", diagnostics.String())
					}
				}
				if status == http.StatusUnprocessableEntity {
					switch scenario {
					case "option-refused":
						admintest.AssertRefusedInput(t, res.Body.String(), "opt-name", "")
					case "value-refused":
						admintest.AssertRefusedInput(t, res.Body.String(), "optval-swatch", form.Get("swatch_hex"))
					case "value-missing":
						if !strings.Contains(res.Body.String(), i18n.T(ctx, i18n.KeyFormOptionMissing)) {
							t.Error("stale option must retain its existing refusal message")
						}
					}
				}
				if status == http.StatusSeeOther {
					page := httptest.NewRecorder()
					mux.ServeHTTP(page, httptest.NewRequestWithContext(ctx, http.MethodGet, location, nil))
					if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), i18n.T(ctx, i18n.KeyAdminNoticeOK)) {
						t.Error("committed write did not return the saved editor")
					}
				}
			})
		}
	}
}

func productWriteRows(t *testing.T, ctx context.Context, p *pgxpool.Pool, slug string) [5]int {
	t.Helper()
	var got [5]int
	err := p.QueryRow(ctx, `SELECT (SELECT count(*) FROM product_images i JOIN products p ON p.id=i.product_id WHERE p.slug=$1), (SELECT count(*) FROM product_specs s JOIN products p ON p.id=s.product_id WHERE p.slug=$1), (SELECT count(*) FROM product_options o JOIN products p ON p.id=o.product_id WHERE p.slug=$1), (SELECT count(*) FROM product_option_values v JOIN product_options o ON o.id=v.option_id JOIN products p ON p.id=o.product_id WHERE p.slug=$1), (SELECT count(*) FROM audit_events WHERE after->>'slug'=$1 OR after->>'product'=$1 OR before->>'slug'=$1 OR before->>'product'=$1)`, slug).Scan(&got[0], &got[1], &got[2], &got[3], &got[4])
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestProductWritesAnswerAnUnavailableDatabaseAsAServerError(t *testing.T) {
	staff, _ := admintest.StaffContext(t, pool)
	closed := admintest.NamedPool(t, pool, "product-closed-"+uuid.NewString())
	closed.Close()
	for _, locale := range i18n.Locales() {
		for _, tt := range []struct {
			name string
			path string
			form url.Values
		}{
			{name: "image", path: "/images/remove", form: url.Values{"digest": {"original-image"}}},
			{name: "spec", path: "/specs/remove", form: url.Values{"spec": {uuid.NewString()}}},
			{name: "option", path: "/options", form: url.Values{"name": {"Size"}}},
			{name: "value", path: "/options/values", form: url.Values{"option": {uuid.NewString()}, "value": {"Blue"}}},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				ctx := i18n.WithLocale(staff, locale)
				var diagnostics bytes.Buffer
				logger := slog.New(slog.NewJSONHandler(&diagnostics, nil))
				h := products.NewHandler(products.NewStore(closed), media.NewHandler(media.NewStore(closed), logger), logger)
				mux := http.NewServeMux()
				h.Routes(mux, admintest.BackOffice)
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/products/unchanged-product"+tt.path, strings.NewReader(tt.form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				res := httptest.NewRecorder()
				mux.ServeHTTP(res, req)
				if res.Code != http.StatusInternalServerError || res.Header().Get("Location") != "" {
					t.Errorf("product write on closed pool = %d Location %q, want 500 without redirect", res.Code, res.Header().Get("Location"))
				}
				if body := res.Body.String(); !strings.Contains(body, i18n.T(ctx, i18n.KeyAdminErrorBody)) || strings.Contains(body, "closed pool") {
					t.Error("unavailable database must show the generic server error without its cause")
				}
				if !strings.Contains(diagnostics.String(), "closed pool") {
					t.Errorf("product write diagnostics = %q, want the closed-pool cause", diagnostics.String())
				}
			})
		}
	}
}
