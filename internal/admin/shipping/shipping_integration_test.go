//go:build integration

package shipping_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/shipping"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

func TestShippingSurchargesStayWithTheirVersionThroughTheForm(t *testing.T) {
	for _, tt := range []struct {
		name     string
		reversed bool
	}{{name: "a before b"}, {name: "b before a", reversed: true}} {
		t.Run(tt.name, func(t *testing.T) {
			owner := admintest.Pool(t)
			ctx, _ := admintest.StaffContext(t, owner)
			writer := admintest.AdminRolePool(t, owner)
			store := shipping.NewStore(writer)
			// Equal zone sort keys use version IDs, so swap those too to exercise both result orders.
			versions := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
			slices.SortFunc(versions, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
			if tt.reversed {
				slices.Reverse(versions[:2])
			}
			for i, name := range []string{"A", "B", "C"} {
				var method uuid.UUID
				if err := owner.QueryRow(ctx, `INSERT INTO shipping_methods (code,destination_kind,is_active) VALUES ($1,'address',false) RETURNING id`, "shared_"+strings.ToLower(name)+"_"+uuid.NewString()[:8]).Scan(&method); err != nil {
					t.Fatalf("create method %s: %v", name, err)
				}
				if _, err := owner.Exec(ctx, `INSERT INTO shipping_method_versions (id,method_id,name,fee_cents,effective_at) VALUES ($1,$2,$3,8000,now()-interval '1 day')`, versions[i], method, "Address "+name); err != nil {
					t.Fatalf("create version %s: %v", name, err)
				}
			}
			var zone uuid.UUID
			if err := owner.QueryRow(ctx, `INSERT INTO shipping_zones (code,name,position) VALUES ($1,'Shared zone',100) RETURNING id`, "shared_"+uuid.NewString()[:8]).Scan(&zone); err != nil {
				t.Fatalf("create shared zone: %v", err)
			}
			insertion := []int{0, 1}
			if tt.reversed {
				slices.Reverse(insertion)
			}
			for _, i := range insertion {
				if _, err := owner.Exec(ctx, `INSERT INTO shipping_version_zones (version_id,zone_id,surcharge_cents) VALUES ($1,$2,$3)`, versions[i], zone, []int64{10000, 20000}[i]); err != nil {
					t.Fatalf("create surcharge %d: %v", i, err)
				}
			}
			view, err := store.Configuration(ctx)
			if err != nil {
				t.Fatalf("Configuration: %v", err)
			}
			type surcharge struct {
				ZoneID string
				Cents  int64
			}
			want := map[string][]surcharge{
				versions[0].String(): {{ZoneID: zone.String(), Cents: 10000}},
				versions[1].String(): {{ZoneID: zone.String(), Cents: 20000}},
				versions[2].String(): {},
			}
			got := map[string][]surcharge{}
			for _, method := range view.Methods {
				if _, fixture := want[method.VersionID]; !fixture {
					continue
				}
				got[method.VersionID] = []surcharge{}
				for _, row := range method.Surcharges {
					got[method.VersionID] = append(got[method.VersionID], surcharge{ZoneID: row.ZoneID, Cents: row.Cents})
				}
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Configuration surcharges (-want +got):\n%s", diff)
			}
			mux := http.NewServeMux()
			handlerOver(store).Routes(mux, admintest.BackOffice)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/shipping", http.NoBody))
			if response.Code != http.StatusOK {
				t.Fatalf("GET /admin/shipping = %d, want 200", response.Code)
			}
			forms := renderedSurchargeForms(t, response.Body.String(), zone.String())
			wantAmounts := map[string]string{versions[0].String(): "100", versions[1].String(): "200", versions[2].String(): ""}
			amounts := map[string]string{}
			for version := range wantAmounts {
				form, exists := forms[version]
				if !exists {
					t.Fatalf("surcharge form for %s is missing", version)
				}
				amounts[version] = form.Get("amount")
			}
			if diff := cmp.Diff(wantAmounts, amounts); diff != "" {
				t.Errorf("rendered surcharge amounts (-want +got):\n%s", diff)
			}
			type storedSurcharge struct {
				Rows  int64
				Cents int64
			}
			wantStored := map[string]storedSurcharge{
				versions[0].String(): {Rows: 1, Cents: 10000},
				versions[1].String(): {Rows: 1, Cents: 20000},
				versions[2].String(): {},
			}
			stored := map[string]storedSurcharge{}
			for _, version := range versions {
				form := forms[version.String()]
				request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/shipping/surcharge", strings.NewReader(form.Encode()))
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, request)
				if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/shipping?ok=1" {
					t.Fatalf("submit surcharge form = %d %q, want 303 to saved configuration", response.Code, response.Header().Get("Location"))
				}
				var row storedSurcharge
				if err := owner.QueryRow(ctx, `SELECT count(*),coalesce(sum(surcharge_cents),0)::bigint FROM shipping_version_zones WHERE version_id=$1 AND zone_id=$2`, version, zone).Scan(&row.Rows, &row.Cents); err != nil {
					t.Fatalf("read submitted surcharge: %v", err)
				}
				stored[version.String()] = row
			}
			if diff := cmp.Diff(wantStored, stored); diff != "" {
				t.Errorf("untouched surcharge resubmission (-want +got):\n%s", diff)
			}
		})
	}
}

func renderedSurchargeForms(t *testing.T, markup, zone string) map[string]url.Values {
	t.Helper()
	root, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatalf("parse shipping page: %v", err)
	}
	attribute := func(node *html.Node, name string) string {
		for _, attr := range node.Attr {
			if attr.Key == name {
				return attr.Val
			}
		}
		return ""
	}
	forms := map[string]url.Values{}
	for node := range root.Descendants() {
		if node.Type != html.ElementNode || node.Data != "form" || attribute(node, "action") != "/admin/shipping/surcharge" {
			continue
		}
		values := url.Values{}
		for input := range node.Descendants() {
			if input.Type == html.ElementNode && input.Data == "input" && attribute(input, "name") != "" {
				values.Set(attribute(input, "name"), attribute(input, "value"))
			}
		}
		if values.Get("zone") == zone {
			version := values.Get("version")
			if _, duplicate := forms[version]; duplicate {
				t.Fatalf("duplicate surcharge form for version %s", version)
			}
			forms[version] = values
		}
	}
	return forms
}

