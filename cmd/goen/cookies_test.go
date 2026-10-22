package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/auth/google"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/site"
)

// cookiePin is one cookie goen sets, written out by value. A browser matches a
// cookie by name, so changing a name after release does not rename anything:
// the old cookie is simply never read again. A renamed session signs everyone
// out, a renamed cart empties every basket, and a renamed placed cookie cuts a
// guest off the page of the order they just paid for.
//
// Every name here is a literal on purpose. A constant imported from the code
// under test would change with the code and pin nothing.
type cookiePin struct {
	purpose string
	// secure is the name served over https; dev the name served over plain
	// http. The browser refuses a __Host- cookie without Secure, so a
	// development server cannot use the prefixed name.
	secure, dev string
	// path, maxAge and sameSite are what a probe must observe in both modes.
	// The cookies with no probe are pinned by name alone.
	path     string
	maxAge   int
	sameSite http.SameSite
	// probe makes the real code set the cookie and returns what it sent; nil
	// where only code inside the owning package can reach the writer.
	probe func(t *testing.T, secure bool) *http.Cookie
}

const (
	cookieDay  = 24 * 60 * 60
	cookieYear = 365 * cookieDay
)

// idleCookiePool is a pool nothing listens behind: the probes below reach only
// code that sets a cookie before it would query.
func idleCookiePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatalf("open an unused pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func onlyCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("response set %d cookies, want exactly 1: %v", len(cookies), cookies)
	}
	return cookies[0]
}

