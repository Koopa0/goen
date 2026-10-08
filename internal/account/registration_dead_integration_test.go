//go:build integration

package account_test

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	htmlparse "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages/pagestest"
)

func TestDeadRegistrationLinksOfferRegistrationRecovery(t *testing.T) {
	for _, mode := range []string{"used", "expired", "unknown", "expired after lookup"} {
		for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			t.Run(mode+"/"+string(locale), func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				appPool := accountStorePool(t, "dead-registration-"+uuid.NewString())
				store := account.NewStore(appPool)
				h := account.NewHandler(store, nil, slog.New(slog.DiscardHandler), false, nil)
				addr, token := recoveryRegistration(t, h, store)
				var expiry *expiryAfterRegistrationLookup
				switch mode {
				case "used":
					if _, err := store.CompleteRegistration(ctx, token, cartOwnerPassword); err != nil {
						t.Fatalf("use the registration link: %v", err)
					}
				case "expired":
					expireRegistrationLink(t, token)
				case "unknown":
					token = "unknown-" + uuid.NewString()
				case "expired after lookup":
					expiry = &expiryAfterRegistrationLookup{digest: account.HashToken(token)}
					cfg := appPool.Config().Copy()
					cfg.ConnConfig.Tracer = expiry
					traced, err := pgxpool.NewWithConfig(ctx, cfg)
					if err != nil {
						t.Fatalf("open the traced store-role pool: %v", err)
					}
					t.Cleanup(traced.Close)
					var role string
					if err := traced.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil || role != "store" {
						t.Fatalf("traced application role = %q, error %v, want store", role, err)
					}
					h = account.NewHandler(account.NewStore(traced), nil, slog.New(slog.DiscardHandler), false, nil)
				default:
					t.Fatalf("unknown fixture mode %q", mode)
				}
				rec := httptest.NewRecorder()
				registrationRecoveryRoutes(h).ServeHTTP(rec, cartForm(ctx, "/register/complete", url.Values{
					"token": {token}, "password": {cartOwnerPassword}, "next": {"/checkout?stage=delivery"},
				}))
				if expiry != nil && (!expiry.fired.Load() || expiry.err != nil) {
					t.Fatalf("expiry after the first live lookup fired = %v, error %v", expiry.fired.Load(), expiry.err)
				}
				if err := ctx.Err(); err != nil {
					t.Fatalf("registration request context stopped before its refusal: %v", err)
				}
				body := html.UnescapeString(rec.Body.String())
				wantBody, wantLink := registrationRecoveryCopy(locale)
				if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, wantBody) ||
					!strings.Contains(body, wantLink) || !strings.Contains(body, `href="/register?next=%2Fcheckout%3Fstage%3Ddelivery&resend=1"`) {
					t.Errorf("dead registration = %d, want 422 with %q and its resend link; body=%s", rec.Code, wantBody, body)
				}
				if strings.Contains(body, "from your account page.") || strings.Contains(body, "請到會員中心重新寄一次。") ||
					strings.Contains(body, addr) || strings.Contains(body, token) || len(rec.Header().Values("Set-Cookie")) != 0 {
					t.Error("dead registration gives account-page directions, echoes the mailbox/token, or starts a session")
				}
			})
		}
	}
}