func TestPublishingAVersionCarriesItsZoneSurcharges(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := shipping.NewStore(pool)

	var methodID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_methods WHERE code = 'home_delivery'`).Scan(&methodID); err != nil {
		t.Fatalf("find the method: %v", err)
	}
	// Its own surcharge and not the seed's: another test clears the current version's.
	var versionID, zoneID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT v.id FROM shipping_method_versions v
		WHERE v.method_id = $1 AND v.effective_at <= now()
		ORDER BY v.effective_at DESC, v.id DESC LIMIT 1`, methodID).Scan(&versionID); err != nil {
		t.Fatalf("find the current version: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_zones WHERE code = 'offshore'`).Scan(&zoneID); err != nil {
		t.Fatalf("find the zone: %v", err)
	}
	if err := s.SetZoneSurcharge(ctx, versionID.String(), zoneID.String(), 200); err != nil {
		t.Fatalf("set the surcharge to carry: %v", err)
	}

	before := surchargeOfCurrentVersion(t, "home_delivery")
	if before == 0 {
		t.Fatal("the surcharge this test just set is not there — it would prove nothing")
	}

	if err := s.PublishShippingVersion(ctx, shipping.ShippingVersion{
		MethodID: methodID.String(), Name: "宅配到府", Carrier: "黑貓宅急便",
		NameEn: "Home delivery", CarrierEn: "T-Cat",
		FeeDollars: 100, FreeOverDollars: 2000,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if got := surchargeOfCurrentVersion(t, "home_delivery"); got != before {
		t.Errorf("the new version charges %d for 離島, want %d carried forward", got, before)
	}
	// The base fee too, or this passes on a publish that did nothing at all.
	var fee int64
	if err := pool.QueryRow(ctx, `
		SELECT v.fee_cents FROM shipping_method_versions v
		JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.code = 'home_delivery' AND v.effective_at <= now()
		ORDER BY v.effective_at DESC, v.id DESC LIMIT 1`).Scan(&fee); err != nil {
		t.Fatalf("read the new fee: %v", err)
	}
	if fee != 10000 {
		t.Errorf("the new version charges %d, want 10000", fee)
	}
}

func TestPublishingAVersionKeepsItsEnglishName(t *testing.T) {
	ctx, actor := admintest.StaffContext(t, pool)
	s := shipping.NewStore(pool)
	code := "english_roundtrip_" + uuid.NewString()[:8]

	// This test owns both rows. Deleting this fixture must make the view lookup
	// below fail loudly rather than attach the assertion to a seed method.
	var methodID uuid.UUID
	if err := pool.QueryRow(ctx, `
		WITH method AS (
			INSERT INTO shipping_methods (code, destination_kind, is_active)
			VALUES ($1, 'address', false) RETURNING id
		)
		INSERT INTO shipping_method_versions
			(method_id, name, carrier, name_en, carrier_en, fee_cents,
			 free_over_cents, effective_at)
		SELECT id, '宅配到府', '黑貓宅急便', 'Home delivery', 'T-Cat', 8000,
		       300000, now() - interval '1 day'
		FROM method
		RETURNING method_id`, code).Scan(&methodID); err != nil {
		t.Fatalf("create the method and version fixture: %v", err)
	}

	view, err := s.Configuration(ctx)
	if err != nil {
		t.Fatalf("read shipping methods: %v", err)
	}
	var method *admin.ShippingMethod
	for i := range view.Methods {
		if view.Methods[i].Code == code {
			method = &view.Methods[i]
			break
		}
	}
	if method == nil {
		t.Fatalf("fixture method %q is absent from the shipping view", code)
	}
	type translations struct {
		Name    string
		Carrier string
	}
	if diff := cmp.Diff(translations{Name: "Home delivery", Carrier: "T-Cat"},
		translations{Name: method.NameEn, Carrier: method.CarrierEn}); diff != "" {
		t.Fatalf("shipping view translations (-want +got):\n%s", diff)
	}

	if err := s.PublishShippingVersion(ctx, shipping.ShippingVersion{
		MethodID: method.MethodID, Name: method.Name, Carrier: method.Carrier,
		NameEn: method.NameEn, CarrierEn: method.CarrierEn,
		FeeDollars: 100, FreeOverDollars: 3000,
	}); err != nil {
		t.Fatalf("publish the untouched form: %v", err)
	}

	var got struct {
		Name      string
		NameEn    *string
		Carrier   string
		CarrierEn *string
		FeeCents  int64
	}
	if err := pool.QueryRow(ctx, `
		SELECT name, name_en, coalesce(carrier, ''), carrier_en, fee_cents
		FROM shipping_method_versions
		WHERE method_id = $1
		ORDER BY effective_at DESC, id DESC LIMIT 1`, methodID).
		Scan(&got.Name, &got.NameEn, &got.Carrier, &got.CarrierEn, &got.FeeCents); err != nil {
		t.Fatalf("read the published version: %v", err)
	}
	wantNameEn, wantCarrierEn := "Home delivery", "T-Cat"
	want := struct {
		Name      string
		NameEn    *string
		Carrier   string
		CarrierEn *string
		FeeCents  int64
	}{
		Name: "宅配到府", NameEn: &wantNameEn,
		Carrier: "黑貓宅急便", CarrierEn: &wantCarrierEn, FeeCents: 10000,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("raw published version (-want +got):\n%s", diff)
	}

	assertAuditTranslations := func(wantNameEn, wantCarrierEn string) {
		t.Helper()
		var payload []byte
		if err := pool.QueryRow(ctx, `
			SELECT after FROM audit_events
			WHERE action = 'shipping.publish' AND entity_id = $1 AND actor_user_id = $2
			ORDER BY occurred_at DESC, id DESC LIMIT 1`, methodID, actor).Scan(&payload); err != nil {
			t.Fatalf("read the publish audit row: %v", err)
		}
		var after map[string]any
		if err := json.Unmarshal(payload, &after); err != nil {
			t.Fatalf("decode the publish audit row: %v", err)
		}
		for key, want := range map[string]string{
			"name_en": wantNameEn, "carrier_en": wantCarrierEn,
		} {
			if got, ok := after[key]; !ok || got != want {
				t.Errorf("audit after[%q] = %#v, present %t; want %q", key, got, ok, want)
			}
		}
	}
	assertAuditTranslations("Home delivery", "T-Cat")

	if err := s.PublishShippingVersion(ctx, shipping.ShippingVersion{
		MethodID: method.MethodID, Name: method.Name, Carrier: method.Carrier,
		NameEn: "", CarrierEn: "", FeeDollars: 101, FreeOverDollars: 3000,
	}); err != nil {
		t.Fatalf("publish the deliberately cleared form: %v", err)
	}
	var clearedNameEn, clearedCarrierEn *string
	var clearedFee int64
	if err := pool.QueryRow(ctx, `
		SELECT name_en, carrier_en, fee_cents FROM shipping_method_versions
		WHERE method_id = $1
		ORDER BY effective_at DESC, id DESC LIMIT 1`, methodID).
		Scan(&clearedNameEn, &clearedCarrierEn, &clearedFee); err != nil {
		t.Fatalf("read the cleared version: %v", err)
	}
	if clearedNameEn != nil || clearedCarrierEn != nil {
		var gotNameEn, gotCarrierEn any
		if clearedNameEn != nil {
			gotNameEn = *clearedNameEn
		}
		if clearedCarrierEn != nil {
			gotCarrierEn = *clearedCarrierEn
		}
		t.Errorf("cleared translations = (%#v, %#v), want SQL NULLs",
			gotNameEn, gotCarrierEn)
	}
	if clearedFee != 10100 {
		t.Errorf("cleared version fee = %d, want 10100", clearedFee)
	}
	assertAuditTranslations("", "")
}

func TestAZeroSurchargeClearsTheRowRatherThanStoringZero(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := shipping.NewStore(pool)

	var versionID, zoneID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT v.id FROM shipping_method_versions v
		JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.code = 'home_delivery' AND v.effective_at <= now()
		ORDER BY v.effective_at DESC, v.id DESC LIMIT 1`).Scan(&versionID); err != nil {
		t.Fatalf("find the version: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_zones WHERE code = 'offshore'`).Scan(&zoneID); err != nil {
		t.Fatalf("find the zone: %v", err)
	}

	if err := s.SetZoneSurcharge(ctx, versionID.String(), zoneID.String(), 250); err != nil {
		t.Fatalf("set: %v", err)
	}
	if n := surchargeRows(t, versionID); n != 1 {
		t.Fatalf("%d surcharge rows after setting one, want 1", n)
	}

	if err := s.SetZoneSurcharge(ctx, versionID.String(), zoneID.String(), 0); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if n := surchargeRows(t, versionID); n != 0 {
		t.Errorf("%d surcharge rows after clearing, want 0 — zero was stored as a row", n)
	}
}

func TestAMethodParcelLimitRefusalKeepsTheRawText(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := shipping.NewStore(pool)
	h := handlerOver(s)

	tests := []struct {
		name  string
		field string
		id    string
		raw   string
	}{
		{name: "longest side", field: "max_parcel_longest", id: "m-max-longest", raw: "5001"},
		{name: "three-side sum", field: "max_parcel_sum", id: "m-max-sum", raw: "15001"},
		{name: "weight", field: "max_parcel_weight", id: "m-max-weight", raw: "200001"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := "limit_" + uuid.NewString()[:8]
			form := url.Values{
				"code":               {code},
				"destination":        {"pickup_point"},
				"max_parcel_longest": {"450"},
				"max_parcel_sum":     {"1050"},
				"max_parcel_weight":  {"10000"},
				"name":               {"拒絕上限測試"},
				"carrier":            {"測試承運人"},
				"fee":                {"60"},
				"free_over":          {"1000"},
			}
			form.Set(tt.field, tt.raw)
			req := httptest.NewRequestWithContext(ctx, http.MethodPost,
				"/admin/shipping/method", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			res := httptest.NewRecorder()
			h.CreateMethod(res, req)

			if res.Code != http.StatusUnprocessableEntity {
				t.Errorf("CreateShippingMethod(%s=%q) status = %d, want 422",
					tt.field, tt.raw, res.Code)
			} else {
				body := res.Body.String()
				admintest.AssertRefusedInput(t, body, tt.id, tt.raw)
				for id, field := range map[string]string{
					"m-max-longest": "max_parcel_longest",
					"m-max-sum":     "max_parcel_sum",
					"m-max-weight":  "max_parcel_weight",
				} {
					admintest.AssertTextNumberControl(t, body, id)
					if got := admintest.InputAttribute(t, admintest.InputElementByID(t, body, id), "value"); got != form.Get(field) {
						t.Errorf("input %q value = %q, want submitted %q", id, got, form.Get(field))
					}
				}
			}

			var methodID uuid.NullUUID
			if err := pool.QueryRow(ctx,
				`SELECT id FROM shipping_methods WHERE code = $1`, code).Scan(&methodID); err != nil &&
				!errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("read refused method: %v", err)
			}
			if methodID.Valid {
				t.Errorf("CreateShippingMethod(%s=%q) inserted method %s",
					tt.field, tt.raw, methodID.UUID)
				if err := s.SetMethodActive(ctx, methodID.UUID.String(), false); err != nil {
					t.Fatalf("deactivate unexpectedly inserted method: %v", err)
				}
			}
		})
	}
}

