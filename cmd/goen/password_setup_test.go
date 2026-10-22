package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/email"
)

// TestNewRouterPasswordSetupBudget: POST /forgot and POST
// /account/password/setup spend one per-client budget: a client that exhausts
// either is answered 429 with Retry-After on the other, and another client is
// not refused.
func TestNewRouterPasswordSetupBudget(t *testing.T) {
	t.Parallel()
	for _, setupFirst := range []bool{false, true} {
		name := "forgot exhausts setup"
		if setupFirst {
			name = "setup exhausts forgot"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			router := storeMapRouter(t, false)
			post := func(path, remote string) *httptest.ResponseRecorder {
				// An overlong address bypasses the mailbox budget and database queue.
				request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader("email="+strings.Repeat("x", email.MaxBytes+1)))
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				request.RemoteAddr = remote
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				return response
			}
			first, second := "/forgot", "/account/password/setup"
			if setupFirst {
				first, second = second, first
			}
			spent := false
			for attempt := range 80 {
				response := post(first, "192.0.2.10:4000")
				if response.Code == http.StatusTooManyRequests {
					spent = true
					break
				}
				if response.Code != http.StatusSeeOther {
					t.Fatalf("budget request %d to %s = %d, want 303", attempt, first, response.Code)
				}
				if first == "/account/password/setup" && response.Header().Get("Location") != "/signin" {
					t.Fatalf("unauthenticated setup redirects to %q, want signin", response.Header().Get("Location"))
				}
			}
			if !spent {
				t.Fatalf("80 requests to %s never spent the client budget", first)
			}
			if response := post(second, "192.0.2.10:4000"); response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" {
				t.Errorf("spent client moved from %s to %s: status %d, Retry-After %q; want 429", first, second, response.Code, response.Header().Get("Retry-After"))
			}
			if response := post(second, "192.0.2.11:4000"); response.Code != http.StatusSeeOther {
				t.Errorf("another client's request to %s = %d, want 303", second, response.Code)
			}
		})
	}
}
