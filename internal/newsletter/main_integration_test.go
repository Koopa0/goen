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
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/ui/uitest"
	"github.com/koopa0/goen/internal/web/ratelimit"
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

// TestStore_ConfirmDoubleOptIn: an address is not on the list until its mailbox
// says so.
func TestStore_ConfirmDoubleOptIn(t *testing.T) {
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

// TestStore_ConfirmSpentOnce: the confirmation link is spent by the statement
// that confirms.
func TestStore_ConfirmSpentOnce(t *testing.T) {
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

// TestStore_ConfirmExpired: an expired confirmation is refused.
//
// TestStore_ConfirmExpired judges the window by the clock that wrote
// expires_at.
func TestStore_ConfirmExpired(t *testing.T) {
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

// TestStore_RequestActiveSubscriber: a second submission does not mail an
// active subscriber.
func TestStore_RequestActiveSubscriber(t *testing.T) {
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

// TestStore_RequestOneLiveLink: one mailbox holds one live link, the newest.
func TestStore_RequestOneLiveLink(t *testing.T) {
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
	if !bytes.Equal(bearer.Digest(newest), digest) {
		t.Error("the stored digest is not the newest token's; an earlier link is the live one")
	}
}

// TestStore_UnsubscribeIdempotent: unsubscribing is idempotent and keeps the
// first opt-out time.
func TestStore_UnsubscribeIdempotent(t *testing.T) {
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

// TestStore_UnsubscribeUnknownToken: an unknown unsubscribe token is refused.
func TestStore_UnsubscribeUnknownToken(t *testing.T) {
	t.Parallel()
	s := store(t)

	token := bearer.New()
	if _, err := s.Unsubscribe(t.Context(), token); !errors.Is(err, newsletter.ErrNotFound) {
		t.Errorf("Unsubscribe with an unknown token = %v, want ErrNotFound", err)
	}
}

// TestStore_RequestResubscribe: re-subscribing after opting out needs the
// mailbox again.
func TestStore_RequestResubscribe(t *testing.T) {
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

// TestStore_ConfirmWelcomeToken: the welcome mail carries a working unsubscribe
// link.
func TestStore_ConfirmWelcomeToken(t *testing.T) {
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

// TestStore_RequestRefusedMailsNothing: a refused request mails nothing.
//
// TestStore_RequestRefusedMailsNothing is scoped to ONE address, because a
// global count in a parallel suite is order-dependent.
func TestStore_RequestRefusedMailsNothing(t *testing.T) {
	t.Parallel()
	s := store(t)

	if _, err := s.Request(t.Context(), "not-an-address"); err == nil {
		t.Fatal("Request accepted an address that is not one")
	}
	if got := enqueued(t, "newsletter.confirm", "not-an-address"); got != 0 {
		t.Errorf("confirmation messages for a refused address = %d, want 0", got)
	}
}

// TestEraseUserNewsletterList: erasure takes the address off the newsletter
// list and drops its pending confirmation.
//
// The newsletter keys on the ADDRESS, so no cascade off the user row reaches
// it.
func TestEraseUserNewsletterList(t *testing.T) {
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

// TestNewsletterSubscribersNoDelete: the app cannot delete a subscriber: role
// store has no DELETE on newsletter_subscribers.
//
// TestNewsletterSubscribersNoDelete proves the suppression record is not a
// handler's to remove: erase_user is the one door.
func TestNewsletterSubscribersNoDelete(t *testing.T) {
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

// TestNewsletterTablesAdminReadOnly: the shop cannot add to its own list: role
// admin holds only SELECT on the newsletter tables.
//
// TestNewsletterTablesAdminReadOnly is what double opt-in MEANS at the
// privilege layer: admin holds SELECT and nothing else.
func TestNewsletterTablesAdminReadOnly(t *testing.T) {
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

// TestStore_StillSubscribedAfterOptOut: an unsubscribe after the send is
// honoured at delivery.
//
// TestStore_StillSubscribedAfterOptOut re-reads consent at delivery. Send
// freezes the recipient list into one outbox row per subscriber, and an issue
// goes out at BulkPriority behind every transactional message, eight at a time
// on a five-second tick — so the window between enqueue and delivery is minutes
// to hours.
func TestStore_StillSubscribedAfterOptOut(t *testing.T) {
	ctx := t.Context()
	s := newsletter.NewStore(pool)

	// Its own address and token: the subscriber table is not emptied between runs.
	address := addr(t)
	var token string
	if err := pool.QueryRow(ctx, `
		INSERT INTO newsletter_subscribers (email, locale, unsubscribe_token, confirmed_at)
		VALUES ($1, 'zh-Hant', 'tok-waiting-probe-' || gen_random_uuid(), now())
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
	if shouty, err := s.StillSubscribed(ctx, strings.ToUpper(address)); err != nil || shouty {
		t.Errorf("StillSubscribed(upper case) = %v, %v — want false", shouty, err)
	}
}

// TestHandler_PostRedirectsBeforeRefresh: successful newsletter confirm and
// unsubscribe POSTs redirect before a refresh can repeat them.
func TestHandler_PostRedirectsBeforeRefresh(t *testing.T) {
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

// TestHandler_DeadLinkRecovery: dead newsletter links offer recovery.
func TestHandler_DeadLinkRecovery(t *testing.T) {
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
				heading := "This link is no longer valid"
				if locale == i18n.ZhHant {
					heading = "\u9019\u500b\u9023\u7d50\u5df2\u5931\u6548"
				}
				uitest.AssertEmailLink(t, res.Body.String(), heading, reason, destination)
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
