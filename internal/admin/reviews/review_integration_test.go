//go:build integration

package reviews_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/reviews"
	"github.com/koopa0/goen/internal/i18n"
)

// TestStore_SetReviewHiddenAudited: hiding a review is reversible and audited.
func TestStore_SetReviewHiddenAudited(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := reviews.NewStore(pool)
	reviewID := someReview(t)

	if err := s.SetReviewHidden(ctx, reviewID, true); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if !reviewHidden(t, reviewID) {
		t.Fatal("the review is not hidden")
	}
	if err := s.SetReviewHidden(ctx, reviewID, true); !errors.Is(err, reviews.ErrNotFound) {
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

// TestStore_ReviewsShowsHidden: the review queue shows hidden reviews.
func TestStore_ReviewsShowsHidden(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := reviews.NewStore(pool)
	reviewID := someReview(t)

	if err := s.SetReviewHidden(ctx, reviewID, true); err != nil {
		t.Fatalf("hide: %v", err)
	}
	view, err := s.Reviews(ctx, reviews.FilterAll, "")
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

// TestHandler_HideReviewStale: a missing id and a stale
// form (already hidden) must redirect with ?gone=1 and render that notice,
// never a bare list or a false success.
func TestHandler_HideReviewStale(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	h := reviews.NewHandler(reviews.NewStore(pool), slog.New(slog.DiscardHandler))
	gone := i18n.T(ctx, i18n.KeyAdminNoticeGone)

	form := url.Values{"review": {uuid.NewString()}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/reviews/hide",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	h.HideReview(res, req)
	const want = "/admin/reviews?gone=1"
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != want {
		t.Fatalf("POST = %d Location %q, want 303 %q", res.Code, res.Header().Get("Location"), want)
	}

	get := httptest.NewRequestWithContext(ctx, http.MethodGet, want, http.NoBody)
	out := httptest.NewRecorder()
	h.Reviews(out, get)
	if out.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200; body=%s", out.Code, out.Body.String())
	}
	if !strings.Contains(out.Body.String(), gone) {
		t.Errorf("GET omitted the gone notice %q; body=%s", gone, out.Body.String())
	}

	reviewID := someReview(t)
	if err := reviews.NewStore(pool).SetReviewHidden(ctx, reviewID, true); err != nil {
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
