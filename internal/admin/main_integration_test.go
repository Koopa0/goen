//go:build integration

package admin_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/campaigns"
	"github.com/koopa0/goen/internal/admin/credits"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/admin/shipping"
	"github.com/koopa0/goen/internal/admin/staff"
	"github.com/koopa0/goen/internal/admin/stock"
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

// TestControl_RequireStaffOutsiders: the back office is invisible to everyone but staff.
func TestControl_RequireStaffOutsiders(t *testing.T) {
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
			_, grantErr := credits.NewStore(pool).GrantCredit(ctx, actor, 500,
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

// TestClosedDatabaseWritesNotRefused: parse-protected writes do not call an infrastructure failure a refusal.
func TestClosedDatabaseWritesNotRefused(t *testing.T) {
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