func TestAShopCanOfferAThirdDeliveryMethod(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := shipping.NewStore(pool)
	emptyCart, cartErr := cart.NewStore(pool).Create(ctx, uuid.NewString(), uuid.NullUUID{})
	if cartErr != nil {
		t.Fatalf("create cart: %v", cartErr)
	}
	code := "express" + uuid.NewString()[:6]

	if errs, err := s.CreateMethod(ctx, &shipping.NewMethod{
		Code: code, Destination: "address",
		Name: "隔日到貨", NameEn: "Next-day delivery",
		Carrier: "順豐", CarrierEn: "SF Express",
		FeeDollars: 150, FreeOverDollars: 5000,
	}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateMethod: %v %v", err, errs)
	}

	var versions int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM shipping_method_versions v
		JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.code = $1`, code).Scan(&versions); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if versions != 1 {
		t.Errorf("%d versions for a new method, want exactly 1", versions)
	}

	basket := cart.NewStore(pool)
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		want   string
	}{
		{name: "Chinese", locale: i18n.ZhHant, want: "隔日到貨"},
		{name: "English", locale: i18n.En, want: "Next-day delivery"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			choices, err := basket.ShippingChoices(i18n.WithLocale(ctx, tt.locale), emptyCart, 100000)
			if err != nil {
				t.Fatalf("ShippingChoices: %v", err)
			}
			var found bool
			for i := range choices {
				if choices[i].Name == tt.want {
					found = true
				}
			}
			if !found {
				t.Errorf("the checkout does not offer %q", tt.want)
			}
		})
	}

	var methodID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM shipping_methods WHERE code = $1`, code).Scan(&methodID); err != nil {
		t.Fatalf("read the method: %v", err)
	}
	if err := s.SetMethodActive(ctx, methodID, false); err != nil {
		t.Fatalf("SetMethodActive: %v", err)
	}
	choices, err := basket.ShippingChoices(ctx, emptyCart, 100000)
	if err != nil {
		t.Fatalf("ShippingChoices: %v", err)
	}
	for i := range choices {
		if choices[i].Name == "隔日到貨" {
			t.Error("a retired method is still offered at checkout")
		}
	}
	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM shipping_methods WHERE code = $1`, code).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Error("retiring a method deleted it — every past order names its version")
	}
}

func TestAShopCanSayWhichPostalCodesCostMore(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := shipping.NewStore(pool)
	code := "remote" + uuid.NewString()[:6]

	if errs, err := s.CreateZone(ctx, &shipping.NewZone{
		Code: code, Name: "山區", NameEn: "Mountain", Prefixes: "546 552, 553",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateZone: %v %v", err, errs)
	}

	var zoneID string
	var prefixes []string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM shipping_zones WHERE code = $1`, code).Scan(&zoneID); err != nil {
		t.Fatalf("read the zone: %v", err)
	}
	rows, err := pool.Query(ctx,
		`SELECT prefix FROM shipping_zone_prefixes WHERE zone_id = $1::uuid ORDER BY prefix`, zoneID)
	if err != nil {
		t.Fatalf("read prefixes: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if scanErr := rows.Scan(&p); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		prefixes = append(prefixes, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate shipping-zone prefixes: %v", err)
	}
	if diff := cmp.Diff([]string{"546", "552", "553"}, prefixes); diff != "" {
		t.Errorf("prefixes (-want +got):\n%s", diff)
	}

	other := "remote2" + uuid.NewString()[:6]
	if errs, zoneErr := s.CreateZone(ctx, &shipping.NewZone{
		Code: other, Name: "另一區", Prefixes: "546",
	}); zoneErr != nil || len(errs) > 0 {
		t.Fatalf("CreateZone: %v %v", zoneErr, errs)
	}
	var owner string
	if err := pool.QueryRow(ctx, `
		SELECT z.code FROM shipping_zone_prefixes p
		JOIN shipping_zones z ON z.id = p.zone_id WHERE p.prefix = '546'`).Scan(&owner); err != nil {
		t.Fatalf("read the owner: %v", err)
	}
	if owner != other {
		t.Errorf("546 belongs to %q, want it moved to %q", owner, other)
	}
}

