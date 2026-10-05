//go:build integration

package products_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/products"
)

// TestAttachImageAnswersALockTimeoutAsAFailure: the catalogue lock timing out
// is the database not answering, and calling it a refusal tells staff the
// image is already on the product.
func TestAttachImageAnswersALockTimeoutAsAFailure(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	slug := admintest.AnyProductSlug(t, pool)
	s := products.NewStore(admintest.LockTimeoutPool(t, pool))
	admintest.HoldRow(t, pool, `SELECT 1 FROM products WHERE slug = $1 FOR UPDATE`, slug)

	err := s.AttachImage(ctx, slug, strings.Repeat("0", 64), "lock timeout", "", "", 800, 600)
	if errors.Is(err, products.ErrRefused) || !admintest.LockTimedOut(err) {
		t.Fatalf("AttachImage(%s) behind a held product lock = %v, want lock_not_available (55P03) and not ErrRefused", slug, err)
	}
}
