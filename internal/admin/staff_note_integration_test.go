//go:build integration

package admin_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin"
)

func staffNoteStore(t *testing.T) *admin.Store {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["role"] = "admin"
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return admin.NewStore(p, fakeRefunder{}, nil, nil)
}

func TestStaffNoteHTTPRecordsOperationsWithoutContent(t *testing.T) {
	ctx, actor := staffContext(t)
	number, orderID, _ := pendingOrderHoldingStock(t)
	s := staffNoteStore(t)
	h := adminHandlerOver(pool, s)
	for _, step := range []struct{ note, action string }{
		{"private first note", "order.note.create"},
		{"private replacement", "order.note.replace"},
		{"", "order.note.clear"},
	} {
		started := time.Now().Add(-time.Second)
		req := httptest.NewRequest(http.MethodPost, "/admin/orders/"+number+"/note", strings.NewReader(url.Values{"note": {step.note}}.Encode())).WithContext(ctx)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("number", number)
		w := httptest.NewRecorder()
		h.RequireStaff(h.StaffNote)(w, req)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("note POST = %d: %s", w.Code, w.Body.String())
		}
		var gotActor uuid.UUID
		var at time.Time
		var payloadEmpty bool
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE entity_id=$1 AND action=$2`, orderID, step.action).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s audit count = %d, want 1", step.action, count)
		}
		if err := pool.QueryRow(ctx, `SELECT actor_user_id, occurred_at, before IS NULL AND after IS NULL FROM audit_events WHERE entity_id=$1 AND action=$2`, orderID, step.action).Scan(&gotActor, &at, &payloadEmpty); err != nil {
			t.Fatal(err)
		}
		if gotActor != actor || at.Before(started) || !payloadEmpty {
			t.Fatalf("note audit actor=%s time=%v content-free=%v", gotActor, at, payloadEmpty)
		}
		var note string
		if err := pool.QueryRow(ctx, `SELECT coalesce(staff_note,'') FROM orders WHERE id=$1`, orderID).Scan(&note); err != nil {
			t.Fatal(err)
		}
		if note != step.note {
			t.Fatalf("persisted note = %q, want %q", note, step.note)
		}
		view, err := s.Audit(ctx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range view.Rows {
			if e.Action == step.action && e.Actor != "" && e.At != "" {
				found = true
				if e.Detail != "" || e.Label(ctx) == step.action {
					t.Errorf("audit presentation lacks translated content-free operation: %+v", e)
				}
			}
		}
		if !found {
			t.Fatalf("audit page omitted %s", step.action)
		}
	}
}

func TestStaffNoteAuditFailureRollsBackTheNote(t *testing.T) {
	ctx, _ := staffContext(t)
	number, orderID, _ := pendingOrderHoldingStock(t)
	s := staffNoteStore(t)
	if err := s.SetStaffNote(ctx, number, "keep this note"); err != nil {
		t.Fatal(err)
	}
	badCtx := account.WithUser(t.Context(), account.User{ID: uuid.NewString(), Role: "admin"})
	err := s.SetStaffNote(badCtx, number, "unrecordable replacement")
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "audit_events_actor_user_id_fkey" {
		t.Fatalf("note audit failure = %v, want audit actor FK", err)
	}
	var note string
	if err := pool.QueryRow(ctx, `SELECT staff_note FROM orders WHERE id=$1`, orderID).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if note != "keep this note" {
		t.Fatalf("audit failure persisted note %q", note)
	}
}

func TestUnchangedStaffNoteDoesNotInventAnOperation(t *testing.T) {
	ctx, _ := staffContext(t)
	number, orderID, _ := pendingOrderHoldingStock(t)
	s := staffNoteStore(t)
	for range 2 {
		if err := s.SetStaffNote(ctx, number, "one note"); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE entity_id=$1 AND action LIKE 'order.note.%'`, orderID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("unchanged note audit count = %d, want 1", count)
	}
}
