package account

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestARefusedSignInKeepsTheConfiguredWaysIn(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, enabled := range []bool{false, true} {
			h := rateLimitedAccountHandler(deadAccountStore(t))
			if enabled {
				h.google = &Google{clientID: "configured"}
			}
			ctx := i18n.WithLocale(t.Context(), locale)
			page := httptest.NewRecorder()
			h.SignInPage(page, httptest.NewRequestWithContext(ctx, http.MethodGet, "/signin?next=/orders", http.NoBody))
			values := url.Values{"email": {"nobody@example.com"}, "password": {strings.Repeat("x", MaxPasswordBytes+1)}, "next": {"/orders"}}
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/signin", strings.NewReader(values.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			failed := httptest.NewRecorder()
			h.SignIn(failed, req)
			if failed.Code != http.StatusUnprocessableEntity {
				t.Fatalf("refused sign-in status = %d, want 422", failed.Code)
			}
			for name, body := range map[string]string{"initial": page.Body.String(), "refused": failed.Body.String()} {
				if got := strings.Contains(body, `href="/auth/google?next=%2Forders"`); got != enabled {
					t.Errorf("%s page, locale %s: Google offered = %t, want %t", name, locale, got, enabled)
				}
			}
			if !strings.Contains(failed.Body.String(), `value="nobody@example.com"`) || !strings.Contains(failed.Body.String(), `name="next" value="/orders"`) {
				t.Error("refused sign-in lost the submitted address or return path")
			}
		}
	}
}

func TestSignInFeedbackSelectsTheRelevantFields(t *testing.T) {
	for _, locale := range i18n.Locales() {
		for _, outcome := range []string{"failed", "state", "collision", "unverified", "demo", "password"} {
			t.Run(locale.Tag()+"/"+outcome, func(t *testing.T) {
				h := rateLimitedAccountHandler(deadAccountStore(t))
				ctx := i18n.WithLocale(t.Context(), locale)
				res := httptest.NewRecorder()
				if outcome == "password" {
					form := url.Values{"email": {"nobody@example.com"}, "password": {strings.Repeat("x", MaxPasswordBytes+1)}, "next": {"/account"}}
					req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/signin", strings.NewReader(form.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					h.SignIn(res, req)
				} else {
					h.SignInPage(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/signin?oauth="+outcome, http.NoBody))
				}
				doc, err := html.Parse(strings.NewReader(res.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				type feedback struct {
					Status                           int
					EmailInvalid, PasswordInvalid   bool
					GoogleAlerts                    int
					Recovery                        []string
				}
				got := feedback{Status: res.Code}
				for node := range doc.Descendants() {
					if node.Type != html.ElementNode {
						continue
					}
					attrs := map[string]string{}
					for _, a := range node.Attr {
						attrs[a.Key] = a.Val
					}
					if node.Data == "input" {
						switch attrs["id"] {
						case "email":
							got.EmailInvalid = attrs["aria-invalid"] == "true"
						case "password":
							got.PasswordInvalid = attrs["aria-invalid"] == "true"
						}
					}
					if attrs["id"] == "signin-google-error" && attrs["role"] == "alert" {
						got.GoogleAlerts++
						for child := range node.Descendants() {
							if child.Type == html.ElementNode && child.Data == "a" {
								for _, a := range child.Attr {
									if a.Key == "href" {
										got.Recovery = append(got.Recovery, a.Val)
									}
								}
							}
						}
					}
				}
				want := feedback{Status: http.StatusOK, GoogleAlerts: 1}
				switch outcome {
				case "password":
					want = feedback{Status: http.StatusUnprocessableEntity, PasswordInvalid: true}
				case "collision":
					want.Recovery = []string{"/forgot"}
				case "unverified":
					want.Recovery = []string{"/register"}
				}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("sign-in feedback (-want +got):\n%s", diff)
				}
			})
		}
	}
}

func TestSignInExplainsTheWishlistReturn(t *testing.T) {
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			h := rateLimitedAccountHandler(deadAccountStore(t))
			res := httptest.NewRecorder()
			ctx := i18n.WithLocale(t.Context(), locale)
			h.SignInPage(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/signin?next=%2Faccount%2Fwishlist", http.NoBody))
			want := "Please sign in. You will return to your wishlist after signing in."
			if locale == i18n.ZhHant {
				want = "\u8acb\u5148\u767b\u5165\uff0c\u767b\u5165\u5f8c\u6703\u56de\u5230\u9858\u671b\u6e05\u55ae\u3002"
			}
			if !strings.Contains(res.Body.String(), want) {
				t.Errorf("sign-in omits return context %q", want)
			}
		})
	}
}
