//go:build integration

package feedback_test

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
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/feedback"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/web"
)

func TestTheQueuePutsWhatTheShopOwesFirst(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := feedback.NewStore(pool)
	ps := product.NewStore(pool, slog.New(slog.DiscardHandler))
	asker := newAskingCustomer(t)
	slug := anyActiveProductSlug(t)

	old := ask(t, ps, slug, asker, "最舊的,店家已回", -3)
	if err := s.AnswerQuestion(ctx, old, asker, "店家的回答"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	middling := ask(t, ps, slug, asker, "中間的,只有顧客回", -2)
	// Historical customer answers do not settle the shop's unanswered queue.
	if _, err := pool.Exec(ctx, `INSERT INTO product_answers (question_id, user_id, body, is_staff) VALUES ($1, $2, $3, false)`, uuid.MustParse(middling), uuid.MustParse(asker), "我覺得可以"); err != nil {
		t.Fatalf("historical customer answer: %v", err)
	}
	newest := ask(t, ps, slug, asker, "最新的,沒人回", -1)

	view, err := s.Questions(ctx, feedback.VisibleQuestions)
	if err != nil {
		t.Fatalf("questions: %v", err)
	}
	order := make([]string, 0, len(view.Rows))
	for _, q := range view.Rows {
		if q.ID == old || q.ID == middling || q.ID == newest {
			order = append(order, q.Body)
		}
	}
	if len(order) != 3 {
		t.Fatalf("%d of the three questions are in the queue", len(order))
	}
	if order[0] != "中間的,只有顧客回" {
		t.Errorf("first is %q, want the oldest one the shop still owes", order[0])
	}
	if order[2] != "最舊的,店家已回" {
		t.Errorf("last is %q, want the one the shop already answered", order[2])
	}
}

func TestHidingAQuestionIsRecordedAndCannotBeRepeated(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := feedback.NewStore(pool)
	ps := product.NewStore(pool, slog.New(slog.DiscardHandler))
	asker := newAskingCustomer(t)
	id := ask(t, ps, anyActiveProductSlug(t), asker, "會被隱藏的", 0)

	before := admintest.AuditRows(t, pool, audit.ActionHideQuestion)
	if err := s.HideQuestion(ctx, id); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if after := admintest.AuditRows(t, pool, audit.ActionHideQuestion); after != before+1 {
		t.Errorf("hiding left %d audit rows, want one more than %d", after, before)
	}
	if err := s.HideQuestion(ctx, id); !errors.Is(err, feedback.ErrNotFound) {
		t.Errorf("hiding twice gave %v, want ErrNotFound", err)
	}
	if after := admintest.AuditRows(t, pool, audit.ActionHideQuestion); after != before+1 {
		t.Errorf("a refused hide wrote an audit row")
	}
}

func TestAnEmptyOfficialAnswerIsRefused(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := feedback.NewStore(pool)
	ps := product.NewStore(pool, slog.New(slog.DiscardHandler))
	asker := newAskingCustomer(t)
	id := ask(t, ps, anyActiveProductSlug(t), asker, "等一個回答", 0)

	for _, body := range []string{"", "   ", "\t\n"} {
		if err := s.AnswerQuestion(ctx, id, asker, body); !errors.Is(err, feedback.ErrInvalid) {
			t.Errorf("%q gave %v, want ErrInvalid", body, err)
		}
	}
	if err := s.AnswerQuestion(ctx, id, asker, "真的回答"); err != nil {
		t.Errorf("a real answer was refused: %v", err)
	}
}

func TestHidingAReviewIsReversibleAndAudited(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := feedback.NewStore(pool)
	reviewID := someReview(t)

	if err := s.SetReviewHidden(ctx, reviewID, true); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if !reviewHidden(t, reviewID) {
		t.Fatal("the review is not hidden")
	}
	if err := s.SetReviewHidden(ctx, reviewID, true); !errors.Is(err, feedback.ErrNotFound) {
		t.Errorf("hiding an already-hidden review answered %v", err)
	}

	if err := s.SetReviewHidden(ctx, reviewID, false); err != nil {
		t.Fatalf("show: %v", err)
	}
	if reviewHidden(t, reviewID) {
		t.Error("the review is still hidden")
	}

	var actions []string
	rows, err := pool.Query(ctx, `
		SELECT action FROM audit_events WHERE action LIKE 'review.%'
		  AND entity_id = $1 ORDER BY occurred_at`, reviewID)
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
		t.Fatalf("iterate review audit trail: %v", err)
	}
	if diff := cmp.Diff([]string{"review.hide", "review.show"}, actions); diff != "" {
		t.Errorf("the trail (-want +got):\n%s", diff)
	}
}

