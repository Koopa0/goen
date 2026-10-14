package invoice

import (
	"slices"
	"strconv"
	"strings"
)

// Reason is why an invoice operation is not settled: the text the issuer stores
// in the operation's last_error column when it reschedules, alarms or rejects
// the operation, and which the health page reads back to explain the row.
//
// The stored string is the contract between the writer and the page, and a
// released database keeps every value it was ever given, so a constant's value
// never changes; a reason which is no longer written stays here for its rows.
// Adding a constant means adding it to Reasons and giving it a label on the
// health page, which a test there checks in both languages.
type Reason string

// The reasons of an issue operation.
const (
	ReasonIssueLocalSettlementFailed   Reason = "issue_local_settlement_failed"
	ReasonIssueLookupFailed            Reason = "issue_lookup_failed"
	ReasonIssueLookupMismatch          Reason = "issue_lookup_mismatch"
	ReasonIssueNotYetVisible           Reason = "issue_not_yet_visible"
	ReasonIssuePostSendLookupFailed    Reason = "issue_post_send_lookup_failed"
	ReasonIssueSendAmbiguous           Reason = "issue_send_ambiguous"
	ReasonIssueSentBeforeCancellation  Reason = "issue_sent_before_cancellation"
	ReasonIssueWithdrawnOrderCancelled Reason = "issue_withdrawn_order_cancelled"
)

// The reasons of an allowance operation. ReasonAllowanceBuyerUnconfirmed,
// ReasonAllowanceAmountStillHeld, ReasonAllowanceMultipleUnknownCandidates and
// ReasonAllowanceSuccessMismatch enter the attention state, where the
// back-office views act on them by name.
const (
	ReasonAllowanceAmountStillHeld               Reason = "allowance_amount_still_held"
	ReasonAllowanceAwaitingBuyer                 Reason = "allowance_awaiting_buyer"
	ReasonAllowanceBuyerUnconfirmed              Reason = "allowance_buyer_unconfirmed"
	ReasonAllowanceCandidateWithoutSendEvidence  Reason = "allowance_candidate_without_send_evidence"
	ReasonAllowanceConsentEncodingFailed         Reason = "allowance_consent_encoding_failed"
	ReasonAllowanceInvalidAfterCurrentSend       Reason = "allowance_invalid_after_current_send"
	ReasonAllowanceInvalidReconcileContradiction Reason = "allowance_invalid_reconcile_contradiction"
	ReasonAllowanceInvalidReconcileFailed        Reason = "allowance_invalid_reconcile_failed"
	ReasonAllowanceInvalidReconcileLostLease     Reason = "allowance_invalid_reconcile_lost_lease"
	ReasonAllowanceInvalidRecordContradiction    Reason = "allowance_invalid_record_contradiction"
	ReasonAllowanceInvalidRecordFailed           Reason = "allowance_invalid_record_failed"
	ReasonAllowanceInvalidRecordLostLease        Reason = "allowance_invalid_record_lost_lease"
	ReasonAllowanceKnownDocumentDuplicate        Reason = "allowance_known_document_duplicate"
	ReasonAllowanceKnownDocumentMalformed        Reason = "allowance_known_document_malformed"
	ReasonAllowanceKnownDocumentMismatch         Reason = "allowance_known_document_mismatch"
	ReasonAllowanceKnownDocumentStatusUnknown    Reason = "allowance_known_document_status_unknown"
	ReasonAllowanceKnownFactsFailed              Reason = "allowance_known_facts_failed"
	ReasonAllowanceLocalSettlementFailed         Reason = "allowance_local_settlement_failed"
	ReasonAllowanceLookupFailed                  Reason = "allowance_lookup_failed"
	ReasonAllowanceLookupMismatch                Reason = "allowance_lookup_mismatch"
	ReasonAllowanceMultipleUnknownCandidates     Reason = "allowance_multiple_unknown_candidates"
	ReasonAllowanceProviderReactivated           Reason = "allowance_provider_reactivated"
	ReasonAllowanceSendAmbiguous                 Reason = "allowance_send_ambiguous"
	ReasonAllowanceSuccessMismatch               Reason = "allowance_success_mismatch"
)

// The reasons of a void operation.
const (
	ReasonVoidLocalSettlementFailed   Reason = "void_local_settlement_failed"
	ReasonVoidLookupFailed            Reason = "void_lookup_failed"
	ReasonVoidLookupMismatch          Reason = "void_lookup_mismatch"
	ReasonVoidProviderRejected        Reason = "void_provider_rejected"
	ReasonVoidSendNeedsLookup         Reason = "void_send_needs_lookup"
	ReasonVoidSuccessIdentityMismatch Reason = "void_success_identity_mismatch"
)

