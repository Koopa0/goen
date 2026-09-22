//go:build integration

package twofactor_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/twofactor"
)

func TestAddingExistingStaffPreservesTheirAccount(t *testing.T) {
	actor, _ := staff(t)
	s := twofactor.NewStore(twofactorRolePool(t, "duplicate-staff", "admin"), testKey)
	for _, existingRole := range []string{"staff", "admin"} {
		for _, requestedRole := range []string{"staff", "admin"} {
			t.Run(existingRole+"-as-"+requestedRole, func(t *testing.T) {
				id, address := staff(t)
				if _, err := pool.Exec(t.Context(), `
					UPDATE users SET role = $2, full_name = 'Original colleague',
					password_hash = 'unchanged-password' WHERE id = $1`, id, existingRole); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), `
					INSERT INTO sessions (token_hash, user_id, expires_at)
					SELECT sha256(($1 || n::text)::bytea), $1::uuid, now() + interval '14 days'
					FROM generate_series(1, 2) AS n`, id); err != nil {
					t.Fatal(err)
				}
				before := staffAuditCount(t, "staff.grant")
				cleared, err := s.AddStaff(t.Context(), strings.ToUpper(address), "Replacement", requestedRole, actor)
				if !errors.Is(err, twofactor.ErrAlreadyStaff) || cleared {
					t.Fatalf("re-add = cleared %v, %v; want false, ErrAlreadyStaff", cleared, err)
				}
				var role, name, password string
				var sessions int
				if err := pool.QueryRow(t.Context(), `
					SELECT role, full_name, password_hash,
					(SELECT count(*) FROM sessions WHERE user_id = users.id)
					FROM users WHERE id = $1`, id).Scan(&role, &name, &password, &sessions); err != nil {
					t.Fatal(err)
				}
				if role != existingRole || name != "Original colleague" || password != "unchanged-password" || sessions != 2 {
					t.Errorf("re-add changed account: role=%q name=%q password preserved=%v sessions=%d",
						role, name, password == "unchanged-password", sessions)
				}
				if after := staffAuditCount(t, "staff.grant"); after != before {
					t.Errorf("refused add wrote %d grant audits", after-before)
				}
			})
		}
	}
}

func TestConcurrentStaffAddsGrantOnce(t *testing.T) {
	actor, _ := staff(t)
	address := "concurrent-add-" + uuid.NewString() + "@goen.invalid"
	stores := []*twofactor.Store{
		twofactor.NewStore(twofactorRolePool(t, "add-first", "admin"), testKey),
		twofactor.NewStore(twofactorRolePool(t, "add-second", "admin"), testKey),
	}
	before := staffAuditCount(t, "staff.grant")
	start := make(chan struct{})
	results := make(chan error, len(stores))
	for _, s := range stores {
		go func() {
			<-start
			_, err := s.AddStaff(t.Context(), address, "Colleague", "staff", actor)
			results <- err
		}()
	}
	close(start)
	var added, refused int
	for range stores {
		err := twofactorOperationResult(t, results)
		switch {
		case err == nil:
			added++
		case errors.Is(err, twofactor.ErrAlreadyStaff):
			refused++
		default:
			t.Fatalf("concurrent add: %v", err)
		}
	}
	if added != 1 || refused != 1 {
		t.Errorf("added/refused = %d/%d, want 1/1", added, refused)
	}
	if after := staffAuditCount(t, "staff.grant"); after != before+1 {
		t.Errorf("concurrent adds wrote %d grant audits, want 1", after-before)
	}
}

func TestDuplicateStaffFormRetainsInputAndExplainsRefusal(t *testing.T) {
	actor, actorEmail := staff(t)
	_, address := staff(t)
	h := twofactor.NewHandler(twofactor.NewStore(twofactorRolePool(t, "duplicate-staff-form", "admin"), testKey),
		slog.New(slog.DiscardHandler), false)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(account.WithUser(t.Context(), account.User{
				ID: actor, Email: actorEmail, Role: "admin",
			}), locale)
			form := url.Values{"email": {address}, "name": {"Submitted name"}, "role": {"admin"}}
			r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/staff", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			out := httptest.NewRecorder()
			h.AddStaff(out, r)
			if out.Code != http.StatusUnprocessableEntity {
				t.Fatalf("duplicate POST = %d, want 422: %s", out.Code, out.Body.String())
			}
			body := out.Body.String()
			for _, want := range []string{
				`value="` + address + `"`, `value="Submitted name"`,
				`value="admin" selected`, `aria-invalid="true"`,
				`aria-describedby="staff-email-error"`, `id="staff-email-error"`,
				i18n.T(ctx, i18n.KeyStaffAlreadyExists),
			} {
				if !strings.Contains(body, want) {
					t.Errorf("duplicate POST omitted %q", want)
				}
			}
		})
	}
}