func TestRegistrationRecoveryFormResendsWithoutAPendingCookie(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, tt := range []struct {
			name, next, wantNext, wantHref string
		}{
			{name: "checkout", next: "/checkout?stage=delivery", wantNext: "/checkout?stage=delivery", wantHref: "/register?next=%2Fcheckout%3Fstage%3Ddelivery&resend=1"},
			{name: "external landing", next: "https://example.com/checkout", wantNext: "/account", wantHref: "/register?next=%2Faccount&resend=1"},
		} {
			t.Run(string(locale)+"/"+tt.name, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				store := account.NewStore(accountStorePool(t, "registration-resend-"+uuid.NewString()))
				h := account.NewHandler(store, nil, slog.New(slog.DiscardHandler), false, nil)
				addr, token := recoveryRegistration(t, h, store)
				expireRegistrationLink(t, token)
				mux := registrationRecoveryRoutes(h)
				dead := httptest.NewRecorder()
				mux.ServeHTTP(dead, cartForm(ctx, "/register/complete", url.Values{
					"token": {token}, "password": {cartOwnerPassword}, "next": {tt.next},
				}))
				if dead.Code != http.StatusUnprocessableEntity {
					t.Fatalf("dead registration page = %d, want 422", dead.Code)
				}
				href := registrationRecoveryHref(t, dead.Body.String(), locale)
				if href != tt.wantHref {
					t.Errorf("registration recovery href = %q, want %q", href, tt.wantHref)
				}
				page := httptest.NewRecorder()
				mux.ServeHTTP(page, httptest.NewRequestWithContext(ctx, http.MethodGet, href, http.NoBody))
				if page.Code != http.StatusOK {
					t.Fatalf("registration resend page = %d, want 200", page.Code)
				}
				if strings.Contains(page.Body.String(), `id="register-sent"`) {
					t.Error("the resend form says mail was sent before anyone requested it")
				}
				correction := "信箱打錯了？換一個重新註冊"
				if locale == i18n.En {
					correction = "Wrong address? Register again"
				}
				if strings.Contains(html.UnescapeString(page.Body.String()), correction) {
					t.Errorf("the resend entry offers address correction %q before any address was entered", correction)
				}
				form := readRegistrationResendForm(t, page.Body.String(), tt.wantNext)
				form.Set("email", addr)
				sent := httptest.NewRecorder()
				mux.ServeHTTP(sent, cartForm(ctx, "/register/resend", form))
				if sent.Code != http.StatusSeeOther || sent.Header().Get("Location") != "/register?sent=1&again=1" {
					t.Fatalf("resend from the recovered form = %d %q, want 303 /register?sent=1&again=1", sent.Code, sent.Header().Get("Location"))
				}
				sentRequest := httptest.NewRequestWithContext(ctx, http.MethodGet, sent.Header().Get("Location"), http.NoBody)
				for _, cookie := range sent.Result().Cookies() {
					sentRequest.AddCookie(cookie)
				}
				sentPage := httptest.NewRecorder()
				mux.ServeHTTP(sentPage, sentRequest)
				if sentPage.Code != http.StatusOK || !strings.Contains(sentPage.Body.String(), `id="register-sent"`) ||
					!strings.Contains(html.UnescapeString(sentPage.Body.String()), correction) {
					t.Error("the sent page lost its notice or address correction link")
				}
				if n := followUpRegistrations(t, store, addr, neverTold(t)); n != 1 {
					t.Fatalf("resending queued %d registration requests, want 1", n)
				}
				fresh, next := queuedLink(t, addr)
				if fresh == token {
					t.Fatal("the resent link kept the dead token")
				}
				if next != tt.wantNext {
					t.Errorf("the resent link lands at %q, want %q", next, tt.wantNext)
				}
				completed := httptest.NewRecorder()
				mux.ServeHTTP(completed, cartForm(ctx, "/register/complete", url.Values{
					"token": {fresh}, "password": {cartOwnerPassword}, "next": {next},
				}))
				wantLanding := "/account?welcome=1"
				if tt.wantNext != "/account" {
					wantLanding = "/account?" + url.Values{"welcome": {"1"}, "next": {tt.wantNext}}.Encode()
				}
				if completed.Code != http.StatusSeeOther || completed.Header().Get("Location") != wantLanding {
					t.Fatalf("the resent link = %d %q, want 303 %s", completed.Code, completed.Header().Get("Location"), wantLanding)
				}
				sessionCookie(t, completed)
			})
		}
	}
}

func TestDeadEmailChangeLinksKeepTheirAccountDirections(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			store := account.NewStore(accountStorePool(t, "email-change-dead-"+uuid.NewString()))
			u := registerProved(t, store, "change-dead-"+uuid.NewString()+"@example.com")
			token := requestVerification(t, store, u.ID, "change-new-"+uuid.NewString()+"@example.com")
			expireRegistrationLink(t, token)
			b := changeBrowser{t: t, h: account.NewHandler(store, nil, slog.New(slog.DiscardHandler), false, nil)}
			rec := b.serve(b.h.Verify, cartForm(ctx, "/verify", url.Values{"token": {token}}), b.signIn(u.Email))
			heading, reason := "This link is no longer valid", "It may have been used already, or be more than two days old."
			if locale == i18n.ZhHant {
				heading = "\u9019\u500b\u9023\u7d50\u5df2\u5931\u6548"
				reason = "\u9023\u7d50\u53ef\u80fd\u5df2\u7d93\u7528\u904e\u6216\u8d85\u904e\u5169\u5929\u3002"
			}
			pagestest.AssertEmailLink(t, rec.Body.String(), heading, reason, "/account#email-heading")
			if rec.Code != http.StatusUnprocessableEntity ||
				strings.Contains(rec.Body.String(), `href="/register?`) {
				t.Errorf("dead email change = %d, want 422 without registration recovery", rec.Code)
			}
		})
	}
}

func registrationRecoveryRoutes(h *account.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /register", h.RegisterPage)
	mux.HandleFunc("POST /register/resend", h.ResendRegistration)
	mux.HandleFunc("POST /register/complete", h.CompleteRegistration)
	return mux
}

