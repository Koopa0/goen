package account

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db"
)

// scriptedDB answers each QueryRow with the next scripted error and refuses
// everything else. It lets a test drive a query helper into its database
// failure branch without a database.
type scriptedDB struct{ errs []error }

func (d *scriptedDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("scriptedDB: Exec is not scripted")
}

func (d *scriptedDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("scriptedDB: Query is not scripted")
}

func (d *scriptedDB) QueryRow(context.Context, string, ...any) pgx.Row {
	if len(d.errs) == 0 {
		return failedRow{errors.New("scriptedDB: no more scripted answers")}
	}
	err := d.errs[0]
	d.errs = d.errs[1:]
	return failedRow{err}
}

type failedRow struct{ err error }

func (r failedRow) Scan(...any) error { return r.err }

// Handlers log the error that SignInWithGoogle returns at ERROR, and a failing
// database is exactly when every Google sign-in would write its customer's
// address there. The error names the step and wraps the cause; it never carries
// the address.
func TestGoogleSignInDatabaseErrorsDoNotCarryTheAddress(t *testing.T) {
	t.Parallel()
	const address = "jane.doe.private@example.test"
	cause := errors.New("connection reset by peer")
	tests := []struct {
		name   string
		script []error
		want   string
	}{
		{"read the account", []error{cause}, "read the account"},
		{"create the account", []error{pgx.ErrNoRows, cause}, "create an account"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := db.New(&scriptedDB{errs: tc.script})
			_, _, _, _, err := googleLinkCandidate(t.Context(), q, Identity{
				Subject: "sub-1", Email: address, Name: "Jane Doe", EmailVerified: true,
			})
			if err == nil {
				t.Fatal("googleLinkCandidate: nil error from a failing database")
			}
			if !errors.Is(err, cause) {
				t.Errorf("error %q does not wrap the database cause", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say which step failed (want %q)", err, tc.want)
			}
			for _, secret := range []string{address, "jane.doe", "example.test"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error %q carries %q", err, secret)
				}
			}
		})
	}
}
