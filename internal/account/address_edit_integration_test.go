//go:build integration

package account_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestSavedAddressEditsKeepOwnershipDraftsAndDefaultSelection(t *testing.T) {
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			app := accountStorePool(t, "address-edit-"+uuid.NewString())
			store := account.NewStore(app)
			authStore := auth.NewStore(app)
			owner := registerProved(t, "address-edit-"+uuid.NewString()+"@example.com")
			other := registerProved(t, "other-address-"+uuid.NewString()+"@example.com")
			first := addAddress(t, store, owner.ID, "Home", true)
			second := addAddress(t, store, owner.ID, "Work", false)
			foreign := addAddress(t, store, other.ID, "Foreign private address", true)
			foreignBefore := savedAddress(t, foreign)
			token, err := authStore.StartSession(ctx, owner.ID, "address edit", "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			cookieResponse := httptest.NewRecorder()
			auth.SetSessionCookie(cookieResponse, token, false)
			cookie := sessionCookie(t, cookieResponse)
			h := account.NewHandler(store, slog.New(slog.DiscardHandler), false, false)
			authn := auth.NewHandler(authStore, nil, nil, slog.New(slog.DiscardHandler), false, nil)
			mux := http.NewServeMux()
			mux.HandleFunc("GET /account/addresses/{address}/edit", authn.RequireUser(h.EditAddress))
			mux.HandleFunc("POST /account/addresses/update", h.UpdateAddress)
			mux.HandleFunc("POST /account/addresses/delete", authn.RequireUser(h.DeleteAddress))
			serve := authn.Authenticate(mux)
			get := httptest.NewRequestWithContext(ctx, http.MethodGet, "/account/addresses/"+first+"/edit", http.NoBody)
			get.AddCookie(cookie)
			prefilled := httptest.NewRecorder()
			serve.ServeHTTP(prefilled, get)
			if prefilled.Code != http.StatusOK {
				t.Fatalf("edit GET = %d, want 200: %s", prefilled.Code, prefilled.Body.String())
			}
			initial := savedAddress(t, first)
			assertAddressInputs(t, prefilled.Body.String(), url.Values{
				"address": {first}, "label": {initial.Label}, "name": {initial.Name}, "phone": {initial.Phone},
				"postal_code": {initial.PostalCode}, "city": {initial.City}, "district": {initial.District}, "street": {initial.Street},
			})
			post := func(values url.Values) *httptest.ResponseRecorder {
				req := postForm(ctx, "/account/addresses/update", values)
				req.AddCookie(cookie)
				res := httptest.NewRecorder()
				serve.ServeHTTP(res, req)
				return res
			}
			values := url.Values{"address": {first}, "label": {"  Edited home  "}, "name": {"New recipient"}, "phone": {"wrong phone"},
				"postal_code": {"110"}, "city": {"Taipei"}, "district": {"Xinyi"}, "street": {"  2 Test Road  "}, "default": {"0"}, "user_id": {other.ID}}
			refused := post(values)
			if refused.Code != http.StatusUnprocessableEntity || refused.Header().Get("Location") != "" {
				t.Fatalf("invalid edit = %d to %q, want rendered 422", refused.Code, refused.Header().Get("Location"))
			}
			assertAddressInputs(t, refused.Body.String(), values)
			if diff := cmp.Diff(initial, savedAddress(t, first)); diff != "" {
				t.Errorf("refused edit changed the saved address (-before +after):\n%s", diff)
			}
			values.Set("phone", "0987654321")
			updated := post(values)
			if updated.Code != http.StatusSeeOther || updated.Header().Get("Location") != "/account?saved=1#addresses-heading" {
				t.Fatalf("valid edit = %d to %q, want the address-section 303", updated.Code, updated.Header().Get("Location"))
			}
			want := pages.AccountAddress{ID: first, Label: "Edited home", Name: "New recipient", Phone: "0987654321", PostalCode: "110", City: "Taipei", District: "Xinyi", Street: "2 Test Road", Default: true}
			if diff := cmp.Diff(want, savedAddress(t, first)); diff != "" {
				t.Errorf("saved edit differs (-want +got):\n%s", diff)
			}
			values.Set("address", second)
			values.Set("default", "1")
			if res := post(values); res.Code != http.StatusSeeOther {
				t.Fatalf("non-default edit = %d, want 303", res.Code)
			}
			if savedAddress(t, second).Default || defaultAddressID(t, owner.ID) != first {
				t.Error("forged default field changed the default selection")
			}
			values.Set("address", foreign)
			if res := post(values); res.Code != http.StatusNotFound || strings.Contains(res.Body.String(), foreignBefore.Label) {
				t.Errorf("foreign update = %d, want 404 without the private address", res.Code)
			}
			foreignGet := httptest.NewRequestWithContext(ctx, http.MethodGet, "/account/addresses/"+foreign+"/edit", http.NoBody)
			foreignGet.AddCookie(cookie)
			foreignRes := httptest.NewRecorder()
			serve.ServeHTTP(foreignRes, foreignGet)
			if foreignRes.Code != http.StatusNotFound || strings.Contains(foreignRes.Body.String(), foreignBefore.Label) {
				t.Errorf("foreign edit GET = %d, want 404 without the private address", foreignRes.Code)
			}
			if diff := cmp.Diff(foreignBefore, savedAddress(t, foreign)); diff != "" {
				t.Errorf("foreign address changed (-before +after):\n%s", diff)
			}
			deleteRequest := postForm(ctx, "/account/addresses/delete", url.Values{"address": {second}})
			deleteRequest.AddCookie(cookie)
			deleted := httptest.NewRecorder()
			serve.ServeHTTP(deleted, deleteRequest)
			var remaining int
			if queryErr := pool.QueryRow(ctx, `SELECT count(*) FROM addresses WHERE id = $1`, second).Scan(&remaining); queryErr != nil {
				t.Fatal(queryErr)
			}
			if deleted.Code != http.StatusSeeOther || remaining != 0 || savedAddress(t, foreign) != foreignBefore || defaultAddressID(t, owner.ID) != first {
				t.Error("native delete did not remove only the selected owned address")
			}
		})
	}
}

func savedAddress(t *testing.T, id string) pages.AccountAddress {
	t.Helper()
	var a pages.AccountAddress
	err := pool.QueryRow(t.Context(), `SELECT id::text, coalesce(label, ''), recipient_name, phone, postal_code, city, district, street, is_default FROM addresses WHERE id = $1`, id).
		Scan(&a.ID, &a.Label, &a.Name, &a.Phone, &a.PostalCode, &a.City, &a.District, &a.Street, &a.Default)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func assertAddressInputs(t *testing.T, body string, want url.Values) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]string)
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			var name, value string
			for _, attr := range n.Attr {
				switch attr.Key {
				case "name":
					name = attr.Val
				case "value":
					value = attr.Val
				}
			}
			got[name] = value
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	for name, values := range want {
		if name == "default" || name == "user_id" {
			continue
		}
		if got[name] != values[0] {
			t.Errorf("address input %s = %q, want %q", name, got[name], values[0])
		}
	}
}
