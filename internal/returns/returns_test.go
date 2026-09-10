package returns

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

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
