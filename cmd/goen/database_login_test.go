package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestDatabaseLoginPosture(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		secureCookies bool
		privileges    databaseLoginPrivileges
		wantCondition string
	}{
		{name: "secure plain member", secureCookies: true},
		{name: "insecure plain member"},
		{name: "secure superuser", secureCookies: true, privileges: databaseLoginPrivileges{superuser: true}, wantCondition: "a superuser"},
		{name: "insecure superuser", privileges: databaseLoginPrivileges{superuser: true}, wantCondition: "a superuser"},
		{name: "secure table owner member", secureCookies: true, privileges: databaseLoginPrivileges{memberOfTableOwner: true}, wantCondition: `a member of the table owner "tbl"`},
		{name: "insecure table owner member", privileges: databaseLoginPrivileges{memberOfTableOwner: true}, wantCondition: `a member of the table owner "tbl"`},
		{name: "secure schema owner member", secureCookies: true, privileges: databaseLoginPrivileges{memberOfSchemaOwner: true}, wantCondition: `a member of the schema owner "sch"`},
		{name: "insecure schema owner member", privileges: databaseLoginPrivileges{memberOfSchemaOwner: true}, wantCondition: `a member of the schema owner "sch"`},
		{name: "secure superuser and owners", secureCookies: true, privileges: databaseLoginPrivileges{superuser: true, memberOfTableOwner: true, memberOfSchemaOwner: true}, wantCondition: "a superuser"},
	}
	wantVariable := map[databaseRole]string{
		storeRole:       "GOEN_DATABASE_URL",
		adminRole:       "GOEN_ADMIN_DATABASE_URL",
		maintenanceRole: "GOEN_MAINTENANCE_DATABASE_URL",
	}
	for _, role := range []databaseRole{storeRole, adminRole, maintenanceRole} {
		for _, tt := range tests {
			t.Run(string(role)+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				privileges := tt.privileges
				privileges.login, privileges.tableOwner, privileges.schemaOwner = "x", "tbl", "sch"
				var output bytes.Buffer
				log := slog.New(slog.NewTextHandler(&output, nil))
				err := checkDatabaseLogin(role, tt.secureCookies, privileges, log)
				unsafe := tt.wantCondition != ""
				if (err != nil) != (unsafe && tt.secureCookies) {
					t.Fatalf("checkDatabaseLogin() = %v, secure %t, unsafe %t", err, tt.secureCookies, unsafe)
				}
				if err != nil {
					assertDatabaseLoginRemedy(t, err.Error(), role, wantVariable[role], tt.wantCondition)
				}
				if unsafe && !tt.secureCookies {
					if !strings.Contains(output.String(), "level=WARN") || strings.Count(output.String(), "\n") != 1 {
						t.Fatalf("unsafe insecure login warning = %q, want one WARN", output.String())
					}
					assertDatabaseLoginRemedy(t, strings.ReplaceAll(output.String(), `\"`, `"`), role, wantVariable[role], tt.wantCondition)
				} else if output.Len() != 0 {
					t.Errorf("unexpected startup log: %s", output.String())
				}
			})
		}
	}
}

// assertDatabaseLoginRemedy checks the message names the pool, the condition
// that fired and the variable that configures that pool's login.
func assertDatabaseLoginRemedy(t *testing.T, message string, role databaseRole, variable, condition string) {
	t.Helper()
	for _, want := range []string{"the " + string(role) + " pool's login", condition, "set " + variable + " to a login that is only a member of " + string(role)} {
		if !strings.Contains(message, want) {
			t.Errorf("startup message %q does not contain %q", message, want)
		}
	}
}

func TestDatabaseLocalePosture(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		provider string
		locale   string
		wantOK   bool
	}{
		{name: "builtin C.UTF-8", provider: "b", locale: "C.UTF-8", wantOK: true},
		{name: "libc", provider: "c", locale: ""},
		{name: "libc with a locale", provider: "c", locale: "en_US.utf8"},
		{name: "builtin C", provider: "b", locale: "C"},
		{name: "icu", provider: "i", locale: "und"},
	}
	wantVariable := map[databaseRole]string{
		storeRole:       "GOEN_DATABASE_URL",
		adminRole:       "GOEN_ADMIN_DATABASE_URL",
		maintenanceRole: "GOEN_MAINTENANCE_DATABASE_URL",
	}
	for _, role := range []databaseRole{storeRole, adminRole, maintenanceRole} {
		for _, secure := range []bool{true, false} {
			for _, tt := range tests {
				t.Run(fmt.Sprintf("%s/secure=%t/%s", role, secure, tt.name), func(t *testing.T) {
					t.Parallel()
					var output bytes.Buffer
					log := slog.New(slog.NewTextHandler(&output, nil))
					err := checkDatabaseLocale(role, tt.provider, tt.locale, secure, log)
					if (err != nil) != (!tt.wantOK && secure) {
						t.Fatalf("checkDatabaseLocale() = %v, secure %t, ok %t", err, secure, tt.wantOK)
					}
					message := output.String()
					if err != nil {
						message = err.Error()
						if !strings.HasPrefix(message, "refusing to start: ") {
							t.Errorf("error %q does not start with %q", message, "refusing to start: ")
						}
					}
					if tt.wantOK {
						if output.Len() != 0 {
							t.Errorf("unexpected startup log: %s", output.String())
						}
						return
					}
					if !secure && (!strings.Contains(output.String(), "level=WARN") || strings.Count(output.String(), "\n") != 1) {
						t.Fatalf("insecure locale warning = %q, want one WARN", output.String())
					}
					message = strings.ReplaceAll(message, `\"`, `"`)
					for _, want := range []string{
						wantVariable[role],
						fmt.Sprintf("locale provider %q and locale %q", tt.provider, tt.locale),
						`builtin provider with locale "C.UTF-8"`,
					} {
						if !strings.Contains(message, want) {
							t.Errorf("startup message %q does not contain %q", message, want)
						}
					}
				})
			}
		}
	}
}
