//go:build integration

package account_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/outbox"
)

// statementLog is every statement a pool sends, in order.
type statementLog struct {
	mu  sync.Mutex
	sql []string
}

func (l *statementLog) TraceQueryStart(
	ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData,
) context.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sql = append(l.sql, data.SQL)
	return ctx
}

func (*statementLog) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// take returns what was recorded since the last call and starts again.
func (l *statementLog) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.sql
	l.sql = nil
	return out
}

// tracedStorePool is the storefront's role on one connection, with every
// statement it sends recorded. The connection is opened here, so its own set-up
// statements are not part of anything a test measures.
func tracedStorePool(t *testing.T, log *statementLog) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse traced pool config: %v", err)
	}
	cfg.MaxConns = 1
	cfg.ConnConfig.Tracer = log
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, setRoleErr := conn.Exec(ctx, `SET ROLE store`)
		return setRoleErr
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open traced pool: %v", err)
	}
	t.Cleanup(p.Close)
	if err := p.Ping(t.Context()); err != nil {
		t.Fatalf("open the traced connection: %v", err)
	}
	log.take()
	return p
}

// TestForgotDoesTheSameWorkWhetherOrNotTheAddressHasAnAccount holds /forgot to
// the promise its answer makes. Identical answers are not enough: a request
// that writes a token, a message and a commit for a customer's address and
// reads one row for a stranger's is timed apart from outside. So the request
// sends the same statements for both, and the token waits for the outbox
// worker, which only a real account reaches.
func TestForgotDoesTheSameWorkWhetherOrNotTheAddressHasAnAccount(t *testing.T) {
	ctx := t.Context()
	known := "forgot-known-" + uuid.NewString() + "@example.com"
	unknown := "forgot-unknown-" + uuid.NewString() + "@example.com"
	u := register(t, account.NewStore(pool), known)

	statements := &statementLog{}
	traced := tracedStorePool(t, statements)
	h := account.NewHandler(account.NewStore(traced), nil, slog.New(slog.DiscardHandler), false, nil)
	forgot := func(addr string) (*httptest.ResponseRecorder, []string) {
		statements.take()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/forgot",
			strings.NewReader(url.Values{"email": {addr}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.Forgot(rec, req)
		return rec, statements.take()
	}

	before := queuedResetRequests(t)
	knownRec, knownSQL := forgot(known)
	unknownRec, unknownSQL := forgot(unknown)

	if knownRec.Code != http.StatusSeeOther || unknownRec.Code != knownRec.Code {
		t.Fatalf("statuses known/unknown = %d/%d, want 303 for both",
			knownRec.Code, unknownRec.Code)
	}
	if k, u := knownRec.Header().Get("Location"), unknownRec.Header().Get("Location"); k != u {
		t.Errorf("redirects differ: known %q, unknown %q", k, u)
	}
	if len(knownSQL) == 0 {
		t.Fatal("the tracer recorded no statement for a request; the comparison below measures nothing")
	}
	if !slices.Equal(knownSQL, unknownSQL) {
		t.Errorf("a known address sends %d statements and an unknown one %d; they must be "+
			"the same work, statement for statement:\nknown:   %q\nunknown: %q",
			len(knownSQL), len(unknownSQL), knownSQL, unknownSQL)
	}

	var tokens int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM password_reset_tokens WHERE user_id = $1`,
		uuid.MustParse(u.ID)).Scan(&tokens); err != nil {
		t.Fatalf("count reset tokens: %v", err)
	}
	if tokens != 0 {
		t.Errorf("the request issued %d reset tokens itself; issuing is the worker's", tokens)
	}

	// The worker issues for the account, and the stranger's request is nothing.
	requests := queuedResetRequests(t)
	for key := range before {
		delete(requests, key)
	}
	if len(requests) != 2 {
		t.Fatalf("the two requests queued %d reset requests, want one each", len(requests))
	}
	for key, user := range requests {
		if user != "" && user != u.ID {
			t.Errorf("a queued request names account %s, which is neither address's", user)
		}
		if err := account.IssueQueuedReset(ctx, account.NewStore(pool), key); err != nil {
			t.Fatalf("issue the queued reset: %v", err)
		}
	}
	var mailed int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = $1 AND lower(payload->>'email') IN (lower($2), lower($3))`,
		outbox.TopicPasswordReset, known, unknown).Scan(&mailed); err != nil {
		t.Fatalf("count reset messages: %v", err)
	}
	if mailed != 1 {
		t.Errorf("the worker queued %d reset messages for the two requests, want one: "+
			"the account's", mailed)
	}
}

// queuedResetRequests is every undelivered forgotten-password request, by its
// dedupe key, with the account it names or "" for none.
func queuedResetRequests(t *testing.T) map[string]string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT dedupe_key, coalesce(payload->>'user_id', '')
		FROM outbox_messages
		WHERE topic = $1 AND delivered_at IS NULL`, outbox.TopicPasswordResetRequest)
	if err != nil {
		t.Fatalf("list queued reset requests: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, user string
		if err := rows.Scan(&key, &user); err != nil {
			t.Fatalf("read a queued reset request: %v", err)
		}
		out[key] = user
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read queued reset requests: %v", err)
	}
	return out
}

// TestResetRequestsForOneAddressAreBoundedWithoutSayingWhoHasAnAccount: a reset
// request mails the account at the address whoever asks, so the requests naming
// one address are bounded at the pace of every other form that mails an
// address, three and then one every ten minutes. The count is the address's
// own, so the refusal is the same whether or not it has an account.
func TestResetRequestsForOneAddressAreBoundedWithoutSayingWhoHasAnAccount(t *testing.T) {
	ctx := t.Context()
	h := account.NewHandler(account.NewStore(pool), nil, slog.New(slog.DiscardHandler), false, nil)
	known := registerProved(t, account.NewStore(pool), "forgot-bound-"+uuid.NewString()+"@example.com").Email
	unknown := "forgot-bound-nobody-" + uuid.NewString() + "@example.com"
	forgot := func(addr string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.Forgot(rec, cartForm(ctx, "/forgot", url.Values{"email": {addr}}))
		return rec
	}
	refusal := func(addr string) *httptest.ResponseRecorder {
		t.Helper()
		for i := range 3 {
			if rec := forgot(addr); rec.Code != http.StatusSeeOther {
				t.Fatalf("reset request %d for %s answered %d, want 303", i+1, addr, rec.Code)
			}
		}
		return forgot(addr)
	}
	knownRec, unknownRec := refusal(known), refusal(unknown)

	if knownRec.Code != http.StatusTooManyRequests || unknownRec.Code != knownRec.Code {
		t.Fatalf("a fourth reset request for a known and an unknown address answered %d and %d, "+
			"want 429 for both", knownRec.Code, unknownRec.Code)
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{"known": knownRec, "unknown": unknownRec} {
		wait, err := strconv.Atoi(rec.Header().Get("Retry-After"))
		if err != nil {
			t.Fatalf("the %s address's refusal carries Retry-After %q", name, rec.Header().Get("Retry-After"))
		}
		if wait <= 9*60 {
			t.Errorf("the %s address may ask again in %d s; want the ten-minute pace of every "+
				"form that mails an address", name, wait)
		}
	}
	// Retry-After counts down from each address's own first request, so its
	// value is compared only above.
	knownHeader, unknownHeader := knownRec.Header().Clone(), unknownRec.Header().Clone()
	knownHeader.Del("Retry-After")
	unknownHeader.Del("Retry-After")
	if diff := cmp.Diff(knownHeader, unknownHeader); diff != "" {
		t.Errorf("the refusals for a known and an unknown address differ (-known +unknown):\n%s", diff)
	}
	if knownRec.Body.String() != unknownRec.Body.String() {
		t.Errorf("the refusals for a known and an unknown address answer %q and %q",
			knownRec.Body.String(), unknownRec.Body.String())
	}
}
