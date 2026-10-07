//go:build integration

package coupons_test

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
	"github.com/koopa0/goen/internal/admin/coupons"
	"github.com/koopa0/goen/internal/coupon"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgtx"
)

func TestCouponToggleFaultsKeepSavedStateAndRecover(t *testing.T) {
	owner := admintest.Pool(t)
	staff, _ := admintest.StaffContext(t, owner)
	adminPool := admintest.AdminRolePool(t, owner)
	for _, locale := range i18n.Locales() {
		for _, fault := range []string{"closed pool", "row lock"} {
			t.Run(locale.Tag()+"/"+fault, func(t *testing.T) {
				ctx := i18n.WithLocale(staff, locale)
				code := seedToggleCoupon(t, ctx, adminPool)
				before := couponToggleState(t, ctx, owner, code)
				beforeAudit := admintest.AuditRows(t, owner, audit.ActionToggleCoupon)
				trace := &couponToggleTrace{}
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
					if _, lockErr := holder.Exec(ctx, `SELECT 1 FROM coupons WHERE code=$1 FOR UPDATE`, code); lockErr != nil {
						t.Fatal(lockErr)
					}
				}
				var diagnostics bytes.Buffer
				logger := slog.New(slog.NewTextHandler(&diagnostics, nil))
				mux := http.NewServeMux()
				coupons.NewHandler(coupons.NewStore(writing), logger).Routes(mux, admintest.BackOffice)
				response := postCouponToggle(t, ctx, mux, code)
				if fault == "row lock" {
					attempts := trace.snapshot()
					if len(attempts) != 1 {
						t.Fatalf("SetCouponActive lock attempts = %d, want one", len(attempts))
					}
					pgErr, ok := errors.AsType[*pgconn.PgError](attempts[0])
					if !ok || pgErr.Code != "55P03" || ctx.Err() != nil {
						t.Fatalf("SetCouponActive error = %v, request = %v, want SQLSTATE55P03 with a live request", attempts[0], ctx.Err())
					}
				}
				if response.Code != http.StatusInternalServerError || response.Header().Get("Location") != "" {
					t.Errorf("coupon toggle on %s = %d to %q, want 500 without Location", fault, response.Code, response.Header().Get("Location"))
				}
				if body := response.Body.String(); !strings.Contains(body, i18n.T(ctx, i18n.KeyAdminErrorBody)) || strings.Contains(body, "SQLSTATE") || strings.Contains(body, "closed pool") {
					t.Error("coupon fault must show the localized server error without the database cause")
				}
				if !strings.Contains(diagnostics.String(), `level=ERROR msg="set coupon active"`) {
					t.Errorf("coupon toggle diagnostics = %q, want Error for the failed write", diagnostics.String())
				}
				if diff := cmp.Diff(before, couponToggleState(t, ctx, owner, code)); diff != "" {
					t.Errorf("failed coupon toggle changed saved rows or audit (-want +got):\n%s", diff)
				}
				if holder != nil {
					if rollbackErr := holder.Rollback(ctx); rollbackErr != nil {
						t.Fatal(rollbackErr)
					}
				}
				mux = http.NewServeMux()
				recoveredPool := writing
				if fault == "closed pool" {
					recoveredPool = adminPool
				}
				coupons.NewHandler(coupons.NewStore(recoveredPool), logger).Routes(mux, admintest.BackOffice)
				retry := postCouponToggle(t, ctx, mux, code)
				if retry.Code != http.StatusSeeOther || retry.Header().Get("Location") != "/admin/coupons?ok=1" {
					t.Fatalf("recovered coupon toggle = %d to %q, want 303 to success", retry.Code, retry.Header().Get("Location"))
				}
				var active bool
				if readErr := owner.QueryRow(ctx, `SELECT is_active FROM coupons WHERE code=$1`, code).Scan(&active); readErr != nil {
					t.Fatal(readErr)
				}
				if active || admintest.AuditRows(t, owner, audit.ActionToggleCoupon) != beforeAudit+1 {
					t.Error("recovered coupon toggle must deactivate the row and commit exactly one audit event")
				}
			})
		}
	}
}

func TestMissingCouponToggleAnswersNotFound(t *testing.T) {
	owner := admintest.Pool(t)
	staff, _ := admintest.StaffContext(t, owner)
	adminPool := admintest.AdminRolePool(t, owner)
	code := seedToggleCoupon(t, staff, adminPool)
	mux := http.NewServeMux()
	coupons.NewHandler(coupons.NewStore(adminPool), slog.New(slog.DiscardHandler)).Routes(mux, admintest.BackOffice)
	before := couponToggleState(t, staff, owner, code)
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(staff, locale)
			response := postCouponToggle(t, ctx, mux, "MISSING-"+uuid.NewString()[:8])
			if response.Code != http.StatusNotFound || response.Header().Get("Location") != "" {
				t.Errorf("missing coupon toggle = %d to %q, want 404 without Location", response.Code, response.Header().Get("Location"))
			}
			if diff := cmp.Diff(before, couponToggleState(t, ctx, owner, code)); diff != "" {
				t.Errorf("missing coupon toggle changed saved rows or audit (-want +got):\n%s", diff)
			}
		})
	}
}

func seedToggleCoupon(t *testing.T, ctx context.Context, p *pgxpool.Pool) string {
	t.Helper()
	code := "OUTCOME-" + strings.ToUpper(uuid.NewString()[:8])
	fields, err := coupons.NewStore(p).CreateCoupon(ctx, &coupons.Form{
		Code: code, Description: "Toggle outcome", Kind: coupon.FreeShipping, PerCustomer: 1, Days: 7,
	})
	if err != nil || len(fields) > 0 {
		t.Fatalf("coupon fixture = %v/%v", fields, err)
	}
	return code
}

func postCouponToggle(t *testing.T, ctx context.Context, mux *http.ServeMux, code string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"active": {"false"}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/coupons/"+code+"/active", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	return response
}

func couponToggleState(t *testing.T, ctx context.Context, p *pgxpool.Pool, code string) string {
	t.Helper()
	var state string
	if err := p.QueryRow(ctx, `SELECT jsonb_build_object(
		'coupon', (SELECT to_jsonb(c) FROM coupons c WHERE code=$1),
		'audit', (SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY id), '[]'::jsonb) FROM audit_events a)
	)::text`, code).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

type couponToggleQueryKey struct{}

type couponToggleTrace struct {
	mu     sync.Mutex
	errors []error
}

func (tr *couponToggleTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, couponToggleQueryKey{}, strings.HasPrefix(data.SQL, "-- name: SetCouponActive :execrows"))
}

func (tr *couponToggleTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if matched, _ := ctx.Value(couponToggleQueryKey{}).(bool); !matched {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.errors = append(tr.errors, data.Err)
}

func (tr *couponToggleTrace) snapshot() []error {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return slices.Clone(tr.errors)
}
