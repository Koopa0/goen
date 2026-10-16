//go:build integration

package auth_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/outbox"
)

func TestAnAddressIsProvedByFollowingTheLink(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	u := register(t, s, "verify-"+uuid.NewString()+"@goen.invalid")

	state, err := account.NewStore(pool).EmailVerification(ctx, u.ID)
	if err != nil {
		t.Fatalf("EmailVerification: %v", err)
	}
	if state.Verified {
		t.Error("a freshly registered address reads as proved; nothing has proved it")
	}

	// An address that was never proved is proved by its link together with
	// the password chosen at registration.
	token := requestVerification(t, s, u.ID, u.Email)
	if _, confirmErr := s.CompleteRegistration(ctx, token, "a sufficiently long password"); confirmErr != nil {
		t.Fatalf("CompleteRegistration: %v", confirmErr)
	}

	state, err = account.NewStore(pool).EmailVerification(ctx, u.ID)
	if err != nil {
		t.Fatalf("EmailVerification: %v", err)
	}
	if !state.Verified {
		t.Error("the address is not proved after its link was followed")
	}
	if _, err := s.ConfirmVerification(ctx, token, u.ID); !errors.Is(err, auth.ErrVerifyInvalid) {
		t.Errorf("re-using the link = %v, want ErrVerifyInvalid", err)
	}
}

func TestAChangeTakesEffectOnlyWhenConfirmed(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	old := "before-" + uuid.NewString() + "@goen.invalid"
	u := register(t, s, old)
	next := "after-" + uuid.NewString() + "@goen.invalid"

	token := requestVerification(t, s, u.ID, next)

	if got := emailOf(t, u.ID); got != old {
		t.Errorf("the account moved to %q before the link was followed", got)
	}
	state, err := account.NewStore(pool).EmailVerification(ctx, u.ID)
	if err != nil {
		t.Fatalf("EmailVerification: %v", err)
	}
	if state.PendingEmail != next {
		t.Errorf("pending address is %q, want %q", state.PendingEmail, next)
	}

	if _, err := s.ConfirmVerification(ctx, token, u.ID); err != nil {
		t.Fatalf("ConfirmVerification: %v", err)
	}
	if got := emailOf(t, u.ID); got != next {
		t.Errorf("the account is at %q after confirming, want %q", got, next)
	}
}

// TestChangingEmailInvalidatesResetLinksSentToTheOldMailbox protects the
// account after mailbox ownership moves: a link already delivered to the old
// address must not remain password authority for the new identity.
func TestChangingEmailInvalidatesResetLinksSentToTheOldMailbox(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	old := "old-reset-" + uuid.NewString() + "@goen.invalid"
	u := register(t, s, old)
	oldMailboxToken := beginReset(t, s, old)
	newAddress := "new-reset-" + uuid.NewString() + "@goen.invalid"

	verification := requestVerification(t, s, u.ID, newAddress)
	if _, err := s.ConfirmVerification(ctx, verification, u.ID); err != nil {
		t.Fatalf("confirm address change: %v", err)
	}
	if _, err := s.CompleteReset(ctx, oldMailboxToken, "password chosen by old mailbox"); !errors.Is(err, auth.ErrResetInvalid) {
		t.Fatalf("old-mailbox reset after address change = %v, want ErrResetInvalid", err)
	}
	var oldMailboxMessages int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = $1
		  AND lower(coalesce(payload ->> 'email', '')) = lower($2)`,
		outbox.TopicPasswordReset.Name(), old).Scan(&oldMailboxMessages); err != nil {
		t.Fatalf("count old-mailbox reset messages: %v", err)
	}
	if oldMailboxMessages != 0 {
		t.Errorf("%d reset messages still retain or target the old mailbox", oldMailboxMessages)
	}
	if _, err := s.Authenticate(ctx, newAddress, "a sufficiently long password"); err != nil {
		t.Errorf("address change disturbed the account's existing password: %v", err)
	}
}

// users_email_key is the real guard: this requests first, then lets somebody
// else take the address before the confirmation.
func TestAChangeToATakenAddressIsRefused(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	mine := register(t, s, "mine-"+uuid.NewString()+"@goen.invalid")
	wanted := "contested-" + uuid.NewString() + "@goen.invalid"

	token := requestVerification(t, s, mine.ID, wanted)
	register(t, s, wanted)

	if _, err := s.ConfirmVerification(ctx, token, mine.ID); !errors.Is(err, auth.ErrEmailTaken) {
		t.Errorf("confirming a taken address = %v, want ErrEmailTaken", err)
	}
	if got := emailOf(t, mine.ID); got != mine.Email {
		t.Errorf("my address became %q after a refused change, want %q", got, mine.Email)
	}
}

func TestAskingAgainLeavesOneLiveLink(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	u := register(t, s, "again-"+uuid.NewString()+"@goen.invalid")

	first := requestVerification(t, s, u.ID, u.Email)
	second := requestVerification(t, s, u.ID, u.Email)

	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM email_verifications WHERE user_id = $1`, u.ID).Scan(&rows); err != nil {
		t.Fatalf("count requests: %v", err)
	}
	if rows != 1 {
		t.Errorf("%d outstanding requests after asking twice, want 1", rows)
	}
	var messages int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = $1 AND lower(payload->>'email') = lower($2)`,
		outbox.TopicEmailVerify.Name(), u.Email).Scan(&messages); err != nil {
		t.Fatalf("count queued verification links: %v", err)
	}
	if messages != 1 {
		t.Errorf("%d verification messages after asking twice, want only the newest", messages)
	}
	if _, err := s.ConfirmVerification(ctx, first, u.ID); !errors.Is(err, auth.ErrVerifyInvalid) {
		t.Errorf("the earlier link still works: %v", err)
	}
	if _, err := s.CompleteRegistration(ctx, second, "a sufficiently long password"); err != nil {
		t.Errorf("the newest link does not work: %v", err)
	}
}

func TestAnExpiredVerificationIsRefused(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	u := register(t, s, "expired-"+uuid.NewString()+"@goen.invalid")

	token := requestVerification(t, s, u.ID, u.Email)
	if _, err := pool.Exec(ctx, `
		UPDATE email_verifications
		SET created_at = now() - interval '50 hours', expires_at = now() - interval '2 hours'
		WHERE user_id = $1`, u.ID); err != nil {
		t.Fatalf("age the request: %v", err)
	}

	if _, err := s.ConfirmVerification(ctx, token, u.ID); !errors.Is(err, auth.ErrVerifyInvalid) {
		t.Errorf("an expired link = %v, want ErrVerifyInvalid", err)
	}
}

func TestRegisteringAsksForTheAddressToBeProved(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	addr := "asked-" + uuid.NewString() + "@goen.invalid"

	if err := s.Register(ctx, &auth.Credentials{
		Email: addr, Password: "a sufficiently long password",
	}, "/account"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	followUpRegistrations(t, s, addr, neverTold(t))

	var messages int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = 'account.email_verify' AND payload->>'email' = $1`,
		addr).Scan(&messages); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if messages != 1 {
		t.Errorf("%d verification messages for a new account, want 1", messages)
	}
}
