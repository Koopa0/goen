//go:build integration

package admin_test

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

type healthOutboxRow struct {
	key                                 string
	age, overdue                        time.Duration
	attempts                            int32
	delivered, dropped, blocked, poison bool
}

func insertHealthOutbox(t *testing.T, p *pgxpool.Pool, row healthOutboxRow) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := p.QueryRow(t.Context(), `
		INSERT INTO outbox_messages(topic,dedupe_key,payload,attempts,created_at,available_at,delivered_at,dropped_at,blocked_at)
		VALUES('health.fixture',$1,CASE WHEN $2 THEN '{}'::jsonb ELSE '{"message":1}'::jsonb END,$3,
		       now()-$4::interval,now()-$5::interval,
		       CASE WHEN $6 THEN now() END,CASE WHEN $7 THEN now() END,CASE WHEN $8 THEN now() END)
		RETURNING id`, row.key, row.poison, row.attempts,
		pgtype.Interval{Microseconds: row.age.Microseconds(), Valid: true},
		pgtype.Interval{Microseconds: row.overdue.Microseconds(), Valid: true},
		row.delivered, row.dropped, row.blocked).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func healthAdminPool(t *testing.T, p *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	cfg := p.Config().Copy()
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE admin")
		return err
	}
	rolePool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rolePool.Close)
	var identity string
	if err := rolePool.QueryRow(t.Context(), `SELECT current_user || ':' || current_setting('is_superuser')`).Scan(&identity); err != nil {
		t.Fatal(err)
	}
	if identity != "admin:off" {
		t.Fatalf("health role=%s", identity)
	}
	return rolePool
}

func TestOutboxHealthSeparatesReadyScheduledAndTerminal(t *testing.T) {
	p := dbtest.Pool(t)
	ctx, _ := staffContextOn(t, p)
	rolePool := healthAdminPool(t, p)
	s := admin.NewStore(rolePool, fakeRefunder{}, nil, nil)
	messages := outbox.NewStore(rolePool, slog.New(slog.DiscardHandler))
	retain := pgtype.Interval{Microseconds: outbox.Retain.Microseconds(), Valid: true}
	q := db.New(p)
	for _, tc := range []struct {
		name                  string
		rows                  []healthOutboxRow
		lease                 bool
		pending, ready, stuck int64
		oldest                time.Duration
	}{
		{name: "empty"},
		{name: "future", rows: []healthOutboxRow{{key: "future", age: time.Hour, overdue: -time.Hour}}, pending: 1},
		{name: "leased", lease: true, pending: 1},
		{name: "delivered", rows: []healthOutboxRow{{key: "sent", age: time.Hour, overdue: time.Hour, delivered: true}}},
		{name: "dropped", rows: []healthOutboxRow{{key: "dropped", age: time.Hour, overdue: time.Hour, dropped: true, blocked: true}}},
		{name: "exhausted", rows: []healthOutboxRow{{key: "exhausted", age: time.Hour, overdue: time.Hour, attempts: outbox.MaxAttempts, blocked: true}}, stuck: 1},
		{name: "poison", rows: []healthOutboxRow{{key: "poison", age: time.Hour, overdue: time.Hour, attempts: outbox.MaxAttempts, blocked: true, poison: true}}, stuck: 1},
		{name: "expired_before_sweep", rows: []healthOutboxRow{{key: "expired", age: outbox.Retain + time.Hour, overdue: time.Hour}}},
		{name: "expired_blocked", rows: []healthOutboxRow{{key: "expired_blocked", age: outbox.Retain + time.Hour, overdue: time.Hour, blocked: true, poison: true}}, stuck: 1},
		{name: "due_retry", rows: []healthOutboxRow{{key: "retry", age: time.Hour, overdue: 2 * time.Minute, attempts: 2}}, pending: 1, ready: 1, oldest: 2 * time.Minute},
		{name: "overdue_retry", rows: []healthOutboxRow{{key: "retry", age: time.Hour, overdue: 20 * time.Minute, attempts: 2}}, pending: 1, ready: 1, oldest: 20 * time.Minute},
		{name: "ready_and_scheduled", rows: []healthOutboxRow{
			{key: "future", age: time.Hour, overdue: -time.Hour},
			{key: "first_retry", age: time.Hour, overdue: 2 * time.Minute, attempts: 2},
			{key: "oldest_retry", age: time.Hour, overdue: 20 * time.Minute, attempts: 3},
		}, pending: 3, ready: 2, oldest: 20 * time.Minute},
		{name: "mixed", lease: true, rows: []healthOutboxRow{
			{key: "future", age: time.Hour, overdue: -time.Hour},
			{key: "due", age: time.Hour, overdue: 2 * time.Minute, attempts: 2},
			{key: "expired", age: outbox.Retain + time.Hour, overdue: 24 * time.Hour},
			{key: "dropped", age: time.Hour, overdue: time.Hour, dropped: true, blocked: true},
			{key: "sent", age: time.Hour, overdue: time.Hour, delivered: true},
			{key: "blocked", age: time.Hour, overdue: time.Hour, blocked: true, attempts: outbox.MaxAttempts},
		}, pending: 3, ready: 1, stuck: 1, oldest: 2 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := p.Exec(t.Context(), `DELETE FROM outbox_messages`); err != nil {
				t.Fatal(err)
			}
			if tc.lease {
				leased := insertHealthOutbox(t, p, healthOutboxRow{key: "lease", age: time.Hour, overdue: time.Minute})
				claimed, err := q.ClaimOutbox(t.Context(), db.ClaimOutboxParams{Retain: retain, Lease: pgtype.Interval{Microseconds: outbox.Lease.Microseconds(), Valid: true}, BatchSize: outbox.BatchSize})
				if err != nil || len(claimed) != 1 || claimed[0].ID != leased {
					t.Fatalf("lease fixture=%v/%v", claimed, err)
				}
			}
			for _, row := range tc.rows {
				insertHealthOutbox(t, p, row)
			}
			view, err := s.WorkerHealth(ctx, messages)
			if err != nil {
				t.Fatal(err)
			}
			if view.OutboxPending != tc.pending || view.OutboxReady != tc.ready || view.OutboxStuck != tc.stuck {
				t.Fatalf("health pending/ready/stuck=%d/%d/%d; want %d/%d/%d", view.OutboxPending, view.OutboxReady, view.OutboxStuck, tc.pending, tc.ready, tc.stuck)
			}
			if tc.ready == 0 && view.OutboxOldest != 0 {
				t.Fatalf("no ready messages have age=%v", view.OutboxOldest)
			}
			if tc.ready > 0 && (view.OutboxOldest < tc.oldest || view.OutboxOldest >= tc.oldest+time.Minute) {
				t.Fatalf("oldest=%v; want age of due retry near %v", view.OutboxOldest, tc.oldest)
			}
			wantHealthy := tc.stuck == 0 && tc.oldest < admin.OutboxStaleAfter
			if view.OutboxHealthy() != wantHealthy {
				t.Fatalf("healthy=%v; want %v", view.OutboxHealthy(), wantHealthy)
			}
			for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
				localeCtx := i18n.WithLocale(ctx, locale)
				var wantText string
				switch {
				case tc.stuck > 0:
					wantText = fmt.Sprintf(i18n.T(localeCtx, i18n.KeyHealthOutboxStuck), tc.stuck)
				case tc.pending == 0:
					wantText = i18n.T(localeCtx, i18n.KeyHealthOutboxClear)
				case tc.ready == 0:
					wantText = fmt.Sprintf(i18n.T(localeCtx, i18n.KeyHealthOutboxNotYetDue), tc.pending)
				default:
					ageText := fmt.Sprintf(i18n.T(localeCtx, i18n.KeyAdminMinutes), int(tc.oldest.Minutes()))
					key := i18n.KeyHealthOutboxWaiting
					if tc.oldest >= admin.OutboxStaleAfter {
						key = i18n.KeyHealthOutboxOverdue
					}
					wantText = fmt.Sprintf(i18n.T(localeCtx, key), tc.ready, ageText)
				}
				if got := view.OutboxText(localeCtx); got != wantText {
					t.Fatalf("dashboard text=%q; want %q", got, wantText)
				}
				if tc.ready > 0 && tc.stuck == 0 && strings.Contains(wantText, fmt.Sprintf(i18n.T(localeCtx, i18n.KeyHealthOutboxNotYetDue), tc.pending)) {
					t.Fatal("ready retry described as not yet due")
				}
				req := httptest.NewRequestWithContext(localeCtx, http.MethodGet, "/admin/health", nil)
				res := httptest.NewRecorder()
				adminHandlerOver(rolePool, s).Health(res, req)
				if res.Code != http.StatusOK || !strings.Contains(html.UnescapeString(res.Body.String()), wantText) {
					t.Fatalf("health HTML status=%d lacks %q", res.Code, wantText)
				}
			}
			claimed, err := q.ClaimOutbox(t.Context(), db.ClaimOutboxParams{Retain: retain, Lease: pgtype.Interval{Microseconds: outbox.Lease.Microseconds(), Valid: true}, BatchSize: outbox.BatchSize})
			if err != nil || int64(len(claimed)) != tc.ready {
				t.Fatalf("ClaimOutbox=%d/%v; health ready=%d", len(claimed), err, tc.ready)
			}
			after, err := s.WorkerHealth(ctx, messages)
			if err != nil {
				t.Fatal(err)
			}
			if after.OutboxReady != 0 || after.OutboxOldest != 0 || after.OutboxPending != tc.pending || after.OutboxStuck != tc.stuck {
				t.Fatalf("claim should move only ready to leased: %+v", after)
			}
		})
	}
}

