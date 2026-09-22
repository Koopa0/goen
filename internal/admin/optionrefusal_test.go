package admin

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/i18n"
)

// TestARefusedOptionWriteNamesTheFieldTheConstraintRefused holds the answer a
// staff member gets when the database turns an option write down.
//
// The handler had one branch for every error: not found. So a value a CHECK
// refuses told the reader that the product they were editing does not exist,
// which sends them looking for a deleted product instead of at the box they
// mistyped. The field is read off ConstraintName because a PgError's message
// never contains it.
//
// Nothing a form can post reaches these constraints today: the Go checks in
// this file mirror each one, and a blank English name is written as NULL. That
// is why the 404 went unseen. The guard is therefore on the mapping the handler
// consults, so the day a check here is loosened the answer is still a refusal
// and not a vanished product.
func TestARefusedOptionWriteNamesTheFieldTheConstraintRefused(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	// Wrapped exactly as the store wraps it, so the guard fails if the store
	// stops passing the database's own error up.
	refused := func(constraint string) error {
		return fmt.Errorf("%w: %w", ErrRefused, &pgconn.PgError{
			Code: "23514", ConstraintName: constraint,
		})
	}

	for constraint, want := range map[string]string{
		"product_option_values_swatch_hex_shape": "swatch_hex",
		"product_option_values_value_present":    "value",
		"product_options_name_present":           "option",
	} {
		got := optionRefusal(ctx, refused(constraint))
		if len(got) != 1 {
			t.Errorf("%s refused %d fields, want exactly 1 — the form marks one "+
				"control and the page answers 422", constraint, len(got))
			continue
		}
		message, ok := got[want]
		if !ok {
			t.Errorf("%s came back on %v, want the field %q", constraint, keysOf(got), want)
			continue
		}
		if message == "" {
			t.Errorf("%s marked %q invalid with no sentence beside it, which is "+
				"the half a screen reader cannot work with", constraint, want)
		}
	}

	// The failures that are NOT a refusal keep their answers: a product that
	// is gone is a 404, and an infrastructure error is not something a staff
	// member can retype.
	for name, err := range map[string]error{
		"a missing product":      ErrNotFound,
		"an unmapped rule":       refused("product_variants_price_positive"),
		"a connection that died": errors.New("read tcp 10.0.0.1:5432: i/o timeout"),
	} {
		if got := optionRefusal(ctx, err); got != nil {
			t.Errorf("%s came back as the field errors %v; the page would answer "+
				"422 for something no field can fix", name, got)
		}
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
