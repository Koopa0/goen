package comparison

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestComparisonCookieIsExplicitBoundedAndSessionOnly(t *testing.T) {
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/compare?p=shared", http.NoBody)
	if got := Read(r); len(got) != 0 {
		t.Fatalf("a shared URL changed selection: %v", got)
	}
	w := httptest.NewRecorder()
	Write(w, []string{"one", "two", "two", "three", "four", "five"}, true)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != CookieName || cookie.Value != "one,two,three,four" || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Domain != "" || cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Fatalf("cookie = %+v", cookie)
	}
	r.AddCookie(cookie)
	if got := Read(r); len(got) != Max {
		t.Fatalf("selection = %v", got)
	}
	cleared := httptest.NewRecorder()
	Write(cleared, nil, true)
	if cleared.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("clear did not delete cookie")
	}
}

func TestComparisonBoundsReportOverflow(t *testing.T) {
	got, overflow := Normalize([]string{"one", "one", "bad/slash", "two", "three", "four", "five"})
	if len(got) != Max || !overflow {
		t.Fatalf("selection %v overflow %v", got, overflow)
	}
	if Href(nil) != "/compare?p=" {
		t.Fatal("empty shared selection fell back to personal state")
	}
}