func TestAZonesPostalCodesAreTheWholeSet(t *testing.T) {
	ctx, actor := admintest.StaffContext(t, pool)
	s := shipping.NewStore(pool)
	p := unusedZonePrefixes(t, 6)

	blankCode := "blank_zone_" + uuid.NewString()[:8]
	if errs, err := s.CreateZone(ctx, &shipping.NewZone{
		Code: blankCode, Name: "空白分區", Prefixes: " \t, ",
	}); err != nil {
		t.Fatalf("CreateZone(blank): %v", err)
	} else if errs["prefixes"] == "" {
		t.Fatalf("CreateZone accepted an empty prefix set: %v", errs)
	}
	var blankRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM shipping_zones WHERE code = $1`, blankCode).
		Scan(&blankRows); err != nil {
		t.Fatalf("count refused blank zone: %v", err)
	}
	if blankRows != 0 {
		t.Fatalf("blank CreateZone inserted %d rows, want 0", blankRows)
	}

	zoneA := createZoneWithPrefixes(t, ctx, s, "甲區", p[0:3])
	zoneB := createZoneWithPrefixes(t, ctx, s, "乙區", p[3:5])

	// Keep p0/p2, omit p1, add p5, and repeat p5: the audit count is the
	// resulting set, not the number of tokens typed.
	first := []string{p[0], p[2], p[5], p[5]}
	if errs, err := s.SetZonePrefixes(ctx, zoneA.String(), strings.Join(first, " ")); err != nil || len(errs) > 0 {
		t.Fatalf("first replacement: %v %v", err, errs)
	}
	assertZonePrefixes(t, zoneA, []string{p[0], p[2], p[5]})
	assertZonePrefixes(t, zoneB, []string{p[3], p[4]})
	assertZonePrefixAudit(t, actor, zoneA, 3, 1)

	// Moving p3 through the production door must keep the UPSERT behavior.
	second := []string{p[0], p[2], p[3], p[5]}
	if errs, err := s.SetZonePrefixes(ctx, zoneA.String(), strings.Join(second, " ")); err != nil || len(errs) > 0 {
		t.Fatalf("replacement that moves a prefix: %v %v", err, errs)
	}
	assertZonePrefixes(t, zoneA, second)
	assertZonePrefixes(t, zoneB, []string{p[4]})
	assertZonePrefixAudit(t, actor, zoneA, 4, 2)

	if errs, err := s.SetZonePrefixes(ctx, zoneA.String(), " \t, "); err != nil || len(errs) > 0 {
		t.Fatalf("clear zone A: %v %v", err, errs)
	}
	assertZonePrefixes(t, zoneA, []string{})
	assertZonePrefixAudit(t, actor, zoneA, 0, 3)
	if err := s.DeleteZone(ctx, zoneA.String()); err != nil {
		t.Fatalf("delete zone after clearing it: %v", err)
	}

	if errs, err := s.SetZonePrefixes(ctx, zoneB.String(), ""); err != nil || len(errs) > 0 {
		t.Fatalf("clear zone B: %v %v", err, errs)
	}
	if err := s.DeleteZone(ctx, zoneB.String()); err != nil {
		t.Fatalf("delete neighbour after clearing it: %v", err)
	}
}

func TestAZonePrefixInfrastructureFailureIsNotADomainRefusal(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := shipping.NewStore(pool)
	p := unusedZonePrefixes(t, 1)
	zoneID := createZoneWithPrefixes(t, ctx, s, "錯誤語法分區", p)

	missing := uuid.New()
	if _, err := s.SetZonePrefixes(ctx, missing.String(), p[0]); !errors.Is(err, shipping.ErrNotFound) || errors.Is(err, shipping.ErrRefused) {
		t.Fatalf("missing zone error = %v, want only ErrNotFound", err)
	}
	missingForm := url.Values{"prefixes": {p[0]}}
	missingReq := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/shipping/zone/"+missing.String()+"/prefixes", strings.NewReader(missingForm.Encode()))
	missingReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	missingReq.SetPathValue("id", missing.String())
	missingRes := httptest.NewRecorder()
	handlerOver(s).SetZonePrefixes(missingRes, missingReq)
	if missingRes.Code != http.StatusNotFound {
		t.Fatalf("missing zone handler answered %d, want 404", missingRes.Code)
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin zone blocker: %v", err)
	}
	released := false
	defer func() {
		if !released {
			_ = blocker.Rollback(context.WithoutCancel(t.Context()))
		}
	}()
	var blockerPID int32
	if err := blocker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatalf("read zone-blocker pid: %v", err)
	}
	if _, err := blocker.Exec(ctx, `SELECT id FROM shipping_zones WHERE id = $1 FOR UPDATE`, zoneID); err != nil {
		t.Fatalf("lock shipping zone: %v", err)
	}

	app := "zone_set_error_" + uuid.NewString()[:8]
	blockedStore := shipping.NewStore(admintest.NamedPool(t, pool, app))
	writeCtx, cancelWrite := context.WithCancel(ctx)
	done := make(chan struct {
		errs map[string]string
		err  error
	}, 1)
	go func() {
		errs, setErr := blockedStore.SetZonePrefixes(writeCtx, zoneID.String(), p[0])
		done <- struct {
			errs map[string]string
			err  error
		}{errs: errs, err: setErr}
	}()
	traceCtx, cancelTrace := context.WithTimeout(ctx, 5*time.Second)
	defer cancelTrace()
	admintest.WaitForBlockedApplication(t, pool, traceCtx, app, blockerPID)
	cancelWrite()
	select {
	case got := <-done:
		if got.err == nil || errors.Is(got.err, shipping.ErrNotFound) || errors.Is(got.err, shipping.ErrRefused) {
			t.Fatalf("cancelled lock error = %v/%v, want infrastructure error only", got.err, got.errs)
		}
	case <-traceCtx.Done():
		t.Fatalf("cancelled prefix writer did not return: %v", traceCtx.Err())
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatalf("release zone blocker: %v", err)
	}
	released = true

	failedPool := admintest.NamedPool(t, pool, "zone_set_closed_"+uuid.NewString()[:8])
	failedPool.Close()
	failedStore := shipping.NewStore(failedPool)
	failedReq := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/shipping/zone/"+zoneID.String()+"/prefixes", strings.NewReader(missingForm.Encode()))
	failedReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	failedReq.SetPathValue("id", zoneID.String())
	failedRes := httptest.NewRecorder()
	handlerOver(failedStore).SetZonePrefixes(failedRes, failedReq)
	if failedRes.Code != http.StatusInternalServerError {
		t.Fatalf("infrastructure failure handler answered %d, want 500", failedRes.Code)
	}

	if errs, clearErr := s.SetZonePrefixes(ctx, zoneID.String(), ""); clearErr != nil || len(errs) > 0 {
		t.Fatalf("clear infrastructure-error fixture: %v %v", clearErr, errs)
	}
	if err := s.DeleteZone(ctx, zoneID.String()); err != nil {
		t.Fatalf("delete infrastructure-error fixture: %v", err)
	}
}

func TestARefusedZonePrefixEditStaysOnItsOwnRow(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := shipping.NewStore(pool)
	p := unusedZonePrefixes(t, 2)
	zoneA := createZoneWithPrefixes(t, ctx, s, "錯誤列", p[:1])
	zoneB := createZoneWithPrefixes(t, ctx, s, "相鄰列", p[1:])

	const raw = "12o & 原樣"
	form := url.Values{"prefixes": {raw}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/shipping/zone/"+zoneA.String()+"/prefixes", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", zoneA.String())
	res := httptest.NewRecorder()
	handlerOver(s).SetZonePrefixes(res, req)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("refused prefix edit answered %d, want 422", res.Code)
	}

	body := res.Body.String()
	targetID := "pre-" + zoneA.String()
	target, targetText := admintest.TextareaByID(t, body, targetID)
	if targetText != raw {
		t.Errorf("target row text = %q, want raw %q", targetText, raw)
	}
	if admintest.InputAttribute(t, target, "aria-invalid") != "true" {
		t.Errorf("target row is not marked aria-invalid: %s", target)
	}
	errorID := targetID + "-error"
	if got := admintest.InputAttribute(t, target, "aria-describedby"); got != errorID {
		t.Errorf("target aria-describedby = %q, want %q", got, errorID)
	}
	if !regexp.MustCompile(`<p[^>]*id="` + regexp.QuoteMeta(errorID) + `"[^>]*>[^<]+</p>`).
		MatchString(body) {
		t.Errorf("no nonempty error element resolves %q", errorID)
	}

	neighbour, neighbourText := admintest.TextareaByID(t, body, "pre-"+zoneB.String())
	if neighbourText != p[1] {
		t.Errorf("neighbour text = %q, want database value %q", neighbourText, p[1])
	}
	if strings.Contains(neighbour, "aria-invalid") {
		t.Errorf("neighbour row inherited the target error: %s", neighbour)
	}
	create, createText := admintest.TextareaByID(t, body, "z-prefixes")
	if strings.Contains(create, "aria-invalid") || createText == raw {
		t.Errorf("create-zone field inherited an existing-row refusal: %s", create)
	}
	assertZonePrefixes(t, zoneA, p[:1])

	for _, id := range []uuid.UUID{zoneA, zoneB} {
		if errs, err := s.SetZonePrefixes(ctx, id.String(), ""); err != nil || len(errs) > 0 {
			t.Fatalf("clear fixture zone %s: %v %v", id, err, errs)
		}
		if err := s.DeleteZone(ctx, id.String()); err != nil {
			t.Fatalf("delete fixture zone %s: %v", id, err)
		}
	}
}

func TestConcurrentZonePrefixSetsCommitOneWholeKnownLastSet(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	seedStore := shipping.NewStore(pool)
	p := unusedZonePrefixes(t, 4)
	zoneID := createZoneWithPrefixes(t, ctx, seedStore, "併發分區", p[:1])

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin prefix blocker: %v", err)
	}
	released := false
	defer func() {
		if !released {
			_ = blocker.Rollback(context.WithoutCancel(t.Context()))
		}
	}()
	var blockerPID int32
	if err := blocker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatalf("read blocker pid: %v", err)
	}
	var lockedPrefix string
	if err := blocker.QueryRow(ctx,
		`SELECT prefix FROM shipping_zone_prefixes WHERE prefix = $1 FOR UPDATE`, p[0]).
		Scan(&lockedPrefix); err != nil {
		t.Fatalf("lock existing prefix: %v", err)
	}

	appA := "zone_set_a_" + uuid.NewString()[:8]
	appB := "zone_set_b_" + uuid.NewString()[:8]
	poolA := admintest.NamedPool(t, pool, appA)
	poolB := admintest.NamedPool(t, pool, appB)
	storeA := shipping.NewStore(poolA)
	storeB := shipping.NewStore(poolB)
	requestA, requestB := "zone-set-a-"+uuid.NewString(), "zone-set-b-"+uuid.NewString()
	writersCtx, cancelWriters := context.WithTimeout(ctx, 10*time.Second)
	defer cancelWriters()
	ctxA := web.WithRequestID(writersCtx, requestA)
	ctxB := web.WithRequestID(writersCtx, requestB)

	type result struct {
		name string
		errs map[string]string
		err  error
	}
	done := make(chan result, 2)
	go func() {
		errs, setErr := storeA.SetZonePrefixes(ctxA, zoneID.String(), strings.Join(p[:2], " "))
		done <- result{name: "A", errs: errs, err: setErr}
	}()

	traceCtx, cancel := context.WithTimeout(writersCtx, 5*time.Second)
	defer cancel()
	writerAPID := admintest.WaitForBlockedApplication(t, pool, traceCtx, appA, blockerPID)
	select {
	case got := <-done:
		t.Fatalf("writer %s returned before its prefix blocker was released: %v %v", got.name, got.err, got.errs)
	default:
	}

	go func() {
		errs, setErr := storeB.SetZonePrefixes(ctxB, zoneID.String(), strings.Join(p[2:], " "))
		done <- result{name: "B", errs: errs, err: setErr}
	}()
	admintest.WaitForBlockedApplication(t, pool, traceCtx, appB, writerAPID)
	select {
	case got := <-done:
		t.Fatalf("writer %s returned while writer A held the zone lock: %v %v", got.name, got.err, got.errs)
	default:
	}

	if err := blocker.Rollback(ctx); err != nil {
		t.Fatalf("release prefix blocker: %v", err)
	}
	released = true
	for range 2 {
		select {
		case got := <-done:
			if got.err != nil || len(got.errs) > 0 {
				t.Errorf("writer %s: %v %v", got.name, got.err, got.errs)
			}
		case <-writersCtx.Done():
			t.Fatalf("concurrent prefix writers did not finish: %v", writersCtx.Err())
		}
	}
	assertZonePrefixes(t, zoneID, p[2:])

	var lastRequest string
	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT request_id, (after->>'prefixes')::int FROM audit_events
		WHERE action = 'shipping.zone.prefixes' AND entity_id = $1
		ORDER BY occurred_at DESC, id DESC LIMIT 1`, zoneID).
		Scan(&lastRequest, &auditCount); err != nil {
		t.Fatalf("read last concurrent audit row: %v", err)
	}
	if lastRequest != requestB || auditCount != len(p[2:]) {
		t.Errorf("last audit = request %q/count %d, want %q/%d",
			lastRequest, auditCount, requestB, len(p[2:]))
	}

	if errs, err := seedStore.SetZonePrefixes(ctx, zoneID.String(), ""); err != nil || len(errs) > 0 {
		t.Fatalf("clear concurrent fixture: %v %v", err, errs)
	}
	if err := seedStore.DeleteZone(ctx, zoneID.String()); err != nil {
		t.Fatalf("delete concurrent fixture: %v", err)
	}
}

