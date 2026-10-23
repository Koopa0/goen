package auth

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/web/ratelimit"
)

// BeginReset runs a forgotten-password request through both of its halves:
// the queueing /forgot does, then the issuing the outbox worker does for that
// one request. External integration tests read the token from the queued
// PasswordReset payload, so no token or recipient enters the production API.
func BeginReset(ctx context.Context, s *Store, email string) error {
	key := "reset-request:" + uuid.NewString()
	if err := s.queueResetRequest(ctx, email, key); err != nil {
		return err
	}
	return IssueQueuedReset(ctx, s, key)
}

// IssueQueuedReset delivers one queued forgotten-password request as the outbox
// worker would: it takes the message and hands its payload to IssueReset. A key
// with no message, which is what an unusable address leaves, is nothing to do.
func IssueQueuedReset(ctx context.Context, s *Store, dedupeKey string) error {
	var payload []byte
	err := s.pool.QueryRow(ctx, `
		UPDATE outbox_messages SET delivered_at = now()
		WHERE topic = $1 AND dedupe_key = $2 AND delivered_at IS NULL
		RETURNING payload`, outbox.TopicPasswordResetRequest.Name(), dedupeKey).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var req outbox.PasswordResetRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return err
	}
	return s.IssueReset(ctx, &req)
}

// RequestVerification exposes the handler-owned operation to external tests;
// tests read the opaque token from the queued AddressVerify payload.
func RequestVerification(ctx context.Context, s *Store, userID, email string) error {
	return s.requestVerification(ctx, userID, email)
}

// LowerPasswordCostForTests makes HashPassword cheap for this package's own
// test binary. Production cannot call it: export_test.go is never linked
// into the program.
func LowerPasswordCostForTests() { passwordCost = argon2Cost{time: 1, memory: 1024, threads: 1} }

// LettersLeft is how many more letters key's flow may send its address now,
// such as "resend:" and the address. It reserves every one and gives them all
// back, so asking spends nothing.
func LettersLeft(h *Handler, key string) int {
	var held []*ratelimit.Reservation
	for {
		r, _, ok := h.letterLimit.Reserve(key)
		if !ok {
			break
		}
		held = append(held, r)
	}
	for _, r := range held {
		r.Refund()
	}
	return len(held)
}