func recoveryRegistration(t *testing.T, h *account.Handler, store *account.Store) (addr, token string) {
	t.Helper()
	addr = "registration-recovery-" + uuid.NewString() + "@example.com"
	rec := httptest.NewRecorder()
	h.Register(rec, registrationForm(t.Context(), addr, cartOwnerPassword, "/account"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("register the recovery fixture = %d, want 303", rec.Code)
	}
	if n := followUpRegistrations(t, store, addr, neverTold(t)); n != 1 {
		t.Fatalf("registration queued %d requests, want 1", n)
	}
	token, _ = queuedLink(t, addr)
	return addr, token
}

func expireRegistrationLink(t *testing.T, token string) {
	t.Helper()
	tag, err := pool.Exec(t.Context(), `UPDATE email_verifications
		SET created_at = now() - interval '49 hours', expires_at = now() - interval '1 hour'
		WHERE digest = $1`, account.HashToken(token))
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("expire the fixture link = %d rows, error %v, want 1 row", tag.RowsAffected(), err)
	}
}

func registrationRecoveryCopy(locale i18n.Locale) (body, link string) {
	if locale == i18n.En {
		return "This sign-up link no longer works (used already, or more than two days old). Ask for a new one.", "Send a new link"
	}
	return "這個註冊連結已失效（已經用過，或超過兩天）。請重新寄一封註冊信。", "重新寄註冊信"
}

func registrationRecoveryHref(t *testing.T, page string, locale i18n.Locale) string {
	t.Helper()
	_, wantLabel := registrationRecoveryCopy(locale)
	z := htmlparse.NewTokenizer(strings.NewReader(page))
	for z.Next() != htmlparse.ErrorToken {
		token := z.Token()
		if token.Type != htmlparse.StartTagToken || token.Data != "a" {
			continue
		}
		var href string
		for _, attr := range token.Attr {
			if attr.Key == "href" {
				href = attr.Val
			}
		}
		if z.Next() == htmlparse.TextToken && strings.TrimSpace(string(z.Text())) == wantLabel {
			return href
		}
	}
	t.Fatalf("the dead registration page has no recovery link labelled %q", wantLabel)
	return ""
}

type registrationResendForm struct {
	Method, Action, EmailID, LabelFor, EmailType string
	Required                                     bool
	Values                                       url.Values
}

func readRegistrationResendForm(t *testing.T, page, next string) url.Values {
	t.Helper()
	root, err := htmlparse.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatalf("parse the recovered resend page: %v", err)
	}
	var got registrationResendForm
	got.Values = url.Values{}
	var visit func(*htmlparse.Node, bool)
	visit = func(n *htmlparse.Node, inForm bool) {
		attrs := map[string]string{}
		for _, attr := range n.Attr {
			attrs[attr.Key] = attr.Val
		}
		if n.Type == htmlparse.ElementNode && n.Data == "form" {
			inForm = attrs["action"] == "/register/resend"
			if inForm {
				got.Action, got.Method = attrs["action"], attrs["method"]
			}
		}
		if inForm && n.Type == htmlparse.ElementNode {
			switch n.Data {
			case "label":
				got.LabelFor = attrs["for"]
			case "input":
				got.Values.Set(attrs["name"], attrs["value"])
				if attrs["name"] == "email" {
					got.EmailID, got.EmailType = attrs["id"], attrs["type"]
					_, got.Required = attrs["required"]
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child, inForm)
		}
	}
	visit(root, false)
	want := registrationResendForm{
		Method: "post", Action: "/register/resend", EmailID: "email", LabelFor: "email", EmailType: "email", Required: true,
		Values: url.Values{"email": {""}, "next": {next}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the recovered resend form without a cookie (-want +got):\n%s", diff)
	}
	return got.Values
}

type registrationLookupKey struct{}

type expiryAfterRegistrationLookup struct {
	digest []byte
	fired  atomic.Bool
	err    error
}

func (e *expiryAfterRegistrationLookup) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "-- name: EmailVerificationToken :one") && len(data.Args) == 1 {
		if digest, ok := data.Args[0].([]byte); ok && bytes.Equal(digest, e.digest) {
			return context.WithValue(ctx, registrationLookupKey{}, e)
		}
	}
	return ctx
}

func (e *expiryAfterRegistrationLookup) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if ctx.Value(registrationLookupKey{}) != e || data.Err != nil || data.CommandTag.RowsAffected() != 1 || !e.fired.CompareAndSwap(false, true) {
		return
	}
	// Expire only this token after preflight returned a live row, before the
	// completion store rereads it; the application still executes its real SQL.
	var tag pgconn.CommandTag
	tag, e.err = pool.Exec(ctx, `UPDATE email_verifications
		SET created_at = now() - interval '49 hours', expires_at = now() - interval '1 hour'
		WHERE digest = $1`, e.digest)
	if e.err == nil && tag.RowsAffected() != 1 {
		e.err = fmt.Errorf("expire after preflight: affected %d rows, want 1", tag.RowsAffected())
	}
}
