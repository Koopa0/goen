//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/outbox"
)

func TestResetMailDeliverySkipsObsoleteTokens(t *testing.T) {
	for _, state := range []string{"live", "expired", "spent", "superseded", "unknown"} {
		t.Run(state, func(t *testing.T) {
			f := newResetDeliveryFixture(t, nil)
			first := f.issue(t)
			live := first
			switch state {
			case "expired":
				expireResetMailToken(t, first)
			case "spent":
				if _, err := f.accounts.CompleteReset(t.Context(), first.Payload.Token, "a newly chosen long password"); err != nil {
					t.Fatalf("CompleteReset = %v", err)
				}
			case "superseded":
				live = f.issue(t)
			case "unknown":
				if _, err := pool.Exec(t.Context(), `DELETE FROM password_reset_tokens WHERE token_hash=$1`, account.HashToken(first.Payload.Token)); err != nil {
					t.Fatalf("remove reset token: %v", err)
				}
			}
			f.drain(t)
			assertResetMailState(t, first.ID, resetMailState{Delivered: true, Attempts: 1})
			if state == "live" || state == "superseded" {
				assertWorkingResetMail(t, f, live)
				if state == "superseded" {
					assertResetMailState(t, live.ID, resetMailState{Delivered: true, Attempts: 1})
				}
			} else {
				assertNoResetMail(t, f)
			}
		})
	}
}

func TestResetMailRetryRechecksTokenAfterSMTPRecovers(t *testing.T) {
	for _, state := range []string{"live", "expired", "superseded"} {
		t.Run(state, func(t *testing.T) {
			f := newResetDeliveryFixture(t, nil)
			first := f.issue(t)
			f.sender.failing.Store(true)
			f.drain(t)
			assertNoResetMail(t, f)
			assertResetMailState(t, first.ID, resetMailState{Attempts: 1, Failed: true})

			live := first
			switch state {
			case "expired":
				expireResetMailToken(t, first)
			case "superseded":
				live = f.issue(t)
			}
			makeResetMailDue(t, first.ID)
			f.sender.failing.Store(false)
			f.drain(t)
			assertResetMailState(t, first.ID, resetMailState{Delivered: true, Attempts: 2})
			if state == "expired" {
				assertNoResetMail(t, f)
			} else {
				assertWorkingResetMail(t, f, live)
				if state == "superseded" {
					assertResetMailState(t, live.ID, resetMailState{Delivered: true, Attempts: 1})
				}
			}
		})
	}
}

func TestResetMailTokenReadFailureRemainsRetryable(t *testing.T) {
	fault := &resetReadCancellation{}
	f := newResetDeliveryFixture(t, fault)
	mail := f.issue(t)
	fault.digest = account.HashToken(mail.Payload.Token)
	fault.enabled.Store(true)
	f.drain(t)
	if !fault.triggered.Load() {
		t.Fatal("the password reset token read did not encounter the injected driver cancellation")
	}
	if err := t.Context().Err(); err != nil {
		t.Fatalf("the worker's parent context was cancelled: %v", err)
	}
	assertNoResetMail(t, f)
	assertResetMailState(t, mail.ID, resetMailState{Attempts: 1, Failed: true})

	fault.enabled.Store(false)
	makeResetMailDue(t, mail.ID)
	f.drain(t)
	assertWorkingResetMail(t, f, mail)
	assertResetMailState(t, mail.ID, resetMailState{Delivered: true, Attempts: 2})
}

type resetReadCancellation struct {
	digest    []byte
	enabled   atomic.Bool
	triggered atomic.Bool
}

func (f *resetReadCancellation) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !f.enabled.Load() || !strings.HasPrefix(data.SQL, "-- name: PasswordResetToken :one") || len(data.Args) != 1 {
		return ctx
	}
	if digest, ok := data.Args[0].([]byte); ok && bytes.Equal(digest, f.digest) {
		f.triggered.Store(true)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		return cancelled
	}
	return ctx
}

func (*resetReadCancellation) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

type resetMailSender struct {
	messages  recordingSender
	recipient string
	failing   atomic.Bool
}

func (s *resetMailSender) Send(ctx context.Context, m *email.Message) error {
	if s.failing.Load() && strings.EqualFold(m.To, s.recipient) {
		return errors.New("smtp unavailable")
	}
	return s.messages.Send(ctx, m)
}

type resetDeliveryFixture struct {
	accounts *account.Store
	worker   *outbox.Store
	sender   *resetMailSender
	userID   uuid.UUID
	address  string
}

