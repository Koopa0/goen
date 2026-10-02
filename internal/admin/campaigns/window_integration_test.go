//go:build integration

package campaigns_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/campaigns"
	"github.com/koopa0/goen/internal/i18n"
)

func TestACampaignsDatesAndStateAreEditedOnItsOwnPage(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := campaigns.NewStore(pool)
	slug := admintest.CampaignSlug(t)
	if _, err := s.Create(ctx, &campaigns.Form{Slug: slug, Title: "秋季精選", Days: 7}); err != nil {
		t.Fatal(err)
	}

	errs, err := s.SetWindow(ctx, slug, "2026-11-01T09:00", "2026-11-30T23:59")
	if err != nil || len(errs) > 0 {
		t.Fatalf("SetCampaignWindow: %v %v", err, errs)
	}
	got, err := s.Detail(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "秋季精選" || got.StartsAtInput != "2026-11-01T09:00" || got.EndsAtInput != "2026-11-30T23:59" {
		t.Errorf("the page reads %+v after the edit", got)
	}

	for name, window := range map[string][2]string{
		"an empty window":    {"2026-12-01T09:00", "2026-12-01T09:00"},
		"a window too long":  {"2026-12-01T09:00", "2027-03-02T09:01"},
		"a malformed minute": {"2026-12-01", "2026-12-02"},
	} {
		errs, err = s.SetWindow(ctx, slug, window[0], window[1])
		if err != nil || errs["window"] != i18n.T(ctx, i18n.KeyFormCampaignWindow) {
			t.Errorf("%s = %v %v, want the window refusal", name, err, errs)
		}
	}
	if after, _ := s.Detail(ctx, slug); after.StartsAtInput != "2026-11-01T09:00" {
		t.Errorf("a refused edit moved the dates to %s", after.StartsAtInput)
	}
	if _, err = s.SetWindow(ctx, "no-such-"+uuid.NewString()[:8], "2026-11-01T09:00", "2026-11-02T09:00"); !errors.Is(err, campaigns.ErrNotFound) {
		t.Errorf("unknown campaign = %v, want ErrNotFound", err)
	}
}

func TestEditingACampaignsDatesRecordsTheOldAndTheNewOnes(t *testing.T) {
	ctx, actor := admintest.StaffContext(t, pool)
	s := campaigns.NewStore(pool)
	slug := admintest.CampaignSlug(t)
	if _, err := s.Create(ctx, &campaigns.Form{Slug: slug, Title: "稽核活動", Days: 7}); err != nil {
		t.Fatal(err)
	}
	for _, window := range [][2]string{{"2026-11-01T09:00", "2026-11-10T09:00"}, {"2026-11-02T10:00", "2026-11-12T10:00"}} {
		if errs, err := s.SetWindow(ctx, slug, window[0], window[1]); err != nil || len(errs) > 0 {
			t.Fatalf("SetCampaignWindow: %v %v", err, errs)
		}
	}

	var ok bool
	if err := pool.QueryRow(ctx, `
		SELECT (before->>'ends_at')::timestamptz = $1::timestamptz
		   AND (after->>'starts_at')::timestamptz = $2::timestamptz
		   AND after->>'slug' = $3
		FROM audit_events
		WHERE action = $4 AND actor_id_snapshot = $5
		ORDER BY occurred_at DESC, id DESC LIMIT 1`,
		time.Date(2026, 11, 10, 1, 0, 0, 0, time.UTC), time.Date(2026, 11, 2, 2, 0, 0, 0, time.UTC),
		slug, string(audit.ActionSetCampaignWindow), actor).Scan(&ok); err != nil {
		t.Fatalf("read the audit row: %v", err)
	}
	if !ok {
		t.Error("the audit row does not hold the previous end and the new start as UTC instants")
	}
}
