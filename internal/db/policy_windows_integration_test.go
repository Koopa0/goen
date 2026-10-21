//go:build integration

package db_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/payment"
)

// dayArithmetic is a window function adding a day count of its own to a date.
var dayArithmetic = regexp.MustCompile(`[+-]\s*\d+`)

// TestTheReturnWindowsAreNamedOnceInTheSchema pins the two return windows every
// page states and the return decision applies. The literals are right: the copy
// promises seven days to cancel (Consumer Protection Act §19, the returns policy,
// the delivery mail, internal/i18n "七日") and fourteen for an unused return (the
// returns policy "within 14 days of delivery", the returns desk's "days 8–14"). A
// change to either function must change that copy in the same PR, and this test
// is where that is forced.
func TestTheReturnWindowsAreNamedOnceInTheSchema(t *testing.T) {
	ctx := t.Context()
	conn := schemaPool(t)

	var rescission, goodwill int
	if err := conn.QueryRow(ctx, `SELECT rescission_days(), return_goodwill_days()`).
		Scan(&rescission, &goodwill); err != nil {
		t.Fatalf("read the windows: %v", err)
	}
	if rescission != 7 || goodwill != 14 {
		t.Errorf("rescission_days() = %d, return_goodwill_days() = %d; want 7 and 14, "+
			"the days the customer-facing copy promises", rescission, goodwill)
	}

	// A parcel delivered at 00:30 on 2 October in Taipei is still 1 October in
	// UTC: the windows count from the shop's day.
	delivered := time.Date(2026, 10, 1, 16, 30, 0, 0, time.UTC)
	deliveryDay := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	t.Run("each last day is the delivery day plus its window", func(t *testing.T) {
		var lastToCancel, lastToReturn time.Time
		if err := conn.QueryRow(ctx, `SELECT return_window_ends($1), return_goodwill_ends($1)`, delivered).
			Scan(&lastToCancel, &lastToReturn); err != nil {
			t.Fatalf("read the last days: %v", err)
		}
		if want := deliveryDay.AddDate(0, 0, rescission); !lastToCancel.Equal(want) {
			t.Errorf("return_window_ends = %s, want %s", lastToCancel.Format(time.DateOnly), want.Format(time.DateOnly))
		}
		if want := deliveryDay.AddDate(0, 0, goodwill); !lastToReturn.Equal(want) {
			t.Errorf("return_goodwill_ends = %s, want %s", lastToReturn.Format(time.DateOnly), want.Format(time.DateOnly))
		}
	})

	// The pages state the two numbers above; the decision on a filed return must
	// change window on exactly those days.
	t.Run("the decision changes window on the stated days", func(t *testing.T) {
		for _, tt := range []struct {
			daysAfter int
			want      string
		}{
			{rescission, "within"},
			{rescission + 1, "goodwill"},
			{goodwill, "goodwill"},
			{goodwill + 1, "after"},
		} {
			// Noon in Taipei on that day.
			requested := deliveryDay.AddDate(0, 0, tt.daysAfter).Add(4 * time.Hour)
			var got string
			if err := conn.QueryRow(ctx, `SELECT return_line_policy_window($1, $2)`, requested, delivered).
				Scan(&got); err != nil {
				t.Fatalf("read the window %d days after delivery: %v", tt.daysAfter, err)
			}
			if got != tt.want {
				t.Errorf("a request %d days after the delivery day is %q, want %q", tt.daysAfter, got, tt.want)
			}
		}
	})

	// A source check on purpose: a day count typed into one of these is a second
	// copy of the number, which a page stating the named one would contradict.
	t.Run("no window function adds a day count of its own", func(t *testing.T) {
		for _, fn := range []string{"return_window_ends", "return_goodwill_ends", "return_line_policy_window"} {
			if src := functionSource(t, fn); dayArithmetic.MatchString(src) {
				t.Errorf("%s adds a literal day count (%q); it must read rescission_days() or return_goodwill_days()",
					fn, dayArithmetic.FindString(src))
			}
		}
	})
}

