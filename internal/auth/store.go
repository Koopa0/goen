package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/auth/google"
	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/web/formtext"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/user"
)

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("auth: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

// Register does the same work whether or not the address already has an
// account: the password is hashed either way, and one statement creates the
// account when the address is free and names it when not. The caller must
// answer both the same. Nobody is signed in: the account is usable once the
// mailed link has been followed with the password chosen here, by
// [Store.CompleteRegistration].
func (s *Store) Register(ctx context.Context, c *Credentials, next string) error {
	hash, err := HashPassword(c.Password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin registration: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	row, err := q.CreateUserUnlessRegistered(ctx, db.CreateUserUnlessRegisteredParams{
		Email: c.Email, PasswordHash: hash, FullName: text(c.Name),
	})
	if err != nil {
		return fmt.Errorf("record registration: %w", err)
	}
	registration := outbox.AccountRegistration{Created: row.Created, Locale: i18n.FromContext(ctx).Tag(), Next: next}
	if row.UserID != uuid.Nil {
		registration.UserID = row.UserID.String()
	}
	if err := outbox.Enqueue(ctx, q, outbox.TopicRegistration, "registration:"+uuid.NewString(), &registration); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit registration: %w", err)
	}
	return nil
}

// ResendRegistration queues another registration link for addr, which is sent
// only when addr has an account that is still unproved. named reports whether
// it did; it is for charging what the link costs, and the caller must answer
// alike whatever it says.
func (s *Store) ResendRegistration(ctx context.Context, addr, next string) (named bool, err error) {
	named, err = s.q.EnqueueRegistrationResend(ctx, db.EnqueueRegistrationResendParams{
		Topic:     outbox.TopicRegistration.Name(),
		DedupeKey: "registration:" + uuid.NewString(),
		Locale:    i18n.FromContext(ctx).Tag(),
		Next:      next,
		Email:     addr,
	})
	if err != nil {
		return false, fmt.Errorf("queue registration resend: %w", err)
	}
	return named, nil
}

// AddressTaken is the account to tell that somebody tried to take its address:
// its address and name as they are when the letter is sent, and the locale the
// attempt was made in. It is read at delivery and never queued.
type AddressTaken struct {
	Locale string
	Email  string
	Name   string
	// Change is an attempt to move another account to the address, rather
	// than to register it again.
	Change bool
}

// FollowUpRegistration reads the account here rather than carrying it in the
// message, so one erased in between, or completed some other way, is nothing to
// do.
func (s *Store) FollowUpRegistration(
	ctx context.Context,
	r *outbox.AccountRegistration,
	tell func(context.Context, *AddressTaken) error,
) error {
	if r.UserID == "" {
		return nil
	}
	id, err := uuid.Parse(r.UserID)
	if err != nil {
		return fmt.Errorf("parse registered account: %w", err)
	}
	row, err := s.q.RegistrationAccount(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read registered account: %w", err)
	}
	if !r.Created {
		return tell(ctx, &AddressTaken{Locale: r.Locale, Email: row.Email, Name: row.FullName.String})
	}
	if row.Verified {
		return nil
	}
	ctx = i18n.WithLocale(ctx, i18n.Parse(r.Locale))
	return s.queueVerification(ctx, r.UserID, row.Email, outbox.AddressVerify{
		Registration: true, Next: r.Next,
	})
}

func (s *Store) Authenticate(ctx context.Context, email, password string) (user.User, error) {
	row, err := s.verifiedCredentials(ctx, email, password)
	if err != nil {
		return user.User{}, err
	}
	if err := s.q.TouchLastLogin(ctx, row.ID); err != nil {
		return user.User{}, fmt.Errorf("touch last login: %w", err)
	}
	return user.User{ID: row.ID.String(), Email: row.Email, Name: row.FullName.String, Role: user.Role(row.Role)}, nil
}

// ConfirmPassword checks the verified account's password without recording a sign-in.
func (s *Store) ConfirmPassword(ctx context.Context, email, password string) error {
	_, err := s.verifiedCredentials(ctx, email, password)
	return err
}

func (s *Store) verifiedCredentials(ctx context.Context, email, password string) (db.UserByEmailRow, error) {
	// Refuse this before reading the account, or the outcomes are
	// distinguishable: burnHashTime returns immediately at this length while
	// VerifyPassword does not. Every password_hash writer in auth goes
	// through HashPassword, which refuses an input over this same bound.
	if len(password) > MaxPasswordBytes {
		return db.UserByEmailRow{}, ErrBadCredentials
	}

	row, err := s.q.UserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			burnHashTime(password)
			return db.UserByEmailRow{}, ErrBadCredentials
		}
		return db.UserByEmailRow{}, fmt.Errorf("read user: %w", err)
	}
	if !passwordMatches(row.PasswordHash, password) {
		return db.UserByEmailRow{}, ErrBadCredentials
	}
	// After the hash, so an unproved account costs what a wrong password does.
	// Its password was chosen by whoever registered the address, who has not
	// yet shown they read the mailbox, and a different answer would say which
	// registrations created an account.
	if !row.Verified {
		return db.UserByEmailRow{}, ErrBadCredentials
	}
	return row, nil
}

