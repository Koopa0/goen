package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/db"
)

// A remembered browser is one that has signed in to an account. Its cookie
// authenticates nothing: it only lets sign-in charge that browser's wrong
// passwords to a budget of its own instead of the account's, which anybody who
// knows the address can spend (#1953). This is OWASP's device cookie ("Slow
// Down Online Guessing Attacks with Device Cookies"), with the state kept in
// known_devices so that a password change, a reset and "sign out other
// devices" can revoke it and a restart does not refill a stolen one.
const (
	// DeviceTTL is how long a browser stays remembered after it last signed
	// in. Every sign-in replaces the row, so a browser in use never lapses.
	DeviceTTL = 90 * 24 * time.Hour
	// DevicesPerAccount is how many browsers an account keeps; the oldest goes
	// when another signs in.
	DevicesPerAccount = 10
	// DeviceMisses is how many wrong passwords a remembered browser may send
	// in all before it is forgotten, and must sign in as a stranger would. It
	// bounds what a stolen cookie buys.
	DeviceMisses = 10
)

// DeviceCookieName carries __Host-, as the session's does.
const DeviceCookieName = "__Host-goen_device"

func deviceCookieName(secure bool) string {
	if secure {
		return DeviceCookieName
	}
	return "goen_device"
}

// SetDeviceCookie hands a browser the token of its known_devices row. Strict,
// because only the sign-in form, a same-site POST, ever needs to read it.
func SetDeviceCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: dev-only opt-out, secure by default
		Name:     deviceCookieName(secure),
		Value:    token,
		Path:     "/",
		MaxAge:   int(DeviceTTL / time.Second),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func readDeviceCookie(r *http.Request, secure bool) string {
	c, err := r.Cookie(deviceCookieName(secure))
	if err != nil {
		return ""
	}
	return c.Value
}

// knownDevice is the digest of the browser's remembered row when it is one for
// the account address names, and nil otherwise.
func (s *Store) knownDevice(ctx context.Context, token, address string) ([]byte, error) {
	if token == "" {
		return nil, nil
	}
	digest, err := s.q.KnownDeviceForAddress(ctx, db.KnownDeviceForAddressParams{
		Digest: bearer.Digest(token), Email: address,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read known device: %w", err)
	}
	return digest, nil
}

// countDeviceMiss records a wrong password sent with a remembered browser, and
// forgets the browser at the DeviceMisses-th.
func (s *Store) countDeviceMiss(ctx context.Context, digest []byte) error {
	misses, err := s.q.CountKnownDeviceMiss(ctx, digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("count known device miss: %w", err)
	}
	if misses < DeviceMisses {
		return nil
	}
	if err := s.q.DeleteKnownDevice(ctx, digest); err != nil {
		return fmt.Errorf("forget known device: %w", err)
	}
	return nil
}

// rememberDevice replaces the browser's row, if it presented one, with a new
// one for userID, and returns the new token for its cookie.
func (s *Store) rememberDevice(ctx context.Context, userID, presented string) (string, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return "", fmt.Errorf("parse user id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin remembering a device: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)
	if presented != "" {
		if err = q.DeleteKnownDevice(ctx, bearer.Digest(presented)); err != nil {
			return "", fmt.Errorf("retire the presented device: %w", err)
		}
	}
	token, err := issueDevice(ctx, q, id)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit remembered device: %w", err)
	}
	return token, nil
}

// issueDevice writes a new row for the account and drops all but its newest
// DevicesPerAccount, inside the caller's transaction.
func issueDevice(ctx context.Context, q *db.Queries, userID uuid.UUID) (string, error) {
	if _, err := q.LockUserForKnownDevices(ctx, userID); err != nil {
		return "", fmt.Errorf("lock account for its devices: %w", err)
	}
	token := bearer.New()
	if err := q.CreateKnownDevice(ctx, db.CreateKnownDeviceParams{
		Digest: bearer.Digest(token),
		UserID: userID,
		Ttl:    pgtype.Interval{Microseconds: int64(DeviceTTL / time.Microsecond), Valid: true},
	}); err != nil {
		return "", fmt.Errorf("create known device: %w", err)
	}
	if err := q.DeleteSurplusKnownDevices(ctx, db.DeleteSurplusKnownDevicesParams{
		UserID: userID, Keep: DevicesPerAccount,
	}); err != nil {
		return "", fmt.Errorf("drop surplus known devices: %w", err)
	}
	return token, nil
}
