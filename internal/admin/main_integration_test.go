//go:build integration

package admin_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/campaigns"
	"github.com/koopa0/goen/internal/admin/loyalty"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/admin/shipping"
	"github.com/koopa0/goen/internal/admin/staff"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/user"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p
	if err := admintest.LoadCatalogue(context.Background(), pool); err != nil {
		slog.Error("load seed", "error", err)
		os.Exit(1)
	}

	code := m.Run()
	stop()
	os.Exit(code)
}

func TestTheBackOfficeIsInvisibleToEveryoneButStaff(t *testing.T) {
	ctx := t.Context()

	var customerID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role) VALUES ('shopper-'||gen_random_uuid()||'@example.com', 'customer')
		RETURNING id`).Scan(&customerID); err != nil {
		t.Fatalf("create customer: %v", err)
	}

	guarded := admintest.BackOffice.RequireStaff(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("the back office"))
	})

	tests := []struct {
		name     string
		signedIn bool
		user     user.User
	}{
		{name: "signed out"},
		{name: "a signed-in customer", signedIn: true,
			user: user.User{ID: customerID.String(), Role: user.RoleCustomer}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin", nil)
			if tt.signedIn {
				req = req.WithContext(user.NewContext(req.Context(), tt.user))
			}
			w := httptest.NewRecorder()
			guarded(w, req)

			if w.Code != http.StatusNotFound {
				t.Errorf("status is %d, want 404 — anything else says /admin is a "+
					"real place", w.Code)
			}
			if loc := w.Header().Get("Location"); loc != "" {
				t.Errorf("redirected to %q; a redirect confirms the page exists and "+
					"puts its path in the visitor's history", loc)
			}
			if strings.Contains(w.Body.String(), "the back office") {
				t.Error("the handler ran")
			}
		})
	}

	// BOTH back-office roles, and 'staff' is the one that matters: a colleague
	// hired as staff must not meet a 404 on the whole back office.
	staffView, err := staff.NewStore(pool).Staff(ctx)
	if err != nil {
		t.Fatalf("read roles offered by /admin/staff: %v", err)
	}
	for _, offered := range staffView.Roles {
		role := string(offered)
		t.Run(role+" reaches the back office", func(t *testing.T) {
			var id uuid.UUID
			if err := pool.QueryRow(ctx, `
				INSERT INTO users (email, role) VALUES ('bo-'||gen_random_uuid()||'@example.com', $1)
				RETURNING id`, role).Scan(&id); err != nil {
				t.Fatalf("create %s: %v", role, err)
			}
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin", nil)
			req = req.WithContext(user.NewContext(req.Context(),
				user.User{ID: id.String(), Role: user.Role(role)}))
			w := httptest.NewRecorder()
			guarded(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("%s got %d, want 200 — /admin/staff offers this role, so a "+
					"colleague hired into it can do no work at all", role, w.Code)
			}
		})
	}
}

func TestEveryBackOfficeWriteLeavesATrail(t *testing.T) {
	ctx, actor := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)

	sku := anyVariantSKU(t)
	slug := admintest.AnyProductSlug(t, pool)

	tests := []struct {
		name   string
		action audit.Action
		run    func() error
	}{
		{"adjust stock", audit.ActionAdjustStock, func() error {
			return stock.NewStore(pool).Adjust(ctx, sku, 3, actor.String(), uuid.NewString())
		}},
		{"set arrival", audit.ActionSetVariantArrival, func() error {
			return stock.NewStore(pool).SetArrival(ctx, sku, pgtype.Date{})
		}},
		{"reprice", audit.ActionRepriceVariant, func() error {
			return stock.NewStore(pool).SetPrice(ctx, sku, 123400, 0)
		}},
		{"publish", audit.ActionPublishProduct, func() error {
			return s.SetStatus(ctx, slug, "draft")
		}},
		{"grant credit", audit.ActionGrantCredit, func() error {
			_, grantErr := loyalty.NewStore(pool).GrantCredit(ctx, actor, 500,
				"測試", uuid.New())
			return grantErr
		}},
		{"create campaign", audit.ActionCreateCampaign, func() error {
			_, err := campaigns.NewStore(pool).Create(ctx, &campaigns.Form{
				Slug: "trail-" + uuid.NewString()[:8], Title: "紀錄", Days: 7,
			})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := admintest.AuditRows(t, pool, tt.action)
			if err := tt.run(); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			if after := admintest.AuditRows(t, pool, tt.action); after != before+1 {
				t.Errorf("%s left %d audit rows, want one more than %d — the action "+
					"happened and nobody can say who did it", tt.name, after, before)
			}
		})
	}
}

func anyVariantSKU(t *testing.T) string {
	t.Helper()
	var sku string
	if err := pool.QueryRow(t.Context(),
		`SELECT v.sku FROM product_variants v
		   WHERE NOT EXISTS (SELECT 1 FROM sale_campaign_products cp WHERE cp.product_id = v.product_id)
		   ORDER BY v.sku LIMIT 1`).Scan(&sku); err != nil {
		t.Fatalf("find variant: %v", err)
	}
	return sku
}

func TestParseProtectedWritesDoNotCallInfrastructureARefusal(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	p := admintest.NamedPool(t, pool, "parse_closed_"+uuid.NewString()[:8])
	p.Close()
	s := products.NewStore(p)

	writes := []struct {
		name              string
		write             func() (map[string]string, error)
		refused, notFound error
	}{
		{name: "create product", refused: products.ErrRefused, notFound: products.ErrNotFound, write: func() (map[string]string, error) {
			_, errs, err := s.Create(ctx, &products.Form{
				Slug: "closed-product", Name: "Closed", BrandID: uuid.NewString(), CategoryID: uuid.NewString(),
			})
			return errs, err
		}},
		{name: "update product", refused: products.ErrRefused, notFound: products.ErrNotFound, write: func() (map[string]string, error) {
			return s.Update(ctx, &products.Form{
				Slug: "closed-product", Name: "Closed", BrandID: uuid.NewString(), CategoryID: uuid.NewString(),
			})
		}},
		{name: "add variant", refused: products.ErrRefused, notFound: products.ErrNotFound, write: func() (map[string]string, error) {
			return s.AddVariant(ctx, "closed-product", &products.VariantForm{SKU: "CLOSED", PriceCents: 100})
		}},
		{name: "create method", refused: shipping.ErrRefused, notFound: shipping.ErrNotFound, write: func() (map[string]string, error) {
			return shipping.NewStore(p).CreateMethod(ctx, &shipping.NewMethod{
				Code: "closed_method", Destination: "address", Name: "Closed",
			})
		}},
	}
	for _, tt := range writes {
		t.Run(tt.name, func(t *testing.T) {
			errs, err := tt.write()
			if err == nil || errors.Is(err, tt.refused) || errors.Is(err, tt.notFound) {
				t.Errorf("write error category = %v, want infrastructure only", err)
			}
			if len(errs) != 0 {
				t.Errorf("write field errors = %v, want none for infrastructure", errs)
			}
		})
	}
}

func TestAMistypedParcelDimensionDoesNotBecomeUnmeasured(t *testing.T) {
	ctx, actor := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	h := admintest.ProductDesk(pool, s)
	slug := admintest.DraftProduct(t, ctx, pool, s)
	defer func() {
		if err := s.SetStatus(context.WithoutCancel(ctx), slug, "archived"); err != nil {
			t.Errorf("archive parcel fixture: %v", err)
		}
	}()

	tests := []struct {
		name  string
		field string
		id    string
		raw   string
	}{
		{name: "safety stock", field: "safety", id: "v-safety", raw: "1000001"},
		{name: "longest side", field: "parcel_longest", id: "v-longest", raw: "5001"},
		{name: "three-side sum", field: "parcel_sum", id: "v-sum", raw: "15001"},
		{name: "weight typo", field: "parcel_weight", id: "v-weight", raw: "10,000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sku := "PARSE-" + strings.ToUpper(uuid.NewString()[:8])
			form := url.Values{
				"sku":            {sku},
				"price":          {"1000"},
				"compare":        {""},
				"safety":         {"0"},
				"parcel_longest": {"450"},
				"parcel_sum":     {"1050"},
				"parcel_weight":  {"500"},
			}
			form.Set(tt.field, tt.raw)
			res := admintest.PostVariantForm(t, h, ctx, slug, form)
			if res.Code != http.StatusUnprocessableEntity {
				t.Errorf("AddVariant(%s=%q) status = %d, want 422", tt.field, tt.raw, res.Code)
			} else {
				body := res.Body.String()
				admintest.AssertRefusedInput(t, body, tt.id, tt.raw)
				for id, field := range map[string]string{
					"v-sku": "sku", "v-price": "price", "v-compare": "compare",
					"v-safety": "safety", "v-longest": "parcel_longest",
					"v-sum": "parcel_sum", "v-weight": "parcel_weight",
				} {
					if got := admintest.InputAttribute(t, admintest.InputElementByID(t, body, id), "value"); got != form.Get(field) {
						t.Errorf("input %q value = %q, want submitted %q", id, got, form.Get(field))
					}
				}
				for _, id := range []string{"v-safety", "v-longest", "v-sum", "v-weight"} {
					admintest.AssertTextNumberControl(t, body, id)
				}
			}
			var count int
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM product_variants WHERE sku = $1`, sku).Scan(&count); err != nil {
				t.Fatalf("count refused variant: %v", err)
			}
			if count != 0 {
				t.Errorf("AddVariant(%s=%q) inserted %d rows", tt.field, tt.raw, count)
			}
		})
	}

	measuredSKU := "MEASURED-" + strings.ToUpper(uuid.NewString()[:8])
	measured := url.Values{
		"sku": {measuredSKU}, "price": {"1000"}, "safety": {"0"},
		"parcel_longest": {"450"}, "parcel_sum": {"1050"}, "parcel_weight": {"15000"},
	}
	if res := admintest.PostVariantForm(t, h, ctx, slug, measured); res.Code != http.StatusSeeOther {
		t.Fatalf("add measured variant status = %d, want 303", res.Code)
	}
	unmeasuredSKU := "UNMEASURED-" + strings.ToUpper(uuid.NewString()[:8])
	unmeasured := url.Values{
		"sku": {unmeasuredSKU}, "price": {"1000"}, "safety": {"0"},
		"parcel_longest": {""}, "parcel_sum": {""}, "parcel_weight": {""},
	}
	if res := admintest.PostVariantForm(t, h, ctx, slug, unmeasured); res.Code != http.StatusSeeOther {
		t.Fatalf("add unmeasured variant status = %d, want 303", res.Code)
	}

	variantIDs := make(map[string]uuid.UUID, 2)
	rows, err := pool.Query(ctx, `
		SELECT sku, id FROM product_variants WHERE sku = ANY($1::text[])
		ORDER BY sku`, []string{measuredSKU, unmeasuredSKU})
	if err != nil {
		t.Fatalf("read parcel variants: %v", err)
	}
	for rows.Next() {
		var sku string
		var id uuid.UUID
		if err := rows.Scan(&sku, &id); err != nil {
			t.Fatalf("scan parcel variant: %v", err)
		}
		variantIDs[sku] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate parcel variants: %v", err)
	}
	rows.Close()
	if len(variantIDs) != 2 {
		t.Fatalf("read %d parcel variants, want 2", len(variantIDs))
	}
	for _, sku := range []string{measuredSKU, unmeasuredSKU} {
		if err := stock.NewStore(pool).Receive(ctx, sku, 2, actor.String(), "parse-stock-"+uuid.NewString()); err != nil {
			t.Fatalf("stock %s: %v", sku, err)
		}
	}
	if err := s.SetStatus(ctx, slug, "active"); err != nil {
		t.Fatalf("publish parcel product: %v", err)
	}

	methodCode := "parse_pickup_" + uuid.NewString()[:8]
	if errs, err := shipping.NewStore(pool).CreateMethod(ctx, &shipping.NewMethod{
		Code: methodCode, Destination: "pickup_point", Name: "量測超取",
		Carrier: "測試承運人", MaxParcelWeightG: 10000,
	}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateMethod: %v %v", err, errs)
	}
	var methodID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_methods WHERE code = $1`, methodCode).Scan(&methodID); err != nil {
		t.Fatalf("read parcel method: %v", err)
	}
	defer func() {
		if err := shipping.NewStore(pool).SetMethodActive(context.WithoutCancel(ctx), methodID.String(), false); err != nil {
			t.Errorf("deactivate parcel method: %v", err)
		}
	}()

	basket := cart.NewStore(pool)
	for _, tt := range []struct {
		name    string
		sku     string
		offered bool
	}{
		{name: "measured oversized parcel", sku: measuredSKU, offered: false},
		{name: "unmeasured parcel", sku: unmeasuredSKU, offered: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cartID, err := basket.Create(ctx, uuid.NewString(), uuid.NullUUID{})
			if err != nil {
				t.Fatalf("create cart: %v", err)
			}
			if addErr := basket.Add(ctx, cartID, variantIDs[tt.sku], 1); addErr != nil {
				t.Fatalf("add %s to cart: %v", tt.sku, addErr)
			}
			choices, err := basket.ShippingChoices(ctx, cartID, 100000)
			if err != nil {
				t.Fatalf("ShippingChoices: %v", err)
			}
			codes := make([]string, 0, len(choices))
			for i := range choices {
				codes = append(codes, choices[i].Code)
			}
			if got := slices.Contains(codes, methodCode); got != tt.offered {
				t.Errorf("ShippingChoices(%s) offered %q = %v, want %v; choices=%v",
					tt.sku, methodCode, got, tt.offered, codes)
			}
		})
	}
}
