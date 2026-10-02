//go:build integration

package admin_test

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/campaigns"
	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/admin/taxonomy"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/ui/pages"
)

// The health page counts uploads nothing references; a category's photograph
// is a reference.
func TestAUploadHeldByACategoryIsNotCountedUnreferenced(t *testing.T) {
	ctx, _ := staffContext(t)
	messages := outbox.NewStore(pool, slog.New(slog.DiscardHandler))
	count := func() int64 {
		t.Helper()
		page, err := health.NewStore(pool).WorkerHealth(ctx, messages)
		if err != nil {
			t.Fatalf("read health: %v", err)
		}
		return page.UnreferencedMedia
	}

	before := count()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	digest := hex.EncodeToString(raw)
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_objects (digest, content_type, byte_size, width, height, bytes, created_at)
		VALUES ($1, 'image/png', 1, 1, 1, '\x00', now() - interval '48 hours')`, digest); err != nil {
		t.Fatalf("store an old upload: %v", err)
	}
	if got := count(); got != before+1 {
		t.Fatalf("an unreferenced upload counts %d, want %d", got, before+1)
	}

	slug := "held-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	if errs, err := taxonomy.NewStore(pool).CreateCategory(ctx, &taxonomy.Form{Slug: slug, Name: "持有照片"}); err != nil || len(errs) > 0 {
		t.Fatalf("create: %v %v", errs, err)
	}
	if err := taxonomy.NewStore(pool).SetCategoryImage(ctx, slug, digest, "照片", ""); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if got := count(); got != before {
		t.Errorf("a category's photograph counts %d unreferenced, want %d", got, before)
	}
}

// A root with no tone of its own is stone, and a campaign takes any tone of the
// closed set: the Go set and the schema's CHECK name the same six.
func TestEveryToneOfTheClosedSetIsStorable(t *testing.T) {
	ctx, _ := staffContext(t)
	slug := "tone-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	if errs, err := taxonomy.NewStore(pool).CreateCategory(ctx, &taxonomy.Form{Slug: slug, Name: "無色調"}); err != nil || len(errs) > 0 {
		t.Fatalf("create: %v %v", errs, err)
	}
	view, err := catalog.NewStore(pool).Listing(ctx, slug, catalog.Filters{})
	if err != nil || view.Theme.ToneAttr() != "stone" {
		t.Fatalf("a root with no tone = %q (err %v), want stone", view.Theme.ToneAttr(), err)
	}

	camp := "tone-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	errs, err := campaigns.NewStore(pool).Create(ctx, &campaigns.Form{Slug: camp, Title: "色調測試", Days: 7})
	if err != nil || len(errs) > 0 {
		t.Fatalf("create the campaign: %v %v", errs, err)
	}
	for _, tone := range pages.Tones() {
		if err = campaigns.NewStore(pool).SetTone(ctx, camp, string(tone)); err != nil {
			t.Errorf("SetTone(%q): %v", tone, err)
		}
		if err = taxonomy.NewStore(pool).Rename(ctx, "category", slug, "無色調", "", "", string(tone), false); err != nil {
			t.Errorf("Rename with tone %q: %v", tone, err)
		}
	}
	if err = campaigns.NewStore(pool).SetTone(ctx, camp, "neon"); err == nil {
		t.Error("a campaign accepted a tone outside the set")
	}
	cv, err := catalog.NewStore(pool).Campaign(ctx, camp)
	if err != nil || cv.Tone != pages.ToneInk {
		t.Fatalf("campaign tone = %q (err %v), want the last one set, ink", cv.Tone, err)
	}
}