func newResetDeliveryFixture(t *testing.T, tracer pgx.QueryTracer) *resetDeliveryFixture {
	t.Helper()
	ctx := t.Context()
	storePool, err := openPool(ctx, pool.Config().ConnString(), quietLog)
	if err != nil {
		t.Fatalf("open store pool: %v", err)
	}
	cfg := storePool.Config().Copy()
	storePool.Close()
	cfg.MaxConns = 2
	if tracer != nil {
		cfg.ConnConfig.Tracer = tracer
	}
	storePool, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open delivery pool: %v", err)
	}
	t.Cleanup(storePool.Close)
	var role string
	if err := storePool.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil || role != "store" {
		t.Fatalf("delivery role = %q, want store: %v", role, err)
	}
	address := "reset-delivery-" + uuid.NewString() + "@example.com"
	f := &resetDeliveryFixture{
		accounts: account.NewStore(storePool), sender: &resetMailSender{recipient: address}, address: address,
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(email,email_verified_at) VALUES($1,now()) RETURNING id`, f.address).Scan(&f.userID); err != nil {
		t.Fatalf("create reset account: %v", err)
	}
	f.worker = newOutboxStore(workerDeps{
		pool: storePool, admin: storePool, maintenance: storePool, log: slog.New(slog.DiscardHandler),
		notifier: email.New(f.sender, "https://goen.test", "", ""), invoices: unconfiguredInvoicing(t),
	})
	return f
}

type queuedResetMail struct {
	ID      uuid.UUID
	Payload email.PasswordReset
}

func (f *resetDeliveryFixture) issue(t *testing.T) queuedResetMail {
	t.Helper()
	ctx := t.Context()
	if err := f.accounts.IssueReset(ctx, &outbox.PasswordResetRequest{UserID: f.userID.String(), Locale: "en"}); err != nil {
		t.Fatalf("IssueReset = %v", err)
	}
	var m queuedResetMail
	var payload []byte
	if err := pool.QueryRow(ctx, `SELECT id,payload FROM outbox_messages
		WHERE topic='account.password_reset' AND payload->>'email'=$1 ORDER BY id DESC LIMIT 1`, f.address).Scan(&m.ID, &payload); err != nil {
		t.Fatalf("read queued reset mail: %v", err)
	}
	if err := json.Unmarshal(payload, &m.Payload); err != nil {
		t.Fatalf("decode queued reset mail: %v", err)
	}
	return m
}

func (f *resetDeliveryFixture) drain(t *testing.T) {
	t.Helper()
	if _, _, err := f.worker.DrainAll(t.Context()); err != nil {
		t.Fatalf("DrainAll = %v", err)
	}
}

func expireResetMailToken(t *testing.T, m queuedResetMail) {
	t.Helper()
	// Keep the table's expiry-after-creation rule while advancing the fixture's
	// database age; no wall-clock delay or token-lifetime change is needed.
	if _, err := pool.Exec(t.Context(), `UPDATE password_reset_tokens
		SET created_at=now()-interval '2 hours', expires_at=now()-interval '1 hour'
		WHERE token_hash=$1`, account.HashToken(m.Payload.Token)); err != nil {
		t.Fatalf("age reset token: %v", err)
	}
}

func makeResetMailDue(t *testing.T, id uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE outbox_messages SET available_at=now() WHERE id=$1`, id); err != nil {
		t.Fatalf("make reset retry due: %v", err)
	}
}

type resetMailState struct {
	Delivered bool
	Attempts  int32
	Failed    bool
	Leased    bool
}

func assertResetMailState(t *testing.T, id uuid.UUID, want resetMailState) {
	t.Helper()
	var got resetMailState
	if err := pool.QueryRow(t.Context(), `SELECT delivered_at IS NOT NULL,attempts,last_error IS NOT NULL,lease_owner IS NOT NULL
		FROM outbox_messages WHERE id=$1`, id).Scan(&got.Delivered, &got.Attempts, &got.Failed, &got.Leased); err != nil {
		t.Fatalf("read reset delivery state: %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("reset mail state (-want +got):\n%s", diff)
	}
}

func assertNoResetMail(t *testing.T, f *resetDeliveryFixture) {
	t.Helper()
	if diff := cmp.Diff([]email.Message(nil), f.sender.messages.to(f.address)); diff != "" {
		t.Errorf("reset mail (-want +got):\n%s", diff)
	}
}

func assertWorkingResetMail(t *testing.T, f *resetDeliveryFixture, m queuedResetMail) {
	t.Helper()
	got := f.sender.messages.to(f.address)
	if len(got) != 1 {
		t.Fatalf("reset mail = %d messages, want one usable link", len(got))
	}
	if !strings.Contains(got[0].Body, "https://goen.test/reset?token="+m.Payload.Token) {
		t.Fatal("the delivered reset mail does not carry the current token")
	}
	if _, err := f.accounts.CompleteReset(t.Context(), m.Payload.Token, "another newly chosen long password"); err != nil {
		t.Fatalf("the delivered link cannot complete a password reset: %v", err)
	}
}
