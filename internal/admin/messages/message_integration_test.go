//go:build integration

package messages_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/messages"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
)

// TestStore_MessagesOrder: the inbox puts the longest wait first.
func TestStore_MessagesOrder(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := messages.NewStore(pool)

	old := messageAgedDays(t, "四天前", 4)
	fresh := messageAgedDays(t, "今天", 0)
	done := messageAgedDays(t, "已處理過", 6)
	if err := s.SetMessageHandled(ctx, done, true); err != nil {
		t.Fatalf("handle the old one: %v", err)
	}

	view, err := s.Messages(ctx, "")
	if err != nil {
		t.Fatalf("read the inbox: %v", err)
	}

	var order []string
	for i := range view.Rows {
		switch view.Rows[i].ID {
		case old, fresh, done:
			order = append(order, view.Rows[i].ID)
		}
	}
	if diff := cmp.Diff([]string{old, fresh, done}, order); diff != "" {
		t.Errorf("queue order (-want +got):\n%s", diff)
	}

	for i := range view.Rows {
		r := &view.Rows[i]
		switch r.ID {
		case old:
			if !r.Overdue() {
				t.Errorf("a four-day wait is not overdue: %+v", r)
			}
			if r.WaitingDays < 4 {
				t.Errorf("the four-day-old message reports %d days", r.WaitingDays)
			}
		case fresh:
			if r.Overdue() {
				t.Error("a message from today reads as overdue")
			}
		case done:
			if !r.Handled || r.Waiting(ctx) != "已處理" {
				t.Errorf("a handled message reads as %q", r.Waiting(ctx))
			}
		}
	}
}

// TestStore_SetMessageHandledAudited: handling a message is reversible and audited.
func TestStore_SetMessageHandledAudited(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := messages.NewStore(pool)
	id := messageAgedDays(t, "來回一次", 1)

	if err := s.SetMessageHandled(ctx, id, true); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if !messageHandled(t, id) {
		t.Fatal("the message is not handled")
	}
	if err := s.SetMessageHandled(ctx, id, true); !errors.Is(err, messages.ErrNotFound) {
		t.Errorf("handling an already-handled message answered %v", err)
	}

	if err := s.SetMessageHandled(ctx, id, false); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if messageHandled(t, id) {
		t.Error("the message is still handled")
	}

	var actions []string
	rows, err := pool.Query(ctx, `
		SELECT action FROM audit_events
		WHERE action LIKE 'message.%' AND entity_id = $1 ORDER BY occurred_at`, id)
	if err != nil {
		t.Fatalf("read the trail: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		if scanErr := rows.Scan(&a); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		actions = append(actions, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate message audit trail: %v", err)
	}
	if diff := cmp.Diff([]string{"message.handle", "message.reopen"}, actions); diff != "" {
		t.Errorf("the trail (-want +got):\n%s", diff)
	}
}

