package db

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestHasConstraintReadsTheConstraintNameNotTheMessage(t *testing.T) {
	t.Parallel()
	violation := fmt.Errorf("insert: %w", &pgconn.PgError{
		Code: "23505", ConstraintName: "coupons_code_key", Message: "duplicate key"})
	if !HasConstraint(violation, "coupons_code_key") {
		t.Error("the named constraint was not recognised through a wrapped error")
	}
	if HasConstraint(violation, "duplicate key") {
		t.Error("the message was matched as a constraint")
	}
	if HasConstraint(errors.New("coupons_code_key"), "coupons_code_key") {
		t.Error("a plain error naming the constraint was matched")
	}
}
