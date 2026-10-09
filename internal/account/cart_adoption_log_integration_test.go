//go:build integration

package account_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/user"
)

// A cart can disappear or acquire an owner after the request resolves its ID.
type resolvedAdoptionCart struct {
	account.CartFinder

	id uuid.UUID
}

func (c resolvedAdoptionCart) IDForRequest(context.Context, *http.Request) (uuid.UUID, bool, error) {
	return c.id, true, nil
}

func TestCartAdoptionLogLevels(t *testing.T) {
	ctx := t.Context()
	accounts := account.NewStore(pool)
	u := register(t, accounts, "adoption-log-"+uuid.NewString()+"@example.com")
	other := register(t, accounts, "adoption-log-owner-"+uuid.NewString()+"@example.com")
	var ownedCart, guestCart uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO carts (token_hash, user_id) VALUES ($1, $2) RETURNING id`,
		account.HashToken("owned-log-"+other.ID), uuid.MustParse(other.ID)).Scan(&ownedCart); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO carts (token_hash) VALUES ($1) RETURNING id`,
		account.HashToken("guest-log-"+u.ID)).Scan(&guestCart); err != nil {
		t.Fatal(err)
	}
	variant := sellableVariant(t, ctx)
	if _, err := pool.Exec(ctx, `INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 1)`, guestCart, variant); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE product_variants SET is_active = false WHERE id = $1`, variant); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.WithoutCancel(ctx), `UPDATE product_variants SET is_active = true WHERE id = $1`, variant); err != nil {
			t.Error(err)
		}
	})
	store := account.NewStore(accountStorePool(t, "adoption-log-levels"))
	for _, tt := range []struct {
		name, userID, level string
		cartID              uuid.UUID
	}{
		{"unavailable line", u.ID, "WARN", guestCart},
		{"another owner", u.ID, "WARN", ownedCart},
		{"missing cart", u.ID, "WARN", uuid.New()},
		{"unexpected input failure", "not-a-uuid", "ERROR", guestCart},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			h := account.NewHandler(store, resolvedAdoptionCart{id: tt.cartID},
				slog.New(slog.NewJSONHandler(&logs, nil)), false, nil)
			for range 2 {
				r := httptest.NewRequestWithContext(user.NewContext(t.Context(), user.User{ID: tt.userID}),
					http.MethodPost, "/account/cart/retry", strings.NewReader("next=%2Fcheckout"))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				w := httptest.NewRecorder()
				h.RetryCartAdoption(w, r)
				if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/account/cart-recovery?next=%2Fcheckout" {
					t.Fatalf("retry status/location = %d/%q", w.Code, w.Header().Get("Location"))
				}
			}
			decoder := json.NewDecoder(&logs)
			for range 2 {
				var record struct {
					Level  string `json:"level"`
					Msg    string `json:"msg"`
					UserID string `json:"user_id"`
					Error  string `json:"error"`
				}
				if err := decoder.Decode(&record); err != nil {
					t.Fatal(err)
				}
				if record.Level != tt.level || record.Msg != "adopt cart" || record.UserID != tt.userID || record.Error == "" {
					t.Errorf("adoption log = %+v, want level=%s user_id=%s and cause", record, tt.level, tt.userID)
				}
			}
			if decoder.More() {
				t.Error("unexpected additional adoption log")
			}
		})
	}
}
