//go:build integration

package products_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/pgtx"
)

func TestOverlappingSpecAdditionsKeepBothSpecifications(t *testing.T) {
	ctx, actor := admintest.StaffContext(t, pool)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	adminPool := admintest.AdminRolePool(t, pool)
	s := products.NewStore(adminPool)
	slug := admintest.DraftProduct(t, ctx, pool, s)
	otherSlug := admintest.DraftProduct(t, ctx, pool, s)

	first, beginErr := adminPool.Begin(ctx)
	if beginErr != nil {
		t.Fatal(beginErr)
	}
	defer pgtx.Rollback(ctx, first)
	var pid int32
	if err := first.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	q := db.New(first)
	if err := q.LockProductSpecAppendPosition(ctx, slug); err != nil {
		t.Fatal(err)
	}
	if _, err := q.AddProductSpec(ctx, db.AddProductSpecParams{
		Slug: slug, Label: "First label", Value: "First value",
		LabelEn: "First English label", ValueEn: "First English value",
	}); err != nil {
		t.Fatal(err)
	}

	app := "spec-append-" + uuid.NewString()
	writerPool := admintest.NamedPool(t, pool, app)
	if _, err := writerPool.Exec(ctx, `SET ROLE admin`); err != nil {
		t.Fatal(err)
	}
	writer := products.NewStore(writerPool)
	mux := http.NewServeMux()
	admintest.ProductDesk(writerPool, writer).Routes(mux, admintest.BackOffice)
	form := url.Values{
		"label": {"Second label"}, "value": {"Second value"},
		"label_en": {"Draft label"}, "value_en": {"Draft value"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/products/"+slug+"/specs", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		mux.ServeHTTP(res, req)
	}()
	admintest.WaitForBlockedApplication(t, pool, ctx, app, pid)

	otherCtx, otherCancel := context.WithTimeout(ctx, 2*time.Second)
	fields, addErr := s.AddSpec(otherCtx, otherSlug, products.SpecDraft{Label: "Other label", Value: "Other value"})
	otherCancel()
	if addErr != nil || len(fields) != 0 {
		t.Fatalf("unrelated product append = %v %v", fields, addErr)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/products/"+slug+"?ok=1" {
		t.Fatalf("second append = HTTP %d, Location %q; want success redirect", res.Code, res.Header().Get("Location"))
	}

	specs, readErr := db.New(adminPool).AdminProductSpecs(ctx, slug)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(specs) != 2 {
		t.Fatalf("persisted specifications = %d, want 2", len(specs))
	}
	for i, want := range []products.SpecDraft{
		{Label: "First label", Value: "First value", LabelEn: "First English label", ValueEn: "First English value"},
		{Label: form.Get("label"), Value: form.Get("value"), LabelEn: form.Get("label_en"), ValueEn: form.Get("value_en")},
	} {
		got := products.SpecDraft{Label: specs[i].Label, Value: specs[i].Value, LabelEn: specs[i].LabelEn, ValueEn: specs[i].ValueEn}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("specification %d (-want +got):\n%s", i, diff)
		}
		if specs[i].Position != int32(i+1) {
			t.Errorf("specification %d position = %d, want %d", i, specs[i].Position, i+1)
		}
	}
	var audits int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		WHERE actor_user_id = $1 AND action = $2
		  AND after->>'slug' = $3 AND after->>'label' = $4`,
		actor, string(audit.ActionAddSpec), slug, form.Get("label")).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Errorf("second specification audit rows = %d, want 1", audits)
	}
}
