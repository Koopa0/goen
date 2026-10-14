package staff

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
// everything else, so promoteStaff reaches its database failure branches
// without a database.
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

// The staff handler logs what AddStaff returns at ERROR. A database that is
// failing must not put the new colleague's address there; the audit rows
// already keep ids and not addresses (#1897).
func TestAddStaffDatabaseErrorsDoNotCarryTheAddress(t *testing.T) {
	t.Parallel()
	const address = "new.colleague.private@example.test"
	cause := errors.New("connection reset by peer")
	tests := []struct {
		name   string
		script []error
		want   string
	}{
		{"write the staff row", []error{cause}, "add staff"},
		{"read the row back", []error{nil, cause}, "read promoted staff"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := db.New(&scriptedDB{errs: tc.script})
			_, _, err := promoteStaff(t.Context(), q, address, "New Colleague", "staff")
			if err == nil {
				t.Fatal("promoteStaff: nil error from a failing database")
			}
			if !errors.Is(err, cause) {
				t.Errorf("error %q does not wrap the database cause", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say which step failed (want %q)", err, tc.want)
			}
			for _, secret := range []string{address, "new.colleague", "example.test"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error %q carries %q", err, secret)
				}
			}
		})
	}
}
