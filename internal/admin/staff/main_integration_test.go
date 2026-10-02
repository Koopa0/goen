//go:build integration

package staff_test

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/db/dbtest"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p
	code := m.Run()
	stop()
	os.Exit(code)
}

// asActor is the context the access wrapper leaves for a signed-in admin.
func asActor(ctx context.Context, id string) context.Context {
	return account.WithUser(ctx, account.User{ID: id, Role: "admin"})
}

// enrolFactor gives a user a confirmed second factor; these tests are about who
// may remove it, not about generating codes.
func enrolFactor(t *testing.T, userID string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO staff_totp_credentials (user_id, secret_encrypted, confirmed_at)
		VALUES ($1, '\x01', now())`, userID); err != nil {
		t.Fatalf("enrol %s: %v", userID, err)
	}
}

// spellingsOf is every form uuid.Parse reads as id, the way a hand-edited
// form field would send it.
func spellingsOf(id string) []string {
	return []string{
		id,
		strings.ToUpper(id),
		"{" + id + "}",
		"urn:uuid:" + id,
	}
}
func roleOf(t *testing.T, userID string) string {
	t.Helper()
	var role string
	if err := pool.QueryRow(t.Context(),
		`SELECT role FROM users WHERE id = $1`, userID).Scan(&role); err != nil {
		t.Fatalf("read role: %v", err)
	}
	return role
}
func enrolled(t *testing.T, userID string) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM staff_totp_credentials WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	return n > 0
}
func staffAuditCount(t *testing.T, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM audit_events WHERE action = $1`, action).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", action, err)
	}
	return n
}
func readStaffAudit(t *testing.T, action, target string) (actor, email, role string) {
	t.Helper()
	if err := pool.QueryRow(t.Context(), `
		SELECT actor_user_id::text, coalesce(after->>'email', ''), coalesce(after->>'role', '')
		FROM audit_events
		WHERE action = $1 AND entity_id = $2
		ORDER BY occurred_at DESC, id DESC LIMIT 1`, action, target).
		Scan(&actor, &email, &role); err != nil {
		t.Fatalf("read %s audit for %s: %v", action, target, err)
	}
	return actor, email, role
}
func rolePool(t *testing.T, applicationName, role string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse twofactor application pool config: %v", err)
	}
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["application_name"] = applicationName
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, execErr := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize())
		return execErr
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open twofactor application pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}
func waitForLock(t *testing.T, applicationName string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("%s returned before reaching the intended database lock: %v",
				applicationName, err)
		default:
		}
		var waiting bool
		err := pool.QueryRow(t.Context(), `
			SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE application_name = $1 AND wait_event_type = 'Lock'
			)`, applicationName).Scan(&waiting)
		if err == nil && waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never blocked on the intended database lock: %v", applicationName, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func operationResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("operation did not finish after its database lock was released")
		return nil
	}
}
