package main

import (
	"bytes"
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
		wantError     bool
		wantWarn      bool
	}{
		{name: "secure plain member", secureCookies: true},
		{name: "insecure plain member"},
		{name: "secure superuser", secureCookies: true, privileges: databaseLoginPrivileges{superuser: true}, wantError: true},
		{name: "insecure superuser", privileges: databaseLoginPrivileges{superuser: true}, wantWarn: true},
		{name: "secure owner member", secureCookies: true, privileges: databaseLoginPrivileges{ownerMember: true}, wantError: true},
		{name: "insecure owner member", privileges: databaseLoginPrivileges{ownerMember: true}, wantWarn: true},
		{name: "secure superuser owner", secureCookies: true, privileges: databaseLoginPrivileges{superuser: true, ownerMember: true}, wantError: true},
		{name: "insecure superuser owner", privileges: databaseLoginPrivileges{superuser: true, ownerMember: true}, wantWarn: true},
	}
	for _, role := range []databaseRole{storeRole, adminRole, maintenanceRole} {
		for _, tt := range tests {
			t.Run(string(role)+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				var output bytes.Buffer
				log := slog.New(slog.NewTextHandler(&output, nil))
				err := checkDatabaseLogin(role, tt.secureCookies, tt.privileges, log)
				if (err != nil) != tt.wantError {
					t.Fatalf("checkDatabaseLogin() = %v, want error %t", err, tt.wantError)
				}
				if tt.wantError {
					assertDatabaseLoginRemedy(t, err.Error(), role)
				}
				if tt.wantWarn {
					if !strings.Contains(output.String(), "level=WARN") || strings.Count(output.String(), "\n") != 1 {
						t.Fatalf("unsafe insecure login warning = %q, want one WARN", output.String())
					}
					assertDatabaseLoginRemedy(t, output.String(), role)
				} else if output.Len() != 0 {
					t.Errorf("unexpected startup log: %s", output.String())
				}
			})
		}
	}
}

func assertDatabaseLoginRemedy(t *testing.T, message string, role databaseRole) {
	t.Helper()
	for _, want := range []string{string(role) + " database login", "connect as a non-superuser member of " + string(role)} {
		if !strings.Contains(message, want) {
			t.Errorf("startup message %q does not contain %q", message, want)
		}
	}
}