func createZoneWithPrefixes(
	t *testing.T, ctx context.Context, s *shipping.Store, name string, prefixes []string,
) uuid.UUID {
	t.Helper()
	code := "zone_" + uuid.NewString()[:8]
	if errs, err := s.CreateZone(ctx, &shipping.NewZone{
		Code: code, Name: name, Prefixes: strings.Join(prefixes, " "),
	}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateZone(%s): %v %v", code, err, errs)
	}
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM shipping_zones WHERE code = $1`, code).Scan(&id); err != nil {
		t.Fatalf("read zone %s: %v", code, err)
	}
	return id
}

func TestAZonePrefixMustBeThreeDigits(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := shipping.NewStore(pool)

	for _, list := range []string{"88", "8801", "abc", "880 xx"} {
		errs, err := s.CreateZone(ctx, &shipping.NewZone{
			Code: "bad" + uuid.NewString()[:6], Name: "壞的", Prefixes: list,
		})
		if err != nil {
			t.Fatalf("CreateZone(%q): %v", list, err)
		}
		if errs["prefixes"] == "" {
			t.Errorf("CreateZone accepted the prefix list %q: %v", list, errs)
		}
	}

	code := "empty" + uuid.NewString()[:6]
	if errs, err := s.CreateZone(ctx, &shipping.NewZone{
		Code: code, Name: "空的", Prefixes: "999",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateZone: %v %v", err, errs)
	}
	var zoneID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM shipping_zones WHERE code = $1`, code).Scan(&zoneID); err != nil {
		t.Fatalf("read the zone: %v", err)
	}
	if err := s.DeleteZone(ctx, zoneID); !errors.Is(err, shipping.ErrInUse) {
		t.Errorf("deleting a zone with prefixes gave %v, want ErrInUse", err)
	}
	if errs, err := s.SetZonePrefixes(ctx, zoneID, ""); err != nil || len(errs) > 0 {
		t.Fatalf("clear prefixes through SetZonePrefixes: %v %v", err, errs)
	}
	if err := s.DeleteZone(ctx, zoneID); err != nil {
		t.Errorf("deleting an empty zone gave %v", err)
	}
}

func TestTheShippingPageSaysWhenCheckoutHidesPickup(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := shipping.NewStore(pool)
	log := slog.New(slog.DiscardHandler)
	enabled, err := cart.NewStoreMap("2000132", string(cart.ModeC2C), "", "https://shop.example")
	if err != nil {
		t.Fatalf("NewStoreMap: %v", err)
	}
	note := i18n.T(ctx, i18n.KeyAdminShipPickupOff)

	// A pickup-point method of its own, so the test does not depend on the seed.
	create := url.Values{
		"code": {"pk_" + uuid.NewString()[:8]}, "destination": {"pickup_point"},
		"max_parcel_longest": {"450"}, "max_parcel_sum": {"1050"}, "max_parcel_weight": {"10000"},
		"name": {"超商取貨測試"}, "carrier": {"測試承運人"}, "fee": {"60"}, "free_over": {"3000"},
	}
	creating := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/shipping/method", strings.NewReader(create.Encode()))
	creating.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	created := httptest.NewRecorder()
	handlerOver(s).CreateMethod(created, creating)
	if created.Code != http.StatusSeeOther {
		t.Fatalf("create pickup method = %d: %s", created.Code, created.Body.String())
	}

	for name, storeMap := range map[string]*cart.StoreMap{"no map": nil, "a map": enabled} {
		h := shipping.NewHandler(s, storeMap.Enabled(), log)
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/shipping", http.NoBody)
		w := httptest.NewRecorder()
		admintest.BackOffice.RequireStaff(h.Page)(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: GET /admin/shipping = %d", name, w.Code)
		}
		if got, want := strings.Contains(w.Body.String(), note), storeMap == nil; got != want {
			t.Errorf("%s: pickup-off note present = %t, want %t", name, got, want)
		}
	}
}

func surchargeRows(t *testing.T, versionID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM shipping_version_zones WHERE version_id = $1`,
		versionID).Scan(&n); err != nil {
		t.Fatalf("count surcharges: %v", err)
	}
	return n
}

func surchargeOfCurrentVersion(t *testing.T, code string) int64 {
	t.Helper()
	var cents int64
	if err := pool.QueryRow(t.Context(), `
		SELECT coalesce((SELECT vz.surcharge_cents FROM shipping_version_zones vz
		                 WHERE vz.version_id = v.id LIMIT 1), 0)
		FROM shipping_method_versions v
		JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.code = $1 AND v.effective_at <= now()
		ORDER BY v.effective_at DESC, v.id DESC LIMIT 1`, code).Scan(&cents); err != nil {
		t.Fatalf("read surcharge: %v", err)
	}
	return cents
}

func assertZonePrefixes(t *testing.T, zoneID uuid.UUID, want []string) {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT prefix FROM shipping_zone_prefixes WHERE zone_id = $1 ORDER BY prefix`, zoneID)
	if err != nil {
		t.Fatalf("read zone %s prefixes: %v", zoneID, err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect zone %s prefixes: %v", zoneID, err)
	}
	if got == nil {
		got = []string{}
	}
	want = slices.Clone(want)
	slices.Sort(want)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("zone %s prefixes (-want +got):\n%s", zoneID, diff)
	}
}

func unusedZonePrefixes(t *testing.T, count int) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT lpad(n::text, 3, '0')
		FROM generate_series(0, 999) AS n
		WHERE NOT EXISTS (
			SELECT 1 FROM shipping_zone_prefixes p WHERE p.prefix = lpad(n::text, 3, '0')
		)
		ORDER BY n LIMIT $1`, count)
	if err != nil {
		t.Fatalf("find unused zone prefixes: %v", err)
	}
	prefixes, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect unused zone prefixes: %v", err)
	}
	if len(prefixes) != count {
		t.Fatalf("found %d unused zone prefixes, want %d", len(prefixes), count)
	}
	return prefixes
}

func assertZonePrefixAudit(
	t *testing.T, actor, zoneID uuid.UUID, wantCount, wantRows int,
) {
	t.Helper()
	ctx := t.Context()
	var auditCount, actualCount, auditRows int
	if err := pool.QueryRow(ctx, `
		SELECT (after->>'prefixes')::int,
		       (SELECT count(*)::int FROM shipping_zone_prefixes WHERE zone_id = $1)
		FROM audit_events
		WHERE action = 'shipping.zone.prefixes' AND entity_id = $1 AND actor_user_id = $2
		ORDER BY occurred_at DESC, id DESC LIMIT 1`, zoneID, actor).
		Scan(&auditCount, &actualCount); err != nil {
		t.Fatalf("read zone-prefix audit: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*)::int FROM audit_events
		WHERE action = 'shipping.zone.prefixes' AND entity_id = $1 AND actor_user_id = $2`,
		zoneID, actor).Scan(&auditRows); err != nil {
		t.Fatalf("count zone-prefix audits: %v", err)
	}
	if auditCount != wantCount || actualCount != wantCount || auditRows != wantRows {
		t.Errorf("zone-prefix audit/result/rows = %d/%d/%d, want %d/%d/%d",
			auditCount, actualCount, auditRows, wantCount, wantCount, wantRows)
	}
}

