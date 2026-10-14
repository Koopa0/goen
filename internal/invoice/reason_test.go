package invoice

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// storedReasons is every value a Reason has ever been stored as, spelled out. The
// values are rows in invoice_operations.last_error and strings the health page
// matches, so a rename is a migration of stored data and not a refactor: it has
// to change this list on purpose.
var storedReasons = []string{
	"allowance_amount_still_held",
	"allowance_awaiting_buyer",
	"allowance_buyer_unconfirmed",
	"allowance_candidate_without_send_evidence",
	"allowance_consent_encoding_failed",
	"allowance_invalid_after_current_send",
	"allowance_invalid_reconcile_contradiction",
	"allowance_invalid_reconcile_failed",
	"allowance_invalid_reconcile_lost_lease",
	"allowance_invalid_record_contradiction",
	"allowance_invalid_record_failed",
	"allowance_invalid_record_lost_lease",
	"allowance_known_document_duplicate",
	"allowance_known_document_malformed",
	"allowance_known_document_mismatch",
	"allowance_known_document_status_unknown",
	"allowance_known_facts_failed",
	"allowance_local_settlement_failed",
	"allowance_lookup_failed",
	"allowance_lookup_mismatch",
	"allowance_multiple_unknown_candidates",
	"allowance_not_yet_visible",
	"allowance_provider_invalid",
	"allowance_provider_invalid_refrozen",
	"allowance_provider_reactivated",
	"allowance_resend_authorized",
	"allowance_send_ambiguous",
	"allowance_success_mismatch",
	"frozen_allowance_request_invalid",
	"frozen_issue_request_invalid",
	"frozen_void_request_invalid",
	"issue_local_settlement_failed",
	"issue_lookup_failed",
	"issue_lookup_mismatch",
	"issue_not_yet_visible",
	"issue_post_send_lookup_failed",
	"issue_send_ambiguous",
	"issue_sent_before_cancellation",
	"issue_withdrawn_order_cancelled",
	"unknown_operation_kind",
	"void_local_settlement_failed",
	"void_lookup_failed",
	"void_lookup_mismatch",
	"void_provider_rejected",
	"void_send_needs_lookup",
	"void_success_identity_mismatch",
}

func TestReasonValuesAreThePinnedStoredStrings(t *testing.T) {
	t.Parallel()
	got := make([]string, 0, len(allReasons))
	for _, r := range Reasons() {
		got = append(got, string(r))
	}
	slices.Sort(got)
	want := slices.Clone(storedReasons)
	slices.Sort(want)
	for _, v := range got {
		if !slices.Contains(want, v) {
			t.Errorf("Reasons() lists %q, which is not a pinned stored value: a renamed or new reason must be added to storedReasons on purpose", v)
		}
	}
	for _, v := range want {
		if !slices.Contains(got, v) {
			t.Errorf("the stored value %q is pinned but no Reason holds it any more: rows may still carry it", v)
		}
	}
	if len(slices.Compact(slices.Clone(got))) != len(got) {
		t.Error("two Reason constants hold the same stored string")
	}
}

func TestEveryReasonFitsTheColumn(t *testing.T) {
	t.Parallel()
	// invoice_operations_last_error_bounded: char_length(last_error) <= 2000.
	for _, r := range Reasons() {
		if r == "" || len(r) > 2000 {
			t.Errorf("reason %q cannot be stored", r)
		}
	}
}

func TestProviderRejectionsAreStoredWithTheirCode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		got  Reason
		want string
	}{
		{IssueProviderRejected(2000006), "issue_provider_rejected_2000006"},
		{AllowanceProviderRejected(3100010), "allowance_provider_rejected_3100010"},
	} {
		if string(tc.got) != tc.want || !tc.got.ProviderRejected() {
			t.Errorf("rejection = %q (rejected %t), want %q", tc.got, tc.got.ProviderRejected(), tc.want)
		}
		if parsed, ok := ParseReason(tc.want); !ok || parsed != tc.got {
			t.Errorf("ParseReason(%q) = %q, %t", tc.want, parsed, ok)
		}
	}
}

func TestParseReasonAdmitsOnlyKnownText(t *testing.T) {
	t.Parallel()
	for _, r := range Reasons() {
		if parsed, ok := ParseReason(string(r)); !ok || parsed != r {
			t.Errorf("ParseReason(%q) = %q, %t", r, parsed, ok)
		}
	}
	for _, text := range []string{"", "future_reason", "issue_provider_rejected", "ISSUE_LOOKUP_FAILED"} {
		if _, ok := ParseReason(text); ok {
			t.Errorf("ParseReason(%q) admitted text which is no reason", text)
		}
	}
}

func TestReasonsCannotBeChangedThroughTheirCopy(t *testing.T) {
	t.Parallel()
	first := Reasons()
	first[0] = "overwritten"
	if Reasons()[0] == "overwritten" {
		t.Error("Reasons() hands out the package's own array")
	}
}

// databaseReasons finds the last_error texts which the schema's functions write
// or test. Each must be a Reason, or the health page cannot explain it.
var databaseReasons = regexp.MustCompile(`last_error\s*=\s*'([a-z_]+)'`)

func TestEveryReasonTheDatabaseNamesIsAReason(t *testing.T) {
	t.Parallel()
	schema, err := os.ReadFile(filepath.Join("..", "..", "migrations", "001_initial_schema.up.sql"))
	if err != nil {
		t.Fatalf("read the schema: %v", err)
	}
	found := map[string]bool{}
	for _, m := range databaseReasons.FindAllSubmatch(schema, -1) {
		found[string(m[1])] = true
	}
	if len(found) == 0 {
		t.Fatal("no last_error text found in the schema; the pattern no longer matches it")
	}
	for _, text := range slices.Sorted(maps.Keys(found)) {
		if _, ok := ParseReason(text); !ok {
			t.Errorf("the schema names last_error %q, which is not an invoice.Reason", text)
		}
	}
}
