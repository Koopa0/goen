package content

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/shoptime"
)

const (
	MaxNewsletterSubjectRunes = 120
	MaxNewsletterBodyRunes    = 20000
)

var ErrAlreadySent = errors.New("content: that newsletter issue has already been sent")

func validateNewsletter(subject, body string) map[string]i18n.Key {
	errs := map[string]i18n.Key{}
	switch n := utf8.RuneCountInString(strings.TrimSpace(subject)); {
	case n == 0:
		errs["subject"] = i18n.KeyIssueSubjectRequired
	case n > MaxNewsletterSubjectRunes:
		errs["subject"] = i18n.KeyIssueSubjectTooLong
	}
	switch n := utf8.RuneCountInString(strings.TrimSpace(body)); {
	case n == 0:
		errs["body"] = i18n.KeyIssueBodyRequired
	case n > MaxNewsletterBodyRunes:
		errs["body"] = i18n.KeyIssueBodyTooLong
	}
	return errs
}

// ComposeNewsletter writes the draft and the staff audit row in one
// transaction, so neither can commit without the other. The subject travels and
// the body does not: audit_events is append-only and erase_user does not reach
// it.
func (s *Store) ComposeNewsletter(ctx context.Context, subject, body string, actor uuid.NullUUID) (string, error) {
	if len(validateNewsletter(subject, body)) > 0 {
		return "", errors.New("composing an issue: refused by validation")
	}
	if !actor.Valid {
		return "", audit.ErrNoActor
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("beginning newsletter compose: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := db.New(tx)

	subject = strings.TrimSpace(subject)
	body = strings.TrimSpace(body)
	id, err := q.CreateNewsletterIssue(ctx, db.CreateNewsletterIssueParams{
		Subject: subject, Body: body,
	})
	if err != nil {
		return "", fmt.Errorf("composing an issue: %w", err)
	}

	after, err := json.Marshal(map[string]any{
		"issue_id": id.String(), "subject": subject,
	})
	if err != nil {
		return "", fmt.Errorf("encoding the audit row: %w", err)
	}
	// RecordNewsletterSend is the granted record_audit_event wrapper; compose
	// and send share it so a new query does not widen the admin grant.
	if err := q.RecordNewsletterSend(ctx, db.RecordNewsletterSendParams{
		Actor: actor, Action: string(audit.ActionComposeNewsletter), IssueID: id, After: after,
	}); err != nil {
		return "", fmt.Errorf("recording the compose: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("committing newsletter compose: %w", err)
	}
	return id.String(), nil
}

// SendNewsletter is ONE transaction, and the stamp's own `sent_at IS NULL` is
// the ONLY place "has this been sent?" is asked.
func (s *Store) SendNewsletter(ctx context.Context, issueID string, actor uuid.NullUUID) (int, error) {
	id, err := uuid.Parse(issueID)
	if err != nil {
		return 0, ErrNotFound
	}
	if !actor.Valid {
		return 0, audit.ErrNoActor
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("beginning newsletter send: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := db.New(tx)

	issue, err := q.NewsletterIssue(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("reading issue: %w", err)
	}
	subscribers, err := q.ActiveSubscribers(ctx)
	if err != nil {
		return 0, fmt.Errorf("reading the list: %w", err)
	}

	keys := make([]string, 0, len(subscribers))
	payloads := make([]email.NewsletterIssue, 0, len(subscribers))
	for i := range subscribers {
		to := &subscribers[i]
		payloads = append(payloads, email.NewsletterIssue{
			Locale: to.Locale, Email: to.Email,
			Subject: issue.Subject, Body: issue.Body,
			// The token the subscriber already holds, so every link ever mailed
			// to them goes on working.
			UnsubscribeToken: to.UnsubscribeToken,
		})
		// Keyed on (issue, subscriber): a retried send cannot mail one person
		// twice. The token is hashed so the key is not itself the link.
		keys = append(keys, issueID+":"+hex.EncodeToString(bearer.Digest(to.UnsubscribeToken)))
	}
	// One statement: a statement per subscriber spends the request's budget on
	// round trips, and a list too long for it could never be sent.
	if enqErr := outbox.EnqueueAll(ctx, q, outbox.TopicNewsletterIssue, outbox.BulkPriority, keys, payloads); enqErr != nil {
		return 0, enqErr
	}

	n, err := q.MarkNewsletterIssueSent(ctx, db.MarkNewsletterIssueSentParams{
		ID: id, Recipients: int32(len(subscribers)), SentBy: actor, //nolint:gosec // G115: bounded by the subscriber table
	})
	if err != nil {
		return 0, fmt.Errorf("marking the issue sent: %w", err)
	}
	if n == 0 {
		// Somebody else sent it between the read above and this statement; the
		// enqueues roll back with the transaction.
		return 0, ErrAlreadySent
	}

	// The subject travels and the body does not: audit_events is append-only
	// and erase_user does not reach it.
	after, err := json.Marshal(map[string]any{
		"issue_id": issueID, "subject": issue.Subject, "recipients": len(subscribers),
	})
	if err != nil {
		return 0, fmt.Errorf("encoding the audit row: %w", err)
	}
	if err := q.RecordNewsletterSend(ctx, db.RecordNewsletterSendParams{
		Actor: actor, Action: string(audit.ActionSendNewsletter), IssueID: id, After: after,
	}); err != nil {
		return 0, fmt.Errorf("recording the send: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("committing newsletter send: %w", err)
	}
	return len(subscribers), nil
}

func (s *Store) NewsletterIssues(ctx context.Context, limit int32) ([]NewsletterIssue, error) {
	rows, err := s.q.NewsletterIssues(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("reading issues: %w", err)
	}
	out := make([]NewsletterIssue, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		issue := NewsletterIssue{
			ID: r.ID.String(), Subject: r.Subject, Body: r.Body,
			Sent: r.SentAt.Valid, Recipients: r.Recipients, SentBy: r.SentByEmail,
		}
		if r.SentAt.Valid {
			issue.SentAt = shoptime.Minute(r.SentAt.Time)
		}
		out = append(out, issue)
	}
	return out, nil
}

// SubscriberCounts is the mailing list as the composer sees it. Awaiting is the
// requests whose confirmation link is still live.
type SubscriberCounts struct {
	Active        int64
	EnglishActive int64
	Unsubscribed  int64
	Awaiting      int64
}

func (s *Store) SubscriberCounts(ctx context.Context) (SubscriberCounts, error) {
	row, err := s.q.NewsletterCounts(ctx)
	if err != nil {
		return SubscriberCounts{}, fmt.Errorf("counting the list: %w", err)
	}
	return SubscriberCounts{Active: row.Active, EnglishActive: row.EnglishActive, Unsubscribed: row.Unsubscribed, Awaiting: row.Awaiting}, nil
}
