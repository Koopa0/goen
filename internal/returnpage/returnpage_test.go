package returnpage

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

// TestValidateAcceptsABlankReason holds Consumer Protection Act §19 I at
// the form gate: a blank reason is legal, and the policy page says so.
func TestValidateAcceptsABlankReason(t *testing.T) {
	t.Parallel()

	for _, reason := range []string{"", "   \t "} {
		req := Request{
			Reason: reason,
			Lines:  map[string]int32{"line": 1},
		}
		if err := req.Validate(); err != nil {
			t.Errorf("reason %q was refused: %v", reason, err)
		}
	}
}

func TestParseWantedDistinguishesQuantityFromMalformedInput(t *testing.T) {
	lineID := uuid.New()
	allowed := map[string]int32{lineID.String(): 1}

	if _, err := parseWanted(lineID.String(), 2, allowed); !errors.Is(err, ErrTooMany) ||
		errors.Is(err, ErrInvalid) {
		t.Fatalf("quantity 2 with ceiling 1 = %v, want only ErrTooMany", err)
	}
	if _, err := parseWanted(uuid.NewString(), 1, allowed); !errors.Is(err, ErrInvalid) ||
		errors.Is(err, ErrTooMany) {
		t.Fatalf("unknown line = %v, want only ErrInvalid", err)
	}
}
