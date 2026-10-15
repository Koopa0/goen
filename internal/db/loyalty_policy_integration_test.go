//go:build integration

package db_test

import (
	"strings"
	"testing"
)

// TestTheLoyaltyPolicyIsNamedOnceInTheSchema pins the two programme rules the
// loyalty pages and the award path must all read from the schema: how long a
// customer's spend counts towards their tier, and how long earned points last.
// The literal is right: the copy promises a year (internal/i18n/points.go "Points
// expire one year after you earn them" and, for the back office, "spent in the
// last year" and "Spend over the last year"; internal/i18n/account.go "spent in
// the last year"). A change to either function must change that copy in the same
// PR, and this test is where that is forced.
func TestTheLoyaltyPolicyIsNamedOnceInTheSchema(t *testing.T) {
	ctx := t.Context()
	conn := schemaPool(t)

	t.Run("the window and the lifetime are a year", func(t *testing.T) {
		var window, lifetime int
		if err := conn.QueryRow(ctx,
			`SELECT membership_window_days(), points_lifetime_days()`).Scan(&window, &lifetime); err != nil {
			t.Fatalf("read the policy: %v", err)
		}
		if window != 365 || lifetime != 365 {
			t.Errorf("membership_window_days() = %d, points_lifetime_days() = %d; want 365 and 365, "+
				"the year the customer-facing copy promises", window, lifetime)
		}
	})

	// A reader that passed its own window would judge a tier by a rule the award
	// does not apply. member_spend and member_tier therefore take no integer.
	t.Run("no reader can pass its own window", func(t *testing.T) {
		for _, fn := range []string{"member_spend", "member_tier"} {
			var overloads, withInteger int
			if err := conn.QueryRow(ctx, `
				SELECT count(*),
				       count(*) FILTER (WHERE 'integer'::regtype = ANY (p.proargtypes::oid[]))
				FROM pg_proc p
				WHERE p.pronamespace = 'public'::regnamespace AND p.proname = $1`, fn).
				Scan(&overloads, &withInteger); err != nil {
				t.Fatalf("read pg_proc for %s: %v", fn, err)
			}
			if overloads != 1 {
				t.Errorf("%s has %d overloads, want exactly 1", fn, overloads)
			}
			if withInteger != 0 {
				t.Errorf("%s takes an integer argument; the window must come from membership_window_days()", fn)
			}
		}
	})

	// A source check on purpose: the award path is where a second copy of the
	// number costs money, because points would be minted at one rule while every
	// page showed another.
	t.Run("the award path holds no literal of its own", func(t *testing.T) {
		var src string
		if err := conn.QueryRow(ctx, `
			SELECT p.prosrc FROM pg_proc p
			WHERE p.pronamespace = 'public'::regnamespace AND p.proname = 'award_loyalty_points'`).
			Scan(&src); err != nil {
			t.Fatalf("read award_loyalty_points: %v", err)
		}
		if strings.Contains(src, "365") {
			t.Errorf("award_loyalty_points holds a literal 365; it must read membership_window_days() through member_tier and points_lifetime_days()")
		}
		if !strings.Contains(src, "points_lifetime_days()") {
			t.Errorf("award_loyalty_points does not read points_lifetime_days()")
		}
	})
}
