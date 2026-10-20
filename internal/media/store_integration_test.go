//go:build integration

package media_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/media/mediatest"
)

// TestStore_PutStorageFailure holds what every desk that stores an upload
// relies on when the database refuses the write: Put's error names the step
// and the image, carries the database's cause for the log, and is not a
// refusal, so the desk answers a server error rather than blaming the file.
// The desks' own suites hold only that they answer such an error with 500.
func TestStore_PutStorageFailure(t *testing.T) {
	owner := dbtest.Pool(t)
	var writes mediatest.Writes
	s := mediatest.NewStore(t, dbtest.RolePool(t, owner, "admin"), &writes)
	picture := samplePNG(t, 67, 41)
	want, _, err := media.Normalise(bytes.NewReader(picture))
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}

	release := mediatest.HoldWrites(t, owner)
	_, err = s.Put(t.Context(), bytes.NewReader(picture))
	release()

	if cause, ok := errors.AsType[*pgconn.PgError](err); !ok || cause.Code != "55P03" {
		t.Fatalf("Put under a held lock = %v, want an error carrying the SQLSTATE 55P03 cause", err)
	}
	if writes.Calls != 1 || writes.Code != "55P03" || !writes.Live {
		t.Errorf("writes = %+v, want one live PutMedia that failed with 55P03", writes)
	}
	if media.IsRefusal(err) {
		t.Errorf("IsRefusal(%v) = true, want false: a desk would blame the file for the database", err)
	}
	if wantText := "store image " + want.Digest; !strings.Contains(err.Error(), wantText) {
		t.Errorf("Put error = %q, want it to name the step and the image, %q", err, wantText)
	}
	var stored bool
	if err := owner.QueryRow(t.Context(),
		`SELECT EXISTS (SELECT 1 FROM media_objects WHERE digest = $1)`, want.Digest).Scan(&stored); err != nil {
		t.Fatalf("read media: %v", err)
	}
	if stored {
		t.Error("a failed Put stored the image")
	}
}