// The reasons which belong to no one kind of operation.
const (
	ReasonFrozenAllowanceRequestInvalid Reason = "frozen_allowance_request_invalid"
	ReasonFrozenIssueRequestInvalid     Reason = "frozen_issue_request_invalid"
	ReasonFrozenVoidRequestInvalid      Reason = "frozen_void_request_invalid"
	ReasonUnknownOperationKind          Reason = "unknown_operation_kind"
)

// The reasons the database writes itself, in the functions which authorize an
// allowance resend and settle a provider invalidation (migrations/001), and one
// the issuer no longer writes. Their values are fixed by those functions, and
// the resend authorization still reads ReasonAllowanceNotYetVisible, which rows
// written by earlier versions may carry.
const (
	ReasonAllowanceResendAuthorized        Reason = "allowance_resend_authorized"
	ReasonAllowanceProviderInvalidRefrozen Reason = "allowance_provider_invalid_refrozen"
	ReasonAllowanceProviderInvalid         Reason = "allowance_provider_invalid"
	ReasonAllowanceNotYetVisible           Reason = "allowance_not_yet_visible"
)

const (
	issueProviderRejectedPrefix     = "issue_provider_rejected_"
	allowanceProviderRejectedPrefix = "allowance_provider_rejected_"
)

// IssueProviderRejected is the reason of an issue which ECPay answered with
// this error code. The code is part of the stored text, so the set of these is
// open and Reasons does not list it.
func IssueProviderRejected(code int) Reason {
	return Reason(issueProviderRejectedPrefix + strconv.Itoa(code))
}

// AllowanceProviderRejected is the reason of an allowance which ECPay answered
// with this error code; see IssueProviderRejected.
func AllowanceProviderRejected(code int) Reason {
	return Reason(allowanceProviderRejectedPrefix + strconv.Itoa(code))
}

// ProviderRejected reports whether r is one of the open family which names an
// ECPay error code.
func (r Reason) ProviderRejected() bool {
	return strings.HasPrefix(string(r), issueProviderRejectedPrefix) ||
		strings.HasPrefix(string(r), allowanceProviderRejectedPrefix)
}

var allReasons = [...]Reason{
	ReasonIssueLocalSettlementFailed,
	ReasonIssueLookupFailed,
	ReasonIssueLookupMismatch,
	ReasonIssueNotYetVisible,
	ReasonIssuePostSendLookupFailed,
	ReasonIssueSendAmbiguous,
	ReasonIssueSentBeforeCancellation,
	ReasonIssueWithdrawnOrderCancelled,
	ReasonAllowanceAmountStillHeld,
	ReasonAllowanceAwaitingBuyer,
	ReasonAllowanceBuyerUnconfirmed,
	ReasonAllowanceCandidateWithoutSendEvidence,
	ReasonAllowanceConsentEncodingFailed,
	ReasonAllowanceInvalidAfterCurrentSend,
	ReasonAllowanceInvalidReconcileContradiction,
	ReasonAllowanceInvalidReconcileFailed,
	ReasonAllowanceInvalidReconcileLostLease,
	ReasonAllowanceInvalidRecordContradiction,
	ReasonAllowanceInvalidRecordFailed,
	ReasonAllowanceInvalidRecordLostLease,
	ReasonAllowanceKnownDocumentDuplicate,
	ReasonAllowanceKnownDocumentMalformed,
	ReasonAllowanceKnownDocumentMismatch,
	ReasonAllowanceKnownDocumentStatusUnknown,
	ReasonAllowanceKnownFactsFailed,
	ReasonAllowanceLocalSettlementFailed,
	ReasonAllowanceLookupFailed,
	ReasonAllowanceLookupMismatch,
	ReasonAllowanceMultipleUnknownCandidates,
	ReasonAllowanceProviderReactivated,
	ReasonAllowanceSendAmbiguous,
	ReasonAllowanceSuccessMismatch,
	ReasonVoidLocalSettlementFailed,
	ReasonVoidLookupFailed,
	ReasonVoidLookupMismatch,
	ReasonVoidProviderRejected,
	ReasonVoidSendNeedsLookup,
	ReasonVoidSuccessIdentityMismatch,
	ReasonFrozenAllowanceRequestInvalid,
	ReasonFrozenIssueRequestInvalid,
	ReasonFrozenVoidRequestInvalid,
	ReasonUnknownOperationKind,
	ReasonAllowanceResendAuthorized,
	ReasonAllowanceProviderInvalidRefrozen,
	ReasonAllowanceProviderInvalid,
	ReasonAllowanceNotYetVisible,
}

// Reasons is every fixed reason an operation can carry, as constants. It leaves
// out the open family IssueProviderRejected and AllowanceProviderRejected build.
func Reasons() []Reason { return slices.Clone(allReasons[:]) }

// ParseReason reads a stored last_error back as a Reason. It reports false for
// text which is neither a fixed reason nor a provider rejection, such as the
// empty text of an operation which has none.
func ParseReason(stored string) (Reason, bool) {
	r := Reason(stored)
	return r, r.ProviderRejected() || slices.Contains(allReasons[:], r)
}
