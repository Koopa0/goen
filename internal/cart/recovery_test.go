package cart

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/user"
)

func TestCartRecoveryDoesNotPretendAReadFailureIsAnEmptyBasket(t *testing.T) {
	t.Parallel()
	dbPool, err := pgxpool.New(t.Context(), "postgres://nobody@127.0.0.1:1/nothing")
	if err != nil {
		t.Fatal(err)
	}
	dbPool.Close()
	carts := &Handler{store: NewStore(dbPool), log: slog.New(slog.DiscardHandler)}
	accounts := account.NewHandler(account.NewStore(dbPool), carts, slog.New(slog.DiscardHandler), false, nil)
	ctx := user.NewContext(t.Context(), user.User{ID: "11111111-1111-4111-8111-111111111111"})
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/account/cart-recovery", http.NoBody)
	res := httptest.NewRecorder()
	accounts.CartRecoveryPage(res, req)
	if res.Code != http.StatusInternalServerError || strings.Contains(res.Body.String(), `action="/account/cart/retry"`) {
		t.Errorf("unreadable recovery = %d with retry %t, want 500 without a comparison", res.Code, strings.Contains(res.Body.String(), `action="/account/cart/retry"`))
	}
}

func TestCartRecoveryDoesNotReadSavedBasketsForASignedOutRequest(t *testing.T) {
	t.Parallel()
	carts := &Handler{}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/account/cart-recovery", http.NoBody)
	contents, err := carts.RecoveryContents(t.Context(), req)
	if !errors.Is(err, ErrNotFound) || len(contents.Account) != 0 || len(contents.BeforeSignIn) != 0 {
		t.Errorf("signed-out recovery = %+v, %v; want no baskets and ErrNotFound", contents, err)
	}
}