func TestStaleSurchargeFormsRefuseSetAndClear(t *testing.T) {
	for _, locale := range i18n.Locales() {
		for _, amount := range []string{"200", ""} {
			t.Run(locale.Tag()+"/amount="+amount, func(t *testing.T) {
				ctx, actor := admintest.StaffContext(t, pool)
				ctx = i18n.WithLocale(ctx, locale)
				method, oldVersion, zone := surchargeFixture(t)
				s := shipping.NewStore(admintest.AdminRolePool(t, pool))
				mux := http.NewServeMux()
				handlerOver(s).Routes(mux, admintest.BackOffice)
				page := httptest.NewRecorder()
				mux.ServeHTTP(page, httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/shipping", nil))
				if page.Code != http.StatusOK {
					t.Fatalf("capture surcharge form status=%d", page.Code)
				}
				form := renderedSurchargeForms(t, page.Body.String(), zone.String())[oldVersion.String()]
				if got := form.Get("amount"); got != "100" {
					t.Fatalf("captured amount=%q, want 100", got)
				}
				if err := s.PublishShippingVersion(ctx, shipping.ShippingVersion{MethodID: method.String(), Name: "Updated shipping", FeeDollars: 90}); err != nil {
					t.Fatal(err)
				}
				current := currentShippingVersion(t, method)
				if current == oldVersion {
					t.Fatal("publishing did not replace the version")
				}
				form.Set("amount", amount)
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/shipping/surcharge", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				res := httptest.NewRecorder()
				mux.ServeHTTP(res, req)
				if res.Code != http.StatusUnprocessableEntity {
					t.Errorf("stale surcharge status=%d location=%q, want 422", res.Code, res.Header().Get("Location"))
				} else {
					assertStaleSurchargeForm(t, ctx, res.Body.String(), method, current, zone, amount)
				}
				assertSurchargeVersions(t, oldVersion, current, zone, 10000, 10000)
				assertSurchargeAudit(t, actor, 0)
				form.Set("version", current.String())
				form.Set("amount", "300")
				req = httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/shipping/surcharge", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				res = httptest.NewRecorder()
				mux.ServeHTTP(res, req)
				if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/shipping?ok=1" {
					t.Errorf("fresh surcharge status/location=%d/%q, want 303 saved", res.Code, res.Header().Get("Location"))
				}
				assertSurchargeVersions(t, oldVersion, current, zone, 10000, 30000)
				assertSurchargeAudit(t, actor, 1)
			})
		}
	}
}

func TestPublicationAndSurchargeEditsSerialize(t *testing.T) {
	for _, publishFirst := range []bool{true, false} {
		name := "edit_first"
		if publishFirst {
			name = "publish_first"
		}
		t.Run(name, func(t *testing.T) {
			ctx, _ := admintest.StaffContext(t, pool)
			method, oldVersion, zone := surchargeFixture(t)
			adminPool := admintest.AdminRolePool(t, pool)
			publishApp, editApp := "publish-"+method.String(), "surcharge-"+method.String()
			publisher := shipping.NewStore(surchargeWriterPool(t, adminPool, publishApp))
			editor := shipping.NewStore(surchargeWriterPool(t, adminPool, editApp))
			blocker, pid := holdShippingMethod(t, method)
			workerCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			var wg sync.WaitGroup
			defer func() { cancel(); _ = blocker.Rollback(context.WithoutCancel(ctx)); wg.Wait() }()
			type result struct {
				publish bool
				err     error
			}
			done := make(chan result, 2) // One result from each of the two writers.
			publish := func() {
				done <- result{publish: true, err: publisher.PublishShippingVersion(workerCtx, shipping.ShippingVersion{MethodID: method.String(), Name: "Published", FeeDollars: 90})}
			}
			edit := func() {
				done <- result{err: editor.SetZoneSurcharge(workerCtx, oldVersion.String(), zone.String(), 200)}
			}
			first, second, firstApp, secondApp := publish, edit, publishApp, editApp
			if !publishFirst {
				first, second, firstApp, secondApp = edit, publish, editApp, publishApp
			}
			traceCtx, stopTrace := context.WithTimeout(ctx, 5*time.Second)
			defer stopTrace()
			wg.Go(first)
			firstPID := admintest.WaitForBlockedApplication(t, pool, traceCtx, firstApp, pid)
			wg.Go(second)
			admintest.WaitForBlockedApplication(t, pool, traceCtx, secondApp, firstPID)
			if err := blocker.Rollback(context.WithoutCancel(ctx)); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				select {
				case r := <-done:
					if publishFirst && !r.publish {
						changed, ok := errors.AsType[*shipping.VersionChangedError](r.err)
						if !ok || changed.MethodID != method {
							t.Errorf("edit after publication error=%v, want stale method %s", r.err, method)
						}
					} else if r.err != nil {
						t.Errorf("writer publish=%t: %v", r.publish, r.err)
					}
				case <-workerCtx.Done():
					t.Fatal(workerCtx.Err())
				}
			}
			current := currentShippingVersion(t, method)
			if current == oldVersion {
				t.Fatal("publisher did not replace the version")
			}
			oldCents, newCents := int64(20000), int64(20000)
			if publishFirst {
				oldCents, newCents = 10000, 10000
			}
			assertSurchargeVersions(t, oldVersion, current, zone, oldCents, newCents)
		})
	}
}

func TestWaitingPublicationBecomesCurrentAndCarriesTheLatestSurcharge(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	method, oldVersion, zone := surchargeFixture(t)
	app := "waiting-publish-" + method.String()
	s := shipping.NewStore(surchargeWriterPool(t, admintest.AdminRolePool(t, pool), app))
	blocker, pid := holdShippingMethod(t, method)
	workerCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	var wg sync.WaitGroup
	defer func() { cancel(); _ = blocker.Rollback(context.WithoutCancel(ctx)); wg.Wait() }()
	done := make(chan error, 1) // The single waiting publisher's result.
	wg.Go(func() {
		done <- s.PublishShippingVersion(workerCtx, shipping.ShippingVersion{MethodID: method.String(), Name: "Final shipping", FeeDollars: 90})
	})
	traceCtx, stopTrace := context.WithTimeout(ctx, 5*time.Second)
	defer stopTrace()
	admintest.WaitForBlockedApplication(t, pool, traceCtx, app, pid)
	// The lock holder publishes after the waiting transaction has already started.
	// Keep the intervening publication's time independent of the query under test.
	var intervening uuid.UUID
	err := blocker.QueryRow(ctx, `INSERT INTO shipping_method_versions
 (method_id,name,fee_cents,effective_at) VALUES ($1,'Intervening shipping',8500,statement_timestamp()) RETURNING id`, method).Scan(&intervening)
	if err != nil {
		t.Fatal(err)
	}
	q := db.New(blocker)
	if err := q.SetZoneSurcharge(ctx, db.SetZoneSurchargeParams{VersionID: intervening, ZoneID: zone, SurchargeCents: 25000}); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-workerCtx.Done():
		t.Fatal(workerCtx.Err())
	}
	current := currentShippingVersion(t, method)
	var published uuid.UUID
	var fee int64
	if err := pool.QueryRow(ctx, `SELECT id,fee_cents FROM shipping_method_versions WHERE method_id=$1 AND name='Final shipping'`, method).Scan(&published, &fee); err != nil {
		t.Fatal(err)
	}
	if current != published || current == oldVersion || fee != 9000 {
		t.Errorf("current/published version and fee=%s/%s/%d, want waiting publisher with 9000", current, published, fee)
	}
	assertSurchargeVersions(t, intervening, published, zone, 25000, 25000)
}

func surchargeFixture(t *testing.T) (method, version, zone uuid.UUID) {
	t.Helper()
	if err := pool.QueryRow(t.Context(), `
 WITH method AS (
  INSERT INTO shipping_methods(code,destination_kind,is_active)
  VALUES ('stale_'||replace(gen_random_uuid()::text, '-', ''),'address',false) RETURNING id
 ), version AS (
  INSERT INTO shipping_method_versions(method_id,name,fee_cents,effective_at)
  SELECT id,'Initial shipping',8000,now()-interval '1 day' FROM method RETURNING id,method_id
 ), zone AS (
  INSERT INTO shipping_zones(code,name) VALUES ('stale_'||replace(gen_random_uuid()::text, '-', ''),'Test zone') RETURNING id
 ), surcharge AS (
  INSERT INTO shipping_version_zones(version_id,zone_id,surcharge_cents)
  SELECT version.id,zone.id,10000 FROM version,zone
 ) SELECT version.method_id,version.id,zone.id FROM version,zone`).Scan(&method, &version, &zone); err != nil {
		t.Fatal(err)
	}
	return method, version, zone
}

