//go:build integration

package account_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/auth/google"
	"github.com/koopa0/goen/internal/outbox"
)

// TestHandler_RefuseLegacyMailbox: auth refuses, and the page it answers with
// is account's, so the test sits on account's side of the import.
func TestHandler_RefuseLegacyMailbox(t *testing.T) {
	ctx := t.Context()
	app := accountStorePool(t, "legacy-password-setup")
	store := auth.NewStore(app)
	person, err := store.SignInWithGoogle(ctx, google.Identity{Subject: "legacy-setup-" + uuid.NewString(), Email: "legacy-setup-" + uuid.NewString() + "@example.com", EmailVerified: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, updateErr := pool.Exec(ctx, "UPDATE users SET email=$1 WHERE id=$2", "legacy@example..com", person.ID); updateErr != nil {
		t.Fatal(updateErr)
	}
	token, err := store.StartSession(ctx, person.ID, "legacy setup", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	cookies := httptest.NewRecorder()
	auth.SetSessionCookie(cookies, token, false)
	handler := auth.NewHandler(store, nil, nil, slog.New(slog.DiscardHandler), false, nil)
	// As cmd/goen wires it: the refusal is the account page's.
	handler.RefuseOnAccountPage(account.NewHandler(account.NewStore(app), slog.New(slog.DiscardHandler), false, false).Refuse)
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/password/setup", nil)
	request.AddCookie(sessionCookie(t, cookies))
	response := httptest.NewRecorder()
	handler.Authenticate(http.HandlerFunc(handler.RequestPasswordSetup)).ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || response.Header().Get("Location") != "" || len(response.Result().Cookies()) != 0 || strings.Contains(response.Body.String(), "password-setup-sent") || !strings.Contains(response.Body.String(), "legacy@example..com") {
		t.Errorf("invalid setup status/location = %d/%q, want 422 with the retained mailbox and no confirmation", response.Code, response.Header().Get("Location"))
	}
	var queued int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM outbox_messages WHERE topic=$1 AND payload->>'user_id'=$2", outbox.TopicPasswordResetRequest.Name(), person.ID).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Errorf("invalid mailbox queued %d requests, want zero", queued)
	}
}