// passwordMatches costs an account with no password the hash a wrong password
// does, so the two cannot be told apart by how long the answer takes.
func passwordMatches(hash pgtype.Text, password string) bool {
	if !hash.Valid {
		burnHashTime(password)
		return false
	}
	return VerifyPassword(hash.String, password)
}

func burnHashTime(password string) {
	_, _ = HashPassword(password) //nolint:errcheck // discarding is the point
}

func (s *Store) StartSession(ctx context.Context, userID, userAgent, ip string) (string, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return "", fmt.Errorf("parse user id: %w", err)
	}
	token := bearer.New()
	// The column is inet: an unparseable address is NULL rather than a refused sign-in.
	var addr *netip.Addr
	if parsed, perr := netip.ParseAddr(ip); perr == nil {
		addr = &parsed
	}
	if err := s.q.CreateSession(ctx, db.CreateSessionParams{
		Digest:    bearer.Digest(token),
		UserID:    id,
		UserAgent: text(normaliseUserAgent(userAgent)),
		IP:        addr,
		Ttl: pgtype.Interval{
			Microseconds: int64(time.Duration(SessionTTL) * time.Second / time.Microsecond),
			Valid:        true,
		},
	}); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return token, nil
}

func (s *Store) SessionUser(ctx context.Context, token string) (user.User, error) {
	if token == "" {
		return user.User{}, ErrNotFound
	}
	row, err := s.q.SessionUser(ctx, bearer.Digest(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return user.User{}, ErrNotFound
		}
		return user.User{}, fmt.Errorf("read session: %w", err)
	}
	return user.User{ID: row.ID.String(), Email: row.Email, Name: row.FullName.String, Role: user.Role(row.Role)}, nil
}

