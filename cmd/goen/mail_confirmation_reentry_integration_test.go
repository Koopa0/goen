//go:build integration

package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/payment"
)

func TestHomepageNewsletterReentryRestoresTheReplacedForm(t *testing.T) {
	for _, language := range []string{"zh-Hant", "en"} {
		t.Run(language, func(t *testing.T) {
			router := confirmationRouter(t)
			start := httptest.NewRecorder()
			initial := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			initial.Header.Set("Accept-Language", language)
			router.ServeHTTP(start, initial)
			if start.Code != http.StatusOK || confirmationElement(t, start.Body.String(), "newsletter-email") == nil {
				t.Fatal("homepage fixture has no usable newsletter form")
			}
			for range 2 {
				submit := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/newsletter", strings.NewReader(url.Values{"email": {"newsletter-reentry-" + uuid.NewString() + "@example.com"}}.Encode()))
				submit.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				submit.Header.Set("HX-Request", "true")
				submit.Header.Set("Accept-Language", language)
				sent := httptest.NewRecorder()
				router.ServeHTTP(sent, submit)
				if sent.Code != http.StatusOK || confirmationElement(t, sent.Body.String(), "newsletter-email") != nil {
					t.Fatal("HTMX fixture did not replace the editable form with its sent state")
				}
				link := confirmationReentryLink(t, sent.Body.String())
				// The ordinary fallback opens a different document after the
				// scripting-off POST redirect. On the homepage, hx-get must
				// issue a fresh request even though href only names a fragment.
				for key, want := range map[string]string{"href": "/#newsletter-form", "hx-get": "/", "hx-select": "#newsletter-form", "hx-target": "#newsletter-form", "hx-swap": "outerHTML"} {
					if got := confirmationAttribute(link, key); got != want {
						t.Fatalf("re-entry %s = %q, want %q", key, got, want)
					}
				}
				destination, err := url.Parse(confirmationAttribute(link, "hx-get"))
				if err != nil || destination == nil || destination.Path != "/" {
					t.Fatalf("invalid re-entry GET destination: %v, %v", destination, err)
				}
				reopened := httptest.NewRecorder()
				request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, destination.RequestURI(), http.NoBody)
				request.Header.Set("Accept-Language", language)
				request.Header.Set("HX-Request", "true")
				router.ServeHTTP(reopened, request)
				form := confirmationElement(t, reopened.Body.String(), "newsletter-form")
				input := confirmationElement(t, reopened.Body.String(), "newsletter-email")
				if reopened.Code != http.StatusOK || form == nil || input == nil {
					t.Fatalf("registered re-entry GET %q = %d, missing editable footer form", destination.RequestURI(), reopened.Code)
				}
				if confirmationAttribute(form, "method") != "post" || confirmationAttribute(form, "action") != "/newsletter" || confirmationAttribute(input, "value") != "" {
					t.Error("re-entry does not restore the empty original POST form")
				}
			}
		})
	}
}

func TestPlainNewsletterConfirmationReentryOpensTheHomepageForm(t *testing.T) {
	for _, language := range []string{"zh-Hant", "en"} {
		t.Run(language, func(t *testing.T) {
			router := confirmationRouter(t)
			address := "newsletter-plain-" + uuid.NewString() + "@example.com"
			posted := httptest.NewRecorder()
			submit := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/newsletter", strings.NewReader(url.Values{"email": {address}}.Encode()))
			submit.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			submit.Header.Set("Accept-Language", language)
			router.ServeHTTP(posted, submit)
			const destination = "/newsletter/thanks?address=n%2A%2A%2A%40example.com"
			if posted.Code != http.StatusSeeOther || posted.Header().Get("Location") != destination {
				t.Fatalf("plain newsletter = %d to %q, want 303 to %q", posted.Code, posted.Header().Get("Location"), destination)
			}
			shown := httptest.NewRecorder()
			get := httptest.NewRequestWithContext(t.Context(), http.MethodGet, destination, http.NoBody)
			get.Header.Set("Accept-Language", language)
			router.ServeHTTP(shown, get)
			if shown.Code != http.StatusOK || !strings.Contains(shown.Body.String(), "n***@example.com") || strings.Contains(shown.Body.String(), address) {
				t.Fatal("registered plain confirmation lost its masked destination")
			}
			link := confirmationReentryLink(t, shown.Body.String())
			if confirmationAttribute(link, "href") != "/#newsletter-form" {
				t.Fatal("plain confirmation does not return to the homepage form")
			}
			reentry, err := url.Parse(confirmationAttribute(link, "href"))
			if err != nil || reentry == nil || reentry.Path != "/" {
				t.Fatalf("invalid plain re-entry destination: %v, %v", reentry, err)
			}
			opened := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, reentry.RequestURI(), http.NoBody)
			request.Header.Set("Accept-Language", language)
			router.ServeHTTP(opened, request)
			form := confirmationElement(t, opened.Body.String(), "newsletter-form")
			input := confirmationElement(t, opened.Body.String(), "newsletter-email")
			if opened.Code != http.StatusOK || form == nil || input == nil {
				t.Fatal("registered plain re-entry did not restore the homepage form")
			}
			if confirmationAttribute(form, "method") != "post" || confirmationAttribute(form, "action") != "/newsletter" || confirmationAttribute(input, "value") != "" {
				t.Error("plain re-entry does not restore the empty original POST form")
			}
		})
	}
}

func confirmationRouter(t *testing.T) http.Handler {
	t.Helper()
	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	return newRouter(&RouterConfig{
		Storefront: StorefrontConfig{StorePool: pool, Payments: gateway, BaseURL: "http://127.0.0.1"},
		BackOffice: BackOfficeConfig{AdminPool: pool, Payments: gateway, Refunder: refunds.NewRefunder("")},
	}, slog.New(slog.DiscardHandler))
}

func confirmationElement(t *testing.T, body, id string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for n := range doc.Descendants() {
		if n.Type == html.ElementNode && confirmationAttribute(n, "id") == id {
			return n
		}
	}
	return nil
}

func confirmationReentryLink(t *testing.T, body string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for n := range doc.Descendants() {
		if n.Type != html.ElementNode || n.Data != "a" {
			continue
		}
		href, parseErr := url.Parse(confirmationAttribute(n, "href"))
		if parseErr == nil && href.Fragment == "newsletter-form" {
			return n
		}
	}
	t.Fatal("sent newsletter has no re-entry destination")
	return nil
}

func confirmationAttribute(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
