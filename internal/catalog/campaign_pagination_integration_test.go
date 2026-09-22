//go:build integration

package catalog_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/catalog"
)

func TestRunningCampaignsExposeTheSeventhCampaign(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })
	if _, err := tx.Exec(ctx, "UPDATE sale_campaigns SET is_active = false"); err != nil {
		t.Fatal(err)
	}
	slugs := make([]string, 7)
	for i := range slugs {
		slugs[i] = "campaign-page-" + uuid.NewString()
		if _, err := tx.Exec(ctx, `INSERT INTO sale_campaigns (slug, title, starts_at, ends_at, is_active) VALUES ($1, $2, now() - interval '1 day', now() + ($3 * interval '1 day'), true)`, slugs[i], fmt.Sprintf("Campaign %d", i), i+1); err != nil {
			t.Fatal(err)
		}
	}
	store := catalog.NewStore(tx)
	first, err := store.RunningCampaigns(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.RunningCampaigns(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Rows) != 6 || first.Total != 7 || first.Pages() != 2 {
		t.Fatalf("first page = %+v", first)
	}
	if len(second.Rows) != 1 || second.Rows[0].Slug != slugs[6] || second.Page != 2 {
		t.Fatalf("second page does not expose seventh campaign: %+v", second)
	}
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/deals?campaign_page=2", http.NoBody)
	w := httptest.NewRecorder()
	catalog.NewHandler(store, slog.New(slog.DiscardHandler)).Deals(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `href="/s/`+slugs[6]+`"`) || strings.Contains(w.Body.String(), `href="/s/`+slugs[0]+`"`) {
		t.Fatalf("HTTP campaign page two omitted the seventh campaign or repeated page one: status=%d", w.Code)
	}
	for i := range first.Rows {
		if first.Rows[i].Slug != slugs[i] {
			t.Errorf("campaign %d = %s, want %s", i, first.Rows[i].Slug, slugs[i])
		}
	}
	beyond, err := store.RunningCampaigns(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	if beyond.Page != 2 || len(beyond.Rows) != 1 {
		t.Fatalf("out-of-range page = %+v", beyond)
	}
}
