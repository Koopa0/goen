//go:build integration

package feedback_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/feedback"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

func TestReviewFilterKeepsTimeOrderHiddenRowsAndKeysetScope(t *testing.T) {
	owner := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, owner)
	want := seedFilteredReviews(t, ctx, owner)
	store := feedback.NewStore(admintest.AdminRolePool(t, owner))
	first, err := store.Reviews(ctx, feedback.ThreeStarsAndBelowReviews)
	if err != nil {
		t.Fatal(err)
	}
	if !first.ThreeStarsAndBelow || len(first.Rows) != web.PageSize || first.Next == "" {
		t.Fatalf("filtered first page: selected=%t rows=%d next=%q", first.ThreeStarsAndBelow, len(first.Rows), first.Next)
	}
	next, err := url.Parse(first.Next)
	if err != nil {
		t.Fatal(err)
	}
	if next.Query().Get("rating") != "3" {
		t.Fatal("next page loses the rating filter")
	}
	token := next.Query().Get(web.KeysetParam)
	last, err := store.Reviews(ctx, feedback.ThreeStarsAndBelowReviews, token)
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Rows) != 3 || last.Next != "" || last.First != "/admin/reviews?rating=3" {
		t.Fatalf("filtered last page: rows=%d next=%q first=%q", len(last.Rows), last.Next, last.First)
	}
	got := make([]string, 0, len(first.Rows)+len(last.Rows))
	var hidden bool
	for _, row := range slices.Concat(first.Rows, last.Rows) {
		got = append(got, row.ID)
		if row.Rating > 3 {
			t.Errorf("filter includes %d stars", row.Rating)
		}
		hidden = hidden || row.Hidden
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("filtered chronological order (-want +got):\n%s", diff)
	}
	if !hidden {
		t.Error("filtered queue loses hidden reviews")
	}
	all, err := store.Reviews(ctx, feedback.AllReviews, token)
	if err != nil {
		t.Fatal(err)
	}
	if all.First != "" || all.ThreeStarsAndBelow || len(all.Rows) != web.PageSize || all.Rows[0].Rating != 5 || all.Rows[1].Rating != 4 {
		t.Fatal("clearing the filter did not restart with all reviews in time order")
	}
	unfilteredNext, err := url.Parse(all.Next)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := store.Reviews(ctx, feedback.ThreeStarsAndBelowReviews, unfilteredNext.Query().Get(web.KeysetParam))
	if err != nil {
		t.Fatal(err)
	}
	if restarted.First != "" || restarted.Rows[0].ID != want[0] {
		t.Error("filtered list accepted a cursor from the unfiltered list")
	}
	checkReviewFilterGET(t, ctx, store, first)
}

func seedFilteredReviews(t *testing.T, ctx context.Context, owner *pgxpool.Pool) []string {
	t.Helper()
	if _, err := owner.Exec(ctx, "DELETE FROM product_reviews"); err != nil {
		t.Fatal(err)
	}
	var productID uuid.UUID
	if err := owner.QueryRow(ctx, "SELECT id FROM products ORDER BY id LIMIT 1").Scan(&productID); err != nil {
		t.Fatal(err)
	}
	var want []string
	for i := range 55 {
		id := uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1))
		rating := i%3 + 1
		if i >= 53 {
			rating = i - 49
		} else {
			want = append(want, id.String())
		}
		at := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i/2) * time.Minute)
		if _, err := owner.Exec(ctx, `INSERT INTO product_reviews (id, product_id, rating, body, created_at, hidden_at)
			VALUES ($1, $2, $3, $4, $5, CASE WHEN $6::boolean THEN $5::timestamptz END)`,
			id, productID, rating, fmt.Sprintf("review-filter-%d", rating), at, i == 0); err != nil {
			t.Fatal(err)
		}
	}
	slices.Reverse(want)
	return want
}

func checkReviewFilterGET(t *testing.T, ctx context.Context, store *feedback.Store, first admin.ReviewsView) {
	t.Helper()
	handler := feedback.NewHandler(store, slog.New(slog.DiscardHandler))
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, path := range []string{"/admin/reviews", "/admin/reviews?rating=3", first.Next, "/admin/reviews?rating=4"} {
			req := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodGet, path, nil)
			out := httptest.NewRecorder()
			handler.Reviews(out, req)
			if out.Code != http.StatusOK {
				t.Fatalf("%s %s: status %d", locale, path, out.Code)
			}
			filtered := req.URL.Query().Get("rating") == "3"
			for _, rating := range []string{"4", "5"} {
				if strings.Contains(out.Body.String(), "review-filter-"+rating) == filtered {
					t.Errorf("%s %s: filtered=%t but unexpected presence of %s stars", locale, path, filtered, rating)
				}
			}
		}
	}
}
