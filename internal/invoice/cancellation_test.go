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
		{
			name:     "last day of a period",
			issuedAt: time.Date(2026, time.February, 28, 12, 0, 0, 0, taipei),
			want:     time.Date(2026, time.March, 13, 23, 59, 59, 0, taipei),
		},
		{
			name:     "first day of a period",
			issuedAt: time.Date(2026, time.March, 1, 0, 0, 0, 0, taipei),
			want:     time.Date(2026, time.May, 13, 23, 59, 59, 0, taipei),
		},
		{
			name:     "year end",
			issuedAt: time.Date(2026, time.December, 31, 23, 0, 0, 0, taipei),
			want:     time.Date(2027, time.January, 13, 23, 59, 59, 0, taipei),
		},
		{
			// 17:00 UTC on 28 February is already 1 March in Taipei.
			name:     "the shop's calendar",
			issuedAt: time.Date(2026, time.February, 28, 17, 0, 0, 0, time.UTC),
			want:     time.Date(2026, time.May, 13, 23, 59, 59, 0, taipei),
		},
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
		name         string
		now          time.Time
		hasAllowance bool
		want         bool
	}{
		{name: "the deadline's last second", now: lastSecond, want: true},
		{name: "the next day", now: lastSecond.Add(time.Second), want: false},
		{name: "inside the window with an allowance", now: issuedAt.Add(time.Hour), hasAllowance: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := voidable(issuedAt, tt.now, tt.hasAllowance); got != tt.want {
				t.Errorf("voidable(%s, hasAllowance=%v) = %v, want %v", tt.now, tt.hasAllowance, got, tt.want)
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
