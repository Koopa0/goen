//go:build integration

package products_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/pgtx"
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

func TestProductWritesKeepDatabaseRuleRefusalsDistinct(t *testing.T) {
	owner := admintest.Pool(t)
	staff, _ := admintest.StaffContext(t, owner)
	app := admintest.AdminRolePool(t, owner)
	s := products.NewStore(app)
	slug := admintest.DraftProduct(t, staff, owner, s)
	const digest = "rule-refusal-image"
	if err := s.AttachImage(staff, slug, digest, "Rule refusal photograph", "", "", 800, 600); err != nil {
		t.Fatal(err)
	}
	if fields, err := s.AddSpec(staff, slug, products.SpecDraft{Label: "Capacity", Value: "Original value"}); err != nil || len(fields) > 0 {
		t.Fatalf("spec fixture = %v/%v", fields, err)
	}
	if fields, err := s.AddOption(staff, slug, products.OptionDraft{Name: "Colour"}); err != nil || len(fields) > 0 {
		t.Fatalf("option fixture = %v/%v", fields, err)
	}
	view, err := s.Product(staff, slug)
	if err != nil {
		t.Fatal(err)
	}
	// Real constraints exercise rules with no matching field control in this isolated database.
	if _, err := owner.Exec(t.Context(), `
		CREATE TABLE product_write_references (
			image_id uuid CONSTRAINT test_product_image_referenced REFERENCES product_images(id),
			spec_id uuid CONSTRAINT test_product_spec_referenced REFERENCES product_specs(id)
		);
		ALTER TABLE product_options ADD CONSTRAINT test_product_option_refused CHECK (name <> 'Blocked option');
		ALTER TABLE product_option_values ADD CONSTRAINT test_product_value_refused CHECK (value <> 'Blocked value');
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(t.Context(), `INSERT INTO product_write_references (image_id, spec_id) SELECT i.id, $2 FROM product_images i JOIN products p ON p.id=i.product_id WHERE p.slug=$1 AND i.storage_key=$3`, slug, view.Specs[0].ID, digest); err != nil {
		t.Fatal(err)
	}
	want := productWriteRows(t, staff, owner, slug)
	for _, locale := range i18n.Locales() {
		for _, tt := range []struct {
			name       string
			path       string
			form       url.Values
			constraint string
		}{
			{name: "image", path: "/images/remove", form: url.Values{"digest": {digest}}, constraint: "test_product_image_referenced"},
			{name: "spec", path: "/specs/remove", form: url.Values{"spec": {view.Specs[0].ID}}, constraint: "test_product_spec_referenced"},
			{name: "option", path: "/options", form: url.Values{"name": {"Blocked option"}}, constraint: "test_product_option_refused"},
			{name: "value", path: "/options/values", form: url.Values{"option": {view.Options[0].ID}, "value": {"Blocked value"}}, constraint: "test_product_value_refused"},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				ctx := i18n.WithLocale(staff, locale)
				var diagnostics bytes.Buffer
				logger := slog.New(slog.NewJSONHandler(&diagnostics, nil))
				h := products.NewHandler(s, media.NewHandler(media.NewStore(app), logger), logger)
				mux := http.NewServeMux()
				h.Routes(mux, admintest.BackOffice)
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/products/"+slug+tt.path, strings.NewReader(tt.form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				res := httptest.NewRecorder()
				mux.ServeHTTP(res, req)
				location := "/admin/products/" + slug + "?refused=1"
				if res.Code != http.StatusSeeOther || res.Header().Get("Location") != location {
					t.Errorf("rule-refused product write = %d Location %q, want 303 %q", res.Code, res.Header().Get("Location"), location)
				}
				if diff := cmp.Diff(want, productWriteRows(t, ctx, owner, slug)); diff != "" {
					t.Errorf("refused product and audit rows (-want +got):\n%s", diff)
				}
				if !strings.Contains(diagnostics.String(), tt.constraint) {
					t.Errorf("rule-refusal diagnostics = %q, want %s", diagnostics.String(), tt.constraint)
				}
				page := httptest.NewRecorder()
				mux.ServeHTTP(page, httptest.NewRequestWithContext(ctx, http.MethodGet, location, nil))
				if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), i18n.T(ctx, i18n.KeyAdminNoticeRefused)) || strings.Contains(page.Body.String(), tt.constraint) {
					t.Error("rule refusal must show the localized notice without its database cause")
				}
			})
		}
	}
}

func TestProductEditorWriteFaultsKeepSavedStateAndRecover(t *testing.T) {
	owner := admintest.Pool(t)
	staff, _ := admintest.StaffContext(t, owner)
	adminPool := admintest.AdminRolePool(t, owner)
	for _, locale := range i18n.Locales() {
		for _, endpoint := range []struct {
			name, path, query, lock, log string
			action                      audit.Action
		}{
			{name: "image option", path: "/images/option", query: "SetProductImageOptionValue", lock: `SELECT 1 FROM product_images i JOIN products p ON p.id=i.product_id WHERE p.slug=$1 FOR UPDATE OF i`, log: "set image option", action: audit.ActionSetImageOption},
			{name: "add spec", path: "/specs", query: "AddProductSpec", lock: `SELECT 1 FROM products WHERE slug=$1 FOR UPDATE`, log: "add spec", action: audit.ActionAddSpec},
		} {
			for _, fault := range []string{"closed pool", "row lock"} {
				t.Run(locale.Tag()+"/"+endpoint.name+"/"+fault, func(t *testing.T) {
					ctx := i18n.WithLocale(staff, locale)
					slug, digest, valueID := seedProductWriteOutcome(t, ctx, owner, adminPool)
					form := productOutcomeForm(endpoint.path, digest, valueID)
					before := productOutcomeState(t, ctx, owner, slug)
					beforeAudit := admintest.AuditRows(t, owner, endpoint.action)
					trace := &productWriteTrace{query: endpoint.query}
					cfg := adminPool.Config().Copy()
					cfg.MaxConns = 1
					cfg.ConnConfig.RuntimeParams["lock_timeout"] = "200"
					cfg.ConnConfig.Tracer = trace
					writing, err := pgxpool.NewWithConfig(t.Context(), cfg)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(writing.Close)
					var role string
					if roleErr := writing.QueryRow(ctx, `SELECT current_user`).Scan(&role); roleErr != nil || role != "admin" {
						t.Fatalf("writer role = %q, want admin: %v", role, roleErr)
					}
					var holder pgx.Tx
					if fault == "closed pool" {
						writing.Close()
						if pingErr := writing.Ping(ctx); pingErr == nil || ctx.Err() != nil {
							t.Fatalf("closed writer = %v, request = %v, want an unavailable pool with a live request", pingErr, ctx.Err())
						}
					} else {
						holder, err = owner.Begin(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer pgtx.Rollback(ctx, holder)
						if _, lockErr := holder.Exec(ctx, endpoint.lock, slug); lockErr != nil {
							t.Fatal(lockErr)
						}
					}
					var diagnostics bytes.Buffer
					logger := slog.New(slog.NewTextHandler(&diagnostics, nil))
					mux := productOutcomeRoutes(writing, logger)
					response := postProductOutcome(t, ctx, mux, "/admin/products/"+slug+endpoint.path, form)
					if fault == "row lock" {
						attempts := trace.snapshot()
						if len(attempts) != 1 {
							t.Fatalf("%s lock attempts = %d, want one", endpoint.query, len(attempts))
						}
						pgErr, ok := errors.AsType[*pgconn.PgError](attempts[0])
						if !ok || pgErr.Code != "55P03" || ctx.Err() != nil {
							t.Fatalf("%s error = %v, request = %v, want SQLSTATE55P03 with a live request", endpoint.query, attempts[0], ctx.Err())
						}
					}
					if response.Code != http.StatusInternalServerError || response.Header().Get("Location") != "" {
						t.Errorf("%s on %s = %d to %q, want 500 without Location", endpoint.name, fault, response.Code, response.Header().Get("Location"))
					}
					if body := response.Body.String(); !strings.Contains(body, i18n.T(ctx, i18n.KeyAdminErrorBody)) || strings.Contains(body, "SQLSTATE") || strings.Contains(body, "closed pool") {
						t.Error("product fault must show the localized server error without the database cause")
					}
					if !strings.Contains(diagnostics.String(), `level=ERROR msg="`+endpoint.log+`"`) {
						t.Errorf("%s diagnostics = %q, want Error for the failed write", endpoint.name, diagnostics.String())
					}
					if diff := cmp.Diff(before, productOutcomeState(t, ctx, owner, slug)); diff != "" {
						t.Errorf("failed product write changed saved rows or audit (-want +got):\n%s", diff)
					}
					if holder != nil {
						if rollbackErr := holder.Rollback(ctx); rollbackErr != nil {
							t.Fatal(rollbackErr)
						}
					}
					if fault == "closed pool" {
						mux = productOutcomeRoutes(adminPool, logger)
					}
					retry := postProductOutcome(t, ctx, mux, "/admin/products/"+slug+endpoint.path, form)
					if retry.Code != http.StatusSeeOther || retry.Header().Get("Location") != "/admin/products/"+slug+"?ok=1" {
						t.Fatalf("recovered %s = %d to %q, want 303 to success", endpoint.name, retry.Code, retry.Header().Get("Location"))
					}
					if admintest.AuditRows(t, owner, endpoint.action) != beforeAudit+1 {
						t.Error("recovered product write must commit exactly one audit event")
					}
					if endpoint.path == "/images/option" {
						var shows string
						if readErr := owner.QueryRow(ctx, `SELECT i.option_value_id::text FROM product_images i JOIN products p ON p.id=i.product_id WHERE p.slug=$1 AND i.storage_key=$2`, slug, digest).Scan(&shows); readErr != nil {
							t.Fatal(readErr)
						}
						if shows != valueID {
							t.Errorf("recovered image shows %q, want %q", shows, valueID)
						}
					} else {
						var values [3]string
						if readErr := owner.QueryRow(ctx, `SELECT s.value, s.label_en, s.value_en FROM product_specs s JOIN products p ON p.id=s.product_id WHERE p.slug=$1 AND s.label='Capacity'`, slug).Scan(&values[0], &values[1], &values[2]); readErr != nil {
							t.Fatal(readErr)
						}
						if diff := cmp.Diff([3]string{"350 mL", "Capacity", "350 mL"}, values); diff != "" {
							t.Errorf("recovered specification (-want +got):\n%s", diff)
						}
					}
				})
			}
		}
	}
}

func TestProductEditorWriteOutcomesPreserveMissingAndRefusedForms(t *testing.T) {
	owner := admintest.Pool(t)
	staff, _ := admintest.StaffContext(t, owner)
	adminPool := admintest.AdminRolePool(t, owner)
	slug, digest, valueID := seedProductWriteOutcome(t, staff, owner, adminPool)
	_, _, foreignValue := seedProductWriteOutcome(t, staff, owner, adminPool)
	mux := productOutcomeRoutes(adminPool, slog.New(slog.DiscardHandler))
	for _, locale := range i18n.Locales() {
		for _, outcome := range []string{"missing image", "missing image product", "missing spec product", "invalid image option", "foreign image option", "empty spec label", "duplicate spec label"} {
			t.Run(locale.Tag()+"/"+outcome, func(t *testing.T) {
				ctx := i18n.WithLocale(staff, locale)
				before := productOutcomeState(t, ctx, owner, slug)
				requestSlug, path := slug, "/images/option"
				form := productOutcomeForm(path, digest, valueID)
				status, location := http.StatusNotFound, ""
				switch outcome {
				case "missing image":
					form.Set("digest", "missing-image")
				case "missing image product":
					requestSlug = "missing-" + uuid.NewString()
				case "missing spec product":
					requestSlug, path = "missing-"+uuid.NewString(), "/specs"
					form = productOutcomeForm(path, digest, valueID)
				case "invalid image option", "foreign image option":
					form.Set("option_value", "not-an-option")
					if outcome == "foreign image option" {
						form.Set("option_value", foreignValue)
					}
					status, location = http.StatusSeeOther, "/admin/products/"+slug+"?badoption=1"
				case "empty spec label", "duplicate spec label":
					path = "/specs"
					form = productOutcomeForm(path, digest, valueID)
					form.Set("label", "   ")
					if outcome == "duplicate spec label" {
						form.Set("label", " Size ")
					}
					status = http.StatusUnprocessableEntity
				}
				response := postProductOutcome(t, ctx, mux, "/admin/products/"+requestSlug+path, form)
				if response.Code != status || response.Header().Get("Location") != location {
					t.Errorf("%s = %d to %q, want %d to %q", outcome, response.Code, response.Header().Get("Location"), status, location)
				}
				if diff := cmp.Diff(before, productOutcomeState(t, ctx, owner, slug)); diff != "" {
					t.Errorf("missing/refused product write changed saved rows or audit (-want +got):\n%s", diff)
				}
				if status == http.StatusUnprocessableEntity {
					admintest.AssertRefusedInput(t, response.Body.String(), "spec-label", form.Get("label"))
					for id, field := range map[string]string{"spec-value": "value", "spec-label-en": "label_en", "spec-value-en": "value_en"} {
						input := admintest.InputElementByID(t, response.Body.String(), id)
						if got := admintest.InputAttribute(t, input, "value"); got != form.Get(field) {
							t.Errorf("refused %s draft = %q, want %q", id, got, form.Get(field))
						}
					}
				}
			})
		}
	}
}

func TestRefusedSpecificationKeepsDraftWhenRatingsAreUnavailable(t *testing.T) {
	owner := admintest.Pool(t)
	staff, _ := admintest.StaffContext(t, owner)
	adminPool := admintest.AdminRolePool(t, owner)
	slug, _, _ := seedProductWriteOutcome(t, staff, owner, adminPool)
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(staff, locale)
			before := productOutcomeState(t, ctx, owner, slug)
			trace := &productWriteTrace{query: "ProductRating"}
			cfg := adminPool.Config().Copy()
			cfg.MaxConns = 1
			cfg.ConnConfig.RuntimeParams["lock_timeout"] = "200"
			cfg.ConnConfig.Tracer = trace
			reading, err := pgxpool.NewWithConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(reading.Close)
			var role string
			if roleErr := reading.QueryRow(ctx, `SELECT current_user`).Scan(&role); roleErr != nil || role != "admin" {
				t.Fatalf("reader role = %q, want admin: %v", role, roleErr)
			}
			holder, err := owner.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer pgtx.Rollback(ctx, holder)
			if _, lockErr := holder.Exec(ctx, `LOCK TABLE reviews IN ACCESS EXCLUSIVE MODE`); lockErr != nil {
				t.Fatal(lockErr)
			}
			var diagnostics bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&diagnostics, nil))
			form := productOutcomeForm("/specs", "", "")
			form.Set("label", "   ")
			response := postProductOutcome(t, ctx, productOutcomeRoutes(reading, logger), "/admin/products/"+slug+"/specs", form)
			attempts := trace.snapshot()
			if len(attempts) != 1 {
				t.Fatalf("ProductRating lock attempts = %d, want one", len(attempts))
			}
			pgErr, ok := errors.AsType[*pgconn.PgError](attempts[0])
			if !ok || pgErr.Code != "55P03" || ctx.Err() != nil {
				t.Fatalf("ProductRating error = %v, request = %v, want SQLSTATE55P03 with a live request", attempts[0], ctx.Err())
			}
			if response.Code != http.StatusUnprocessableEntity || response.Header().Get("Location") != "" {
				t.Fatalf("spec refusal with unavailable ratings = %d to %q, want 422 without Location", response.Code, response.Header().Get("Location"))
			}
			admintest.AssertRefusedInput(t, response.Body.String(), "spec-label", form.Get("label"))
			input := admintest.InputElementByID(t, response.Body.String(), "spec-value")
			if got := admintest.InputAttribute(t, input, "value"); got != form.Get("value") {
				t.Errorf("refused spec value = %q, want raw %q", got, form.Get("value"))
			}
			if !strings.Contains(diagnostics.String(), `level=ERROR msg="read product sales and reviews"`) {
				t.Errorf("partial editor diagnostics = %q, want the standing failure", diagnostics.String())
			}
			if diff := cmp.Diff(before, productOutcomeState(t, ctx, owner, slug)); diff != "" {
				t.Errorf("partial editor refusal changed saved rows or audit (-want +got):\n%s", diff)
			}
		})
	}
}

func seedProductWriteOutcome(t *testing.T, ctx context.Context, owner, adminPool *pgxpool.Pool) (slug, digest, valueID string) {
	t.Helper()
	s := products.NewStore(adminPool)
	slug = admintest.DraftProduct(t, ctx, owner, s)
	digest = "write-outcome-" + uuid.NewString()
	if err := s.AttachImage(ctx, slug, digest, "Outcome photograph", "", "", 800, 600); err != nil {
		t.Fatal(err)
	}
	if fields, err := s.AddSpec(ctx, slug, products.SpecDraft{Label: "Size", Value: "Original value"}); err != nil || len(fields) > 0 {
		t.Fatalf("spec fixture = %v/%v", fields, err)
	}
	if fields, err := s.AddOption(ctx, slug, products.OptionDraft{Name: "Colour"}); err != nil || len(fields) > 0 {
		t.Fatalf("option fixture = %v/%v", fields, err)
	}
	view, err := s.Product(ctx, slug)
	if err != nil || len(view.Options) != 1 {
		t.Fatalf("product fixture options = %d, want one: %v", len(view.Options), err)
	}
	if fields, addErr := s.AddOptionValue(ctx, slug, products.OptionDraft{OptionID: view.Options[0].ID, Name: "Blue"}); addErr != nil || len(fields) > 0 {
		t.Fatalf("option value fixture = %v/%v", fields, addErr)
	}
	if err := owner.QueryRow(ctx, `SELECT v.id::text FROM product_option_values v JOIN product_options o ON o.id=v.option_id JOIN products p ON p.id=o.product_id WHERE p.slug=$1`, slug).Scan(&valueID); err != nil {
		t.Fatal(err)
	}
	return slug, digest, valueID
}

func productOutcomeForm(path, digest, valueID string) url.Values {
	if path == "/images/option" {
		return url.Values{"digest": {digest}, "option_value": {valueID}}
	}
	return url.Values{"label": {" Capacity "}, "value": {" 350 mL "}, "label_en": {" Capacity "}, "value_en": {" 350 mL "}}
}

func productOutcomeRoutes(p *pgxpool.Pool, logger *slog.Logger) *http.ServeMux {
	mux := http.NewServeMux()
	products.NewHandler(products.NewStore(p), media.NewHandler(media.NewStore(p), logger), logger).Routes(mux, admintest.BackOffice)
	return mux
}

func postProductOutcome(t *testing.T, ctx context.Context, mux *http.ServeMux, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	return response
}

func productOutcomeState(t *testing.T, ctx context.Context, p *pgxpool.Pool, slug string) string {
	t.Helper()
	var state string
	if err := p.QueryRow(ctx, `SELECT jsonb_build_object(
		'product', (SELECT to_jsonb(p) FROM products p WHERE slug=$1),
		'images', (SELECT coalesce(jsonb_agg(to_jsonb(i) ORDER BY i.id), '[]'::jsonb) FROM product_images i JOIN products p ON p.id=i.product_id WHERE p.slug=$1),
		'specs', (SELECT coalesce(jsonb_agg(to_jsonb(s) ORDER BY s.id), '[]'::jsonb) FROM product_specs s JOIN products p ON p.id=s.product_id WHERE p.slug=$1),
		'audit', (SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY id), '[]'::jsonb) FROM audit_events a)
	)::text`, slug).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

type productWriteQueryKey struct{}

type productWriteTrace struct {
	mu     sync.Mutex
	query  string
	errors []error
}

func (tr *productWriteTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, productWriteQueryKey{}, strings.HasPrefix(data.SQL, "-- name: "+tr.query+" :"))
}

func (tr *productWriteTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if matched, _ := ctx.Value(productWriteQueryKey{}).(bool); !matched {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.errors = append(tr.errors, data.Err)
}

func (tr *productWriteTrace) snapshot() []error {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return slices.Clone(tr.errors)
}
