// Package orderaccess decides who may open a placed order's pages: a browser
// granted it by placing or finding the order, or the signed-in account that
// owns it.
package orderaccess

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/user"
)

// CookieName holds high-entropy tokens and never order numbers, which are
// guessable.
const CookieName = "__Host-goen_placed"

// Retain is how long a grant lives. The cookie's MaxAge is derived from it, so
// a grant is never shorter than the cookie that presents it.
const Retain = 30 * 24 * time.Hour

const SweepInterval = 24 * time.Hour

// ErrNotFound means the order number names no order.
var ErrNotFound = errors.New("orderaccess: order not found")

const (
	maxGrants = 10
	tokenSize = 32
)

type Store struct {
	pool   *pgxpool.Pool
	q      *db.Queries
	secure bool
}

func NewStore(pool *pgxpool.Pool, secure bool) *Store {
	if pool == nil {
		panic("orderaccess: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool), secure: secure}
}

// Grant writes the grant first: a cookie naming a token this database does not
// know locks the customer out of their own order.
func (s *Store) Grant(w http.ResponseWriter, r *http.Request, number string) error {
	ctx := r.Context()
	token, err := newToken()
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin grant order access: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	n, err := q.GrantOrderAccess(ctx, db.GrantOrderAccessParams{
		Digest: digest(token), OrderNumber: number,
	})
	if err != nil {
		return fmt.Errorf("grant access to order %s: %w", number, err)
	}
	if n == 0 {
		// The INSERT ... SELECT matched no order, which SQL does not call an
		// error.
		return fmt.Errorf("grant access to order %s: %w", number, ErrNotFound)
	}

	// Carried tokens get a fresh MaxAge, so their grants need the retention
	// clock restarted.
	carried := s.tokens(r)
	if len(carried) > 0 {
		if err := q.TouchOrderAccessGrants(ctx, db.TouchOrderAccessGrantsParams{
			Digests: digests(carried),
			Retain:  retainInterval(),
		}); err != nil {
			return fmt.Errorf("refresh carried order access grants: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit order access for %s: %w", number, err)
	}

	s.writeCookie(w, token, carried)
	return nil
}

// Revoke deletes the grants this browser presents as well as its cookie,
// because a client can ignore an expiry and present the tokens again.
func (s *Store) Revoke(w http.ResponseWriter, r *http.Request) error {
	defer s.expireCookie(w)
	tokens := s.tokens(r)
	if len(tokens) == 0 {
		return nil
	}
	if err := s.q.RevokeOrderAccess(r.Context(), digests(tokens)); err != nil {
		return fmt.Errorf("revoke this browser's order access: %w", err)
	}
	return nil
}

// Allows is true for a browser holding a grant for the order and for the
// signed-in account that owns it. The bool is the decision even when err is
// not nil: a lookup that failed counts as no, and the error says why.
func (s *Store) Allows(r *http.Request, number string) (bool, error) {
	granted, grantErr := s.granted(r, number)
	if granted {
		return true, nil
	}
	owned, ownErr := s.OwnedBySignedInUser(r, number)
	return owned, errors.Join(grantErr, ownErr)
}

// OwnedBySignedInUser is true only for the account the order was placed under.
func (s *Store) OwnedBySignedInUser(r *http.Request, number string) (bool, error) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		return false, nil
	}
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return false, nil //nolint:nilerr // an unparseable id simply owns nothing
	}
	owns, err := s.q.OrderBelongsTo(r.Context(), db.OrderBelongsToParams{
		OrderNumber: number, UserID: uuid.NullUUID{UUID: id, Valid: true},
	})
	if err != nil {
		return false, fmt.Errorf("check order ownership: %w", err)
	}
	return owns, nil
}

// Sweep deletes the grants nobody can present any more.
func (s *Store) Sweep(ctx context.Context) error {
	if err := s.q.DeleteOldOrderAccessGrants(ctx, retainInterval()); err != nil {
		return fmt.Errorf("delete old order access grants: %w", err)
	}
	return nil
}

func (s *Store) SweepForever(ctx context.Context, log *slog.Logger) {
	t := time.NewTicker(SweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
				log.ErrorContext(ctx, "sweep old order access grants", "error", err)
			}
		}
	}
}

func (s *Store) granted(r *http.Request, number string) (bool, error) {
	tokens := s.tokens(r)
	if len(tokens) == 0 || number == "" {
		return false, nil
	}
	ok, err := s.q.OrderAccessibleWith(r.Context(), db.OrderAccessibleWithParams{
		OrderNumber: number, Digests: digests(tokens), Retain: retainInterval(),
	})
	if err != nil {
		return false, fmt.Errorf("check order access grant: %w", err)
	}
	return ok, nil
}

func (s *Store) writeCookie(w http.ResponseWriter, token string, carried []string) {
	seen := make(map[string]bool, maxGrants)
	kept := make([]string, 0, maxGrants)
	for _, t := range append([]string{token}, carried...) {
		if seen[t] || t == "" {
			continue
		}
		seen[t] = true
		kept = append(kept, t)
		if len(kept) == maxGrants {
			break
		}
	}
	//nolint:gosec // G124: Secure is set from the deployment's own flag, below
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(),
		Value:    strings.Join(kept, "."),
		Path:     "/",
		MaxAge:   int(Retain / time.Second),
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// A browser replaces a cookie only with one of the same name and path, and
// refuses a __Host- name without Secure; the expiry sends both set-time
// attributes.
func (s *Store) expireCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: dev-only opt-out, secure by default
		Name:     s.cookieName(),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Store) tokens(r *http.Request) []string {
	c, err := r.Cookie(s.cookieName())
	if err != nil || c.Value == "" {
		return nil
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) > maxGrants {
		parts = parts[:maxGrants]
	}
	return parts
}

func (s *Store) cookieName() string {
	if s.secure {
		return CookieName
	}
	return "goen_placed"
}

func retainInterval() pgtype.Interval {
	return pgtype.Interval{Microseconds: int64(Retain / time.Microsecond), Valid: true}
}

func newToken() (string, error) {
	b := make([]byte, tokenSize)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func digest(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func digests(tokens []string) [][]byte {
	out := make([][]byte, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, digest(t))
	}
	return out
}
