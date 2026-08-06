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
)

// Field bounds for an issue, counted in RUNES so a Chinese letter is measured the
// way the person writing it would count it.
const (
	MaxIssueSubjectRunes = 120
	MaxIssueBodyRunes    = 20000
)

// ErrAlreadySent is an issue somebody is trying to send twice.
//
// Distinct from every other refusal because the caller's next step differs: there
// is nothing to fix, and the mail has already gone.
var ErrAlreadySent = errors.New("newsletter: that issue has already been sent")

// ActionSend is what a send is called in audit_events.
//
// Declared here rather than beside internal/admin's other actions, because the
// write is here: internal/admin imports this package, so the constant cannot live
// there without a cycle, and a raw string at the call site is a name nothing can
// find. internal/ui/pages/audit.go holds its label.
const ActionSend = "newsletter.send"

// ErrNoActor is a send with nobody to attribute it to.
//
// Refused before anything is enqueued rather than left to record_audit_event's
// own NOT NULL, so the caller gets a sentence instead of a SQLSTATE. Sending to
// the whole list is the least anonymous thing the back office does.
var ErrNoActor = errors.New("newsletter: a send needs a staff member to attribute it to")

// ErrNoSuchIssue is an issue id that matches nothing.
//
// Its own sentinel rather than ErrNotFound, which reads "that link is not usable"
// — a sentence about a confirmation link. Reusing it here put that message in the
// log for a missing issue, which sends whoever is reading to the wrong feature.
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
//
// Keys rather than sentences, for the reason every other validator in goen
// returns them: this runs from a handler and from a test.
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

// Compose writes a draft and returns its id. It sends nothing.
//
// Two steps rather than one, because a send is irreversible and a compose is not:
// ten thousand mailboxes cannot be edited, so the moment of no return gets its own
// button.
func (s *Store) Compose(ctx context.Context, subject, body string) (string, error) {
	if len(ValidateIssue(subject, body)) > 0 {
		return "", errors.New("composing an issue: refused by validation")
	}
	id, err := s.q.CreateNewsletterIssue(ctx, db.CreateNewsletterIssueParams{
		Subject: strings.TrimSpace(subject), Body: strings.TrimSpace(body),
	})
	if err != nil {
		return "", fmt.Errorf("composing an issue: %w", err)
	}
	return id.String(), nil
}

// Send enqueues one copy of an issue for everybody on the list, and stamps it.
//
// ONE transaction. The subscriber list, the messages and the stamp commit
// together, which is what makes the recipient count honest and the send
// unrepeatable:
//
//   - reading the list inside it means somebody who unsubscribes during the send
//     is either in it or not, never half — the alternative is mailing an address
//     the shop has already promised to stop mailing;
//   - the stamp's own WHERE clause carries `sent_at IS NULL`, and that is the
//     ONLY place "has this been sent?" is asked. There was a read-and-branch in Go
//     above it as well, and it had to go: it answered first in the ordinary case,
//     so the statement guard was almost never reached and a mutation deleting the
//     WHERE clause only sometimes went red. A redundant check that makes the real
//     one untestable is worse than no redundant check;
//   - the count comes from the rows actually enqueued rather than from a figure
//     read beforehand, so it cannot describe a send that did not happen.
//
// Every message goes in at [outbox.BulkPriority]. A receipt written a second later
// is delivered first, which is the difference between a newsletter and an outage.
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
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
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
			// The token the subscriber already holds, so the link in this issue is
			// the same one their welcome mail carried and the same one the next
			// issue will carry.
			UnsubscribeToken: to.UnsubscribeToken,
		}
		// Keyed on (issue, subscriber): a retried send cannot mail one person
		// twice, and two issues to one address are two messages.
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

	// In the SAME transaction as the send. The subject travels and the body does
	// not: audit_events is append-only and erase_user does not reach it, so a
	// letter copied there would outlive every other record of it, and
	// newsletter_issues already holds the text.
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
			issue.SentAt = r.SentAt.Time.Format("2006-01-02 15:04")
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