func TestTheReviewQueueShowsHiddenOnes(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := feedback.NewStore(pool)
	reviewID := someReview(t)

	if err := s.SetReviewHidden(ctx, reviewID, true); err != nil {
		t.Fatalf("hide: %v", err)
	}
	view, err := s.Reviews(ctx, feedback.AllReviews)
	if err != nil {
		t.Fatalf("read the queue: %v", err)
	}

	var found bool
	for _, r := range view.Rows {
		if r.ID == reviewID {
			found = true
			if !r.Hidden {
				t.Error("the queue does not mark it hidden")
			}
			if r.Action() != "/admin/reviews/show" {
				t.Errorf("the button posts to %s, want the show path", r.Action())
			}
		}
	}
	if !found {
		t.Error("a hidden review is missing from the queue — it cannot be put back")
	}
}

func TestTheInboxPutsTheLongestWaitFirst(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := feedback.NewStore(pool)

	old := messageAgedDays(t, "四天前", 4)
	fresh := messageAgedDays(t, "今天", 0)
	done := messageAgedDays(t, "已處理過", 6)
	if err := s.SetMessageHandled(ctx, done, true); err != nil {
		t.Fatalf("handle the old one: %v", err)
	}

	view, err := s.Messages(ctx)
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

func TestHandlingAMessageIsReversibleAndAudited(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := feedback.NewStore(pool)
	id := messageAgedDays(t, "來回一次", 1)

	if err := s.SetMessageHandled(ctx, id, true); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if !messageHandled(t, id) {
		t.Fatal("the message is not handled")
	}
	if err := s.SetMessageHandled(ctx, id, true); !errors.Is(err, feedback.ErrNotFound) {
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

func TestTheInboxDoesNotCopyTheMessageIntoTheAuditTrail(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := feedback.NewStore(pool)

	const body = "非常獨特的訊息內容 987654"
	var id string
	if err := pool.QueryRow(ctx, `
		INSERT INTO contact_messages (name, email, subject, message)
		VALUES ('王小明', 'privacy@example.com', '訂單問題', $1) RETURNING id::text`,
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

// TestStaleReviewAndMessageActionsExplainInsteadOfVanishing covers both queues:
// a missing id and a stale form (already hidden or handled) must redirect with
// ?gone=1 and render that notice, never a bare list or a false success.
func TestStaleReviewAndMessageActionsExplainInsteadOfVanishing(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	h := feedback.NewHandler(feedback.NewStore(pool), slog.New(slog.DiscardHandler))
	gone := i18n.T(ctx, i18n.KeyAdminNoticeGone)

	for _, tt := range []struct {
		name     string
		postPath string
		listPath string
		field    string
		id       string
	}{
		{
			name:     "unknown review",
			postPath: "/admin/reviews/hide",
			listPath: "/admin/reviews",
			field:    "review",
			id:       uuid.NewString(),
		},
		{
			name:     "unknown message",
			postPath: "/admin/messages/handle",
			listPath: "/admin/messages",
			field:    "message",
			id:       uuid.NewString(),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{tt.field: {tt.id}}
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, tt.postPath,
				strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			res := httptest.NewRecorder()
			switch tt.postPath {
			case "/admin/reviews/hide":
				h.HideReview(res, req)
			case "/admin/messages/handle":
				h.HandleMessage(res, req)
			}
			want := tt.listPath + "?gone=1"
			if res.Code != http.StatusSeeOther || res.Header().Get("Location") != want {
				t.Fatalf("POST = %d Location %q, want 303 %q", res.Code, res.Header().Get("Location"), want)
			}

			get := httptest.NewRequestWithContext(ctx, http.MethodGet, want, http.NoBody)
			out := httptest.NewRecorder()
			switch tt.listPath {
			case "/admin/reviews":
				h.Reviews(out, get)
			case "/admin/messages":
				h.Messages(out, get)
			}
			if out.Code != http.StatusOK {
				t.Fatalf("GET = %d, want 200; body=%s", out.Code, out.Body.String())
			}
			if !strings.Contains(out.Body.String(), gone) {
				t.Errorf("GET omitted the gone notice %q; body=%s", gone, out.Body.String())
			}
		})
	}

	reviewID := someReview(t)
	if err := feedback.NewStore(pool).SetReviewHidden(ctx, reviewID, true); err != nil {
		t.Fatalf("hide review for stale case: %v", err)
	}
	staleReview := url.Values{"review": {reviewID}}
	staleReviewReq := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/reviews/hide",
		strings.NewReader(staleReview.Encode()))
	staleReviewReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	staleReviewRes := httptest.NewRecorder()
	h.HideReview(staleReviewRes, staleReviewReq)
	if staleReviewRes.Code != http.StatusSeeOther ||
		staleReviewRes.Header().Get("Location") != "/admin/reviews?gone=1" {
		t.Fatalf("stale review hide = %d %q, want 303 /admin/reviews?gone=1",
			staleReviewRes.Code, staleReviewRes.Header().Get("Location"))
	}

	msgID := messageAgedDays(t, "已處理過", 0)
	if err := feedback.NewStore(pool).SetMessageHandled(ctx, msgID, true); err != nil {
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

// TestABoundedListSaysSoAtTheBoundary is the integration half of the change
// #400's sibling made: every back-office list reads a page and shows it, and
// until now no page said so.
//
// The boundary is the only place this can be wrong, so that is what is tested:
// exactly a page says nothing, and one row past a page says something. The
// contact inbox is the fixture because a message needs no product, no order and
// no customer — every other capped list would need a catalogue built first to
// prove a property that has nothing to do with catalogues.
func TestABoundedListSaysSoAtTheBoundary(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := feedback.NewStore(pool)

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
	if existing > web.PageSize {
		t.Skipf("the inbox already holds %d messages, more than a page; "+
			"this test needs to own the boundary", existing)
	}

	write := func(n int) {
		t.Helper()
		for i := range n {
			if _, err := pool.Exec(ctx, `
				INSERT INTO contact_messages (name, email, subject, message)
				VALUES ($1, $2, '商品諮詢', $3)`,
				"版面測試", marker,
				fmt.Sprintf("訊息內容 %03d", i)); err != nil {
				t.Fatalf("write fixture message %d: %v", i, err)
			}
		}
	}

	write(web.PageSize - existing)
	full, err := s.Messages(ctx)
	if err != nil {
		t.Fatalf("read the inbox at exactly a page: %v", err)
	}
	if len(full.Rows) != web.PageSize {
		t.Fatalf("a full page holds %d rows, want %d", len(full.Rows), web.PageSize)
	}
	if full.Next != "" {
		t.Error("a list holding exactly a page offers a next page; the link " +
			"would appear on an inbox nobody has anything left to read in")
	}

	write(1)
	over, err := s.Messages(ctx)
	if err != nil {
		t.Fatalf("read the inbox one past a page: %v", err)
	}
	if len(over.Rows) != web.PageSize {
		t.Errorf("one row past a page renders %d rows, want %d — the extra row is "+
			"there to be counted, not shown", len(over.Rows), web.PageSize)
	}
	if over.Next == "" {
		t.Error("a list with more than a page offers no next page; a staff member " +
			"cannot tell fifty messages from fifty of nine hundred")
	}
}

func ask(t *testing.T, s *product.Store, slug, userID, body string, days int) string {
	t.Helper()
	ctx := t.Context()
	if err := s.Ask(ctx, slug, userID, body); err != nil {
		t.Fatalf("ask %q: %v", body, err)
	}
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		UPDATE product_questions SET created_at = now() + make_interval(days => $2)
		WHERE body = $1 RETURNING id`, body, days).Scan(&id); err != nil {
		t.Fatalf("backdate %q: %v", body, err)
	}
	return id.String()
}

func newAskingCustomer(t *testing.T) string {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ('aq-' || gen_random_uuid() || '@goen.invalid', 'customer', '提問者')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	return id.String()
}

func anyActiveProductSlug(t *testing.T) string {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(),
		`SELECT slug FROM products WHERE status = 'active' LIMIT 1`).Scan(&slug); err != nil {
		t.Fatalf("find product: %v", err)
	}
	return slug
}

func someReview(t *testing.T) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO product_reviews (product_id, rating, body)
		SELECT id, 1, '測試評價 ' || gen_random_uuid() FROM products ORDER BY slug LIMIT 1
		RETURNING id::text`).Scan(&id); err != nil {
		t.Fatalf("create review: %v", err)
	}
	return id
}

func reviewHidden(t *testing.T, id string) bool {
	t.Helper()
	var hidden bool
	if err := pool.QueryRow(t.Context(),
		`SELECT hidden_at IS NOT NULL FROM product_reviews WHERE id = $1`, id).Scan(&hidden); err != nil {
		t.Fatalf("read hidden_at: %v", err)
	}
	return hidden
}

func messageAgedDays(t *testing.T, message string, days int) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO contact_messages (name, email, subject, message, created_at)
		VALUES ('王小明', 'inbox@example.com', '訂單問題', $1,
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
