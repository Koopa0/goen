package acceptance_test

import (
	"fmt"
	"testing"

	"github.com/koopa0/goen/internal/acceptance"
)

// checkoutAttemptsOracle is the business invariant C06 locks: one idempotency key
// must never produce two checkout attempts.
func checkoutAttemptsOracle(attempts int) error {
	if attempts != 1 {
		return wrongBusinessResultError{want: 1, got: attempts}
	}
	return nil
}

type wrongBusinessResultError struct {
	want int
	got  int
}

func (e wrongBusinessResultError) Error() string {
	return fmt.Sprintf("checkout attempts = %d, want %d", e.got, e.want)
}

func TestPlantedWrongBusinessResultIsDetected(t *testing.T) {
	t.Parallel()
	if err := checkoutAttemptsOracle(1); err != nil {
		t.Fatalf("correct oracle: %v", err)
	}
	if err := checkoutAttemptsOracle(2); err == nil {
		t.Fatal("a planted duplicate checkout attempt was accepted")
	}
}

func TestRunnerExitCodeTreatsBlockedAsFailureInAllMode(t *testing.T) {
	t.Parallel()
	results := []acceptance.Result{
		{ScenarioID: "C02", Status: acceptance.StatusBlocked, Err: wrongBusinessResultError{want: 0, got: 1}},
	}
	if acceptance.ExitCode(results, true) == 0 {
		t.Fatal("all mode accepted a blocked scenario")
	}
	if acceptance.ExitCode(results, false) == 0 {
		t.Fatal("explicit failure was accepted in ready-only mode")
	}
}
