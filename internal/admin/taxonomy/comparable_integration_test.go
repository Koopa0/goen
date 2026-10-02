//go:build integration

package taxonomy_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/taxonomy"
)

// Only a department answers "compare products here". The answer is written on
// create and on edit, a sub-category stores none and takes its department's,
// and each write is audited with the value it made.
func TestOnlyADepartmentStoresWhetherItCompares(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := taxonomy.NewStore(pool)

	root := "cmp-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	child := root + "-sub"
	if errs, err := s.CreateCategory(ctx, &taxonomy.Form{Slug: root, Name: "可比較部門", Comparable: true}); err != nil || len(errs) > 0 {
		t.Fatalf("create the department: %v %v", errs, err)
	}
	// A ticked box on a sub-category is not stored: it is not the one that answers.
	if errs, err := s.CreateCategory(ctx, &taxonomy.Form{Slug: child, Name: "子架", Parent: root, Comparable: true}); err != nil || len(errs) > 0 {
		t.Fatalf("create the sub-category: %v %v", errs, err)
	}

	stored := func(slug string) (comparable *bool) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT comparable FROM categories WHERE slug = $1`, slug).Scan(&comparable); err != nil {
			t.Fatalf("read %s: %v", slug, err)
		}
		return comparable
	}
	if got := stored(root); got == nil || !*got {
		t.Errorf("the department stored %v, want true", got)
	}
	if got := stored(child); got != nil {
		t.Errorf("the sub-category stored %v, want NULL: it takes its department's", *got)
	}

	shown := func(slug string) bool {
		t.Helper()
		view, err := s.List(ctx)
		if err != nil {
			t.Fatalf("taxonomy: %v", err)
		}
		for _, c := range view.Categories {
			if c.Slug == slug {
				return c.Comparable
			}
		}
		t.Fatalf("%s is not in the taxonomy", slug)
		return false
	}
	if !shown(root) {
		t.Error("the back office does not show the department as comparing")
	}

	// An unticked box posts nothing, which edits the answer to no.
	if err := s.Rename(ctx, "category", root, "可比較部門", "", "", "", false); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got := stored(root); got == nil || *got {
		t.Errorf("after an unticked edit the department stored %v, want false", got)
	}
	if shown(root) {
		t.Error("the back office still shows the department as comparing")
	}
	if err := s.Rename(ctx, "category", child, "子架", "", "", "", true); err != nil {
		t.Fatalf("rename the sub-category: %v", err)
	}
	if got := stored(child); got != nil {
		t.Errorf("editing a sub-category stored %v, want NULL", *got)
	}

	for _, c := range []struct {
		action, where, want string
	}{
		{"category.create", `after->>'slug' = $1`, "true"},
		{"category.rename", `before->>'slug' = $1`, "false"},
	} {
		var got string
		if err := pool.QueryRow(ctx, `
			SELECT coalesce(after->>'comparable', '') FROM audit_events
			WHERE action = $2 AND `+c.where+`
			ORDER BY occurred_at DESC, id DESC LIMIT 1`, root, c.action).Scan(&got); err != nil {
			t.Fatalf("read the %s audit row: %v", c.action, err)
		}
		if got != c.want {
			t.Errorf("%s audited comparable = %q, want %q", c.action, got, c.want)
		}
	}
}
