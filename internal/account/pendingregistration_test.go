package account

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestThePendingRegistrationTravelsInAnHttpOnlyCookieScopedToRegister: the sent
// page needs the address, and a URL is kept by history and proxy logs.
func TestThePendingRegistrationTravelsInAnHttpOnlyCookieScopedToRegister(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	writePendingRegistration(rec, "ada@example.com", "/cart", true)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("%d cookies set, want 1", len(cookies))
	}
	c := cookies[0]
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/register" || c.MaxAge <= 0 || c.MaxAge > 900 {
		t.Errorf("cookie = %+v, want HttpOnly, Secure, Lax, Path /register, a few minutes", c)
	}

	req := httptest.NewRequest(http.MethodGet, "/register?sent=1", nil)
	req.AddCookie(c)
	addr, next, ok := readPendingRegistration(req)
	if !ok || addr != "ada@example.com" || next != "/cart" {
		t.Errorf("read back %q, %q, %v", addr, next, ok)
	}

	bad := httptest.NewRequest(http.MethodGet, "/register?sent=1", nil)
	bad.AddCookie(&http.Cookie{Name: pendingRegistrationCookie, Value: "bm90LWFuLWFkZHJlc3M"})
	if _, _, ok := readPendingRegistration(bad); ok {
		t.Error("a cookie that holds no address was accepted")
	}
	if _, _, ok := readPendingRegistration(httptest.NewRequest(http.MethodGet, "/register?sent=1", nil)); ok {
		t.Error("no cookie was read as one")
	}
}