func currentShippingVersion(t *testing.T, method uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT id FROM shipping_method_versions WHERE method_id=$1 AND effective_at<=statement_timestamp() ORDER BY effective_at DESC,id DESC LIMIT 1`, method).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func assertSurchargeVersions(t *testing.T, old, current, zone uuid.UUID, oldCents, newCents int64) {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT version_id,surcharge_cents FROM shipping_version_zones WHERE version_id=ANY($1::uuid[]) AND zone_id=$2`, []uuid.UUID{old, current}, zone)
	if err != nil {
		t.Fatal(err)
	}
	got := map[uuid.UUID]int64{}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var cents int64
		if err := rows.Scan(&id, &cents); err != nil {
			t.Fatal(err)
		}
		got[id] = cents
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(map[uuid.UUID]int64{old: oldCents, current: newCents}, got); diff != "" {
		t.Errorf("version surcharges (-want +got):\n%s", diff)
	}
}

func assertSurchargeAudit(t *testing.T, actor uuid.UUID, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action='shipping.surcharge' AND actor_user_id=$1`, actor).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("surcharge audits=%d, want %d", got, want)
	}
}

func holdShippingMethod(t *testing.T, method uuid.UUID) (tx pgx.Tx, pid int32) {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) })
	if err := tx.QueryRow(t.Context(), `SELECT pg_backend_pid() FROM shipping_methods WHERE id=$1 FOR NO KEY UPDATE`, method).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	return tx, pid
}

func surchargeWriterPool(t *testing.T, p *pgxpool.Pool, app string) *pgxpool.Pool {
	t.Helper()
	cfg := p.Config().Copy() // Keep the real admin role's AfterConnect hook.
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["application_name"] = app
	writer, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Close)
	var role string
	if err := writer.QueryRow(t.Context(), `SELECT current_user`).Scan(&role); err != nil || role != "admin" {
		t.Fatalf("writer role=%q, want admin: %v", role, err)
	}
	return writer
}

func assertStaleSurchargeForm(t *testing.T, ctx context.Context, body string, method, current, zone uuid.UUID, amount string) {
	t.Helper()
	admintest.AssertRefusedInput(t, body, "sur-"+current.String()+"-"+zone.String(), amount)
	if !strings.Contains(body, i18n.T(ctx, i18n.KeyAdminShipVersionChanged)) {
		t.Error("stale refusal explanation is absent")
	}
	fresh := renderedSurchargeForms(t, body, zone.String())[current.String()]
	if diff := cmp.Diff(url.Values{"version": {current.String()}, "zone": {zone.String()}, "amount": {amount}}, fresh); diff != "" {
		t.Errorf("current resubmission form (-want +got):\n%s", diff)
	}
	feeInput := admintest.InputElementByID(t, body, "fee-"+method.String())
	if got := admintest.InputAttribute(t, feeInput, "value"); got != "90" {
		t.Errorf("current fee=%q, want 90", got)
	}
}

func TestSurchargeChecksTheCurrentVersionAfterItsLockWait(t *testing.T) {
	ctx, actor := admintest.StaffContext(t, pool)
	method, oldVersion, zone := surchargeFixture(t)
	app := "waiting-surcharge-" + method.String()
	s := shipping.NewStore(surchargeWriterPool(t, admintest.AdminRolePool(t, pool), app))
	blocker, pid := holdShippingMethod(t, method)
	workerCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	var wg sync.WaitGroup
	defer func() { cancel(); _ = blocker.Rollback(context.WithoutCancel(ctx)); wg.Wait() }()
	done := make(chan error, 1) // The single waiting editor's result.
	wg.Go(func() { done <- s.SetZoneSurcharge(workerCtx, oldVersion.String(), zone.String(), 200) })
	traceCtx, stopTrace := context.WithTimeout(ctx, 5*time.Second)
	defer stopTrace()
	admintest.WaitForBlockedApplication(t, pool, traceCtx, app, pid)
	// A transaction-time cutoff would exclude this newly committed fixture.
	var current uuid.UUID
	if err := blocker.QueryRow(ctx, `INSERT INTO shipping_method_versions
 (method_id,name,fee_cents,effective_at) VALUES ($1,'Published during wait',9000,statement_timestamp()) RETURNING id`, method).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if err := db.New(blocker).SetZoneSurcharge(ctx, db.SetZoneSurchargeParams{VersionID: current, ZoneID: zone, SurchargeCents: 10000}); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		changed, ok := errors.AsType[*shipping.VersionChangedError](err)
		if !ok || changed.MethodID != method {
			t.Errorf("edit after lock wait error=%v, want stale method %s", err, method)
		}
	case <-workerCtx.Done():
		t.Fatal(workerCtx.Err())
	}
	assertSurchargeVersions(t, oldVersion, current, zone, 10000, 10000)
	assertSurchargeAudit(t, actor, 0)
}

func TestZoneRemovalRefusalsKeepTheirCauseAndRows(t *testing.T) {
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			owner := admintest.Pool(t)
			ctx, _ := admintest.StaffContext(t, owner)
			ctx = i18n.WithLocale(ctx, locale)
			store := shipping.NewStore(admintest.AdminRolePool(t, owner))
			mux := http.NewServeMux()
			handlerOver(store).Routes(mux, admintest.BackOffice)
			for _, tt := range []struct {
				name       string
				prefixes   bool
				versions   bool
				historical bool
				use        shipping.ZoneUse
				location   string
				message    i18n.Key
			}{
				{name: "prefixes", prefixes: true, use: shipping.ZoneUsedByPrefixes, location: "/admin/shipping?zoneprefixes=1", message: i18n.KeyAdminShipZoneHasPrefixes},
				{name: "current version", versions: true, use: shipping.ZoneUsedByVersions, location: "/admin/shipping?zoneversions=1", message: i18n.KeyAdminShipZoneHasVersions},
				{name: "historical version", versions: true, historical: true, use: shipping.ZoneUsedByVersions, location: "/admin/shipping?zoneversions=1", message: i18n.KeyAdminShipZoneHasVersions},
				{name: "both blockers", prefixes: true, versions: true, use: shipping.ZoneUsedByPrefixes, location: "/admin/shipping?zoneprefixes=1", message: i18n.KeyAdminShipZoneHasPrefixes},
			} {
				t.Run(tt.name, func(t *testing.T) {
					zone, version := zoneRemovalFixture(t, ctx, owner, store, tt.prefixes, tt.versions, tt.historical)
					assertZoneRemovalResponse(t, ctx, mux, zone, tt.location, tt.message)
					err := store.DeleteZone(ctx, zone.String())
					inUse, ok := errors.AsType[*shipping.ZoneInUseError](err)
					if !ok || inUse.Use != tt.use || !errors.Is(err, shipping.ErrInUse) {
						t.Fatalf("DeleteZone refusal=%v, want %s with ErrInUse compatibility", err, tt.use)
					}
					want := zoneRemovalState{Zones: 1}
					if tt.prefixes {
						want.Prefixes = 1
					}
					if tt.versions {
						want.VersionReferences = 1
					}
					assertZoneRemovalState(t, ctx, owner, zone, want)
					if tt.historical {
						var currentReferences int
						if err := owner.QueryRow(ctx, `SELECT count(*) FROM shipping_version_zones WHERE version_id=$1 AND zone_id=$2`, version, zone).Scan(&currentReferences); err != nil {
							t.Fatalf("read current surcharge: %v", err)
						}
						if currentReferences != 0 {
							t.Fatalf("current surcharge references=%d, want 0 while history prevents deletion", currentReferences)
						}
						return
					}
					if tt.prefixes {
						if errs, err := store.SetZonePrefixes(ctx, zone.String(), ""); err != nil || len(errs) != 0 {
							t.Fatalf("clear prefixes: %v %v", err, errs)
						}
					}
					if tt.versions {
						if tt.prefixes {
							assertZoneRemovalResponse(t, ctx, mux, zone, "/admin/shipping?zoneversions=1", i18n.KeyAdminShipZoneHasVersions)
							assertZoneRemovalState(t, ctx, owner, zone, zoneRemovalState{Zones: 1, VersionReferences: 1})
						}
						if err := store.SetZoneSurcharge(ctx, version.String(), zone.String(), 0); err != nil {
							t.Fatalf("clear current surcharge: %v", err)
						}
					}
					assertZoneRemovalResponse(t, ctx, mux, zone, "/admin/shipping?ok=1", i18n.KeyAdminNoticeOK)
					assertZoneRemovalState(t, ctx, owner, zone, zoneRemovalState{DeleteAudits: 1})
				})
			}
		})
	}
}

func TestZoneRemovalMissingAndUnusedZones(t *testing.T) {
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			owner := admintest.Pool(t)
			ctx, _ := admintest.StaffContext(t, owner)
			ctx = i18n.WithLocale(ctx, locale)
			store := shipping.NewStore(admintest.AdminRolePool(t, owner))
			mux := http.NewServeMux()
			handlerOver(store).Routes(mux, admintest.BackOffice)
			for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
				if err := store.DeleteZone(ctx, id); !errors.Is(err, shipping.ErrNotFound) {
					t.Fatalf("DeleteZone(%q)=%v, want ErrNotFound", id, err)
				}
				res := submitZoneRemoval(ctx, mux, id)
				if res.Code != http.StatusNotFound || res.Header().Get("Location") != "" {
					t.Errorf("missing zone status/location=%d/%q, want 404 without redirect", res.Code, res.Header().Get("Location"))
				}
			}
			zone, _ := zoneRemovalFixture(t, ctx, owner, store, false, false, false)
			assertZoneRemovalResponse(t, ctx, mux, zone, "/admin/shipping?ok=1", i18n.KeyAdminNoticeOK)
			assertZoneRemovalState(t, ctx, owner, zone, zoneRemovalState{DeleteAudits: 1})
			res := httptest.NewRecorder()
			mux.ServeHTTP(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/shipping?inuse=1", http.NoBody))
			if res.Code != http.StatusOK || strings.Contains(res.Body.String(), i18n.T(ctx, i18n.KeyAdminNoticeInUse)) {
				t.Errorf("legacy inuse notice status=%d, want 200 without the taxonomy sentence", res.Code)
			}
		})
	}
}

func zoneRemovalFixture(
	t *testing.T, ctx context.Context, owner *pgxpool.Pool, store *shipping.Store, prefixes, versions, historical bool,
) (zone, version uuid.UUID) {
	t.Helper()
	var method uuid.UUID
	if err := owner.QueryRow(ctx, `INSERT INTO shipping_zones (code,name) VALUES ($1,'Removal fixture') RETURNING id`, "removal_"+uuid.NewString()[:8]).Scan(&zone); err != nil {
		t.Fatalf("create removal zone: %v", err)
	}
	if prefixes {
		var prefix string
		if err := owner.QueryRow(ctx, `SELECT lpad(n::text,3,'0') FROM generate_series(0,999) n WHERE NOT EXISTS (SELECT 1 FROM shipping_zone_prefixes p WHERE p.prefix=lpad(n::text,3,'0')) ORDER BY n LIMIT 1`).Scan(&prefix); err != nil {
			t.Fatalf("find free prefix: %v", err)
		}
		if _, err := owner.Exec(ctx, `INSERT INTO shipping_zone_prefixes (prefix,zone_id) VALUES ($1,$2)`, prefix, zone); err != nil {
			t.Fatalf("create blocking prefix: %v", err)
		}
	}
	if !versions {
		return zone, version
	}
	if err := owner.QueryRow(ctx, `INSERT INTO shipping_methods (code,destination_kind,is_active) VALUES ($1,'address',false) RETURNING id`, "removal_"+uuid.NewString()[:8]).Scan(&method); err != nil {
		t.Fatalf("create removal method: %v", err)
	}
	if err := owner.QueryRow(ctx, `INSERT INTO shipping_method_versions (method_id,name,fee_cents,effective_at) VALUES ($1,'Removal shipping',8000,now()-interval '1 day') RETURNING id`, method).Scan(&version); err != nil {
		t.Fatalf("create removal version: %v", err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO shipping_version_zones (version_id,zone_id,surcharge_cents) VALUES ($1,$2,10000)`, version, zone); err != nil {
		t.Fatalf("create blocking version: %v", err)
	}
	if historical {
		if err := store.PublishShippingVersion(ctx, shipping.ShippingVersion{MethodID: method.String(), Name: "New removal shipping", FeeDollars: 90}); err != nil {
			t.Fatalf("publish newer version: %v", err)
		}
		if err := owner.QueryRow(ctx, `SELECT id FROM shipping_method_versions WHERE method_id=$1 AND effective_at <= statement_timestamp() ORDER BY effective_at DESC,id DESC LIMIT 1`, method).Scan(&version); err != nil {
			t.Fatalf("read new removal version: %v", err)
		}
		if err := store.SetZoneSurcharge(ctx, version.String(), zone.String(), 0); err != nil {
			t.Fatalf("clear new version surcharge: %v", err)
		}
	}
	return zone, version
}