func TestOutboxHealthAndClaimUseTheSameRetentionBoundary(t *testing.T) {
	p := dbtest.Pool(t)
	retain := pgtype.Interval{Microseconds: outbox.Retain.Microseconds(), Valid: true}
	for _, offset := range []int64{-1, 0, 1} {
		t.Run(fmt.Sprintf("offset_%d", offset), func(t *testing.T) {
			tx, err := p.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
			if _, err := tx.Exec(t.Context(), `INSERT INTO outbox_messages(topic,dedupe_key,payload,created_at,available_at) VALUES('health.boundary',$1,'{"message":1}',now()-$2::interval+$3*interval '1 microsecond',now())`, uuid.NewString(), retain, offset); err != nil {
				t.Fatal(err)
			}
			q := db.New(tx)
			health, err := q.WorkerHealth(t.Context(), retain)
			if err != nil {
				t.Fatal(err)
			}
			want := int64(0)
			if offset > 0 {
				want = 1
			}
			if health.OutboxPending != want || health.OutboxReady != want || health.OutboxOldestSeconds != 0 {
				t.Fatalf("boundary health=%+v; want pending/ready=%d age=0", health, want)
			}
			claimed, err := q.ClaimOutbox(t.Context(), db.ClaimOutboxParams{Retain: retain, Lease: pgtype.Interval{Microseconds: outbox.Lease.Microseconds(), Valid: true}, BatchSize: outbox.BatchSize})
			if err != nil || int64(len(claimed)) != want {
				t.Fatalf("boundary claim=%d/%v; health=%d", len(claimed), err, health.OutboxReady)
			}
		})
	}
}
