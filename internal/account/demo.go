package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
)

// ErrDemoAccountIsStaff exists because the demo password would be printed on the sign-in
// page, and the storefront role cannot make the account a customer again.
var ErrDemoAccountIsStaff = errors.New("account: the demo address belongs to a staff account")

// DemoAccount is shared with every visitor and its password is printed on the
// sign-in page, so nothing done while signed in to it may change how the next
// visitor signs in, or mail an address a visitor chose. The zero value is no
// demo account.
type DemoAccount struct {
	email    string
	password string
}

func NewDemoAccount(addr, password string) (DemoAccount, error) {
	if addr == "" && password == "" {
		return DemoAccount{}, nil
	}
	if addr == "" || password == "" {
		return DemoAccount{}, errors.New("account: a demo account needs both an address and a " +
			"password; one without the other is offered on the sign-in page and opens nothing")
	}
	addr = email.Clean(addr)
	if EmailError(addr) != "" {
		return DemoAccount{}, errors.New("account: the demo account's address is not a usable email address")
	}
	if PasswordError(password) != "" {
		return DemoAccount{}, fmt.Errorf("account: the demo account's password must be %d to %d "+
			"characters, as every account's is", MinPasswordRunes, MaxPasswordBytes)
	}
	return DemoAccount{email: addr, password: password}, nil
}

func (d DemoAccount) Enabled() bool { return d.email != "" }

func (d DemoAccount) holds(addr string) bool {
	return d.Enabled() && email.Clean(addr) == d.email
}

// EnsureDemoAccount runs at startup because goen restarts after every nightly
// demo restore, so a snapshot holding another password, or no such account, is
// put right before anybody is served.
func (s *Store) EnsureDemoAccount(ctx context.Context, d DemoAccount) error {
	if !d.Enabled() {
		return nil
	}
	row, err := s.q.UserByEmail(ctx, d.email)
	if errors.Is(err, pgx.ErrNoRows) {
		hash, hashErr := HashPassword(d.password)
		if hashErr != nil {
			return fmt.Errorf("hash the demo password: %w", hashErr)
		}
		if createErr := s.q.CreateVerifiedUser(ctx, db.CreateVerifiedUserParams{
			Email: d.email, PasswordHash: hash,
		}); createErr != nil {
			return fmt.Errorf("create the demo account: %w", createErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the demo account: %w", err)
	}
	if (User{Role: Role(row.Role)}).IsStaff() {
		return ErrDemoAccountIsStaff
	}
	if !passwordMatches(row.PasswordHash, d.password) {
		if err := s.ChangePassword(ctx, row.ID.String(), d.password); err != nil {
			return fmt.Errorf("reset the demo password: %w", err)
		}
	}
	if !row.Verified {
		if err := s.q.SetVerifiedEmail(ctx, db.SetVerifiedEmailParams{
			Email: row.Email, UserID: row.ID,
		}); err != nil {
			return fmt.Errorf("prove the demo address: %w", err)
		}
	}
	return nil
}
