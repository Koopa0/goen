//go:build integration

package shipping_test

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/shipping"
	"github.com/koopa0/goen/internal/i18n"
)

func TestRefusedShippingEditsRetainTheDraftWithoutChangingTheConfiguration(t *testing.T) {
	p := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, p)
	var methodID, versionID, zoneID uuid.UUID
	if err := p.QueryRow(ctx, `
		WITH method AS (
		    INSERT INTO shipping_methods (code, destination_kind) VALUES ('refusal_fixture', 'address') RETURNING id
		)
		INSERT INTO shipping_method_versions (method_id, name, name_en, carrier, carrier_en, fee_cents, free_over_cents)
		SELECT id, 'Original', 'Original EN', 'Original carrier', 'Original carrier EN', 10000, 200000 FROM method
		RETURNING method_id, id`).Scan(&methodID, &versionID); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `INSERT INTO shipping_zones (code, name) VALUES ('refusal_fixture', 'Own zone') RETURNING id`).Scan(&zoneID); err != nil {
		t.Fatal(err)
	}
	// These checks force in-range refusals and prevent an over-bound name from
	// publishing a version before the independent stale-surcharge cases.
	if _, err := p.Exec(ctx, `
		ALTER TABLE shipping_method_versions ADD CONSTRAINT refusal_fixture_name CHECK (name <> 'Database refusal' AND char_length(name) <= 60);
		ALTER TABLE shipping_version_zones ADD CONSTRAINT refusal_fixture_amount CHECK (surcharge_cents <> 499900)`); err != nil {
		t.Fatal(err)
	}
	adminPool := admintest.AdminRolePool(t, p)
	s := shipping.NewStore(adminPool)
	if err := s.SetZoneSurcharge(ctx, versionID.String(), zoneID.String(), 100); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	shipping.NewHandler(s, true, slog.New(slog.DiscardHandler)).Routes(mux, admintest.BackOffice)
	post := func(values url.Values, path string, locale i18n.Locale) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodPost, path, strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, r)
		return res
	}
	base := url.Values{"method": {methodID.String()}, "name": {" Draft name "}, "name_en": {" Draft name EN "}, "carrier": {" Draft carrier "}, "carrier_en": {" Draft carrier EN "}, "fee": {"250"}, "free_over": {"3000"}}
	before, readErr := s.Configuration(ctx)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var auditBefore int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM audit_events`).Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	for _, locale := range i18n.Locales() {
		for _, tt := range []struct{ name, field, raw, inputID string }{
			{"unreadable fee", "fee", " unreadable ", "fee-"},
			{"exponent fee", "fee", "1e3", "fee-"},
			{"fee ceiling", "fee", "5001", "fee-"},
			{"negative fee", "fee", "-1", "fee-"},
			{"overflow fee", "fee", "9223372036854775808", "fee-"},
			{"unreadable threshold", "free_over", " not money ", "free-"},
			{"threshold ceiling", "free_over", "100000001", "free-"},
			{"negative threshold", "free_over", "-1", "free-"},
			{"missing name", "name", " ", "name-"},
			{"name rune ceiling", "name", strings.Repeat("\u754c", 61), "name-"},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				values := url.Values{}
				for key, value := range base {
					values[key] = append([]string(nil), value...)
				}
				values.Set(tt.field, tt.raw)
				res := post(values, "/admin/shipping/version", locale)
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatalf("refused version status=%d, want 422", res.Code)
				}
				body := res.Body.String()
				admintest.AssertRefusedInput(t, body, tt.inputID+methodID.String(), tt.raw)
				for field, id := range map[string]string{"name": "name-", "name_en": "name-en-", "carrier": "carrier-", "carrier_en": "carrier-en-", "fee": "fee-", "free_over": "free-"} {
					attrs := admintest.InputElementByID(t, body, id+methodID.String())
					if got := admintest.InputAttribute(t, attrs, "value"); got != values.Get(field) {
						t.Errorf("%s=%q, want raw %q", field, got, values.Get(field))
					}
					if field != tt.field && admintest.InputAttribute(t, attrs, "aria-invalid") != "" {
						t.Errorf("version refusal leaked to %s", field)
					}
				}
				if attrs := admintest.InputElementByID(t, body, "m-fee"); admintest.InputAttribute(t, attrs, "aria-invalid") != "" {
					t.Error("version refusal leaked to the new-method form")
				}
			})
		}
		t.Run(locale.Tag()+"/database version refusal", func(t *testing.T) {
			values := url.Values{}
			for key, value := range base {
				values[key] = append([]string(nil), value...)
			}
			values.Set("name", "Database refusal")
			res := post(values, "/admin/shipping/version", locale)
			if res.Code != http.StatusUnprocessableEntity {
				t.Fatalf("database version refusal status=%d, want 422", res.Code)
			}
			input := admintest.InputElementByID(t, res.Body.String(), "name-"+methodID.String())
			if admintest.InputAttribute(t, input, "value") != "Database refusal" || !strings.Contains(res.Body.String(), `id="version-`+methodID.String()+`-error" role="alert"`) || !strings.Contains(res.Body.String(), i18n.T(i18n.WithLocale(ctx, locale), i18n.KeyAdminShipRefused)) {
				t.Error("database version refusal lost its draft or owning-form explanation")
			}
		})
		for _, amount := range []string{"not money", " 5001 ", "-1", "9223372036854775808", "4999"} {
			t.Run(locale.Tag()+"/surcharge/"+amount, func(t *testing.T) {
				values := url.Values{"version": {versionID.String()}, "zone": {zoneID.String()}, "amount": {amount}}
				res := post(values, "/admin/shipping/surcharge", locale)
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatalf("refused surcharge status=%d, want 422", res.Code)
				}
				admintest.AssertRefusedInput(t, res.Body.String(), "sur-"+versionID.String()+"-"+zoneID.String(), amount)
				message := fmt.Sprintf(i18n.T(i18n.WithLocale(ctx, locale), i18n.KeyFormShippingSurcharge), "NT$5,000")
				if amount == "4999" {
					message = i18n.T(i18n.WithLocale(ctx, locale), i18n.KeyAdminShipRefused)
				}
				if !strings.Contains(res.Body.String(), message) {
					t.Error("the surcharge has no translated range explanation")
				}
			})
		}
	}
	for _, values := range []url.Values{
		{"method": {""}, "name": {"Unknown"}, "fee": {"5001"}},
		{"method": {uuid.NewString()}, "name": {"Unknown"}, "fee": {"5001"}},
		{"method": {uuid.NewString()}, "name": {"Unknown"}, "fee": {"100"}},
	} {
		if res := post(values, "/admin/shipping/version", i18n.En); res.Code != http.StatusNotFound {
			t.Errorf("unknown method status=%d, want 404", res.Code)
		}
	}
	if res := post(url.Values{"version": {versionID.String()}, "zone": {uuid.NewString()}, "amount": {"bad"}}, "/admin/shipping/surcharge", i18n.En); res.Code != http.StatusNotFound {
		t.Errorf("unknown zone status=%d, want 404", res.Code)
	}
	for _, version := range []string{"not a version", uuid.NewString()} {
		if res := post(url.Values{"version": {version}, "zone": {zoneID.String()}, "amount": {"bad"}}, "/admin/shipping/surcharge", i18n.En); res.Code != http.StatusNotFound {
			t.Errorf("unknown version status=%d, want 404", res.Code)
		}
	}
	after, readErr := s.Configuration(ctx)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if diff := cmp.Diff(before, after); diff != "" {
		t.Errorf("refusals changed the configuration (-want +got):\n%s", diff)
	}
	var auditAfter int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM audit_events`).Scan(&auditAfter); err != nil {
		t.Fatal(err)
	}
	if auditAfter != auditBefore {
		t.Errorf("refusals wrote %d audit rows", auditAfter-auditBefore)
	}

	for _, tt := range []struct {
		amount string
		want   int64
	}{{"5000", 500000}, {"", 0}} {
		res := post(url.Values{"version": {versionID.String()}, "zone": {zoneID.String()}, "amount": {tt.amount}}, "/admin/shipping/surcharge", i18n.En)
		if res.Code != http.StatusSeeOther {
			t.Fatalf("accepted surcharge status=%d", res.Code)
		}
		var amount int64
		if err := p.QueryRow(ctx, `SELECT coalesce((SELECT surcharge_cents FROM shipping_version_zones WHERE version_id=$1 AND zone_id=$2), 0)`, versionID, zoneID).Scan(&amount); err != nil || amount != tt.want {
			t.Fatalf("saved surcharge=%d, want %d: %v", amount, tt.want, err)
		}
	}
	base.Set("fee", "5000")
	base.Set("free_over", "")
	res := post(base, "/admin/shipping/version", i18n.En)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("accepted version status=%d", res.Code)
	}
	var name, nameEn, carrier, carrierEn string
	var fee, threshold int64
	if err := p.QueryRow(ctx, `SELECT name, name_en, carrier, carrier_en, fee_cents, coalesce(free_over_cents,0) FROM shipping_method_versions WHERE method_id=$1 ORDER BY effective_at DESC, id DESC LIMIT 1`, methodID).Scan(&name, &nameEn, &carrier, &carrierEn, &fee, &threshold); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]any{"Draft name", "Draft name EN", "Draft carrier", "Draft carrier EN", int64(500000), int64(0)}, []any{name, nameEn, carrier, carrierEn, fee, threshold}); diff != "" {
		t.Errorf("accepted version (-want +got):\n%s", diff)
	}
	var currentVersion uuid.UUID
	if err := p.QueryRow(ctx, `SELECT id FROM shipping_method_versions WHERE method_id=$1 ORDER BY effective_at DESC, id DESC LIMIT 1`, methodID).Scan(&currentVersion); err != nil {
		t.Fatal(err)
	}
	before, readErr = s.Configuration(ctx)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err := p.QueryRow(ctx, `SELECT count(*) FROM audit_events`).Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	for _, locale := range i18n.Locales() {
		for _, amount := range []string{"300", " 5001 ", " not money "} {
			t.Run(locale.Tag()+"/stale surcharge/"+amount, func(t *testing.T) {
				res := post(url.Values{"version": {versionID.String()}, "zone": {zoneID.String()}, "amount": {amount}}, "/admin/shipping/surcharge", locale)
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatalf("stale surcharge status=%d, want 422", res.Code)
				}
				body := res.Body.String()
				admintest.AssertRefusedInput(t, body, "sur-"+currentVersion.String()+"-"+zoneID.String(), amount)
				message := i18n.T(i18n.WithLocale(ctx, locale), i18n.KeyAdminShipVersionChanged)
				if strings.Count(body, message) != 1 {
					t.Errorf("stale surcharge explanations=%d, want one owning-control explanation", strings.Count(body, message))
				}
				if strings.Contains(body, fmt.Sprintf(i18n.T(i18n.WithLocale(ctx, locale), i18n.KeyFormShippingSurcharge), "NT$5,000")) {
					t.Error("range refusal took precedence over the stale version")
				}
			})
		}
	}
	after, readErr = s.Configuration(ctx)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if diff := cmp.Diff(before, after); diff != "" {
		t.Errorf("stale refusals changed configuration (-want +got):\n%s", diff)
	}
	if err := p.QueryRow(ctx, `SELECT count(*) FROM audit_events`).Scan(&auditAfter); err != nil {
		t.Fatal(err)
	}
	if auditAfter != auditBefore {
		t.Errorf("stale refusals wrote %d audit rows, want zero", auditAfter-auditBefore)
	}
}

