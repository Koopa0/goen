//go:build integration

package admin_test

import (
	"encoding/json"
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
	pagesadmin "github.com/koopa0/goen/internal/ui/pages/admin"
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
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/orders/"+number+"/note", strings.NewReader(url.Values{"note": {step.note}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("number", number)
		w := httptest.NewRecorder()
		h.RequireStaff(h.StaffNote)(w, req)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("note POST = %d: %s", w.Code, w.Body.String())
		}
		var gotActor uuid.UUID
		var at time.Time
		var noPrior bool
		var after []byte
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE entity_id=$1 AND action=$2`, orderID, step.action).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s audit count = %d, want 1", step.action, count)
		}
		if err := pool.QueryRow(ctx, `SELECT actor_user_id, occurred_at, before IS NULL, after FROM audit_events WHERE entity_id=$1 AND action=$2`, orderID, step.action).Scan(&gotActor, &at, &noPrior, &after); err != nil {
			t.Fatal(err)
		}
		if gotActor != actor || at.Before(started) || !noPrior {
			t.Fatalf("note audit actor=%s time=%v before-empty=%v", gotActor, at, noPrior)
		}
		var payload map[string]any
		if err := json.Unmarshal(after, &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload) != 1 || payload["number"] != number {
			t.Fatalf("note audit payload = %s, want only the order number %s", after, number)
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
		// The audit page lists every order's notes; this order's row is the one
		// whose detail carries its number.
		shown := 0
		for _, e := range view.Rows {
			if e.Action != step.action || !strings.Contains(changesText(e.Changes), number) {
				continue
			}
			shown++
			if e.Actor == "" || e.At == "" || e.Label(ctx) == step.action || (step.note != "" && strings.Contains(changesText(e.Changes), step.note)) {
				t.Errorf("audit row must be attributed, translated and hold no note text: %+v", e)
			}
		}
		if shown != 1 {
			t.Fatalf("audit page shows %d %s rows naming %s, want 1", shown, step.action, number)
		}
	}
}

func TestStaffNoteForUnknownOrderIs404WithoutAudit(t *testing.T) {
	ctx, _ := staffContext(t)
	h := adminHandlerOver(pool, staffNoteStore(t))
	number := "GO-999999-999999"
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/orders/"+number+"/note", strings.NewReader(url.Values{"note": {"orphan"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("number", number)
	w := httptest.NewRecorder()
	h.RequireStaff(h.StaffNote)(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("note on unknown order = %d, want 404", w.Code)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action LIKE 'order.note.%' AND after->>'number'=$1`, number).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unknown order left %d note audit rows", count)
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

func changesText(changes []pagesadmin.AuditChange) string {
	var b strings.Builder
	for _, c := range changes {
		b.WriteString(c.Field + " " + c.Text() + "\n")
	}
	return b.String()
}
