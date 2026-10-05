//go:build integration

package campaigns_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/campaigns"
)

// TestCampaignWritesAnswerAFailureAsAServerError: a database that cannot be
// reached decided nothing, and answering "no discount" or "refused" sends staff
// looking for a rule.
func TestCampaignWritesAnswerAFailureAsAServerError(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	closed := admintest.NamedPool(t, pool, "campaign_closed_"+uuid.NewString()[:8])
	closed.Close()
	h := handlerOver(campaigns.NewStore(closed))
	slug := admintest.CampaignSlug(t)
	for _, tt := range []struct {
		name  string
		form  url.Values
		serve http.HandlerFunc
	}{
		{name: "feature", form: url.Values{"product": {"any"}}, serve: h.FeatureProduct},
		{name: "unfeature", form: url.Values{"product": {"any"}, "action": {"remove"}}, serve: h.FeatureProduct},
		{name: "set active", form: url.Values{"active": {"true"}}, serve: h.SetActive},
	} {
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/campaigns/"+slug, strings.NewReader(tt.form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("slug", slug)
		res := httptest.NewRecorder()
		tt.serve(res, req)
		if res.Code != http.StatusInternalServerError {
			t.Errorf("%s on a closed pool = %d %s, want 500", tt.name, res.Code, res.Header().Get("Location"))
		}
	}
}

// TestACampaignWriteBehindALockIsNotARefusal: the write's own statement timing
// out on a row lock is the database not answering, not a rule refusing it.
func TestACampaignWriteBehindALockIsNotARefusal(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	slug := admintest.CampaignSlug(t)
	if _, err := campaigns.NewStore(pool).Create(ctx, &campaigns.Form{Slug: slug, Title: "鎖定測試", Days: 7}); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse lock-timeout pool config: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "200"
	timed, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open lock-timeout pool: %v", err)
	}
	t.Cleanup(timed.Close)

	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the lock holder: %v", err)
	}
	t.Cleanup(func() {
		if rollbackErr := holder.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil {
			t.Errorf("release the campaign row: %v", rollbackErr)
		}
	})
	if _, err = holder.Exec(ctx, `SELECT 1 FROM sale_campaigns WHERE slug = $1 FOR UPDATE`, slug); err != nil {
		t.Fatalf("hold the campaign row: %v", err)
	}

	err = campaigns.NewStore(timed).SetActive(ctx, slug, false)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); errors.Is(err, campaigns.ErrRefused) || !ok || pgErr.Code != "55P03" {
		t.Fatalf("SetActive(%s) behind a held row = %v, want lock_not_available (55P03) and not ErrRefused", slug, err)
	}
}
