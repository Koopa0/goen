package returns

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
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

// TestEveryKnownReturnStatusHasACustomerLabel holds the closed set together:
// a status with no catalogue entry must render as itself, never panic.
func TestEveryKnownReturnStatusHasACustomerLabel(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, status := range knownReturnStatuses {
			label := StatusLabel(ctx, status)
			if label == "" || label == string(status) {
				t.Errorf("StatusLabel(%q) in %s = %q, want a catalogue label",
					status, locale, label)
			}
		}
	}
}

func TestParseDecisionRejectsTypos(t *testing.T) {
	t.Parallel()

	for _, typo := range []string{"approveed", "rejectted", "requested", "completed", ""} {
		if _, ok := ParseDecision(typo); ok {
			t.Errorf("ParseDecision(%q) accepted a non-decision", typo)
		}
	}
	for _, decision := range []ReturnStatus{ReturnApproved, ReturnRejected} {
		got, ok := ParseDecision(string(decision))
		if !ok || got != decision {
			t.Errorf("ParseDecision(%q) = %q/%t, want %q/true", decision, got, ok, decision)
		}
	}
}

func TestUnknownReturnStatusRendersAsItself(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	unknown := ReturnStatus("legacy_foo")
	if got := StatusLabel(ctx, unknown); got != "legacy_foo" {
		t.Fatalf("StatusLabel(%q) = %q, want the raw status", unknown, got)
	}
}