func (s *Store) SignedInRecently(ctx context.Context, token string, window time.Duration) (bool, error) {
	if token == "" {
		return false, nil
	}
	recent, err := s.q.SessionCreatedSince(ctx, db.SessionCreatedSinceParams{
		Digest: bearer.Digest(token),
		MaxAge: pgtype.Interval{Microseconds: int64(window / time.Microsecond), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read session age: %w", err)
	}
	return recent, nil
}

func (s *Store) EndSession(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if err := s.q.DeleteSession(ctx, bearer.Digest(token)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// endOtherSessions ends every session of the account but this browser's, and
// forgets every browser but this one: a device signed out is no longer the
// owner's to vouch for. Both in one transaction, so neither half survives alone.
func (s *Store) endOtherSessions(ctx context.Context, userID, currentToken, currentDevice string) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin ending other sessions: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)
	if err := q.DeleteOtherUserSessions(ctx, db.DeleteOtherUserSessionsParams{
		UserID: id, CurrentDigest: bearer.Digest(currentToken),
	}); err != nil {
		return fmt.Errorf("end other sessions: %w", err)
	}
	if err := q.DeleteOtherUserKnownDevices(ctx, db.DeleteOtherUserKnownDevicesParams{
		UserID: id, CurrentDigest: bearer.Digest(currentDevice),
	}); err != nil {
		return fmt.Errorf("forget other devices: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit ending other sessions: %w", err)
	}
	return nil
}

func (s *Store) ChangePassword(ctx context.Context, userID, password string) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	hash, err := HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin password change: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	if err := q.SetPasswordHash(ctx, db.SetPasswordHashParams{
		ID: id, PasswordHash: pgtype.Text{String: hash, Valid: true},
	}); err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if err := q.InvalidateResetTokens(ctx, id); err != nil {
		return fmt.Errorf("invalidate reset tokens: %w", err)
	}
	if err := q.DeleteUserSessions(ctx, id); err != nil {
		return fmt.Errorf("end sessions: %w", err)
	}
	if err := q.DeleteUserKnownDevices(ctx, id); err != nil {
		return fmt.Errorf("forget devices: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit password change: %w", err)
	}
	return nil
}

func text(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

const SessionSweepInterval = 6 * time.Hour

const ResetTokenGrace = 7 * 24 * time.Hour

func (s *Store) SweepSessions(ctx context.Context) error {
	if err := s.q.DeleteExpiredSessions(ctx); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	if err := s.q.DeleteExpiredKnownDevices(ctx); err != nil {
		return fmt.Errorf("delete expired known devices: %w", err)
	}
	if err := s.q.DeleteDeadResetTokens(ctx, pgtype.Interval{
		Microseconds: int64(ResetTokenGrace / time.Microsecond), Valid: true,
	}); err != nil {
		return fmt.Errorf("delete dead reset tokens: %w", err)
	}
	return nil
}

// SweepSessionsForever runs once at startup, so a host redeployed more often
// than the interval still clears expired sessions, lapsed remembered browsers
// and dead reset tokens.
func (s *Store) SweepSessionsForever(ctx context.Context, log *slog.Logger) {
	ticker := time.NewTicker(SessionSweepInterval)
	defer ticker.Stop()
	for {
		if err := s.SweepSessions(ctx); err != nil && ctx.Err() == nil {
			log.ErrorContext(ctx, "sweep expired sessions", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Erase runs the schema's erase_user, the only door an account leaves by.
func (s *Store) Erase(ctx context.Context, userID string) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	if err := s.q.EraseUser(ctx, id); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
			switch pgErr.ConstraintName {
			case "erase_user_open_return":
				return ErrOpenReturn
			// users_keep_one_admin is the trigger behind erase_user's own
			// check, reached only if that check is bypassed; the refusal is the
			// same. The database's answer stays in the chain for callers that
			// name it.
			case "erase_user_keeps_one_admin", "users_keep_one_admin":
				return fmt.Errorf("%w: %w", ErrLastAdmin, err)
			}
		}
		return fmt.Errorf("erase user: %w", err)
	}
	return nil
}

var (
	ErrOAuthUnverified = errors.New("auth: google has not verified that address")
	ErrOAuthCollision  = errors.New("auth: that address already has an unverified account here")
	errOAuthIdentity   = errors.New("auth: google returned an invalid subject")
)

func (s *Store) SignInWithGoogle(ctx context.Context, id google.Identity) (user.User, error) {
	id, err := normaliseGoogleIdentity(id)
	if err != nil {
		return user.User{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return user.User{}, fmt.Errorf("begin google sign-in: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	// The provider subject, not its mutable email, is the identity. Serialising
	// on it makes the lookup and a possible link one decision across every
	// process. hashtextextended collisions only serialize unrelated sign-ins;
	// the unique index remains the final guard.
	if lockErr := q.LockGoogleSubject(ctx, id.Subject); lockErr != nil {
		return user.User{}, fmt.Errorf("lock google subject: %w", lockErr)
	}

	linked, lookupErr := q.UserByGoogleSubject(ctx, id.Subject)
	if lookupErr == nil {
		return finishGoogleSignIn(ctx, q, tx,
			linked.ID, linked.Email, linked.FullName, linked.Role)
	}
	if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return user.User{}, fmt.Errorf("read the linked account: %w", lookupErr)
	}

	candidateID, candidateEmail, candidateName, candidateRole, candidateErr :=
		googleLinkCandidate(ctx, q, id)
	if candidateErr != nil {
		return user.User{}, candidateErr
	}

	n, linkErr := q.LinkIdentity(ctx, db.LinkIdentityParams{
		UserID: candidateID, Subject: id.Subject,
	})
	if linkErr != nil {
		return user.User{}, fmt.Errorf("link the google identity: %w", linkErr)
	}
	if n == 0 {
		// Defensive even for a writer that did not take the advisory lock:
		// never commit a just-created orphan or return the email-selected
		// candidate.
		db.Rollback(ctx, tx)
		return s.googleSubjectOwner(ctx, id.Subject)
	}
	return finishGoogleSignIn(ctx, q, tx,
		candidateID, candidateEmail, candidateName, candidateRole)
}

func googleLinkCandidate(
	ctx context.Context,
	q *db.Queries,
	id google.Identity,
) (userID uuid.UUID, email string, fullName pgtype.Text, role string, err error) {
	existing, err := q.UserForOAuthLink(ctx, id.Email)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		row, createErr := q.CreateUserFromIdentity(ctx, db.CreateUserFromIdentityParams{
			Email: id.Email, FullName: id.Name,
		})
		if createErr != nil {
			// users_email_key is the real guard: another subject can claim the
			// address while this subject lock is held.
			if pgErr, ok := errors.AsType[*pgconn.PgError](createErr); ok && pgErr.Code == "23505" {
				return uuid.UUID{}, "", pgtype.Text{}, "", ErrOAuthCollision
			}
			return uuid.UUID{}, "", pgtype.Text{}, "",
				fmt.Errorf("create an account for a google identity: %w", createErr)
		}
		return row.ID, row.Email, row.FullName, row.Role, nil
	case err != nil:
		return uuid.UUID{}, "", pgtype.Text{}, "",
			fmt.Errorf("read the account for a google identity: %w", err)
	case !existing.Verified:
		// Pre-hijacking: an unverified account may belong to whoever registered
		// the address rather than whoever reads the mailbox.
		return uuid.UUID{}, "", pgtype.Text{}, "", ErrOAuthCollision
	default:
		return existing.ID, existing.Email, existing.FullName, existing.Role, nil
	}
}

func finishGoogleSignIn(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	userID uuid.UUID,
	email string,
	fullName pgtype.Text,
	role string,
) (user.User, error) {
	if err := q.TouchLastLogin(ctx, userID); err != nil {
		return user.User{}, fmt.Errorf("touch last login: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return user.User{}, fmt.Errorf("commit google sign-in: %w", err)
	}
	return user.User{
		ID: userID.String(), Email: email, Name: fullName.String, Role: user.Role(role),
	}, nil
}

func normaliseGoogleIdentity(id google.Identity) (google.Identity, error) {
	id.Email = strings.TrimSpace(id.Email)
	if !id.EmailVerified || EmailError(id.Email) != "" {
		return google.Identity{}, ErrOAuthUnverified
	}

	subject := strings.TrimSpace(id.Subject)
	if subject == "" || subject != id.Subject ||
		utf8.RuneCountInString(subject) > maxOAuthSubjectRunes || formtext.HasControlChars(subject) {
		return google.Identity{}, errOAuthIdentity
	}

	// The display name is decoration, not identity: a malformed or oversized
	// value must not prevent sign-in or become an unbounded row.
	id.Name = strings.TrimSpace(id.Name)
	if utf8.RuneCountInString(id.Name) > user.MaxNameRunes || formtext.HasControlChars(id.Name) {
		id.Name = ""
	}
	return id, nil
}

// googleSubjectOwner is the conflict fallback for a writer that did not take
// our lock.
func (s *Store) googleSubjectOwner(ctx context.Context, subject string) (user.User, error) {
	linked, err := s.q.UserByGoogleSubject(ctx, subject)
	if err != nil {
		return user.User{}, fmt.Errorf("read the winning google link: %w", err)
	}
	if err := s.q.TouchLastLogin(ctx, linked.ID); err != nil {
		return user.User{}, fmt.Errorf("touch last login: %w", err)
	}
	return user.User{
		ID: linked.ID.String(), Email: linked.Email,
		Name: linked.FullName.String, Role: user.Role(linked.Role),
	}, nil
}

func (s *Store) UnlinkGoogle(ctx context.Context, u user.User) error {
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	row, err := s.q.UserByID(ctx, id)
	if err != nil {
		return fmt.Errorf("read the account: %w", err)
	}
	if !row.HasPassword {
		return ErrLastSignInMethod
	}
	n, err := s.q.UnlinkIdentity(ctx, db.UnlinkIdentityParams{UserID: id, Provider: "google"})
	if err != nil {
		return fmt.Errorf("unlink google: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