func TestShippingNameBoundsAreEnforcedByTheDatabase(t *testing.T) {
	p := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, p)
	adminPool := admintest.AdminRolePool(t, p)
	var methodID uuid.UUID
	if err := p.QueryRow(ctx, `INSERT INTO shipping_methods (code, destination_kind) VALUES ('name_bound_fixture', 'address') RETURNING id`).Scan(&methodID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []struct {
		name, constraint, query string
	}{
		{name: "version", constraint: "shipping_method_versions_name_bounded", query: `INSERT INTO shipping_method_versions (method_id, name, fee_cents) VALUES ($1, $2, 0) RETURNING name`},
		{name: "zone", constraint: "shipping_zones_name_bounded", query: `INSERT INTO shipping_zones (code, name) VALUES ($1, $2) RETURNING name`},
	} {
		for _, tt := range []struct {
			name, value string
			accept      bool
		}{
			{name: "ascii-bound", value: strings.Repeat("a", 60), accept: true},
			{name: "unicode-bound", value: strings.Repeat("\u754c", 60), accept: true},
			{name: "ascii-over-bound", value: strings.Repeat("a", 61)},
			{name: "unicode-over-bound", value: strings.Repeat("\u754c", 61)},
		} {
			t.Run(table.name+"/"+tt.name, func(t *testing.T) {
				var identity any = methodID
				if table.name == "zone" {
					identity = "name_bound_" + strings.ReplaceAll(uuid.NewString(), "-", "")
				}
				var saved string
				err := adminPool.QueryRow(ctx, table.query, identity, tt.value).Scan(&saved)
				if tt.accept {
					if err != nil || saved != tt.value {
						t.Errorf("legal name saved=%q error=%v, want %q", saved, err, tt.value)
					}
					return
				}
				pgErr, ok := errors.AsType[*pgconn.PgError](err)
				if !ok || pgErr.Code != "23514" || pgErr.ConstraintName != table.constraint {
					t.Errorf("over-bound name error=%v, want CHECK %s", err, table.constraint)
				}
			})
		}
	}
}
