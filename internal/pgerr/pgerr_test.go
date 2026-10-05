package pgerr

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsConstraintReadsTheConstraintNameNotTheMessage(t *testing.T) {
	t.Parallel()
	violation := fmt.Errorf("insert: %w", &pgconn.PgError{
		Code: "23505", ConstraintName: "coupons_code_key", Message: "duplicate key"})
	if !IsConstraint(violation, "coupons_code_key") {
		t.Error("the named constraint was not recognised through a wrapped error")
	}
	if IsConstraint(violation, "duplicate key") {
		t.Error("the message was matched as a constraint")
	}
	if IsConstraint(errors.New("coupons_code_key"), "coupons_code_key") {
		t.Error("a plain error naming the constraint was matched")
	}
}

func TestWrapRefusalMarksOnlyWhatARuleDecided(t *testing.T) {
	t.Parallel()
	errRefused := errors.New("desk: refused")
	for _, tt := range []struct {
		name    string
		err     error
		refused bool
	}{
		{name: "a trigger's named refusal", err: &pgconn.PgError{Code: "23514", ConstraintName: "orders_legal_transition"}, refused: true},
		{name: "a unique violation", err: &pgconn.PgError{Code: "23505", ConstraintName: "coupons_code_key"}, refused: true},
		{name: "a not-null violation", err: &pgconn.PgError{Code: "23502"}, refused: true},
		{name: "a lock timeout", err: &pgconn.PgError{Code: "55P03"}, refused: false},
		{name: "a deadlock", err: &pgconn.PgError{Code: "40P01"}, refused: false},
		{name: "a statement timeout", err: &pgconn.PgError{Code: "57014"}, refused: false},
		{name: "a closed pool", err: errors.New("closed pool"), refused: false},
	} {
		wrapped := fmt.Errorf("commit: %w", tt.err)
		got := WrapRefusal(wrapped, errRefused)
		if errors.Is(got, errRefused) != tt.refused {
			t.Errorf("WrapRefusal(%s) = %v, refused %t, want %t", tt.name, got, !tt.refused, tt.refused)
		}
		if !errors.Is(got, tt.err) {
			t.Errorf("WrapRefusal(%s) = %v, which no longer wraps %v", tt.name, got, tt.err)
		}
	}
	if got := WrapRefusal(nil, errRefused); got != nil {
		t.Errorf("WrapRefusal(nil) = %v, want nil", got)
	}
}
