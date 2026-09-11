package newsletter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/shoptime"
)

// Field bounds for an issue, counted in RUNES.
const (
	MaxIssueSubjectRunes = 120
	MaxIssueBodyRunes    = 20000
)

// ErrAlreadySent is an issue somebody is trying to send twice.
var ErrAlreadySent = errors.New("newsletter: that issue has already been sent")

// Action names live here rather than beside internal/admin's other actions
// because internal/admin imports this package.
const (
	ActionSend    = "newsletter.send"
	ActionCompose = "newsletter.compose"
)

// ErrNoActor is a compose or send with nobody to attribute it to.
var ErrNoActor = errors.New("newsletter: a staff write needs a staff member to attribute it to")

// ErrNoSuchIssue is an issue id that matches nothing.
var ErrNoSuchIssue = errors.New("newsletter: no such issue")

// Issue is one newsletter as the back office sees it.
type Issue struct {
	ID         string
	Subject    string
	Body       string
	Sent       bool
	SentAt     string
	Recipients int32
	SentBy     string
}

// ValidateIssue returns the field errors in an issue, keyed by form field.
func ValidateIssue(subject, body string) map[string]i18n.Key {
	errs := map[string]i18n.Key{}
	switch n := utf8.RuneCountInString(strings.TrimSpace(subject)); {
	case n == 0:
		errs["subject"] = i18n.KeyIssueSubjectRequired
	case n > MaxIssueSubjectRunes:
		errs["subject"] = i18n.KeyIssueSubjectTooLong
	}
	switch n := utf8.RuneCountInString(strings.TrimSpace(body)); {
	case n == 0:
		errs["body"] = i18n.KeyIssueBodyRequired
	case n > MaxIssueBodyRunes:
		errs["body"] = i18n.KeyIssueBodyTooLong
	}
	return errs
}

// Compose writes a draft and the staff audit row that names who wrote it.
// One transaction: a draft with no trail, or a trail with no draft, cannot
// commit. The subject travels and the body does not — audit_events is
// append-only and erase_user does not reach it.
func (s *Store) Compose(ctx context.Context, subject, body string, actor uuid.NullUUID) (string, error) {
	if len(ValidateIssue(subject, body)) > 0 {
		return "", errors.New("composing an issue: refused by validation")
	}
	if !actor.Valid {
		return "", ErrNoActor
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("beginning newsletter compose: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
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
		Actor: actor, Action: ActionCompose, IssueID: id, After: after,
	}); err != nil {
		return "", fmt.Errorf("recording the compose: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("committing newsletter compose: %w", err)
	}
	return id.String(), nil
}

// Send enqueues one copy of an issue for everybody on the list, and stamps it.
// ONE transaction, and the stamp's own `sent_at IS NULL` is the ONLY place "has
// this been sent?" is asked.
func (s *Store) Send(ctx context.Context, issueID string, actor uuid.NullUUID) (int, error) {
	id, err := uuid.Parse(issueID)
	if err != nil {
		return 0, ErrNoSuchIssue
	}
	if !actor.Valid {
		return 0, ErrNoActor
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("beginning newsletter send: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := db.New(tx)

	issue, err := q.NewsletterIssue(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNoSuchIssue
		}
		return 0, fmt.Errorf("reading issue: %w", err)
	}
	subscribers, err := q.ActiveSubscribers(ctx)
	if err != nil {
		return 0, fmt.Errorf("reading the list: %w", err)
	}

	for i := range subscribers {
		to := &subscribers[i]
		payload := email.NewsletterIssue{
			Locale: to.Locale, Email: to.Email,
			Subject: issue.Subject, Body: issue.Body,
			// The token the subscriber already holds, so every link ever mailed
			// to them goes on working.
			UnsubscribeToken: to.UnsubscribeToken,
		}
		// Keyed on (issue, subscriber): a retried send cannot mail one person twice.
		key := issueID + ":" + dedupeOf(to.UnsubscribeToken)
		if enqErr := enqueueBulk(ctx, q, outbox.TopicNewsletterIssue, key, payload); enqErr != nil {
			return 0, enqErr
		}
	}

	n, err := q.MarkNewsletterIssueSent(ctx, db.MarkNewsletterIssueSentParams{
		ID: id, Recipients: int32(len(subscribers)), SentBy: actor, //nolint:gosec // G115: bounded by the subscriber table
	})
	if err != nil {
		return 0, fmt.Errorf("marking the issue sent: %w", err)
	}
	if n == 0 {
		// Somebody else sent it between the read above and this statement. The
		// enqueues roll back with the transaction.
		return 0, ErrAlreadySent
	}

	// The subject travels and the body does not: audit_events is append-only and
	// erase_user does not reach it.
	after, err := json.Marshal(map[string]any{
		"issue_id": issueID, "subject": issue.Subject, "recipients": len(subscribers),
	})
	if err != nil {
		return 0, fmt.Errorf("encoding the audit row: %w", err)
	}
	if err := q.RecordNewsletterSend(ctx, db.RecordNewsletterSendParams{
		Actor: actor, Action: ActionSend, IssueID: id, After: after,
	}); err != nil {
		return 0, fmt.Errorf("recording the send: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("committing newsletter send: %w", err)
	}
	return len(subscribers), nil
}

// Issues is the back office's list, newest first.
func (s *Store) Issues(ctx context.Context, limit int32) ([]Issue, error) {
	rows, err := s.q.NewsletterIssues(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("reading issues: %w", err)
	}
	out := make([]Issue, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		issue := Issue{
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

// Counts is how many addresses are in each state.
type Counts struct {
	Active       int64
	Unsubscribed int64
	Awaiting     int64
}

// Counts reads the three figures the back office asks for.
func (s *Store) Counts(ctx context.Context) (Counts, error) {
	row, err := s.q.NewsletterCounts(ctx)
	if err != nil {
		return Counts{}, fmt.Errorf("counting the list: %w", err)
	}
	return Counts{Active: row.Active, Unsubscribed: row.Unsubscribed, Awaiting: row.Awaiting}, nil
}
