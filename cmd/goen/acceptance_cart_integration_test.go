//go:build integration

package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/payment"
)

func TestGuestCartSignInHTTPJourney(t *testing.T) {
	database := dbtest.Pool(t)
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(t.Context(), string(seed)); err != nil {
		t.Fatal(err)
	}
	rows, err := database.Query(t.Context(), `SELECT id::text FROM product_variants WHERE is_active ORDER BY id LIMIT 3`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	variants := make([]string, 0, 3)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		variants = append(variants, id)
	}
	rows.Close()
	if rows.Err() != nil || len(variants) != 3 {
		t.Fatalf("catalogue variants=%v error=%v", variants, rows.Err())
	}
	server := cartJourneyServer(t, database)
	for _, locale := range []string{"en", "zh-TW"} {
		t.Run(locale, func(t *testing.T) {
			credentials := account.Credentials{Email: uuid.NewString() + "@journey.invalid", Password: "a sufficiently long password"}
			user, err := account.NewStore(database).Register(t.Context(), &credentials)
			if err != nil {
				t.Fatal(err)
			}
			member, guest := cartJourneyClient(t), cartJourneyClient(t)
			form := url.Values{"email": {credentials.Email}, "password": {credentials.Password}, "next": {"/cart"}}
			cartJourneyRequest(t, member, server.URL, locale, "/signin", form, http.StatusSeeOther)
			add := func(client *http.Client, variant, quantity string) {
				t.Helper()
				cartJourneyRequest(t, client, server.URL, locale, "/cart/items", url.Values{"variant": {variant}, "quantity": {quantity}}, http.StatusSeeOther)
			}
			add(member, variants[0], "1")
			add(member, variants[1], "2")
			add(guest, variants[0], "2")
			add(guest, variants[2], "3")
			origin, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			var guestToken string
			for _, cookie := range guest.Jar.Cookies(origin) {
				if cookie.Name == "goen_cart" {
					guestToken = cookie.Value
				}
			}
			if guestToken == "" {
				t.Fatal("anonymous add did not create the browser cart cookie")
			}
			before := cartJourneyRows(t, database, user.ID, guestToken)
			form.Set("password", "an incorrect password")
			cartJourneyRequest(t, guest, server.URL, locale, "/signin", form, http.StatusUnprocessableEntity)
			if after := cartJourneyRows(t, database, user.ID, guestToken); after != before {
				t.Fatal("failed authentication changed ownership or cart lines")
			}
			for _, cookie := range guest.Jar.Cookies(origin) {
				if cookie.Name == "goen_session" {
					t.Fatal("failed authentication issued a session")
				}
			}
			guestLines := map[string]int{variants[0]: 2, variants[2]: 3}
			cartJourneyPage(t, guest, server.URL, locale, guestLines)
			form.Set("password", credentials.Password)
			cartJourneyRequest(t, guest, server.URL, locale, "/signin", form, http.StatusSeeOther)
			merged := map[string]int{variants[0]: 3, variants[1]: 2, variants[2]: 3}
			cartJourneyPage(t, guest, server.URL, locale, merged)
			// The previously signed-in device must see the same surviving cart.
			cartJourneyPage(t, member, server.URL, locale, merged)
			cartJourneyDatabase(t, database, user.ID, guestToken, merged)
			anonymous := cartJourneyClient(t)
			anonymous.Jar.SetCookies(origin, []*http.Cookie{{Name: "goen_cart", Value: guestToken, Path: "/"}})
			cartJourneyPage(t, anonymous, server.URL, locale, map[string]int{})
			t.Logf("C03 locale=%s wrong-password=422 sign-in=303 member-and-guest-lines=3,2,3 stale-guest-lines=0 database=one-owned-cart", locale)
		})
	}
}

