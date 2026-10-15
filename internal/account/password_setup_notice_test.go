package account

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/koopa0/goen/internal/user"
)

func TestPasswordSetupNoticeRequiresTheCurrentAccountAndMailbox(t *testing.T) {
	t.Parallel()
	const receipt = "MTExMTExMTEtMTExMS00MTExLTgxMTEtMTExMTExMTExMTExfHNob3BwZXJAZXhhbXBsZS5jb20"
	for _, tc := range []struct {
		name, value, id, address string
		want                     bool
	}{
		{name: "query only", id: "11111111-1111-4111-8111-111111111111", address: "shopper@example.com"},
		{name: "malformed", value: "@", id: "11111111-1111-4111-8111-111111111111", address: "shopper@example.com"},
		{name: "different account", value: receipt, id: "22222222-2222-4222-8222-222222222222", address: "shopper@example.com"},
		{name: "old mailbox", value: receipt, id: "11111111-1111-4111-8111-111111111111", address: "new@example.com"},
		{name: "current account and mailbox", value: receipt, id: "11111111-1111-4111-8111-111111111111", address: "shopper@example.com", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/account?password_setup=sent", nil)
			if tc.value != "" {
				//nolint:gosec // Request cookies carry only a name and value; response attributes are checked separately.
				request.AddCookie(&http.Cookie{Name: "goen_password_setup", Value: tc.value})
			}
			response := httptest.NewRecorder()
			h := &Handler{}
			got := h.consumePasswordSetupNotice(response, request, user.User{ID: tc.id, Email: tc.address})
			if got != tc.want {
				t.Errorf("completion notice = %v, want %v", got, tc.want)
			}
			if tc.value != "" {
				cookies := response.Result().Cookies()
				if len(cookies) != 1 || cookies[0].MaxAge != -1 || cookies[0].Path != "/account" {
					t.Errorf("consumed cookies = %v, want account-path expiry", cookies)
				}
			}
		})
	}
}

func TestPasswordSetupNoticeIsConsumedFromTheBrowserJar(t *testing.T) {
	t.Parallel()
	person := user.User{ID: "11111111-1111-4111-8111-111111111111", Email: "shopper@example.com"}
	for _, secure := range []bool{false, true} {
		scheme := "http"
		if secure {
			scheme = "https"
		}
		address := &url.URL{Scheme: scheme, Host: "goen.example", Path: "/account", RawQuery: "password_setup=sent"}
		h := &Handler{secure: secure}
		sent := httptest.NewRecorder()
		h.writePasswordSetupNotice(sent, person)
		cookies := sent.Result().Cookies()
		if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].Secure != secure || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Path != "/account" || cookies[0].MaxAge != 600 {
			t.Fatalf("setup cookie = %v, want a short-lived deployment cookie", cookies)
		}
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		jar.SetCookies(address, cookies)
		for attempt := range 2 {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, address.String(), nil)
			for _, cookie := range jar.Cookies(address) {
				request.AddCookie(cookie)
			}
			response := httptest.NewRecorder()
			if got := h.consumePasswordSetupNotice(response, request, person); got != (attempt == 0) {
				t.Errorf("notice attempt %d = %v, want %v", attempt, got, attempt == 0)
			}
			jar.SetCookies(address, response.Result().Cookies())
		}
	}
}
