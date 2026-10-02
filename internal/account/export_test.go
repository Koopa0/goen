package account

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/outbox"
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

// SetGoogleHTTPClient lets integration tests replace Google's two HTTP
// endpoints without making transport injection part of the production API.
func SetGoogleHTTPClient(g *Google, client *http.Client) {
	g.http = client
}
