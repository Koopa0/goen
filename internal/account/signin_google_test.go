package account

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestSignInProductReturnDoesNotClaimTheHeartWasSaved(t *testing.T) {
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		want := "To save it, press"
		if locale == i18n.ZhHant {
			want = "\u82e5\u8981\u52a0\u5165\u9858\u671b\u6e05\u55ae\uff0c\u8acb\u518d\u6309"
		}
		if got := signInReturnMessage(ctx, "/p/a-product?option=one"); !strings.Contains(got, want) || !strings.Contains(got, i18n.T(ctx, i18n.KeyWishlistAdd)) {
			t.Errorf("product return omits the explicit wishlist action: %q", got)
		}
		if got := signInReturnMessage(ctx, "https://elsewhere.invalid/private-token"); got != "" {
			t.Errorf("unsafe return exposes context: %q", got)
		}
	}
}

func TestSignInContextRejectsMalformedOrUnrelatedSuggestions(t *testing.T) {
	h := rateLimitedAccountHandler(deadAccountStore(t))
	for _, value := range []string{"reset:not-base64!", "other:" + base64.RawURLEncoding.EncodeToString([]byte("suggested@example.com")), "reset:" + base64.RawURLEncoding.EncodeToString([]byte("not an address")), strings.Repeat("a", 513)} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/signin?reset=1", http.NoBody)
		request.AddCookie(&http.Cookie{Name: h.signInContextCookie(), Value: value}) //nolint:gosec // G124: an untrusted request cookie.
		response := httptest.NewRecorder()
		if purpose, address := h.takeSignInContext(response, request); purpose != "" || address != "" {
			t.Errorf("malformed suggestion accepted: purpose=%q address=%q", purpose, address)
		}
		if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].MaxAge != -1 {
			t.Errorf("malformed suggestion was not consumed: %v", cookies)
		}
	}
	written := httptest.NewRecorder()
	h.writeSignInContext(written, signInBeforeErasure, "suggested@example.com")
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/signin?reset=1", http.NoBody)
	request.AddCookie(written.Result().Cookies()[0])
	response := httptest.NewRecorder()
	h.SignInPage(response, request)
	if strings.Contains(response.Body.String(), "suggested@example.com") {
		t.Error("reset sign-in accepted an erasure suggestion")
	}
}

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
					Status                        int
					EmailInvalid, PasswordInvalid bool
					GoogleAlerts                  int
					Recovery                      []string
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

func TestSignInPrefillIsPrivateAndConsumed(t *testing.T) {
	for _, purpose := range []signInPurpose{signInAfterReset, signInBeforeErasure} {
		for _, secure := range []bool{false, true} {
			t.Run(string(purpose)+"/"+strconv.FormatBool(secure), func(t *testing.T) {
				h := rateLimitedAccountHandler(deadAccountStore(t))
				h.secure = secure
				const address = "private-prefill@example.com"
				written := httptest.NewRecorder()
				h.writeSignInContext(written, purpose, address)
				cookies := written.Result().Cookies()
				if len(cookies) != 1 {
					t.Fatalf("prefill cookies = %d, want 1", len(cookies))
				}
				type cookiePolicy struct {
					Name             string
					MaxAge           int
					HTTPOnly, Secure bool
					SameSite         http.SameSite
				}
				got := cookiePolicy{cookies[0].Name, cookies[0].MaxAge, cookies[0].HttpOnly, cookies[0].Secure, cookies[0].SameSite}
				name := "goen_signin_context"
				if secure {
					name = "__Host-goen_signin_context"
				}
				want := cookiePolicy{Name: name, MaxAge: 120, HTTPOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("private prefill cookie (-want +got):\n%s", diff)
				}
				target := "/signin?reset=1"
				if purpose == signInBeforeErasure {
					target = "/signin?next=%2Faccount&reauth=erase"
				}
				request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
				request.AddCookie(cookies[0])
				page := httptest.NewRecorder()
				h.SignInPage(page, request)
				doc, err := html.Parse(strings.NewReader(page.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				type formState struct {
					Email                                       string
					EmailFocus, PasswordFocus, Reauthentication bool
					RegisterLinks                               int
				}
				var state formState
				for node := range doc.Descendants() {
					attrs := map[string]string{}
					for _, a := range node.Attr {
						attrs[a.Key] = a.Val
					}
					if node.Data == "input" && attrs["id"] == "email" {
						state.Email = attrs["value"]
						_, state.EmailFocus = attrs["autofocus"]
					}
					if node.Data == "input" && attrs["id"] == "password" {
						_, state.PasswordFocus = attrs["autofocus"]
					}
					if node.Data == "input" && attrs["name"] == "reauth" && attrs["value"] == "erase" {
						state.Reauthentication = true
					}
					if node.Data == "a" && attrs["href"] == "/register" {
						state.RegisterLinks++
					}
				}
				expected := formState{Email: address, PasswordFocus: true, RegisterLinks: 1}
				if purpose == signInBeforeErasure {
					expected.RegisterLinks = 0
					expected.Reauthentication = true
				}
				if diff := cmp.Diff(expected, state); diff != "" {
					t.Errorf("prefilled sign-in form (-want +got):\n%s", diff)
				}
				cleared := page.Result().Cookies()
				if len(cleared) != 1 || cleared[0].Name != name || cleared[0].MaxAge != -1 {
					t.Errorf("prefill was not consumed: %+v", cleared)
				}
				second := httptest.NewRecorder()
				h.SignInPage(second, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody))
				if strings.Contains(second.Body.String(), address) {
					t.Error("refresh exposed the consumed address without its private cookie")
				}
			})
		}
	}
}
