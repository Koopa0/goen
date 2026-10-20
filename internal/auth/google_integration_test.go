//go:build integration

package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/auth/google"
	"github.com/koopa0/goen/internal/user"
)

func TestGoogleSignInCreatesAnAccountWithNoPassword(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	email := "oauth-new-" + uuid.NewString()[:8] + "@example.com"

	u, err := s.SignInWithGoogle(ctx, google.Identity{
		Subject: "google-sub-" + uuid.NewString()[:8],
		Email:   email, EmailVerified: true, Name: "谷歌使用者",
	})
	if err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}
	if u.Email != email {
		t.Errorf("signed in as %q, want %q", u.Email, email)
	}

	var hash *string
	var verified *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT password_hash, email_verified_at FROM users WHERE id = $1`,
		u.ID).Scan(&hash, &verified); err != nil {
		t.Fatalf("read the new account: %v", err)
	}
	if hash != nil {
		t.Errorf("an account created from an identity has a password hash: %q", *hash)
	}
	if verified == nil {
		t.Error("google proved the address and goen did not record it as proved")
	}

	if _, authErr := s.Authenticate(ctx, email, ""); !errors.Is(authErr, auth.ErrBadCredentials) {
		t.Errorf("signing in with no password gave %v, want ErrBadCredentials", authErr)
	}
}

func TestGoogleSignInIsIdempotentOnTheSubject(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	subject := "google-sub-" + uuid.NewString()[:8]

	first, err := s.SignInWithGoogle(ctx, google.Identity{
		Subject: subject, Email: "oauth-same-" + uuid.NewString()[:8] + "@example.com",
		EmailVerified: true, Name: "谷歌使用者",
	})
	if err != nil {
		t.Fatalf("first sign-in: %v", err)
	}

	second, err := s.SignInWithGoogle(ctx, google.Identity{
		Subject: subject, Email: "changed-" + uuid.NewString()[:8] + "@example.com",
		EmailVerified: true, Name: "谷歌使用者",
	})
	if err != nil {
		t.Fatalf("second sign-in: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("the same Google account signed into %s and then %s — a changed "+
			"address stranded the customer with their orders behind them",
			first.ID, second.ID)
	}
}

// TestConcurrentGoogleLinkingReturnsTheDurableExistingOwner makes both
// callbacks reach the subject lock before either can choose an email account.
// Whichever verified account wins is acceptable; returning both is not.
func TestConcurrentGoogleLinkingReturnsTheDurableExistingOwner(t *testing.T) {
	ctx := t.Context()
	suffix := uuid.NewString()[:8]
	subject := "google-race-existing-" + suffix
	emailA := "google-race-a-" + suffix + "@example.com"
	emailB := "google-race-b-" + suffix + "@example.com"
	var idA, idB string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, email_verified_at) VALUES ($1, now()) RETURNING id`, emailA).
		Scan(&idA); err != nil {
		t.Fatalf("create first verified account: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, email_verified_at) VALUES ($1, now()) RETURNING id`, emailB).
		Scan(&idB); err != nil {
		t.Fatalf("create second verified account: %v", err)
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin subject blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended('google:' || $1::text, 0))`, subject); err != nil {
		t.Fatalf("lock subject: %v", err)
	}

	firstName, secondName := "google-existing-a-"+suffix, "google-existing-b-"+suffix
	firstStore := auth.NewStore(accountStorePool(t, firstName))
	secondStore := auth.NewStore(accountStorePool(t, secondName))
	var first, second user.User
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() {
		var signInErr error
		first, signInErr = firstStore.SignInWithGoogle(context.WithoutCancel(ctx), google.Identity{
			Subject: subject, Email: emailA, EmailVerified: true,
		})
		firstDone <- signInErr
	}()
	go func() {
		var signInErr error
		second, signInErr = secondStore.SignInWithGoogle(context.WithoutCancel(ctx), google.Identity{
			Subject: subject, Email: emailB, EmailVerified: true,
		})
		secondDone <- signInErr
	}()
	waitForAccountLock(t, firstName, firstDone)
	waitForAccountLock(t, secondName, secondDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release subject: %v", err)
	}
	if err := operationResult(t, firstDone); err != nil {
		t.Fatalf("first sign-in: %v", err)
	}
	if err := operationResult(t, secondDone); err != nil {
		t.Fatalf("second sign-in: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("one Google subject returned accounts %s and %s", first.ID, second.ID)
	}
	if first.ID != idA && first.ID != idB {
		t.Fatalf("subject owner %s is neither candidate account", first.ID)
	}
	var durable string
	if err := pool.QueryRow(ctx, `
		SELECT user_id::text FROM user_identities
		WHERE provider = 'google' AND provider_subject = $1`, subject).Scan(&durable); err != nil {
		t.Fatalf("read durable subject owner: %v", err)
	}
	if first.ID != durable {
		t.Errorf("both callbacks returned %s, durable subject owner is %s", first.ID, durable)
	}
}

// TestConcurrentGoogleSignUpCreatesOnlyTheSubjectOwner covers the create path:
// the losing callback must not commit and return a second, unlinked account.
func TestConcurrentGoogleSignUpCreatesOnlyTheSubjectOwner(t *testing.T) {
	ctx := t.Context()
	suffix := uuid.NewString()[:8]
	subject := "google-race-new-" + suffix
	emailA := "google-new-a-" + suffix + "@example.com"
	emailB := "google-new-b-" + suffix + "@example.com"

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin subject blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended('google:' || $1::text, 0))`, subject); err != nil {
		t.Fatalf("lock subject: %v", err)
	}

	firstName, secondName := "google-new-a-"+suffix, "google-new-b-"+suffix
	firstStore := auth.NewStore(accountStorePool(t, firstName))
	secondStore := auth.NewStore(accountStorePool(t, secondName))
	var first, second user.User
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() {
		var signInErr error
		first, signInErr = firstStore.SignInWithGoogle(context.WithoutCancel(ctx), google.Identity{
			Subject: subject, Email: emailA, EmailVerified: true,
		})
		firstDone <- signInErr
	}()
	go func() {
		var signInErr error
		second, signInErr = secondStore.SignInWithGoogle(context.WithoutCancel(ctx), google.Identity{
			Subject: subject, Email: emailB, EmailVerified: true,
		})
		secondDone <- signInErr
	}()
	waitForAccountLock(t, firstName, firstDone)
	waitForAccountLock(t, secondName, secondDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release subject: %v", err)
	}
	if err := operationResult(t, firstDone); err != nil {
		t.Fatalf("first sign-up: %v", err)
	}
	if err := operationResult(t, secondDone); err != nil {
		t.Fatalf("second sign-up: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("one new Google subject returned accounts %s and %s", first.ID, second.ID)
	}
	var accounts, identities int
	var durable string
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM users WHERE email IN ($1, $2)`, emailA, emailB).Scan(&accounts); err != nil {
		t.Fatalf("count candidate accounts: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM user_identities
		WHERE provider = 'google' AND provider_subject = $1`, subject).Scan(&identities); err != nil {
		t.Fatalf("count durable identity: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT user_id::text FROM user_identities
		WHERE provider = 'google' AND provider_subject = $1`, subject).Scan(&durable); err != nil {
		t.Fatalf("read durable identity owner: %v", err)
	}
	if accounts != 1 || identities != 1 {
		t.Errorf("concurrent sign-up left %d accounts and %d identity rows, want 1 and 1",
			accounts, identities)
	}
	if first.ID != durable {
		t.Errorf("callbacks returned %s, durable subject owner is %s", first.ID, durable)
	}
}

func TestGoogleWillNotLinkToAnUnverifiedAccount(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	email := "oauth-collide-" + uuid.NewString()[:8] + "@example.com"

	victim := register(t, s, email)

	_, err := s.SignInWithGoogle(ctx, google.Identity{
		Subject: "google-sub-" + uuid.NewString()[:8],
		Email:   email, EmailVerified: true, Name: "谷歌使用者",
	})
	if !errors.Is(err, auth.ErrOAuthCollision) {
		t.Fatalf("linking to an unverified account = %v, want ErrOAuthCollision", err)
	}

	var identities, accounts int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_identities WHERE user_id = $1`, victim.ID).Scan(&identities); err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if identities != 0 {
		t.Errorf("%d identities linked to an unverified account, want 0", identities)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE lower(email) = lower($1)`, email).Scan(&accounts); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	if accounts != 1 {
		t.Errorf("%d accounts hold that address, want 1", accounts)
	}
}

func TestGoogleLinksToAVerifiedAccount(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	email := "oauth-verified-" + uuid.NewString()[:8] + "@example.com"

	u := register(t, s, email)
	if _, err := pool.Exec(ctx,
		`UPDATE users SET email_verified_at = now() WHERE id = $1`, u.ID); err != nil {
		t.Fatalf("verify the address: %v", err)
	}

	got, err := s.SignInWithGoogle(ctx, google.Identity{
		Subject: "google-sub-" + uuid.NewString()[:8],
		Email:   email, EmailVerified: true, Name: "谷歌使用者",
	})
	if err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("signed into %s, want the existing account %s", got.ID, u.ID)
	}

	// The password still works: linking adds a way in rather than replacing one.
	if _, authErr := s.Authenticate(ctx, email, "a sufficiently long password"); authErr != nil {
		t.Errorf("the password stopped working after linking Google: %v", authErr)
	}
}

// email_verified is false for some Workspace configurations, where the domain
// administrator controls what the address says.
func TestGoogleRefusesAnAddressGoogleHasNotVerified(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	email := "oauth-unverified-" + uuid.NewString()[:8] + "@example.com"

	_, err := s.SignInWithGoogle(ctx, google.Identity{
		Subject: "google-sub-" + uuid.NewString()[:8],
		Email:   email, EmailVerified: false, Name: "谷歌使用者",
	})
	if !errors.Is(err, auth.ErrOAuthUnverified) {
		t.Fatalf("an unverified google address = %v, want ErrOAuthUnverified", err)
	}
	var accounts int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE lower(email) = lower($1)`, email).Scan(&accounts); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	if accounts != 0 {
		t.Errorf("%d accounts created from an unverified address, want 0", accounts)
	}
}

func TestUnlinkingTheOnlyWayInIsRefused(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	email := "oauth-only-" + uuid.NewString()[:8] + "@example.com"

	u, err := s.SignInWithGoogle(ctx, google.Identity{
		Subject: "google-sub-" + uuid.NewString()[:8],
		Email:   email, EmailVerified: true, Name: "谷歌使用者",
	})
	if err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}

	if unlinkErr := s.UnlinkGoogle(ctx, u); !errors.Is(unlinkErr, auth.ErrLastSignInMethod) {
		t.Fatalf("unlinking the only way in = %v, want ErrLastSignInMethod", unlinkErr)
	}
	var identities int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_identities WHERE user_id = $1`, u.ID).Scan(&identities); err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if identities != 1 {
		t.Errorf("%d identities after a refused unlink, want 1", identities)
	}

	// The control: a function that refused everything would pass the assertion above.
	if _, err := pool.Exec(ctx, `
		UPDATE users SET password_hash = 'x' WHERE id = $1`, u.ID); err != nil {
		t.Fatalf("set a password: %v", err)
	}
	if err := s.UnlinkGoogle(ctx, u); err != nil {
		t.Fatalf("unlinking with a password set: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_identities WHERE user_id = $1`, u.ID).Scan(&identities); err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if identities != 0 {
		t.Errorf("%d identities after unlinking, want 0", identities)
	}
}
