package auth

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web/ratelimit"
)

// TestHandler_RequestPasswordSetupAnon: RequestPasswordSetup sends an
// unauthenticated request to /signin.
func TestHandler_RequestPasswordSetupAnon(t *testing.T) {
	t.Parallel()
	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/account/password/setup", nil)
	h := &Handler{}
	h.RequestPasswordSetup(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/signin" {
		t.Errorf("unauthenticated setup = %d %q, want 303 signin", response.Code, response.Header().Get("Location"))
	}
}

// TestHandler_RequestPasswordSetupSpent: once the mailbox's reset links have
// been spent by somebody, RequestPasswordSetup answers the owner as ever and
// queues nothing, which a store that cannot be reached proves.
func TestHandler_RequestPasswordSetupSpent(t *testing.T) {
	t.Parallel()
	limit := ratelimit.New(addressMailPace)
	for range 3 {
		if _, allowed := limit.Allow("forgot:shopper@example.com"); !allowed {
			t.Fatal("initial mailbox allowance was refused")
		}
	}
	ctx := user.NewContext(t.Context(), user.User{Email: "shopper@example.com"})
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/password/setup", strings.NewReader("email=another@example.com"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	h := &Handler{store: deadAccountStore(t), resetLimit: limit, log: slog.New(slog.DiscardHandler)}
	h.RequestPasswordSetup(response, request)
	if want := "/account?password_setup=sent#password-heading"; response.Code != http.StatusSeeOther || response.Header().Get("Location") != want {
		t.Errorf("spent mailbox setup = %d %q, want 303 %q", response.Code, response.Header().Get("Location"), want)
	}
}

// TestHandler_RequestPasswordSetupFailure: when queueing the setup mail fails,
// RequestPasswordSetup answers 500 without a confirmation redirect or a
// completion cookie.
func TestHandler_RequestPasswordSetupFailure(t *testing.T) {
	t.Parallel()
	closed, err := pgxpool.New(t.Context(), "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	h := NewHandler(NewStore(closed), nil, nil, slog.New(slog.DiscardHandler), false, nil)
	ctx := user.NewContext(t.Context(), user.User{Email: "shopper@example.com"})
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/password/setup", nil)
	response := httptest.NewRecorder()
	h.RequestPasswordSetup(response, request)
	if response.Code != http.StatusInternalServerError || response.Header().Get("Location") != "" || len(response.Result().Cookies()) != 0 {
		t.Errorf("failed setup queue = %d %q, want 500 without confirmation or a completion cookie", response.Code, response.Header().Get("Location"))
	}
}