// TestTheCheckoutWindowsAreTheOnesTheStorefrontEnforces holds every Go copy of
// the checkout windows to the schema's: the hold the checkout asks
// hold_inventory for, the pay window, and the payment page's admission rule, which
// leaves Stripe its session floor out of the hold. The FAQ seed types both
// numbers, so it is held here too.
func TestTheCheckoutWindowsAreTheOnesTheStorefrontEnforces(t *testing.T) {
	ctx := t.Context()
	conn := schemaPool(t)

	var hold, payStart int
	if err := conn.QueryRow(ctx, `SELECT checkout_hold_minutes(), checkout_pay_start_minutes()`).
		Scan(&hold, &payStart); err != nil {
		t.Fatalf("read the checkout windows: %v", err)
	}
	holdFor, payFor := time.Duration(hold)*time.Minute, time.Duration(payStart)*time.Minute

	if payment.HoldTTL != holdFor {
		t.Errorf("payment.HoldTTL = %s, checkout_hold_minutes() = %s", payment.HoldTTL, holdFor)
	}
	if payment.PayWindow != payFor {
		t.Errorf("payment.PayWindow = %s, checkout_pay_start_minutes() = %s", payment.PayWindow, payFor)
	}
	placed := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	if got := payment.StartBy(placed.Add(holdFor)).Sub(placed); got != payFor {
		t.Errorf("payment admits a session for %s after placing the order, checkout_pay_start_minutes() = %s", got, payFor)
	}

	t.Run("hold_inventory reads the named hold", func(t *testing.T) {
		src := functionSource(t, "hold_inventory")
		if !strings.Contains(src, "checkout_hold_minutes()") {
			t.Error("hold_inventory does not read checkout_hold_minutes()")
		}
		if strings.Contains(src, "minutes'") {
			t.Error("hold_inventory holds a literal duration; the cap must be checkout_hold_minutes()")
		}
	})

	t.Run("the FAQ seed states both", func(t *testing.T) {
		seed, err := os.ReadFile(filepath.Join("..", "..", "seed", "dev_catalog.sql"))
		if err != nil {
			t.Fatalf("read the seed: %v", err)
		}
		for _, want := range []string{
			fmt.Sprintf("保留庫存 %d 分鐘", hold),
			fmt.Sprintf("holds the stock for %d minutes", hold),
			fmt.Sprintf("%d 分鐘內開始付款", payStart),
			fmt.Sprintf("start the payment within %d minutes", payStart),
		} {
			if !strings.Contains(string(seed), want) {
				t.Errorf("no FAQ row states %q, so the answer a customer reads and the "+
					"window the storefront enforces are two different numbers", want)
			}
		}
	})
}

// TestTheStorefrontRolesReadTheNamedWindows runs each window function as the
// roles that call it in production. The suites connect as the owner, so a
// missing grant would pass every other test and fail on the live site.
func TestTheStorefrontRolesReadTheNamedWindows(t *testing.T) {
	ctx := t.Context()
	const returns = `SELECT rescission_days(), return_goodwill_days(),
		return_window_ends(now()), return_goodwill_ends(now()),
		return_line_policy_window(now(), now())`
	const checkout = `SELECT checkout_hold_minutes(), checkout_pay_start_minutes()`
	for _, tt := range []struct {
		name, role, query string
	}{
		{"store reads the return windows", "store", returns},
		{"store reads the checkout windows", "store", checkout},
		{"admin reads the return windows", "admin", returns},
		{"reporting reads the return windows", "reporting", returns},
	} {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := schemaPool(t).Acquire(ctx)
			if err != nil {
				t.Fatalf("acquire a connection: %v", err)
			}
			defer conn.Release()
			if _, err := conn.Exec(ctx, "SET ROLE "+tt.role); err != nil {
				t.Fatalf("assume %s: %v", tt.role, err)
			}
			defer func() {
				if _, err := conn.Exec(ctx, "RESET ROLE"); err != nil {
					t.Errorf("reset role: %v", err)
				}
			}()
			if _, err := conn.Exec(ctx, tt.query); err != nil {
				t.Errorf("%s cannot run %s: %v", tt.role, tt.query, err)
			}
		})
	}
}

// functionSource is the body of the one public function called name.
func functionSource(t *testing.T, name string) string {
	t.Helper()
	var src string
	if err := schemaPool(t).QueryRow(t.Context(), `
		SELECT p.prosrc FROM pg_proc p
		WHERE p.pronamespace = 'public'::regnamespace AND p.proname = $1`, name).Scan(&src); err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return src
}
