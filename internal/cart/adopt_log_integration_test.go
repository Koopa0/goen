//go:build integration

package cart_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/order/access"
)

// adoptionLogRecord is the one line AdoptRequestCart writes for a cart it could
// not carry over.
type adoptionLogRecord struct {
	Level  string `json:"level"`
	Msg    string `json:"msg"`
	UserID string `json:"user_id"`
	Error  string `json:"error"`
}

// TestAdoptRequestCartLogLevels holds the log level of each way adoption fails:
// a cart that is not this account's to take is the shopper's situation, so it is
// a warning; anything the shop could not do is an error. The two races (the cart
// gains an owner, or disappears, after the cookie resolved to it) are made
// deterministic by holding the account row, which is the first thing adoption
// locks.
func TestAdoptRequestCartLogLevels(t *testing.T) {
	ctx := t.Context()
	for _, tt := range []struct {
		name, level, msg string
		// betweenLookupAndAdoption changes the guest cart once the handler has
		// resolved it and waits for the account row.
		betweenLookupAndAdoption func(t *testing.T, guestCart, owner uuid.UUID)
		badUserID                bool
		closedPool               bool
		retireLine               bool
	}{
		{name: "unavailable line", level: "WARN", msg: "adopt cart", retireLine: true},
		{name: "another owner", level: "WARN", msg: "adopt cart",
			betweenLookupAndAdoption: func(t *testing.T, guestCart, owner uuid.UUID) {
				t.Helper()
				if _, err := pool.Exec(t.Context(), `UPDATE carts SET user_id = $2 WHERE id = $1`, guestCart, owner); err != nil {
					t.Fatalf("give the guest cart another owner: %v", err)
				}
			}},
		{name: "missing cart", level: "WARN", msg: "adopt cart",
			betweenLookupAndAdoption: func(t *testing.T, guestCart, _ uuid.UUID) {
				t.Helper()
				if _, err := pool.Exec(t.Context(), `DELETE FROM carts WHERE id = $1`, guestCart); err != nil {
					t.Fatalf("delete the guest cart: %v", err)
				}
			}},
		{name: "unexpected input failure", level: "ERROR", msg: "adopt cart", badUserID: true},
		{name: "database unavailable", level: "ERROR", msg: "read cart for sign-in", closedPool: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			u := adopter(t, "adoption-log-"+uuid.NewString()+"@example.com")
			other := adopter(t, "adoption-log-owner-"+uuid.NewString()+"@example.com")
			token := "adoption-log-" + uuid.NewString()
			var guestCart uuid.UUID
			if err := pool.QueryRow(ctx, `INSERT INTO carts (digest) VALUES ($1) RETURNING id`,
				bearer.Digest(token)).Scan(&guestCart); err != nil {
				t.Fatal(err)
			}
			// A sibling keeps the product listed when the guest line is retired.
			variant, _, _ := threeVariants(t, "aurora")
			if _, err := pool.Exec(ctx, `INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 1)`,
				guestCart, variant); err != nil {
				t.Fatal(err)
			}
			if tt.retireLine {
				if _, err := pool.Exec(ctx, `UPDATE product_variants SET is_active = false WHERE id = $1`, variant); err != nil {
					t.Fatal(err)
				}
			}

			applicationName := "adoption-log-" + uuid.NewString()[:8]
			storePool := adoptionPool(t, applicationName)
			if tt.closedPool {
				storePool.Close()
			}
			var logs bytes.Buffer
			h := cart.NewHandler(cart.NewStore(storePool), access.NewStore(pool, false),
				slog.New(slog.NewJSONHandler(&logs, nil)), false, nil, nil)
			userID := u.ID
			if tt.badUserID {
				userID = "not-a-uuid"
			}

			adjusted, err := adoptRequestCartHolding(ctx, t, h, tt.betweenLookupAndAdoption,
				applicationName, token, userID, guestCart, uuid.MustParse(other.ID), u.ID)
			if err == nil || adjusted {
				t.Fatalf("adjusted/err = %v/%v, want an error", adjusted, err)
			}

			decoder := json.NewDecoder(&logs)
			var record adoptionLogRecord
			if err := decoder.Decode(&record); err != nil {
				t.Fatal(err)
			}
			if record.Level != tt.level || record.Msg != tt.msg || record.Error == "" ||
				(tt.msg == "adopt cart" && record.UserID != userID) {
				t.Errorf("adoption log = %+v, want level=%s msg=%q user_id=%s and cause", record, tt.level, tt.msg, userID)
			}
			if decoder.More() {
				t.Error("unexpected additional adoption log")
			}
		})
	}
}

// adoptRequestCartHolding runs AdoptRequestCart for a browser that carries the
// guest cart's cookie. When change is set it holds the account row first, lets
// the handler resolve the cart and block on that row, applies change, and then
// releases it.
func adoptRequestCartHolding(
	ctx context.Context, t *testing.T, h *cart.Handler,
	change func(t *testing.T, guestCart, owner uuid.UUID),
	applicationName, token, userID string, guestCart, owner uuid.UUID, accountID string,
) (adjusted bool, err error) {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/signin", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cookie under test
	if change == nil {
		return h.AdoptRequestCart(ctx, req, userID)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin account blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, lockErr := blocker.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, uuid.MustParse(accountID)); lockErr != nil {
		t.Fatalf("lock account: %v", lockErr)
	}
	done := make(chan error, 1)
	go func() {
		var adoptErr error
		adjusted, adoptErr = h.AdoptRequestCart(ctx, req, userID)
		done <- adoptErr
	}()
	waitForApplicationLock(t, applicationName, done)
	change(t, guestCart, owner)
	if commitErr := blocker.Commit(ctx); commitErr != nil {
		t.Fatalf("release account: %v", commitErr)
	}
	err = operationResult(t, done)
	return adjusted, err
}
