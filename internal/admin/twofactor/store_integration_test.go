//go:build integration

package twofactor_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/twofactor"
)

// storedSecret reads the sealed secret as the database holds it.
func storedSecret(t *testing.T, userID string) []byte {
	t.Helper()
	var stored []byte
	if err := pool.QueryRow(t.Context(),
		`SELECT secret_encrypted FROM staff_totp_credentials WHERE user_id = $1`,
		uuid.MustParse(userID)).Scan(&stored); err != nil {
		t.Fatalf("read the stored secret: %v", err)
	}
	return stored
}

// keyID is what the header of a secret sealed under key carries: bytes 1-4 of
// the stored value, after the version byte.
func keyID(key []byte) []byte {
	digest := sha256.Sum256(key)
	return digest[:4]
}

// TestVerifyResealsASecretTheCurrentKeyDidNotOpen finishes a key rotation one
// sign-in at a time: the first accepted code rewrites the secret under the
// current key, after which the previous key is no longer needed.
func TestVerifyResealsASecretTheCurrentKeyDidNotOpen(t *testing.T) {
	ctx := t.Context()
	userID, email := admintest.AdminUser(t, pool)
	secret := enrol(t, twofactor.NewStore(pool, testKey, nil), userID, email)
	if got := storedSecret(t, userID)[1:5]; !bytes.Equal(got, keyID(testKey)) {
		t.Fatalf("key id after enrolment = %x, want the first key's %x", got, keyID(testKey))
	}
	// Enrolment accepted the current step; move the record back so the next two
	// steps are fresh codes.
	if _, err := pool.Exec(ctx, `UPDATE staff_totp_credentials SET last_step = last_step - 5 WHERE user_id = $1`,
		uuid.MustParse(userID)); err != nil {
		t.Fatalf("rewind the recorded step: %v", err)
	}
	step := twofactor.StepAt(time.Now())

	rotating := twofactor.NewStore(pool, differentKey, testKey)
	if err := rotating.Verify(ctx, userID, twofactor.Code(secret, step)); err != nil {
		t.Fatalf("verify under (new, old): %v", err)
	}
	if got := storedSecret(t, userID)[1:5]; !bytes.Equal(got, keyID(differentKey)) {
		t.Errorf("key id after verify is %x, want the new key's %x", got, keyID(differentKey))
	}

	// The previous key is gone: the re-sealed secret opens under the new one alone.
	if err := twofactor.NewStore(pool, differentKey, nil).Verify(ctx, userID, twofactor.Code(secret, step+1)); err != nil {
		t.Errorf("verify under the new key alone after the re-seal: %v", err)
	}
}

// TestAVerifyRefusedForABadCodeDoesNotReseal: only an accepted code rewrites the
// stored secret.
func TestAVerifyRefusedForABadCodeDoesNotReseal(t *testing.T) {
	userID, email := admintest.AdminUser(t, pool)
	enrol(t, twofactor.NewStore(pool, testKey, nil), userID, email)
	before := storedSecret(t, userID)

	err := twofactor.NewStore(pool, differentKey, testKey).Verify(asActor(t.Context(), userID), userID, "000000")
	if !errors.Is(err, twofactor.ErrBadCode) {
		t.Fatalf("Verify with a wrong code = %v, want ErrBadCode", err)
	}
	if after := storedSecret(t, userID); !bytes.Equal(before, after) {
		t.Error("a refused code rewrote the stored secret")
	}
}

// TestAMovedRowDoesNotOpenForItsNewOwner: the owner is bound into the sealed
// secret, so copying one staff member's row onto another's gives the second no
// factor at all.
func TestAMovedRowDoesNotOpenForItsNewOwner(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey, nil)
	aliceID, aliceEmail := admintest.AdminUser(t, pool)
	bobID, bobEmail := admintest.AdminUser(t, pool)
	aliceSecret := enrol(t, s, aliceID, aliceEmail)
	enrol(t, s, bobID, bobEmail)

	if _, err := pool.Exec(ctx, `
		UPDATE staff_totp_credentials
		SET secret_encrypted = (SELECT secret_encrypted FROM staff_totp_credentials WHERE user_id = $1)
		WHERE user_id = $2`, uuid.MustParse(aliceID), uuid.MustParse(bobID)); err != nil {
		t.Fatalf("move the row: %v", err)
	}

	// The code is right for the secret now in Bob's row; only the owner binding
	// refuses it.
	err := s.Verify(ctx, bobID, twofactor.Code(aliceSecret, twofactor.StepAt(time.Now())+1))
	if !errors.Is(err, twofactor.ErrSecretUnreadable) {
		t.Fatalf("Verify on a moved row = %v, want ErrSecretUnreadable", err)
	}
	if errors.Is(err, twofactor.ErrBadCode) {
		t.Errorf("Verify on a moved row also reports ErrBadCode: %v", err)
	}
	if err := s.Verify(ctx, aliceID, twofactor.Code(aliceSecret, twofactor.StepAt(time.Now())+1)); err != nil {
		t.Errorf("the row's real owner was locked out: %v", err)
	}
}
