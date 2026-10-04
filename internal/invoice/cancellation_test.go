package invoice

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestVoidDeadlineIsTheThirteenthAfterThePeriod(t *testing.T) {
	t.Parallel()

	taipei := time.FixedZone("Asia/Taipei", 8*60*60)
	tests := []struct {
		name     string
		issuedAt time.Time
		want     time.Time
	}{
		{"last day of a period", time.Date(2026, time.February, 28, 12, 0, 0, 0, taipei),
			time.Date(2026, time.March, 13, 23, 59, 59, 0, taipei)},
		{"first day of a period", time.Date(2026, time.March, 1, 0, 0, 0, 0, taipei),
			time.Date(2026, time.May, 13, 23, 59, 59, 0, taipei)},
		{"year end", time.Date(2026, time.December, 31, 23, 0, 0, 0, taipei),
			time.Date(2027, time.January, 13, 23, 59, 59, 0, taipei)},
		// 17:00 UTC on 28 February is already 1 March in Taipei.
		{"the shop's calendar", time.Date(2026, time.February, 28, 17, 0, 0, 0, time.UTC),
			time.Date(2026, time.May, 13, 23, 59, 59, 0, taipei)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := VoidDeadline(tt.issuedAt); !got.Equal(tt.want) {
				t.Errorf("VoidDeadline(%s) = %s, want %s", tt.issuedAt, got, tt.want)
			}
		})
	}
}

func TestAVoidIsOfferedUntilTheDeadlineAndNotAfterAnAllowance(t *testing.T) {
	t.Parallel()

	taipei := time.FixedZone("Asia/Taipei", 8*60*60)
	issuedAt := time.Date(2026, time.August, 20, 10, 0, 0, 0, taipei)
	lastSecond := time.Date(2026, time.September, 13, 23, 59, 59, 0, taipei)
	tests := []struct {
		name    string
		now     time.Time
		allowed bool
		want    bool
	}{
		{"the deadline's last second", lastSecond, false, true},
		{"the next day", lastSecond.Add(time.Second), false, false},
		{"inside the window with an allowance", issuedAt.Add(time.Hour), true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := voidable(issuedAt, tt.now, tt.allowed); got != tt.want {
				t.Errorf("voidable(%s, allowed=%v) = %v, want %v", tt.now, tt.allowed, got, tt.want)
			}
		})
	}
}

func TestAVoidDueEndsWhereNoVoidCanReachTheInvoice(t *testing.T) {
	t.Parallel()

	for constraint, wantDone := range map[string]bool{
		"invoice_void_target":          true,
		"invoice_void_issue_operation": true,
		"invoice_audit_actor":          false,
	} {
		err := voidDueClaimOutcome("AB12345678", &pgconn.PgError{Code: "23514", ConstraintName: constraint})
		if done := err == nil; done != wantDone {
			t.Errorf("a claim refused by %s: done = %v (%v), want %v", constraint, done, err, wantDone)
		}
	}
}
