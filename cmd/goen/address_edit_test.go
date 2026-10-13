package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSavedAddressEditRoutesRecoverSignedOutRequests(t *testing.T) {
	t.Parallel()
	router := storeMapRouter(t, false)
	for _, route := range []struct {
		method, path, location string
	}{
		{http.MethodGet, "/account/addresses/11111111-1111-4111-8111-111111111111/edit", "/signin?next=/account/addresses/11111111-1111-4111-8111-111111111111/edit"},
		{http.MethodPost, "/account/addresses/update", "/signin"},
	} {
		req := httptest.NewRequestWithContext(t.Context(), route.method, route.path, strings.NewReader("address=forged"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusSeeOther || res.Header().Get("Location") != route.location {
			t.Errorf("%s %s = %d to %q, want 303 to %q", route.method, route.path, res.Code, res.Header().Get("Location"), route.location)
		}
	}
}
