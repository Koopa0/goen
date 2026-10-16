//go:build integration

package content_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/content"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/outbox"
)

// addr is an address nothing else in the suite touches: the unique index is on
// the address.
func addr(t *testing.T) string {
	t.Helper()
	return "news-" + strings.ReplaceAll(uuid.NewString(), "-", "") + "@goen.invalid"
}

func enqueued(t *testing.T, topic, email string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM outbox_messages
		 WHERE topic = $1 AND payload->>'email' = $2`, topic, email).Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	return n
}

// tokenFor reads a token back out of an outbox payload. Newest by ID:
// outbox_messages has no created_at and its key is uuidv7.
func tokenFor(t *testing.T, topic, email, field string) string {
	t.Helper()
	var token string
	if err := pool.QueryRow(t.Context(),
		`SELECT payload->>`+`'`+field+`'`+` FROM outbox_messages
		 WHERE topic = $1 AND payload->>'email' = $2
		 ORDER BY id DESC LIMIT 1`, topic, email).Scan(&token); err != nil {
		t.Fatalf("read %s from %s payload: %v", field, topic, err)
	}
	return token
}

func TestASentIssueReachesEveryoneOnTheListExactlyOnce(t *testing.T) {
	emptyList(t)
	ctx := t.Context()
	s := newStore(pool)

	stay, left := addr(t), addr(t)
	joinList(t, stay)
	leaveToken := joinList(t, left)
	if _, err := newsletter.NewStore(pool).Unsubscribe(ctx, leaveToken); err != nil {
		t.Fatalf("unsubscribe %s: %v", left, err)
	}

	id, err := s.ComposeNewsletter(ctx, "本月新品", "三款值得看的耳機。", staffActor(t))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if n, err := s.SendNewsletter(ctx, id, staffActor(t)); err != nil {
		t.Fatalf("Send: %v", err)
	} else if n == 0 {
		t.Fatal("Send enqueued nothing")
	}

	if got := enqueued(t, "newsletter.issue", stay); got != 1 {
		t.Errorf("copies for the subscriber who stayed = %d, want 1", got)
	}
	if got := enqueued(t, "newsletter.issue", left); got != 0 {
		t.Errorf("copies for the address that unsubscribed = %d, want 0 — the shop "+
			"has already promised to stop emailing it", got)
	}

	token := tokenFor(t, "newsletter.issue", stay, "unsubscribe_token")
	if want := tokenFor(t, "newsletter.welcome", stay, "unsubscribe_token"); token != want {
		t.Errorf("the issue carries a different unsubscribe token from the welcome mail")
	}
	if _, err := newsletter.NewStore(pool).Unsubscribe(ctx, token); err != nil {
		t.Errorf("the token in the issue does not work: %v", err)
	}
}

// TestAnIssueIsSentOnceUnderConcurrency proves the STATEMENT guard: behind one
// barrier every racer passes the Go check, and exactly one may stamp.
func TestAnIssueIsSentOnceUnderConcurrency(t *testing.T) {
	emptyList(t)
	ctx := t.Context()
	s := newStore(pool)
	joinList(t, addr(t))

	id, err := s.ComposeNewsletter(ctx, "只送一次", "內容。", staffActor(t))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	const racers = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var sent int
	var others []error
	for range racers {
		wg.Go(func() {
			<-start
			_, err := s.SendNewsletter(ctx, id, staffActor(t))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				sent++
			case errors.Is(err, content.ErrAlreadySent):
			default:
				others = append(others, err)
			}
		})
	}
	close(start)
	wg.Wait()

	// Deterministic, because the statement's WHERE clause is the only place the
	// question is asked; a Go read-and-branch makes this count vary run to run.
	if sent != 1 {
		t.Errorf("%d of %d concurrent sends succeeded, want exactly 1 — the list was "+
			"mailed %d times", sent, racers, sent)
	}
	// A deadlock would also leave sent == 1 and would not be this rule working.
	if len(others) > 0 {
		t.Errorf("sends failed for reasons other than ErrAlreadySent: %v", others)
	}

	var stamped int
	if err := pool.QueryRow(ctx,
		`SELECT recipients FROM newsletter_issues WHERE id = $1`, id).Scan(&stamped); err != nil {
		t.Fatalf("read the stamp: %v", err)
	}
	if stamped != 1 {
		t.Errorf("the issue records %d recipients, want 1", stamped)
	}
}

// TestABulkSendWaitsBehindTransactionalMail asserts the priority rule through
// the queue's OWN claim; re-implementing its ORDER BY here could not fail.
func TestABulkSendWaitsBehindTransactionalMail(t *testing.T) {
	emptyList(t)
	emptyOutbox(t)
	ctx := t.Context()
	s := newStore(pool)
	for range 3 {
		joinList(t, addr(t))
	}

	id, err := s.ComposeNewsletter(ctx, "大量寄送", "內容。", staffActor(t))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if _, err := s.SendNewsletter(ctx, id, staffActor(t)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Written AFTER the whole send, so age alone would put it last.
	if _, err := pool.Exec(ctx, `
		INSERT INTO outbox_messages (topic, dedupe_key, payload)
		VALUES ('order.paid', $1, '{}'::jsonb)`, "urgent-"+uuid.NewString()); err != nil {
		t.Fatalf("enqueue the urgent message: %v", err)
	}

	var order []string
	var mu sync.Mutex
	record := func(topic string) func(context.Context, []byte) error {
		return func(context.Context, []byte) error {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, topic)
			return nil
		}
	}
	q := outbox.NewStore(pool, slog.New(slog.DiscardHandler))
	q.Handle("order.paid", record("order.paid"))
	q.Handle(outbox.TopicNewsletterIssue.Name(), record(outbox.TopicNewsletterIssue.Name()))
	q.Handle(outbox.TopicNewsletterConfirm.Name(), record("other"))
	q.Handle(outbox.TopicNewsletterWelcome.Name(), record("other"))
	// DrainAll, not one Drain: the assertion is about ORDER, and one batch
	// reaches both kinds only while BatchSize happens to exceed whatever else
	// the suite has left queued. Priority is applied per claim, so the ordering
	// holds across batch boundaries.
	if _, _, err := q.DrainAll(ctx); err != nil {
		t.Fatalf("DrainAll: %v", err)
	}

	firstIssue, lastUrgent := -1, -1
	for i, topic := range order {
		if topic == outbox.TopicNewsletterIssue.Name() && firstIssue < 0 {
			firstIssue = i
		}
		if topic == "order.paid" {
			lastUrgent = i
		}
	}
	if firstIssue < 0 || lastUrgent < 0 {
		t.Fatalf("the drain did not see both kinds of message: %v", order)
	}
	if firstIssue < lastUrgent {
		t.Errorf("a newsletter copy was delivered before a receipt written after it: %v",
			order)
	}
}

func TestASentIssueCannotBeRewritten(t *testing.T) {
	emptyList(t)
	ctx := t.Context()
	s := newStore(pool)
	joinList(t, addr(t))

	id, err := s.ComposeNewsletter(ctx, "原本的主旨", "原本的內容。", staffActor(t))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	// A draft may still be edited.
	if _, editErr := pool.Exec(ctx,
		`UPDATE newsletter_issues SET subject = '改過的主旨' WHERE id = $1`, id); editErr != nil {
		t.Fatalf("a draft refused an edit: %v", editErr)
	}
	if _, sendErr := s.SendNewsletter(ctx, id, staffActor(t)); sendErr != nil {
		t.Fatalf("Send: %v", sendErr)
	}

	_, err = pool.Exec(ctx,
		`UPDATE newsletter_issues SET subject = '事後改的主旨' WHERE id = $1`, id)
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "newsletter_issues_frozen_once_sent" {
		t.Errorf("editing a sent issue = %v, want newsletter_issues_frozen_once_sent", err)
	}
}

// emptyOutbox clears the queue: a drain reads the whole table.
func emptyOutbox(t *testing.T) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `DELETE FROM outbox_messages`); err != nil {
		t.Fatalf("empty the outbox: %v", err)
	}
}

// emptyList clears the subscriber table: a send reads the WHOLE list. Deleting
// works only because the test connects as the OWNER.
func emptyList(t *testing.T) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `DELETE FROM newsletter_subscribers`); err != nil {
		t.Fatalf("empty the list: %v", err)
	}
}

// joinList subscribes an address the way a visitor does, returning its token.
func joinList(t *testing.T, email string) string {
	t.Helper()
	s := newsletter.NewStore(pool)
	if _, err := s.Request(t.Context(), email); err != nil {
		t.Fatalf("Request %s: %v", email, err)
	}
	if _, err := s.Confirm(t.Context(), tokenFor(t, "newsletter.confirm", email, "token")); err != nil {
		t.Fatalf("Confirm %s: %v", email, err)
	}
	return tokenFor(t, "newsletter.welcome", email, "unsubscribe_token")
}

// TestASecondSendIsRefusedSequentially is the deterministic direction of the
// same guard; the concurrent test above goes red only sometimes.
func TestASecondSendIsRefusedSequentially(t *testing.T) {
	emptyList(t)
	ctx := t.Context()
	s := newStore(pool)
	joinList(t, addr(t))

	id, err := s.ComposeNewsletter(ctx, "序列送兩次", "內容。", staffActor(t))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if _, sendErr := s.SendNewsletter(ctx, id, staffActor(t)); sendErr != nil {
		t.Fatalf("first Send: %v", sendErr)
	}
	if _, sendErr := s.SendNewsletter(ctx, id, staffActor(t)); !errors.Is(sendErr, content.ErrAlreadySent) {
		t.Errorf("second Send = %v, want ErrAlreadySent", sendErr)
	}
}

// staffActor is somebody to attribute a send to; record_audit_event refuses a
// row with no actor.
func staffActor(t *testing.T) uuid.NullUUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ('sender-' || gen_random_uuid() || '@goen.invalid', 'admin', '寄送')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	return uuid.NullUUID{UUID: id, Valid: true}
}

func TestASendNeedsAnActor(t *testing.T) {
	emptyList(t)
	ctx := t.Context()
	s := newStore(pool)
	subscriber := addr(t)
	joinList(t, subscriber)

	id, err := s.ComposeNewsletter(ctx, "沒有操作者", "內容。", staffActor(t))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if _, sendErr := s.SendNewsletter(ctx, id, uuid.NullUUID{}); !errors.Is(sendErr, audit.ErrNoActor) {
		t.Errorf("Send with no actor = %v, want ErrNoActor", sendErr)
	}
	if got := enqueued(t, "newsletter.issue", subscriber); got != 0 {
		t.Errorf("%d copies were enqueued for a send that was refused, want 0", got)
	}
}

// TestComposeRecordsWhoWroteTheDraft locks the staff trail: a draft that is
// never sent still names who wrote it. The body stays out of audit_events
// because erase_user cannot reach that table.
func TestComposeRecordsWhoWroteTheDraft(t *testing.T) {
	ctx := t.Context()
	s := newStore(pool)
	actor := staffActor(t)
	const subject = "本月草稿"
	const body = "這段不得寫進 audit_events。"

	id, err := s.ComposeNewsletter(ctx, subject, body, actor)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	var after, actorID, auditedID, auditedSubject string
	if err := pool.QueryRow(ctx, `
		SELECT after::text, actor_user_id::text,
		       after->>'issue_id', after->>'subject'
		FROM audit_events
		WHERE entity_id = $1 AND action = $2`,
		id, audit.ActionComposeNewsletter).Scan(&after, &actorID, &auditedID, &auditedSubject); err != nil {
		t.Fatalf("read compose audit: %v", err)
	}
	if actorID != actor.UUID.String() {
		t.Errorf("compose actor = %s, want %s", actorID, actor.UUID)
	}
	if auditedID != id {
		t.Errorf("audit issue_id = %s, want %s", auditedID, id)
	}
	if auditedSubject != subject {
		t.Errorf("audit subject = %q, want %q", auditedSubject, subject)
	}
	if strings.Contains(after, body) {
		t.Errorf("audit after retained the body: %s", after)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM newsletter_issues WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("count issue: %v", err)
	}
	if n != 1 {
		t.Errorf("issues for the draft = %d, want 1", n)
	}
}

func TestComposeNeedsAnActor(t *testing.T) {
	ctx := t.Context()
	s := newStore(pool)
	const subject = "沒有操作者的草稿"

	id, err := s.ComposeNewsletter(ctx, subject, "內容。", uuid.NullUUID{})
	if id != "" || !errors.Is(err, audit.ErrNoActor) {
		t.Errorf("Compose with no actor = %q, %v; want ErrNoActor", id, err)
	}

	var issues, audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM newsletter_issues WHERE subject = $1`, subject).Scan(&issues); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		WHERE action = $1 AND after->>'subject' = $2`,
		audit.ActionComposeNewsletter, subject).Scan(&audits); err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if issues != 0 || audits != 0 {
		t.Errorf("unattributed compose left %d issue(s) and %d audit row(s), want 0/0", issues, audits)
	}
}

func TestComposeLeavesNeitherRowWhenAuditFails(t *testing.T) {
	ctx := t.Context()
	s := newStore(pool)
	subject := "不得落地 " + uuid.NewString()[:8]
	missing := uuid.NullUUID{UUID: uuid.New(), Valid: true}

	id, err := s.ComposeNewsletter(ctx, subject, "內容。", missing)
	if err == nil {
		t.Fatalf("Compose with an unrecordable actor = %s, nil; want audit error", id)
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "audit_events_actor_user_id_fkey" {
		t.Fatalf("compose audit insertion failure = %v, want audit actor FK", err)
	}

	var issues, audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM newsletter_issues WHERE subject = $1`, subject).Scan(&issues); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		WHERE action = $1 AND after->>'subject' = $2`,
		audit.ActionComposeNewsletter, subject).Scan(&audits); err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if issues != 0 || audits != 0 {
		t.Errorf("failed audit left %d issue(s) and %d audit row(s), want 0/0", issues, audits)
	}
}

// TestAnIssueCopyKeepsItsDedupeKey pins the outbox key of one copy of an issue:
// the issue id as the send was asked for it, a colon, and the hex SHA-256 of the
// subscriber's unsubscribe token. A retried send is told apart from a new one
// only by this key, so it must stay byte for byte what queued rows already carry.
func TestAnIssueCopyKeepsItsDedupeKey(t *testing.T) {
	emptyList(t)
	ctx := t.Context()
	s := newStore(pool)
	subscriber := addr(t)
	const token = "issue-key-golden-unsubscribe-token-0001"
	if _, err := pool.Exec(ctx, `
		INSERT INTO newsletter_subscribers (email, unsubscribe_token) VALUES ($1, $2)`,
		subscriber, token); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	id, err := s.ComposeNewsletter(ctx, "去重鍵", "內容。", staffActor(t))
	if err != nil {
		t.Fatalf("ComposeNewsletter: %v", err)
	}
	if _, err := s.SendNewsletter(ctx, id, staffActor(t)); err != nil {
		t.Fatalf("SendNewsletter: %v", err)
	}

	var key string
	if err := pool.QueryRow(ctx, `
		SELECT dedupe_key FROM outbox_messages
		WHERE topic = 'newsletter.issue' AND payload->>'email' = $1`, subscriber).Scan(&key); err != nil {
		t.Fatalf("read the copy's key: %v", err)
	}
	// printf '%s' "$token" | shasum -a 256
	const digest = "11e7fce1c5b322f4e3d2f436ca4e124e1f5965a0fd9b2cf16f0ef43c56cf8d42"
	if want := id + ":" + digest; key != want {
		t.Errorf("dedupe key = %q, want %q", key, want)
	}
}
