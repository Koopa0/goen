//go:build integration

package policy

import (
	"context"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
)

// /shipping and /payment state the checkout windows the schema names, read
// through the store role the storefront's pool connects as.
func TestThePoliciesStateTheSchemasCheckoutWindows(t *testing.T) {
	owner := dbtest.Pool(t)
	var want CheckoutWindows
	if err := owner.QueryRow(t.Context(), `SELECT checkout_hold_minutes(), checkout_pay_start_minutes()`).
		Scan(&want.HoldMinutes, &want.PayStartMinutes); err != nil {
		t.Fatalf("read the checkout windows: %v", err)
	}

	cfg := owner.Config().Copy()
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE store`)
		return err
	}
	storePool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open a store-role pool: %v", err)
	}
	t.Cleanup(storePool.Close)
	h := Handler{store: NewStore(storePool), log: slog.New(slog.DiscardHandler)}

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, tc := range []struct {
			path    string
			serve   http.HandlerFunc
			message i18n.Key
		}{
			{"/shipping", h.Shipping, i18n.KeyShippingHoldBody},
			{"/payment", h.Policy, i18n.KeyPolicyPaymentHold},
		} {
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, tc.path, http.NoBody)
			res := httptest.NewRecorder()
			tc.serve(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("%s %s: status = %d, want 200", locale, tc.path, res.Code)
			}
			if sentence := want.State(ctx, tc.message); !strings.Contains(html.UnescapeString(res.Body.String()), sentence) {
				t.Errorf("%s %s does not state %q", locale, tc.path, sentence)
			}
		}
	}
}