// goenCookies lists the eleven cookies the application sets: nine with a
// __Host- name over https and a plain one over http, and two with one plain
// name in both modes because their Path is not "/", which __Host- forbids.
func goenCookies(pool *pgxpool.Pool) map[string]cookiePin {
	return map[string]cookiePin{
		"session": {
			purpose: "the signed-in customer's session token",
			secure:  "__Host-goen_session", dev: "goen_session",
			path: "/", maxAge: 44 * cookieDay, sameSite: http.SameSiteStrictMode,
			probe: func(t *testing.T, secure bool) *http.Cookie {
				t.Helper()
				rec := httptest.NewRecorder()
				auth.SetSessionCookie(rec, "token", secure)
				return onlyCookie(t, rec)
			},
		},
		"device": {
			purpose: "the token of the browser's remembered row, which keeps the owner's sign-in off the account's guess budget",
			secure:  "__Host-goen_device", dev: "goen_device",
			path: "/", maxAge: 90 * cookieDay, sameSite: http.SameSiteStrictMode,
			probe: func(t *testing.T, secure bool) *http.Cookie {
				t.Helper()
				rec := httptest.NewRecorder()
				auth.SetDeviceCookie(rec, "token", secure)
				return onlyCookie(t, rec)
			},
		},
		"cart": {
			purpose: "the anonymous cart's token",
			secure:  "__Host-goen_cart", dev: "goen_cart",
			path: "/", maxAge: 30 * cookieDay, sameSite: http.SameSiteLaxMode,
			probe: func(t *testing.T, secure bool) *http.Cookie {
				t.Helper()
				rec := httptest.NewRecorder()
				cart.SetCookie(rec, "token", secure)
				return onlyCookie(t, rec)
			},
		},
		"locale": {
			purpose: "the visitor's chosen language",
			secure:  "__Host-goen_locale", dev: "goen_locale",
			path: "/", maxAge: cookieYear, sameSite: http.SameSiteLaxMode,
			probe: func(t *testing.T, secure bool) *http.Cookie {
				t.Helper()
				rec := httptest.NewRecorder()
				i18n.SetCookie(rec, i18n.En, secure)
				return onlyCookie(t, rec)
			},
		},
		"promo": {
			purpose: "the digest of the promotion strip the visitor dismissed",
			secure:  "__Host-goen_promo", dev: "goen_promo",
			path: "/", maxAge: cookieYear, sameSite: http.SameSiteLaxMode,
			probe: func(t *testing.T, secure bool) *http.Cookie {
				t.Helper()
				h := site.NewHandler(slog.New(slog.DiscardHandler), "", catalog.NewStore(pool, slog.New(slog.DiscardHandler)), campaign.NewStore(pool), site.NewStore(pool), secure)
				rec := httptest.NewRecorder()
				req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/promo/dismiss",
					strings.NewReader("banner=banner&return=/"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				h.DismissBanner(rec, req)
				return onlyCookie(t, rec)
			},
		},
		"oauth": {
			purpose: "the state and verifier of a Google sign-in in flight",
			secure:  "__Host-goen_oauth", dev: "goen_oauth",
			path: "/", maxAge: 10 * 60, sameSite: http.SameSiteLaxMode,
			probe: func(t *testing.T, secure bool) *http.Cookie {
				t.Helper()
				client, err := google.New("client", "secret", "https://goen.test")
				if err != nil {
					t.Fatalf("build the Google client: %v", err)
				}
				h := auth.NewHandler(auth.NewStore(pool), nil, nil, slog.New(slog.DiscardHandler), secure, client)
				rec := httptest.NewRecorder()
				h.GoogleSignIn(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/signin/google", nil))
				return onlyCookie(t, rec)
			},
		},
		"placed": {
			purpose: "the grants that let a guest open the orders they placed",
			secure:  "__Host-goen_placed", dev: "goen_placed",
			// Revoke is the one writer reachable without a database, and it
			// sends the expiry; the grant's own lifetime is access.Retain,
			// checked in the test body.
			path: "/", maxAge: -1, sameSite: http.SameSiteLaxMode,
			probe: func(t *testing.T, secure bool) *http.Cookie {
				t.Helper()
				rec := httptest.NewRecorder()
				req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
				if err := access.NewStore(pool, secure).Revoke(rec, req); err != nil {
					t.Fatalf("revoke with nothing presented: %v", err)
				}
				return onlyCookie(t, rec)
			},
		},
		"pickup": {
			purpose: "the convenience-store pick-up nonce and the checkout choices that travel with it",
			secure:  "__Host-goen_pickup", dev: "goen_pickup",
		},
		"signin_context": {
			purpose: "the address a sign-in form is suggested after a reset or an erasure",
			secure:  "__Host-goen_signin_context", dev: "goen_signin_context",
		},
		"register_sent": {
			purpose: "the address a registration mail was sent to, scoped to /register",
			secure:  "goen_register_sent", dev: "goen_register_sent",
		},
		"password_setup": {
			purpose: "the display receipt that a password was just set, scoped to /account",
			secure:  "goen_password_setup", dev: "goen_password_setup",
		},
	}
}

// TestCookieNames: the production source holds exactly the pinned cookie names,
// and each cookie that can be probed is set with its pinned name, Path, MaxAge,
// SameSite, HttpOnly and Secure in both the https and the http posture.
//
// It is the tripwire on the names a browser holds. It is written to go red when
// anyone edits a name, however the code around it is later arranged, and to ask
// for a decision when a new cookie appears.
func TestCookieNames(t *testing.T) {
	t.Parallel()
	pool := idleCookiePool(t)
	pins := goenCookies(pool)

	t.Run("the production source names exactly these cookies", func(t *testing.T) {
		t.Parallel()
		inSource := cookieNameLiterals(t)
		for id, pin := range pins {
			for _, name := range slices.Compact([]string{pin.secure, pin.dev}) {
				if !inSource[name] {
					t.Errorf("%s: no production source holds the name %q; it was renamed or removed, "+
						"and every browser holding the old cookie loses what it carried", id, name)
				}
			}
		}
		known := map[string]bool{}
		for _, pin := range pins {
			known[pin.secure], known[pin.dev] = true, true
		}
		for name := range inSource {
			if strings.HasPrefix(name, "__Host-") && !known[name] {
				t.Errorf("production source holds the cookie name %q that this test does not pin; "+
					"add it to goenCookies with its plain development name", name)
			}
		}
	})

	for id, pin := range pins {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			if strings.HasPrefix(pin.secure, "__Host-") {
				if pin.secure != "__Host-"+pin.dev {
					t.Errorf("secure name %q is not the development name %q with the __Host- prefix", pin.secure, pin.dev)
				}
			} else if pin.secure != pin.dev {
				t.Errorf("%q has no __Host- prefix, so one name serves both modes; got %q in development", pin.secure, pin.dev)
			}
			if pin.probe == nil {
				return
			}
			for _, mode := range []struct {
				name   string
				secure bool
				want   string
			}{
				{"https", true, pin.secure},
				{"http", false, pin.dev},
			} {
				got := pin.probe(t, mode.secure)
				if got.Name != mode.want {
					t.Errorf("%s: cookie is named %q, want %q (%s)", mode.name, got.Name, mode.want, pin.purpose)
				}
				if got.Path != pin.path {
					t.Errorf("%s: Path = %q, want %q", mode.name, got.Path, pin.path)
				}
				if got.Domain != "" {
					t.Errorf("%s: Domain = %q, want none: a Domain widens the cookie to sibling subdomains", mode.name, got.Domain)
				}
				if got.MaxAge != pin.maxAge {
					t.Errorf("%s: MaxAge = %d, want %d", mode.name, got.MaxAge, pin.maxAge)
				}
				if !got.HttpOnly {
					t.Errorf("%s: HttpOnly is off; a script on the page could read %q", mode.name, got.Name)
				}
				if got.SameSite != pin.sameSite {
					t.Errorf("%s: SameSite = %s, want %s", mode.name, sameSiteName(got.SameSite), sameSiteName(pin.sameSite))
				}
				if got.Secure != mode.secure {
					t.Errorf("%s: Secure = %t, want %t", mode.name, got.Secure, mode.secure)
				}
			}
		})
	}

	t.Run("a placed-order grant lives as long as its cookie", func(t *testing.T) {
		t.Parallel()
		if access.Retain != 30*24*time.Hour {
			t.Errorf("access.Retain = %v, want 720h0m0s: the cookie's MaxAge is derived from it", access.Retain)
		}
	})
}

func sameSiteName(s http.SameSite) string {
	switch s {
	case http.SameSiteLaxMode:
		return "Lax"
	case http.SameSiteStrictMode:
		return "Strict"
	case http.SameSiteNoneMode:
		return "None"
	case http.SameSiteDefaultMode:
		return "Default"
	}
	return "unset"
}

// cookieNameLiterals returns every string literal in production code that
// looks like a goen cookie name. It reads literals, not constants or function
// results, so how the names are arranged does not matter, only what they say.
func cookieNameLiterals(t *testing.T) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	fset := token.NewFileSet()
	for _, dir := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				if value, err := strconv.Unquote(lit.Value); err == nil &&
					(strings.HasPrefix(value, "__Host-goen_") || strings.HasPrefix(value, "goen_")) {
					found[value] = true
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("read production source under %s: %v", dir, err)
		}
	}
	if len(found) == 0 {
		t.Fatal("found no goen_ literal at all; the walk is reading the wrong tree")
	}
	return found
}
