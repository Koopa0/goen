package admin

import (
	"slices"
	"testing"
)

func TestABlankPrefixSetIsDistinctFromAMissingCreationPrefix(t *testing.T) {
	set, message := parsePrefixes(t.Context(), "  , ; \n\t")
	if message != "" {
		t.Fatalf("blank replacement was refused: %q", message)
	}
	if set == nil {
		t.Fatal("blank replacement parsed as nil, want a non-nil empty set")
	}
	if len(set) != 0 {
		t.Fatalf("blank replacement parsed as %v, want an empty set", set)
	}

	if _, message := parseRequiredPrefixes(t.Context(), "  , ; \n\t"); message == "" {
		t.Fatal("blank prefixes were accepted while creating a zone")
	}
}

func FuzzParsePrefixes(f *testing.F) {
	for _, seed := range []string{
		"", "000", "001, 002;003\n004", "001 001", "12", "1000", "abc", "１２３",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		prefixes, message := parsePrefixes(t.Context(), input)
		required, requiredMessage := parseRequiredPrefixes(t.Context(), input)
		if message != "" {
			if requiredMessage == "" {
				t.Fatalf("parseRequiredPrefixes(%q) accepted input refused by parsePrefixes", input)
			}
			return
		}

		seen := make(map[string]bool, len(prefixes))
		for _, prefix := range prefixes {
			if len(prefix) != 3 || prefix[0] < '0' || prefix[0] > '9' ||
				prefix[1] < '0' || prefix[1] > '9' || prefix[2] < '0' || prefix[2] > '9' {
				t.Fatalf("parsePrefixes(%q) returned non-prefix %q", input, prefix)
			}
			if seen[prefix] {
				t.Fatalf("parsePrefixes(%q) returned duplicate %q", input, prefix)
			}
			seen[prefix] = true
		}

		if len(prefixes) == 0 {
			if prefixes == nil {
				t.Fatalf("parsePrefixes(%q) returned nil for an accepted empty replacement", input)
			}
			if requiredMessage == "" {
				t.Fatalf("parseRequiredPrefixes(%q) accepted an empty creation set", input)
			}
			return
		}
		if requiredMessage != "" || !slices.Equal(required, prefixes) {
			t.Fatalf("parseRequiredPrefixes(%q) = %v/%q, want %v/empty", input,
				required, requiredMessage, prefixes)
		}
	})
}
