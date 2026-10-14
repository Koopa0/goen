//go:build integration

package account_test

import (
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/user"
)

func TestCartRecoveryReadsBothBasketsWithoutChangingOrExposingAnotherAccount(t *testing.T) {
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			app := accountStorePool(t, "cart-recovery-"+uuid.NewString())
			accounts := account.NewStore(app)
			owner := registerProved(t, accounts, "recovery-"+uuid.NewString()+"@example.com")
			foreign := registerProved(t, accounts, "foreign-recovery-"+uuid.NewString()+"@example.com")
			accountVariant := sellableVariant(t, ctx)
			guestVariant := anotherSellableVariant(t, ctx, accountVariant)
			var ownedID, guestID, foreignID uuid.UUID
			guestToken, ownedToken, foreignToken := "guest-recovery-"+owner.ID, "owned-recovery-"+owner.ID, "foreign-recovery-"+foreign.ID
			for _, fixture := range []struct {
				id       *uuid.UUID
				token    string
				owner    uuid.NullUUID
				variant  uuid.UUID
				quantity int32
			}{
				{&ownedID, ownedToken, uuid.NullUUID{UUID: uuid.MustParse(owner.ID), Valid: true}, accountVariant, 3},
				{&guestID, guestToken, uuid.NullUUID{}, guestVariant, 2},
				{&foreignID, foreignToken, uuid.NullUUID{UUID: uuid.MustParse(foreign.ID), Valid: true}, guestVariant, 99},
			} {
				if insertErr := pool.QueryRow(ctx, `INSERT INTO carts (token_hash, user_id) VALUES ($1, $2) RETURNING id`, bearer.Digest(fixture.token), fixture.owner).Scan(fixture.id); insertErr != nil {
					t.Fatal(insertErr)
				}
				if _, lineErr := pool.Exec(ctx, `INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, $3)`, *fixture.id, fixture.variant, fixture.quantity); lineErr != nil {
					t.Fatal(lineErr)
				}
			}
			carts := cart.NewHandler(cart.NewStore(app), access.NewStore(app, false), slog.New(slog.DiscardHandler), false,
				ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}), nil, nil)
			h := account.NewHandler(accounts, carts, slog.New(slog.DiscardHandler), false, nil)
			token, err := accounts.StartSession(ctx, owner.ID, "recovery", "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			cookieResponse := httptest.NewRecorder()
			account.SetSessionCookie(cookieResponse, token, false)
			session := sessionCookie(t, cookieResponse)
			for _, browser := range []struct {
				name, token string
				guest       bool
			}{
				{"guest basket", guestToken, true}, {"own account cookie", ownedToken, false},
				{"foreign account cookie", foreignToken, false}, {"missing cookie", "", false}, {"stale cookie", "stale", false},
			} {
				t.Run(browser.name, func(t *testing.T) {
					req := httptest.NewRequestWithContext(user.NewContext(ctx, owner), http.MethodGet, "/account/cart-recovery?next=%2Fcheckout", http.NoBody)
					if browser.token != "" {
						req.AddCookie(browserCart(browser.token))
					}
					contents, readErr := carts.RecoveryContents(req.Context(), req)
					if readErr != nil {
						t.Fatal(readErr)
					}
					if len(contents.Account) != 1 || contents.Account[0].VariantID != accountVariant.String() || contents.Account[0].Quantity != 3 {
						t.Fatalf("account recovery lines = %+v, want only the current account's three units", contents.Account)
					}
					if browser.guest {
						if len(contents.BeforeSignIn) != 1 || contents.BeforeSignIn[0].VariantID != guestVariant.String() || contents.BeforeSignIn[0].Quantity != 2 {
							t.Fatalf("before-sign-in lines = %+v, want the guest basket's two units", contents.BeforeSignIn)
						}
					} else if len(contents.BeforeSignIn) != 0 {
						t.Errorf("an absent or owned cookie exposed a before-sign-in basket: %+v", contents.BeforeSignIn)
					}
					req = req.WithContext(ctx)
					req.AddCookie(session)
					res := httptest.NewRecorder()
					h.Authenticate(http.HandlerFunc(h.CartRecoveryPage)).ServeHTTP(res, req)
					if res.Code != http.StatusOK || len(res.Header().Values("Set-Cookie")) != 0 {
						t.Fatalf("recovery GET = %d with cookies %v, want 200 with no cookie writes", res.Code, res.Header().Values("Set-Cookie"))
					}
					body := res.Body.String()
					if !strings.Contains(html.UnescapeString(body), contents.Account[0].Name) || !strings.Contains(body, `value="/checkout"`) {
						t.Error("rendered recovery lost the account item or retry destination")
					}
					for _, fixture := range []struct {
						id, variant uuid.UUID
						quantity    int32
					}{{ownedID, accountVariant, 3}, {guestID, guestVariant, 2}, {foreignID, guestVariant, 99}} {
						if got := cartItemQuantity(t, fixture.id, fixture.variant); got != fixture.quantity {
							t.Errorf("recovery changed cart %s quantity to %d, want %d", fixture.id, got, fixture.quantity)
						}
					}
				})
			}
		})
	}
}