// TestStore_SetMessageHandledNoCopy: the inbox does not copy the message into the audit trail.
func TestStore_SetMessageHandledNoCopy(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := messages.NewStore(pool)

	const body = "非常獨特的訊息內容 987654"
	var id string
	if err := pool.QueryRow(ctx, `
		INSERT INTO contact_messages (name, email, subject, message)
		VALUES ('王小明', 'privacy@example.com', 'order', $1) RETURNING id::text`,
		body).Scan(&id); err != nil {
		t.Fatalf("create message: %v", err)
	}
	if err := s.SetMessageHandled(ctx, id, true); err != nil {
		t.Fatalf("handle: %v", err)
	}

	var after string
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(after::text, '') FROM audit_events
		WHERE action = 'message.handle' AND entity_id = $1`, id).Scan(&after); err != nil {
		t.Fatalf("read the trail: %v", err)
	}
	if strings.Contains(after, body) {
		t.Errorf("the customer's message is in the audit trail: %s", after)
	}
	if !strings.Contains(after, id) {
		t.Errorf("the audit row does not say which message: %s", after)
	}
}

// TestHandler_HandleMessageStale: a missing id and a stale
// form (already handled) must redirect with ?gone=1 and render that notice,
// never a bare list or a false success.
func TestHandler_HandleMessageStale(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	h := messages.NewHandler(messages.NewStore(pool), slog.New(slog.DiscardHandler))
	gone := i18n.T(ctx, i18n.KeyAdminNoticeGone)

	form := url.Values{"message": {uuid.NewString()}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/messages/handle",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	h.HandleMessage(res, req)
	const want = "/admin/messages?gone=1"
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != want {
		t.Fatalf("POST = %d Location %q, want 303 %q", res.Code, res.Header().Get("Location"), want)
	}

	get := httptest.NewRequestWithContext(ctx, http.MethodGet, want, http.NoBody)
	out := httptest.NewRecorder()
	h.Messages(out, get)
	if out.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200; body=%s", out.Code, out.Body.String())
	}
	if !strings.Contains(out.Body.String(), gone) {
		t.Errorf("GET omitted the gone notice %q; body=%s", gone, out.Body.String())
	}

	msgID := messageAgedDays(t, "已處理過", 0)
	if err := messages.NewStore(pool).SetMessageHandled(ctx, msgID, true); err != nil {
		t.Fatalf("handle message for stale case: %v", err)
	}
	staleMsg := url.Values{"message": {msgID}}
	staleMsgReq := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/messages/handle",
		strings.NewReader(staleMsg.Encode()))
	staleMsgReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	staleMsgRes := httptest.NewRecorder()
	h.HandleMessage(staleMsgRes, staleMsgReq)
	if staleMsgRes.Code != http.StatusSeeOther ||
		staleMsgRes.Header().Get("Location") != "/admin/messages?gone=1" {
		t.Fatalf("stale message handle = %d %q, want 303 /admin/messages?gone=1",
			staleMsgRes.Code, staleMsgRes.Header().Get("Location"))
	}
}

// TestStore_MessagesBoundary: a bounded list says so at the boundary.
//
// It is the integration half of the change
// #400's sibling made: every back-office list reads a page and shows it, and
// until now no page said so.
//
// The boundary is the only place this can be wrong, so that is what is tested:
// exactly a page says nothing, and one row past a page says something. The
// contact inbox is the fixture because a message needs no product, no order and
// no customer — every other capped list would need a catalogue built first to
// prove a property that has nothing to do with catalogues.
func TestStore_MessagesBoundary(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := messages.NewStore(pool)

	// The marker goes in the address, not the subject: contact_messages.subject
	// is a closed set the schema enforces, so a made-up one is refused.
	marker := "bound-" + uuid.NewString()[:8] + "@goen.invalid"
	defer func() {
		if _, err := pool.Exec(context.WithoutCancel(ctx),
			`DELETE FROM contact_messages WHERE email = $1`, marker); err != nil {
			t.Errorf("clean up the bounded-list fixture: %v", err)
		}
	}()

	// Anything already in the inbox counts towards the page, so the fixture
	// fills whatever is left rather than assuming it starts empty.
	var existing int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM contact_messages`).Scan(&existing); err != nil {
		t.Fatalf("count the inbox: %v", err)
	}
	if existing > keyset.PageSize {
		t.Skipf("the inbox already holds %d messages, more than a page; "+
			"this test needs to own the boundary", existing)
	}

	write := func(n int) {
		t.Helper()
		for i := range n {
			if _, err := pool.Exec(ctx, `
				INSERT INTO contact_messages (name, email, subject, message)
				VALUES ($1, $2, 'product', $3)`,
				"版面測試", marker,
				fmt.Sprintf("訊息內容 %03d", i)); err != nil {
				t.Fatalf("write fixture message %d: %v", i, err)
			}
		}
	}

	write(keyset.PageSize - existing)
	full, err := s.Messages(ctx, "")
	if err != nil {
		t.Fatalf("read the inbox at exactly a page: %v", err)
	}
	if len(full.Rows) != keyset.PageSize {
		t.Fatalf("a full page holds %d rows, want %d", len(full.Rows), keyset.PageSize)
	}
	if full.Next != "" {
		t.Error("a list holding exactly a page offers a next page; the link " +
			"would appear on an inbox nobody has anything left to read in")
	}

	write(1)
	over, err := s.Messages(ctx, "")
	if err != nil {
		t.Fatalf("read the inbox one past a page: %v", err)
	}
	if len(over.Rows) != keyset.PageSize {
		t.Errorf("one row past a page renders %d rows, want %d — the extra row is "+
			"there to be counted, not shown", len(over.Rows), keyset.PageSize)
	}
	if over.Next == "" {
		t.Error("a list with more than a page offers no next page; a staff member " +
			"cannot tell fifty messages from fifty of nine hundred")
	}
}

func messageAgedDays(t *testing.T, message string, days int) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO contact_messages (name, email, subject, message, created_at)
		VALUES ('王小明', 'inbox@example.com', 'order', $1,
		        now() - make_interval(days => $2, hours => 1))
		RETURNING id::text`, message, days).Scan(&id); err != nil {
		t.Fatalf("create message: %v", err)
	}
	return id
}

func messageHandled(t *testing.T, id string) bool {
	t.Helper()
	var handled bool
	if err := pool.QueryRow(t.Context(),
		`SELECT handled_at IS NOT NULL FROM contact_messages WHERE id = $1`,
		id).Scan(&handled); err != nil {
		t.Fatalf("read handled_at: %v", err)
	}
	return handled
}
