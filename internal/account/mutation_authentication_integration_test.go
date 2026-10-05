//go:build integration

package account_test

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
)

func TestAccountMutationsDistinguishWrongPasswordFromAuthenticationFailure(t *testing.T) {
	type accountMutationFacts struct {
		OriginalPassword, OriginalEmail, NoLastLogin bool
		Sessions, EmailLinks                         int64
	}
	owner := dbtest.Pool(t)
	parentCtx := t.Context()
	password := "a valid current password"
	hash, hashErr := account.HashPassword(password)
	if hashErr != nil {
		t.Fatal(hashErr)
	}
	addr := "mutation-auth@example.com"
	var id uuid.UUID
	if err := owner.QueryRow(parentCtx, `
		INSERT INTO users(email,password_hash,email_verified_at,role)
		VALUES($1,$2,now(),'customer') RETURNING id`, addr, hash).Scan(&id); err != nil {
		t.Fatal(err)
	}
	s := account.NewStore(mutationAuthenticationPool(t, owner))
	token, sessionErr := s.StartSession(parentCtx, id.String(), "authentication refusal test", "127.0.0.1")
	if sessionErr != nil {
		t.Fatal(sessionErr)
	}
	cookies := httptest.NewRecorder()
	account.SetSessionCookie(cookies, token, false)
	cookie := cookies.Result().Cookies()[0]
	var diagnostics bytes.Buffer
	h := account.NewHandler(s, nil, slog.New(slog.NewJSONHandler(&diagnostics, nil)), false, nil)
	mux := http.NewServeMux()
	mux.Handle("POST /account/password", h.Authenticate(h.RequireUser(h.ChangePassword)))
	mux.Handle("POST /account/email", h.Authenticate(h.RequireUser(h.ChangeEmail)))
	for _, backendFailure := range []bool{false, true} {
		if backendFailure {
			// A correct password reaches TouchLastLogin; the CHECK fails only that write.
			if _, err := owner.Exec(parentCtx, `ALTER TABLE users ADD CONSTRAINT refuse_login_touch CHECK(last_login_at IS NULL)`); err != nil {
				t.Fatal(err)
			}
		}
		for _, path := range []string{"/account/password", "/account/email"} {
			for _, locale := range i18n.Locales() {
				phase := "wrong password"
				current := "an incorrect current password"
				if backendFailure {
					phase = "database refusal after correct password"
					current = password
				}
				t.Run(locale.Tag()+path+"/"+phase, func(t *testing.T) {
					diagnostics.Reset()
					form := url.Values{
						"current": {current}, "password": {"an acceptable next password"},
						"confirm": {"an acceptable next password"}, "email": {"changed@example.com"},
					}
					ctx := i18n.WithLocale(t.Context(), locale)
					request := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(form.Encode()))
					request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					request.AddCookie(cookie)
					response := httptest.NewRecorder()
					mux.ServeHTTP(response, request)
					if backendFailure {
						assertMutationAuthenticationFailure(t, ctx, response, diagnostics.Bytes(), password)
					} else {
						if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/account?password=wrong" {
							t.Errorf("wrong password = %d %q, want 303 /account?password=wrong", response.Code, response.Header().Get("Location"))
						}
						if diagnostics.Len() != 0 {
							t.Errorf("wrong password logged an operational error: %s", diagnostics.String())
						}
					}
					if len(response.Result().Cookies()) != 0 {
						t.Error("refused authentication changed the browser session cookie")
					}
					var facts accountMutationFacts
					if err := owner.QueryRow(ctx, `
						SELECT password_hash=$2,email=$3,last_login_at IS NULL,
						       (SELECT count(*) FROM sessions WHERE user_id=$1),
						       (SELECT count(*) FROM email_verifications WHERE user_id=$1)
						FROM users WHERE id=$1`, id, hash, addr).Scan(&facts.OriginalPassword, &facts.OriginalEmail, &facts.NoLastLogin, &facts.Sessions, &facts.EmailLinks); err != nil {
						t.Fatal(err)
					}
					want := accountMutationFacts{OriginalPassword: true, OriginalEmail: true, NoLastLogin: true, Sessions: 1, EmailLinks: 0}
					if diff := cmp.Diff(want, facts); diff != "" {
						t.Errorf("refused account mutation (-want +got):\n%s", diff)
					}
				})
			}
		}
	}
}

func mutationAuthenticationPool(t *testing.T, owner *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	cfg := owner.Config().Copy()
	cfg.MaxConns = 2
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE store`)
		return err
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	var role string
	if err := p.QueryRow(t.Context(), `SELECT current_user`).Scan(&role); err != nil || role != "store" {
		t.Fatalf("current_user = %q, want store: %v", role, err)
	}
	return p
}

func assertMutationAuthenticationFailure(t *testing.T, ctx context.Context, response *httptest.ResponseRecorder, diagnostic []byte, password string) {
	t.Helper()
	if response.Code != http.StatusInternalServerError || response.Header().Get("Location") != "" {
		t.Errorf("backend authentication refusal = %d %q, want 500 without a wrong-password redirect", response.Code, response.Header().Get("Location"))
	}
	body := html.UnescapeString(response.Body.String())
	if !strings.Contains(body, i18n.T(ctx, i18n.KeyBusyBody)) {
		t.Error("backend authentication refusal lost its generic translated service explanation")
	}
	for _, private := range []string{password, "touch last login", "refuse_login_touch"} {
		if strings.Contains(body, private) {
			t.Errorf("backend authentication refusal leaked %q", private)
		}
	}
	var record struct {
		Level string `json:"level"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(diagnostic, &record); err != nil {
		t.Errorf("backend authentication diagnostic = %q, want one error record: %v", string(diagnostic), err)
	} else if record.Level != "ERROR" || !strings.Contains(record.Error, "touch last login") || !strings.Contains(record.Error, "refuse_login_touch") {
		t.Errorf("backend authentication diagnostic = %+v, want the actual login-write failure", record)
	}
	if strings.Contains(string(diagnostic), password) {
		t.Error("backend authentication diagnostic contains the password")
	}
}
