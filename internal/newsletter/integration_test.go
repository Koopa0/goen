//go:build integration

package newsletter_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	htmlnode "golang.org/x/net/html"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/ratelimit"
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

// addr is an address nothing else in the suite touches: the unique index is on
// the address.
func addr(t *testing.T) string {
	t.Helper()
	return "news-" + strings.ReplaceAll(uuid.NewString(), "-", "") + "@goen.invalid"
}

func store(t *testing.T) *newsletter.Store {
	t.Helper()
	return newsletter.NewStore(pool)
}

func subscribed(t *testing.T, email string) (onList, known bool) {
	t.Helper()
	var unsubscribed *string
	err := pool.QueryRow(t.Context(),
		`SELECT unsubscribed_at::text FROM newsletter_subscribers WHERE lower(email) = lower($1)`,
		email).Scan(&unsubscribed)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return false, false
		}
		t.Fatalf("read subscriber: %v", err)
	}
	return unsubscribed == nil, true
}

func pendingConfirmations(t *testing.T, email string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM newsletter_confirmations WHERE lower(email) = lower($1)`,
		email).Scan(&n); err != nil {
		t.Fatalf("count confirmations: %v", err)
	}
	return n
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

func TestAnAddressIsNotOnTheListUntilItSaysSo(t *testing.T) {
	t.Parallel()
	s, email := store(t), addr(t)

	if _, err := s.Request(t.Context(), email); err != nil {
		t.Fatalf("Request: %v", err)
	}

	if _, known := subscribed(t, email); known {
		t.Error("the address is a subscriber after one form submission; " +
			"nothing has confirmed it owns the mailbox")
	}
	if got := pendingConfirmations(t, email); got != 1 {
		t.Errorf("pending confirmations = %d, want 1", got)
	}
	if got := enqueued(t, "newsletter.confirm", email); got != 1 {
		t.Errorf("confirmation messages = %d, want 1", got)
	}

	token := tokenFor(t, "newsletter.confirm", email, "token")
	if _, err := s.Confirm(t.Context(), token); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	onList, known := subscribed(t, email)
	if !known || !onList {
		t.Errorf("after confirming: known=%v onList=%v, want both true", known, onList)
	}
	if got := pendingConfirmations(t, email); got != 0 {
		t.Errorf("the confirmation survived being spent: %d rows left", got)
	}
}

func TestTheConfirmationLinkIsSpentByTheStatement(t *testing.T) {
	t.Parallel()
	s, email := store(t), addr(t)

	if _, err := s.Request(t.Context(), email); err != nil {
		t.Fatalf("Request: %v", err)
	}
	token := tokenFor(t, "newsletter.confirm", email, "token")

	if _, err := s.Confirm(t.Context(), token); err != nil {
		t.Fatalf("first Confirm: %v", err)
	}
	if _, err := s.Confirm(t.Context(), token); !errors.Is(err, newsletter.ErrNotFound) {
		t.Errorf("second Confirm with the same token = %v, want ErrNotFound", err)
	}
}

// TestAnExpiredConfirmationIsRefused judges the window by the clock that wrote
// expires_at.
func TestAnExpiredConfirmationIsRefused(t *testing.T) {
	t.Parallel()
	s, email := store(t), addr(t)

	if _, err := s.Request(t.Context(), email); err != nil {
		t.Fatalf("Request: %v", err)
	}
	token := tokenFor(t, "newsletter.confirm", email, "token")

	// created_at moves with it, or expires_after_created refuses the update.
	if _, err := pool.Exec(t.Context(), `
		UPDATE newsletter_confirmations
		SET created_at = now() - interval '50 hours',
		    expires_at = now() - interval '2 hours'
		WHERE lower(email) = lower($1)`, email); err != nil {
		t.Fatalf("age the confirmation: %v", err)
	}

	if _, err := s.Confirm(t.Context(), token); !errors.Is(err, newsletter.ErrNotFound) {
		t.Errorf("Confirm with an expired token = %v, want ErrNotFound", err)
	}
	if onList, known := subscribed(t, email); known || onList {
		t.Error("an expired link put the address on the list")
	}
}

func TestASecondSubmissionDoesNotMailAnActiveSubscriber(t *testing.T) {
	t.Parallel()
	s, email := store(t), addr(t)

	if _, err := s.Request(t.Context(), email); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if _, err := s.Confirm(t.Context(), tokenFor(t, "newsletter.confirm", email, "token")); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	outcome, err := s.Request(t.Context(), email)
	if err != nil {
		t.Fatalf("second Request: %v", err)
	}
	if outcome != newsletter.AlreadyActive {
		t.Errorf("outcome = %v, want AlreadyActive", outcome)
	}
	if got := pendingConfirmations(t, email); got != 0 {
		t.Errorf("a live subscriber has %d pending confirmations, want 0", got)
	}
	if got := enqueued(t, "newsletter.confirm", email); got != 1 {
		t.Errorf("confirmation messages = %d after two submissions, want 1 — the second "+
			"submission mailed somebody who is already on the list", got)
	}
}

func TestOneMailboxHoldsOneLiveLink(t *testing.T) {
	t.Parallel()
	s, email := store(t), addr(t)

	for range 3 {
		if _, err := s.Request(t.Context(), email); err != nil {
			t.Fatalf("Request: %v", err)
		}
	}
	if got := pendingConfirmations(t, email); got != 1 {
		t.Errorf("pending confirmations = %d after three submissions, want 1", got)
	}

	var digest []byte
	if err := pool.QueryRow(t.Context(),
		`SELECT digest FROM newsletter_confirmations WHERE lower(email) = lower($1)`,
		email).Scan(&digest); err != nil {
		t.Fatalf("read digest: %v", err)
	}
	newest := tokenFor(t, "newsletter.confirm", email, "token")
	if !bytes.Equal(newsletter.HashToken(newest), digest) {
		t.Error("the stored digest is not the newest token's; an earlier link is the live one")
	}
}

func TestUnsubscribingIsIdempotent(t *testing.T) {
	t.Parallel()
	s, email := store(t), addr(t)

	if _, err := s.Request(t.Context(), email); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if _, err := s.Confirm(t.Context(), tokenFor(t, "newsletter.confirm", email, "token")); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	leave := tokenFor(t, "newsletter.welcome", email, "unsubscribe_token")

	var first time.Time
	for i := range 2 {
		got, err := s.Unsubscribe(t.Context(), leave)
		if err != nil {
			t.Fatalf("Unsubscribe %d: %v", i+1, err)
		}
		if got != email {
			t.Errorf("Unsubscribe returned %q, want %q", got, email)
		}
		if i == 0 {
			// Age this fixture's first opt-out so the second database now()
			// cannot equal it, without sleeping or relying on clock precision.
			if err := pool.QueryRow(t.Context(), `
				UPDATE newsletter_subscribers
				SET confirmed_at = confirmed_at - interval '1 day',
				    unsubscribed_at = unsubscribed_at - interval '1 day'
				WHERE lower(email) = lower($1)
				RETURNING unsubscribed_at`, email).Scan(&first); err != nil {
				t.Fatalf("record first opt-out: %v", err)
			}
		}
	}

	onList, known := subscribed(t, email)
	if !known {
		t.Fatal("the row was deleted; the record of an opt-out has to survive")
	}
	if onList {
		t.Error("the address is still on the list after unsubscribing")
	}

	var repeated time.Time
	if err := pool.QueryRow(t.Context(), `
		SELECT unsubscribed_at
		FROM newsletter_subscribers WHERE lower(email) = lower($1)`, email).Scan(&repeated); err != nil {
		t.Fatalf("read repeated opt-out: %v", err)
	}
	if !repeated.Equal(first) {
		t.Errorf("repeated opt-out time = %s, want first opt-out %s", repeated, first)
	}
}

func TestAnUnknownUnsubscribeTokenIsRefused(t *testing.T) {
	t.Parallel()
	s := store(t)

	token, err := newsletter.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if _, err := s.Unsubscribe(t.Context(), token); !errors.Is(err, newsletter.ErrNotFound) {
		t.Errorf("Unsubscribe with an unknown token = %v, want ErrNotFound", err)
	}
}

func TestReSubscribingAfterOptingOutNeedsTheMailboxAgain(t *testing.T) {
	t.Parallel()
	s, email := store(t), addr(t)

	if _, err := s.Request(t.Context(), email); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if _, err := s.Confirm(t.Context(), tokenFor(t, "newsletter.confirm", email, "token")); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := s.Unsubscribe(t.Context(),
		tokenFor(t, "newsletter.welcome", email, "unsubscribe_token")); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	outcome, err := s.Request(t.Context(), email)
	if err != nil {
		t.Fatalf("Request after opting out: %v", err)
	}
	if outcome != newsletter.Requested {
		t.Errorf("outcome = %v, want Requested: an address that has left must be able "+
			"to come back, through its mailbox", outcome)
	}
	if onList, _ := subscribed(t, email); onList {
		t.Fatal("a form submission put an opted-out address back on the list without " +
			"asking the mailbox")
	}

	if _, err := s.Confirm(t.Context(), tokenFor(t, "newsletter.confirm", email, "token")); err != nil {
		t.Fatalf("Confirm the rejoin: %v", err)
	}
	if onList, _ := subscribed(t, email); !onList {
		t.Error("confirming did not clear the opt-out")
	}
}

func TestTheWelcomeMailCarriesAWorkingUnsubscribeLink(t *testing.T) {
	t.Parallel()
	s, email := store(t), addr(t)

	if _, err := s.Request(t.Context(), email); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if _, err := s.Confirm(t.Context(), tokenFor(t, "newsletter.confirm", email, "token")); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if got := enqueued(t, "newsletter.welcome", email); got != 1 {
		t.Fatalf("welcome messages = %d, want 1 — without one the unsubscribe token "+
			"reaches nobody and the subscription cannot be left", got)
	}

	got, err := s.Unsubscribe(t.Context(), tokenFor(t, "newsletter.welcome", email, "unsubscribe_token"))
	if err != nil {
		t.Fatalf("the token from the welcome mail does not work: %v", err)
	}
	if got != email {
		t.Errorf("Unsubscribe returned %q, want %q", got, email)
	}
}

// TestARefusedRequestMailsNothing is scoped to ONE address, because a global
// count in a parallel suite is order-dependent.
func TestARefusedRequestMailsNothing(t *testing.T) {
	t.Parallel()
	s := store(t)

	if _, err := s.Request(t.Context(), "not-an-address"); err == nil {
		t.Fatal("Request accepted an address that is not one")
	}
	if got := enqueued(t, "newsletter.confirm", "not-an-address"); got != 0 {
		t.Errorf("confirmation messages for a refused address = %d, want 0", got)
	}
}

// TestErasureTakesTheAddressOffTheList: the newsletter keys on the ADDRESS, so
// no cascade off the user row reaches it.
func TestErasureTakesTheAddressOffTheList(t *testing.T) {
	t.Parallel()
	s := store(t)
	email := addr(t)

	var userID uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name, email_verified_at)
		VALUES ($1, 'customer', '退訂測試', now()) RETURNING id`, email).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := s.Request(t.Context(), email); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if _, err := s.Confirm(t.Context(), tokenFor(t, "newsletter.confirm", email, "token")); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	// A link already in the mailbox would otherwise let the address rejoin.
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO newsletter_confirmations (email, digest, expires_at)
		VALUES ($1, sha256('leftover'::bytea), now() + interval '1 day')
		ON CONFLICT (lower(email)) DO UPDATE SET digest = EXCLUDED.digest`, email); err != nil {
		t.Fatalf("plant a pending confirmation: %v", err)
	}

	if _, err := pool.Exec(t.Context(), `SELECT erase_user($1)`, userID); err != nil {
		t.Fatalf("erase_user: %v", err)
	}

	if _, known := subscribed(t, email); known {
		t.Error("the erased address is still on the mailing list")
	}
	if got := pendingConfirmations(t, email); got != 0 {
		t.Errorf("%d pending confirmations survived the erasure — a link already in the "+
			"mailbox would put the address back", got)
	}
}

// TestTheAppCannotDeleteASubscriber proves the suppression record is not a
// handler's to remove: erase_user is the one door.
func TestTheAppCannotDeleteASubscriber(t *testing.T) {
	t.Parallel()
	email := addr(t)

	if _, err := pool.Exec(t.Context(), `
		INSERT INTO newsletter_subscribers (email, unsubscribe_token)
		VALUES ($1, 'token-' || replace($2, '-', '') || '-padding-to-thirty-two')`,
		email, email); err != nil {
		t.Fatalf("seed subscriber: %v", err)
	}

	tx, txErr := pool.Begin(t.Context())
	if txErr != nil {
		t.Fatalf("begin: %v", txErr)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), `SET ROLE store`); err != nil {
		t.Fatalf("SET ROLE store: %v", err)
	}

	_, err := tx.Exec(t.Context(),
		`DELETE FROM newsletter_subscribers WHERE lower(email) = lower($1)`, email)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "42501" {
		t.Errorf("store DELETE on newsletter_subscribers = %v, want insufficient_privilege", err)
	}
}

// TestTheShopCannotAddToItsOwnList is what double opt-in MEANS at the privilege
// layer: admin holds SELECT and nothing else.
func TestTheShopCannotAddToItsOwnList(t *testing.T) {
	t.Parallel()

	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), `SET ROLE admin`); err != nil {
		t.Fatalf("SET ROLE admin: %v", err)
	}

	for _, table := range []string{"newsletter_subscribers", "newsletter_confirmations"} {
		for _, verb := range []string{"INSERT", "UPDATE", "DELETE"} {
			var allowed bool
			if err := tx.QueryRow(t.Context(),
				`SELECT has_table_privilege('admin', $1, $2)`, table, verb).Scan(&allowed); err != nil {
				t.Fatalf("has_table_privilege: %v", err)
			}
			if allowed {
				t.Errorf("admin has %s on %s — the shop must not be able to put an "+
					"address on its own mailing list", verb, table)
			}
		}
		var canRead bool
		if err := tx.QueryRow(t.Context(),
			`SELECT has_table_privilege('admin', $1, 'SELECT')`, table).Scan(&canRead); err != nil {
			t.Fatalf("has_table_privilege: %v", err)
		}
		if !canRead {
			t.Errorf("admin cannot SELECT %s, so the shop cannot see its own list", table)
		}
	}
}

func TestASentIssueReachesEveryoneOnTheListExactlyOnce(t *testing.T) {
	emptyList(t)
	ctx := t.Context()
	s := store(t)

	stay, left := addr(t), addr(t)
	joinList(t, s, stay)
	leaveToken := joinList(t, s, left)
	if _, err := s.Unsubscribe(ctx, leaveToken); err != nil {
		t.Fatalf("unsubscribe %s: %v", left, err)
	}

	id, err := s.Compose(ctx, "本月新品", "三款值得看的耳機。", staffActor(t))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if n, err := s.Send(ctx, id, staffActor(t)); err != nil {
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
	if _, err := s.Unsubscribe(ctx, token); err != nil {
		t.Errorf("the token in the issue does not work: %v", err)
	}
}

// TestAnIssueIsSentOnceUnderConcurrency proves the STATEMENT guard: behind one
// barrier every racer passes the Go check, and exactly one may stamp.
func TestAnIssueIsSentOnceUnderConcurrency(t *testing.T) {
	emptyList(t)
	ctx := t.Context()
	s := store(t)
	joinList(t, s, addr(t))

	id, err := s.Compose(ctx, "只送一次", "內容。", staffActor(t))
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
			_, err := s.Send(ctx, id, staffActor(t))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				sent++
			case errors.Is(err, newsletter.ErrAlreadySent):
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
	s := store(t)
	for range 3 {
		joinList(t, s, addr(t))
	}

	id, err := s.Compose(ctx, "大量寄送", "內容。", staffActor(t))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if _, err := s.Send(ctx, id, staffActor(t)); err != nil {
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
	s := store(t)
	joinList(t, s, addr(t))

	id, err := s.Compose(ctx, "原本的主旨", "原本的內容。", staffActor(t))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	// A draft may still be edited.
	if _, editErr := pool.Exec(ctx,
		`UPDATE newsletter_issues SET subject = '改過的主旨' WHERE id = $1`, id); editErr != nil {
		t.Fatalf("a draft refused an edit: %v", editErr)
	}
	if _, sendErr := s.Send(ctx, id, staffActor(t)); sendErr != nil {
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
func joinList(t *testing.T, s *newsletter.Store, email string) string {
	t.Helper()
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
	s := store(t)
	joinList(t, s, addr(t))

	id, err := s.Compose(ctx, "序列送兩次", "內容。", staffActor(t))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if _, sendErr := s.Send(ctx, id, staffActor(t)); sendErr != nil {
		t.Fatalf("first Send: %v", sendErr)
	}
	if _, sendErr := s.Send(ctx, id, staffActor(t)); !errors.Is(sendErr, newsletter.ErrAlreadySent) {
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
	s := store(t)
	subscriber := addr(t)
	joinList(t, s, subscriber)

	id, err := s.Compose(ctx, "沒有操作者", "內容。", staffActor(t))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if _, sendErr := s.Send(ctx, id, uuid.NullUUID{}); !errors.Is(sendErr, newsletter.ErrNoActor) {
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
	s := store(t)
	actor := staffActor(t)
	const subject = "本月草稿"
	const body = "這段不得寫進 audit_events。"

	id, err := s.Compose(ctx, subject, body, actor)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	var after, actorID, auditedID, auditedSubject string
	if err := pool.QueryRow(ctx, `
		SELECT after::text, actor_user_id::text,
		       after->>'issue_id', after->>'subject'
		FROM audit_events
		WHERE entity_id = $1 AND action = $2`,
		id, newsletter.ActionCompose).Scan(&after, &actorID, &auditedID, &auditedSubject); err != nil {
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
	s := store(t)
	const subject = "沒有操作者的草稿"

	id, err := s.Compose(ctx, subject, "內容。", uuid.NullUUID{})
	if id != "" || !errors.Is(err, newsletter.ErrNoActor) {
		t.Errorf("Compose with no actor = %q, %v; want ErrNoActor", id, err)
	}

	var issues, audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM newsletter_issues WHERE subject = $1`, subject).Scan(&issues); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		WHERE action = $1 AND after->>'subject' = $2`,
		newsletter.ActionCompose, subject).Scan(&audits); err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if issues != 0 || audits != 0 {
		t.Errorf("unattributed compose left %d issue(s) and %d audit row(s), want 0/0", issues, audits)
	}
}

func TestComposeLeavesNeitherRowWhenAuditFails(t *testing.T) {
	ctx := t.Context()
	s := store(t)
	subject := "不得落地 " + uuid.NewString()[:8]
	missing := uuid.NullUUID{UUID: uuid.New(), Valid: true}

	id, err := s.Compose(ctx, subject, "內容。", missing)
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
		newsletter.ActionCompose, subject).Scan(&audits); err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if issues != 0 || audits != 0 {
		t.Errorf("failed audit left %d issue(s) and %d audit row(s), want 0/0", issues, audits)
	}
}

// TestAnUnsubscribeAfterTheSendIsHonouredAtDelivery re-reads consent at
// delivery. Send freezes the recipient list into one outbox row per subscriber,
// and an issue goes out at BulkPriority behind every transactional message,
// eight at a time on a five-second tick — so the window between enqueue and
// delivery is minutes to hours.
func TestAnUnsubscribeAfterTheSendIsHonouredAtDelivery(t *testing.T) {
	ctx := t.Context()
	s := newsletter.NewStore(pool)

	const address = "waiting@goen.invalid"
	var token string
	if err := pool.QueryRow(ctx, `
		INSERT INTO newsletter_subscribers (email, locale, unsubscribe_token, confirmed_at)
		VALUES ($1, 'zh-Hant', 'tok-waiting-probe-0123456789abcdefghij', now())
		RETURNING unsubscribe_token`, address).Scan(&token); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if yes, err := s.StillSubscribed(ctx, address); err != nil || !yes {
		t.Fatalf("StillSubscribed = %v, %v — want true before the opt-out", yes, err)
	}

	// The letter is now queued. They unsubscribe while it waits.
	if _, err := pool.Exec(ctx,
		`UPDATE newsletter_subscribers SET unsubscribed_at = now() WHERE unsubscribe_token = $1`,
		token); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}

	yes, err := s.StillSubscribed(ctx, address)
	if err != nil {
		t.Fatalf("StillSubscribed: %v", err)
	}
	if yes {
		t.Error("an address that opted out while its copy waited in the queue is " +
			"still reported as wanting the newsletter, so the copy goes out")
	}

	// Case-folded, because users_email_key and every other address comparison
	// here is: an opt-out typed in another case is the same mailbox.
	if shouty, err := s.StillSubscribed(ctx, "WAITING@GOEN.INVALID"); err != nil || shouty {
		t.Errorf("StillSubscribed(upper case) = %v, %v — want false", shouty, err)
	}
}

func TestNewsletterSuccessfulPostsRedirectBeforeRefresh(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, operation := range []string{"confirm", "unsubscribe"} {
			t.Run(string(locale)+"/"+operation, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				s, address := store(t), addr(t)
				if _, err := s.Request(ctx, address); err != nil {
					t.Fatalf("request subscription: %v", err)
				}
				token := tokenFor(t, "newsletter.confirm", address, "token")
				if operation == "unsubscribe" {
					if _, err := s.Confirm(ctx, token); err != nil {
						t.Fatalf("confirm subscription fixture: %v", err)
					}
					token = tokenFor(t, "newsletter.welcome", address, "unsubscribe_token")
				}
				cfg := pool.Config().Copy()
				cfg.MaxConns = 1
				cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
					_, err := conn.Exec(ctx, `SET ROLE store`)
					return err
				}
				p, err := pgxpool.NewWithConfig(ctx, cfg)
				if err != nil {
					t.Fatalf("open application pool: %v", err)
				}
				t.Cleanup(p.Close)
				h := newsletter.NewHandler(newsletter.NewStore(p), ratelimit.New(ratelimit.Config{
					Every: time.Second, Burst: 10, TTL: time.Hour, MaxKeys: 100,
				}), slog.New(slog.DiscardHandler))
				post, get := h.Confirm, h.ConfirmPage
				if operation == "unsubscribe" {
					post, get = h.Unsubscribe, h.UnsubscribePage
				}
				path := "/newsletter/" + operation
				snapshot := func() string {
					t.Helper()
					var state string
					if err := pool.QueryRow(ctx, `SELECT jsonb_build_object(
					    'subscriber', (SELECT to_jsonb(n) FROM newsletter_subscribers n WHERE lower(email) = lower($1)),
					    'tokens', (SELECT count(*) FROM newsletter_confirmations WHERE lower(email) = lower($1)),
					    'mail', (SELECT count(*) FROM outbox_messages WHERE lower(payload->>'email') = lower($1)))::text`,
						address).Scan(&state); err != nil {
						t.Fatalf("snapshot newsletter state: %v", err)
					}
					return state
				}
				repeatState := func() string {
					t.Helper()
					var state string
					if err := pool.QueryRow(ctx, `SELECT jsonb_build_object(
					    'subscriber', (SELECT to_jsonb(n) - 'updated_at' FROM newsletter_subscribers n WHERE lower(email) = lower($1)),
					    'tokens', (SELECT count(*) FROM newsletter_confirmations WHERE lower(email) = lower($1)),
					    'mail', (SELECT count(*) FROM outbox_messages WHERE lower(payload->>'email') = lower($1)))::text`,
						address).Scan(&state); err != nil {
						t.Fatalf("snapshot repeated newsletter state: %v", err)
					}
					return state
				}
				before := snapshot()
				form := httptest.NewRecorder()
				get(form, httptest.NewRequestWithContext(ctx, http.MethodGet, path+"?token="+token, http.NoBody))
				if form.Code != http.StatusOK || !strings.Contains(form.Body.String(), `name="token" value="`+token+`"`) {
					t.Fatalf("token GET = %d, want 200 confirmation form", form.Code)
				}
				if diff := cmp.Diff(before, snapshot()); diff != "" {
					t.Fatalf("token GET changed newsletter state (-before +after):\n%s", diff)
				}
				write := func() *httptest.ResponseRecorder {
					t.Helper()
					req := httptest.NewRequestWithContext(ctx, http.MethodPost, path,
						strings.NewReader(url.Values{"token": {token}}.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					res := httptest.NewRecorder()
					post(res, req)
					return res
				}
				res := write()
				if res.Code != http.StatusSeeOther {
					t.Errorf("successful %s POST = %d, want 303 before refresh", operation, res.Code)
				}
				location := res.Header().Get("Location")
				if location != path+"?done=1" {
					t.Errorf("successful %s Location = %q, want %q", operation, location, path+"?done=1")
				}
				if got := res.Header().Values("Set-Cookie"); len(got) != 0 {
					t.Errorf("successful POST Set-Cookie = %q, want none", got)
				}
				onList, known := subscribed(t, address)
				if !known || onList != (operation == "confirm") || pendingConfirmations(t, address) != 0 {
					t.Fatal("successful POST did not commit the requested subscription state and spend its confirmation")
				}
				heading, body := "Subscribed", "You are subscribed to the goen newsletter. We send occasionally. The unsubscribe link is in the email we just sent."
				if operation == "unsubscribe" {
					heading, body = "Unsubscribed", "You will not receive the goen newsletter again. Order notices are not affected."
				}
				if locale == i18n.ZhHant {
					heading, body = "\u5df2\u8a02\u95b1", "\u4f60\u5df2\u8a02\u95b1 goen \u96fb\u5b50\u5831\u3002\u4e0d\u5b9a\u671f\u5bc4\u9001\uff1b\u9000\u8a02\u9023\u7d50\u5728\u525b\u525b\u5bc4\u51fa\u7684\u90a3\u5c01\u4fe1\u88e1\u3002"
					if operation == "unsubscribe" {
						heading, body = "\u5df2\u9000\u8a02", "\u4f60\u4e0d\u6703\u518d\u6536\u5230 goen \u96fb\u5b50\u5831\u3002\u8a02\u55ae\u76f8\u95dc\u7684\u901a\u77e5\u4fe1\u4e0d\u53d7\u5f71\u97ff\u3002"
					}
				}
				committed := snapshot()
				checkAcknowledgement := func() {
					t.Helper()
					var first string
					for attempt := range 2 {
						ack := httptest.NewRecorder()
						get(ack, httptest.NewRequestWithContext(ctx, http.MethodGet, location, http.NoBody))
						if ack.Code != http.StatusOK || strings.Contains(ack.Body.String(), `class="notice__form"`) ||
							strings.Contains(ack.Body.String(), token) || strings.Contains(ack.Body.String(), address) {
							t.Errorf("acknowledgement GET = %d, want private-data-free 200 without a confirmation form", ack.Code)
						}
						for _, want := range []string{heading, body} {
							if !strings.Contains(ack.Body.String(), want) {
								t.Errorf("newsletter acknowledgement omits %q", want)
							}
						}
						if got := ack.Header().Values("Set-Cookie"); len(got) != 0 {
							t.Errorf("acknowledgement Set-Cookie = %q, want none", got)
						}
						if attempt == 0 {
							first = ack.Body.String()
						} else if ack.Body.String() != first {
							t.Error("refresh changed the acknowledgement")
						}
						if diff := cmp.Diff(committed, snapshot()); diff != "" {
							t.Errorf("acknowledgement GET changed committed state (-before +after):\n%s", diff)
						}
					}
				}
				if location == path+"?done=1" {
					checkAcknowledgement()
				}
				beforeRepeat := committed
				if operation == "unsubscribe" {
					beforeRepeat = repeatState()
				}
				repeated := write()
				want := http.StatusUnprocessableEntity
				if operation == "unsubscribe" {
					want = http.StatusSeeOther
				}
				if repeated.Code != want {
					t.Errorf("repeated %s POST = %d, want %d", operation, repeated.Code, want)
				}
				afterRepeat := snapshot()
				if operation == "unsubscribe" {
					afterRepeat = repeatState()
				}
				if diff := cmp.Diff(beforeRepeat, afterRepeat); diff != "" {
					t.Errorf("repeated POST changed committed state (-before +after):\n%s", diff)
				}
			})
		}
	}
}

func assertEmailLinkRecovery(t *testing.T, body, heading, reason, destination string) {
	t.Helper()
	doc, err := htmlnode.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var panel *htmlnode.Node
	var headings, duplicates []string
	ids := map[string]bool{}
	for n := range doc.Descendants() {
		id := emailLinkAttr(n, "id")
		if id != "" && ids[id] {
			duplicates = append(duplicates, id)
		}
		ids[id] = true
		if n.Type == htmlnode.ElementNode && n.Data == "h1" {
			headings = append(headings, emailLinkText(n))
			panel = n.Parent
		}
	}
	if panel == nil {
		t.Fatal("recovery has no heading")
	}
	type recoveryFacts struct {
		Headings, Reasons, Destinations, DuplicateIDs []string
		Envelopes, Tokens                            int
		ObsoleteProse                                bool
	}
	got := recoveryFacts{Headings: headings, DuplicateIDs: duplicates}
	for n := range panel.Descendants() {
		class := emailLinkAttr(n, "class")
		if n.Data == "p" && strings.Contains(class, "notice__body") {
			got.Reasons = append(got.Reasons, emailLinkText(n))
		}
		if strings.Contains(class, "goen-medallion") {
			got.Envelopes++
		}
		if emailLinkAttr(n, "name") == "token" {
			got.Tokens++
		}
		if strings.Contains(class, "goen-btn--primary") {
			got.Destinations = append(got.Destinations, emailLinkDestination(t, n, destination))
		}
	}
	text := emailLinkText(panel)
	got.ObsoleteProse = strings.Contains(text, "One button") || strings.Contains(text, "\u6309\u4e0b\u6309\u9215")
	want := recoveryFacts{Headings: []string{heading}, Reasons: []string{reason}, Destinations: []string{destination}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("email link recovery (-want +got):\n%s", diff)
	}
}

func emailLinkAttr(n *htmlnode.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func emailLinkText(n *htmlnode.Node) string {
	var text strings.Builder
	for part := range n.Descendants() {
		if part.Type == htmlnode.TextNode {
			text.WriteString(part.Data)
		}
	}
	return text.String()
}

func emailLinkDestination(t *testing.T, n *htmlnode.Node, want string) string {
	t.Helper()
	if n.Data == "a" {
		return emailLinkAttr(n, "href")
	}
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Data != "form" {
			continue
		}
		if want == "/newsletter" && emailLinkAttr(p, "method") != "post" {
			t.Error("newsletter recovery is not a plain POST form")
		}
		return emailLinkAttr(p, "action")
	}
	return ""
}

func TestDeadNewsletterLinksOfferRecovery(t *testing.T) {
	for _, locale := range i18n.Locales() {
		for _, state := range []string{"expired-confirm", "spent-confirm", "unknown-unsubscribe"} {
			t.Run(locale.Tag()+"/"+state, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				s, address := store(t), addr(t)
				if _, err := s.Request(ctx, address); err != nil {
					t.Fatal(err)
				}
				token := tokenFor(t, "newsletter.confirm", address, "token")
				switch state {
				case "expired-confirm":
					if _, err := pool.Exec(ctx, `UPDATE newsletter_confirmations
					    SET created_at = now() - interval '50 hours', expires_at = now() - interval '2 hours'
					    WHERE lower(email) = lower($1)`, address); err != nil {
						t.Fatal(err)
					}
				case "spent-confirm", "unknown-unsubscribe":
					if _, err := s.Confirm(ctx, token); err != nil {
						t.Fatal(err)
					}
				}
				h := recoveryNewsletterHandler(t)
				get, post, path := h.ConfirmPage, h.Confirm, "/newsletter/confirm"
				destination := "/newsletter"
				reason := "It may have been used already, or be more than two days old."
				if locale == i18n.ZhHant {
					reason = "\u9023\u7d50\u53ef\u80fd\u5df2\u7d93\u7528\u904e\u6216\u8d85\u904e\u5169\u5929\u3002"
				}
				if state == "unknown-unsubscribe" {
					get, post, path = h.UnsubscribePage, h.Unsubscribe, "/newsletter/unsubscribe"
					token = "unknown-" + uuid.NewString()
					destination = "mailto:contact@koopa0.dev"
					reason = "This unsubscribe link is not valid. If the newsletter keeps arriving, contact us."
					if locale == i18n.ZhHant {
						reason = "\u9019\u500b\u9000\u8a02\u9023\u7d50\u4e0d\u6b63\u78ba\u3002\u5982\u679c\u9084\u5728\u6536\u5230\u96fb\u5b50\u5831\uff0c\u8acb\u806f\u7d61\u6211\u5011\u3002"
					}
				}
				before := newsletterRecoveryState(t, address)
				page := httptest.NewRecorder()
				get(page, httptest.NewRequestWithContext(ctx, http.MethodGet, path+"?token="+token, http.NoBody))
				if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `name="token" value="`+token+`"`) {
					t.Fatal("GET validated a dead token instead of presenting the confirmation form")
				}
				if after := newsletterRecoveryState(t, address); after != before {
					t.Fatal("scanner GET changed newsletter state")
				}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(url.Values{"token": {token}}.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				res := httptest.NewRecorder()
				post(res, req)
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatalf("dead link POST = %d, want 422", res.Code)
				}
				heading := "This link has expired"
				if locale == i18n.ZhHant {
					heading = "\u9019\u500b\u9023\u7d50\u5df2\u5931\u6548"
				}
				assertEmailLinkRecovery(t, res.Body.String(), heading, reason, destination)
				if strings.Contains(res.Body.String(), token) {
					t.Error("dead-link recovery leaks the token")
				}
				if after := newsletterRecoveryState(t, address); after != before {
					t.Error("refused link changed newsletter state")
				}
			})
		}
	}
}

func recoveryNewsletterHandler(t *testing.T) *newsletter.Handler {
	t.Helper()
	cfg := pool.Config().Copy()
	cfg.MaxConns = 1
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE store`)
		return err
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return newsletter.NewHandler(newsletter.NewStore(p), ratelimit.New(ratelimit.Config{
		Every: time.Second, Burst: 10, TTL: time.Hour, MaxKeys: 100,
	}), slog.New(slog.DiscardHandler))
}

func newsletterRecoveryState(t *testing.T, address string) string {
	t.Helper()
	var state string
	if err := pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
	    'subscriber', (SELECT to_jsonb(n) FROM newsletter_subscribers n WHERE lower(email) = lower($1)),
	    'tokens', (SELECT jsonb_agg(to_jsonb(n) ORDER BY n.digest) FROM newsletter_confirmations n WHERE lower(email) = lower($1)),
	    'mail', (SELECT jsonb_agg(to_jsonb(n) ORDER BY n.id) FROM outbox_messages n WHERE lower(payload->>'email') = lower($1)))::text`,
		address).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}
