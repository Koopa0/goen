package products_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/products"
)

// TestForm_ValidateBrand: the product form accepts no brand but refuses an
// invalid brand.
func TestForm_ValidateBrand(t *testing.T) {
	for _, brand := range []string{"", uuid.NewString(), "not-a-brand-id"} {
		form := products.Form{Slug: "generic", Name: "Generic", BrandID: brand, CategoryID: uuid.NewString()}
		errs := form.Validate(t.Context())
		_, refused := errs["brand"]
		if want := brand == "not-a-brand-id"; refused != want {
			t.Errorf("brand %q refused = %t, want %t: %v", brand, refused, want, errs)
		}
		delete(errs, "brand")
		if len(errs) != 0 {
			t.Fatalf("other form errors: %v", errs)
		}
	}
}