func submitZoneRemoval(ctx context.Context, mux *http.ServeMux, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/shipping/zone/"+id+"/delete", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	return res
}

func assertZoneRemovalResponse(
	t *testing.T, ctx context.Context, mux *http.ServeMux, zone uuid.UUID, location string, message i18n.Key,
) {
	t.Helper()
	res := submitZoneRemoval(ctx, mux, zone.String())
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != location {
		t.Fatalf("zone removal status/location=%d/%q, want 303/%q", res.Code, res.Header().Get("Location"), location)
	}
	page := httptest.NewRecorder()
	mux.ServeHTTP(page, httptest.NewRequestWithContext(ctx, http.MethodGet, location, http.NoBody))
	if page.Code != http.StatusOK || strings.Count(page.Body.String(), i18n.T(ctx, message)) != 1 {
		t.Errorf("zone removal landing status=%d, want 200 and one %q", page.Code, i18n.T(ctx, message))
	}
	if strings.Contains(page.Body.String(), i18n.T(ctx, i18n.KeyAdminNoticeInUse)) {
		t.Error("shipping page renders the taxonomy in-use refusal")
	}
}

type zoneRemovalState struct {
	Zones             int
	Prefixes          int
	VersionReferences int
	DeleteAudits      int
}

func assertZoneRemovalState(t *testing.T, ctx context.Context, owner *pgxpool.Pool, zone uuid.UUID, want zoneRemovalState) {
	t.Helper()
	var got zoneRemovalState
	if err := owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM shipping_zones WHERE id=$1),
		(SELECT count(*) FROM shipping_zone_prefixes WHERE zone_id=$1),
		(SELECT count(*) FROM shipping_version_zones WHERE zone_id=$1),
		(SELECT count(*) FROM audit_events WHERE entity_id=$1 AND action='shipping.zone.delete')`, zone).
		Scan(&got.Zones, &got.Prefixes, &got.VersionReferences, &got.DeleteAudits); err != nil {
		t.Fatalf("read zone removal state: %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("zone removal state (-want +got):\n%s", diff)
	}
}

func TestZoneRemovalInfrastructureFailuresStayServerErrors(t *testing.T) {
	for _, locale := range i18n.Locales() {
		for _, column := range []string{"id", "name"} {
			t.Run(locale.Tag()+"/"+column, func(t *testing.T) {
				owner := admintest.Pool(t)
				ctx, _ := admintest.StaffContext(t, owner)
				ctx = i18n.WithLocale(ctx, locale)
				store := shipping.NewStore(admintest.AdminRolePool(t, owner))
				zone, _ := zoneRemovalFixture(t, ctx, owner, store, true, false, false)
				if _, err := owner.Exec(ctx, "ALTER TABLE shipping_zones RENAME COLUMN "+column+" TO broken_removal_column"); err != nil {
					t.Fatalf("break zone %s query: %v", column, err)
				}
				restore := "ALTER TABLE shipping_zones RENAME COLUMN broken_removal_column TO " + column
				t.Cleanup(func() {
					if _, err := owner.Exec(context.WithoutCancel(ctx), restore); err != nil {
						t.Errorf("restore zone column: %v", err)
					}
				})
				err := store.DeleteZone(ctx, zone.String())
				if err == nil || errors.Is(err, shipping.ErrInUse) || errors.Is(err, shipping.ErrRefused) || errors.Is(err, shipping.ErrNotFound) {
					t.Fatalf("DeleteZone query fault=%v, want infrastructure error", err)
				}
				mux := http.NewServeMux()
				handlerOver(store).Routes(mux, admintest.BackOffice)
				res := submitZoneRemoval(ctx, mux, zone.String())
				if res.Code != http.StatusInternalServerError || res.Header().Get("Location") != "" {
					t.Errorf("zone query fault status/location=%d/%q, want 500 without redirect", res.Code, res.Header().Get("Location"))
				}
				var prefixes, refs, audits int
				if err := owner.QueryRow(ctx, `SELECT
					(SELECT count(*) FROM shipping_zone_prefixes WHERE zone_id=$1),
					(SELECT count(*) FROM shipping_version_zones WHERE zone_id=$1),
					(SELECT count(*) FROM audit_events WHERE entity_id=$1 AND action='shipping.zone.delete')`, zone).Scan(&prefixes, &refs, &audits); err != nil {
					t.Fatalf("read query-fault state: %v", err)
				}
				if diff := cmp.Diff([]int{1, 0, 0}, []int{prefixes, refs, audits}); diff != "" {
					t.Errorf("query-fault state (-want +got):\n%s", diff)
				}
			})
		}
	}
}
