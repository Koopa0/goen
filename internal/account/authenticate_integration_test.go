//go:build integration

package account_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/account"
)

func TestPasswordIsNeverStoredInTheClear(t *testing.T) {
	s := account.NewStore(pool)
	const pw = "a sufficiently long password"
	u := register(t, s, "clear@example.com")

	var stored string
	if err := pool.QueryRow(t.Context(),
		`SELECT password_hash FROM users WHERE id = $1`, u.ID).Scan(&stored); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if stored == pw {
		t.Fatal("the password is stored in the clear")
	}
	if !account.VerifyPassword(stored, pw) {
		t.Error("the stored value does not verify the password it was made from")
	}
	if len(stored) < 40 || stored[:10] != "$argon2id$" {
		t.Errorf("the stored value is not an argon2id hash: %q", stored)
	}
}

func TestAuthenticateDoesNotDistinguishUnknownFromWrong(t *testing.T) {
	s := account.NewStore(pool)
	register(t, s, "enum@example.com")

	_, wrongPassword := s.Authenticate(t.Context(), "enum@example.com", "not the password")
	_, unknownEmail := s.Authenticate(t.Context(), "nobody@example.com", "not the password")

	if !errors.Is(wrongPassword, account.ErrBadCredentials) {
		t.Errorf("a wrong password gave %v, want ErrBadCredentials", wrongPassword)
	}
	if !errors.Is(unknownEmail, account.ErrBadCredentials) {
		t.Errorf("an unknown email gave %v, want ErrBadCredentials", unknownEmail)
	}
}

func TestAnOverLongPasswordIsAlwaysBadCredentials(t *testing.T) {
	s := account.NewStore(pool)
	register(t, s, "overlong-password@example.com")
	password := strings.Repeat("a", account.MaxPasswordBytes+1)

	for _, email := range []string{"overlong-password@example.com", "absent-overlong@example.com"} {
		if _, err := s.Authenticate(t.Context(), email, password); !errors.Is(err, account.ErrBadCredentials) {
			t.Errorf("Authenticate(%q, over-long password) = %v, want ErrBadCredentials", email, err)
		}
	}
}

// The unique index is on lower(email).
func TestAuthenticateIsCaseInsensitiveOnEmail(t *testing.T) {
	s := account.NewStore(pool)
	registerProved(t, s, "Mixed@Example.com")

	if _, err := s.Authenticate(t.Context(), "mixed@example.com", "a sufficiently long password"); err != nil {
		t.Errorf("sign-in with a differently-cased email failed: %v", err)
	}
	// A registration answers a taken address as it answers a free one, so the
	// case-folded duplicate is told apart by the account count, not an error.
	if dupErr := s.Register(t.Context(), &account.Credentials{
		Email: "MIXED@EXAMPLE.COM", Password: "a sufficiently long password",
	}, "/account"); dupErr != nil {
		t.Errorf("registering the same address in another case gave %v, want nil", dupErr)
	}
	var accounts int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM users WHERE lower(email) = 'mixed@example.com'`).Scan(&accounts); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	if accounts != 1 {
		t.Errorf("%d accounts hold one address in two cases, want 1", accounts)
	}
}