func cartJourneyServer(t *testing.T, database *pgxpool.Pool) *httptest.Server {
	t.Helper()
	storePool, err := openPool(t.Context(), database.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(storePool.Close)
	adminPool, err := openAdminPool(t.Context(), database.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)
	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newRouter(&RouterConfig{Pool: storePool, AdminPool: adminPool,
		Payments: gateway, Refunder: admin.NewRefunder(""), BaseURL: "http://127.0.0.1"}, slog.New(slog.DiscardHandler)))
	t.Cleanup(server.Close)
	return server
}

func cartJourneyClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func cartJourneyRequest(t *testing.T, client *http.Client, origin, locale, path string, form url.Values, want int) string {
	t.Helper()
	method, body := http.MethodGet, ""
	if form != nil {
		method, body = http.MethodPost, form.Encode()
	}
	req, err := http.NewRequestWithContext(t.Context(), method, origin+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept-Language", locale)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	content, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, res.StatusCode, want, content)
	}
	if want == http.StatusSeeOther && res.Header.Get("Location") != "/cart" {
		t.Fatalf("%s location=%q want /cart", path, res.Header.Get("Location"))
	}
	return string(content)
}

func cartJourneyPage(t *testing.T, client *http.Client, origin, locale string, want map[string]int) {
	t.Helper()
	body := cartJourneyRequest(t, client, origin, locale, "/cart", nil, http.StatusOK)
	language := i18n.En.Tag()
	if locale == "zh-TW" {
		language = i18n.ZhHant.Tag()
	}
	if !strings.Contains(body, `<html lang="`+language+`"`) {
		t.Fatalf("cart page did not render requested language %q", language)
	}
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
		if node.Type != html.ElementNode || node.Data != "input" {
			return
		}
		var id, value string
		for _, attribute := range node.Attr {
			switch attribute.Key {
			case "id":
				id = attribute.Val
			case "value":
				value = attribute.Val
			}
		}
		if !strings.HasPrefix(id, "qty-") {
			return
		}
		quantity, err := strconv.Atoi(value)
		if err != nil {
			t.Fatal(err)
		}
		variant := strings.TrimPrefix(id, "qty-")
		if _, exists := got[variant]; exists {
			t.Fatalf("duplicate quantity control for %s", variant)
		}
		got[variant] = quantity
	}
	visit(document)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("visible cart lines=%v want=%v", got, want)
	}
}

func cartJourneyRows(t *testing.T, database *pgxpool.Pool, userID, guestToken string) string {
	t.Helper()
	var snapshot string
	if err := database.QueryRow(t.Context(), `SELECT coalesce(jsonb_agg(jsonb_build_object(
 'id', c.id, 'owner', c.user_id, 'variant', i.variant_id, 'quantity', i.quantity)
 ORDER BY c.id, i.variant_id), '[]'::jsonb)::text FROM carts c
 LEFT JOIN cart_items i ON i.cart_id = c.id WHERE c.user_id = $1 OR c.token_hash = $2`, userID, account.HashToken(guestToken)).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func cartJourneyDatabase(t *testing.T, database *pgxpool.Pool, userID, guestToken string, want map[string]int) {
	t.Helper()
	var owned, anonymous int
	if err := database.QueryRow(t.Context(), `SELECT
 (SELECT count(*) FROM carts WHERE user_id = $1),
 (SELECT count(*) FROM carts WHERE token_hash = $2 AND user_id IS NULL)`, userID, account.HashToken(guestToken)).Scan(&owned, &anonymous); err != nil {
		t.Fatal(err)
	}
	if owned != 1 || anonymous != 0 {
		t.Fatalf("owned carts=%d surviving guest carts=%d", owned, anonymous)
	}
	rows, err := database.Query(t.Context(), `SELECT i.variant_id::text, i.quantity FROM cart_items i JOIN carts c ON c.id = i.cart_id WHERE c.user_id = $1`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]int{}
	for rows.Next() {
		var variant string
		var quantity int
		if err := rows.Scan(&variant, &quantity); err != nil {
			t.Fatal(err)
		}
		got[variant] = quantity
	}
	if rows.Err() != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("authoritative cart lines=%v want=%v error=%v", got, want, rows.Err())
	}
}
