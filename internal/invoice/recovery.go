package invoice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"
)

type frozenRequest struct {
	RelateNumber  string     `json:"relate_number"`
	InvoiceNumber string     `json:"invoice_number"`
	InvoiceDate   string     `json:"invoice_date"`
	RandomNumber  string     `json:"random_number"`
	Reason        string     `json:"reason"`
	CustomerName  string     `json:"customer_name"`
	Email         string     `json:"email"`
	Preference    Preference `json:"preference"`
	CarrierCode   string     `json:"carrier_code"`
	TaxID         string     `json:"tax_id"`
	AmountCents   int64      `json:"amount_cents"`
	Lines         []Line     `json:"lines"`
}

type operation struct {
	ID                   uuid.UUID
	OrderID              uuid.UUID
	Kind                 string
	TargetDocumentID     uuid.UUID
	ResultDocumentID     uuid.UUID
	ProviderKey          string
	AmountCents          int64
	Request              frozenRequest
	Status               string
	Reconciles           int32
	Sends                int32
	ResendAuthorizations int32
	LastError            string
}

func (s *Store) operation(ctx context.Context, id uuid.UUID) (operation, error) {
	row, err := s.q.InvoiceOperation(ctx, id)
	if err != nil {
		return operation{}, err
	}
	var request frozenRequest
	if err := json.Unmarshal(row.RequestPayload, &request); err != nil {
		return operation{}, fmt.Errorf("decode frozen invoice operation %s: %w", id, err)
	}
	return operation{
		ID: row.ID, OrderID: row.OrderID, Kind: row.Kind,
		TargetDocumentID: row.TargetDocumentID, ResultDocumentID: row.ResultDocumentID,
		ProviderKey: row.ProviderKey, AmountCents: row.AmountCents, Request: request,
		Status: row.Status, Reconciles: row.ReconcileAttempts,
		Sends: row.SendAttempts, ResendAuthorizations: row.ResendAuthorizations,
		LastError: row.LastError,
	}, nil
}

func (s *Store) processClaim(ctx context.Context, operationID uuid.UUID) (Document, error) {
	op, err := s.operation(ctx, operationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Document{}, ErrNotFound
		}
		return Document{}, fmt.Errorf("read invoice operation %s: %w", operationID, err)
	}
	switch op.Status {
	case "succeeded":
		return s.operationDocument(ctx, operationID)
	case "rejected":
		return Document{}, fmt.Errorf("%w: operation %s was rejected (%s)",
			ErrRejected, operationID, op.LastError)
	case "attention":
		return Document{}, fmt.Errorf("%w: operation %s needs reconciliation (%s)",
			ErrPending, operationID, op.LastError)
	}

	owner := uuid.New()
	leased, err := s.q.LeaseInvoiceOperation(ctx, db.LeaseInvoiceOperationParams{
		OperationID: operationID, LeaseOwner: owner, LeaseFor: interval(operationLease),
	})
	if err != nil {
		return Document{}, fmt.Errorf("lease invoice operation %s: %w", operationID, err)
	}
	if leased == uuid.Nil {
		return Document{}, fmt.Errorf("%w: operation %s is already being reconciled",
			ErrPending, operationID)
	}
	return s.processLeased(ctx, leased, owner)
}

func (s *Store) processLeased(ctx context.Context, operationID, owner uuid.UUID) (Document, error) {
	op, err := s.operation(ctx, operationID)
	if err != nil {
		return Document{}, err
	}
	switch op.Kind {
	case "issue":
		return s.processIssue(ctx, &op, owner)
	case "allowance":
		return s.processAllowance(ctx, &op, owner)
	case "void":
		return s.processVoid(ctx, &op, owner)
	default:
		cause := fmt.Errorf("invoice operation %s has unknown kind %q", op.ID, op.Kind)
		return Document{}, s.alarm(ctx, &op, owner, "unknown_operation_kind", cause)
	}
}

func (s *Store) processIssue(ctx context.Context, op *operation, owner uuid.UUID) (Document, error) {
	in := IssueRequest{
		OrderNumber: op.Request.RelateNumber, CustomerName: op.Request.CustomerName,
		Email: op.Request.Email, Preference: op.Request.Preference,
		CarrierCode: op.Request.CarrierCode, TaxID: op.Request.TaxID,
		AmountCents: op.Request.AmountCents, Lines: op.Request.Lines,
	}
	if err := in.validate(); err != nil || op.ProviderKey != in.OrderNumber ||
		op.AmountCents != in.AmountCents {
		cause := fmt.Errorf("%w: frozen Issue operation is invalid", ErrRejected)
		return Document{}, s.alarm(ctx, op, owner, "frozen_issue_request_invalid", cause)
	}

	lookup, found, lookupErr := s.gateway.FetchIssue(ctx, op.ProviderKey)
	if lookupErr != nil {
		return Document{}, s.retry(ctx, op, owner, "issue_lookup_failed", lookupErr)
	}
	if found {
		if !issueMatches(in, lookup, false) {
			cause := fmt.Errorf("%w: provider Issue differs from frozen request", ErrPending)
			return Document{}, s.alarm(ctx, op, owner, "issue_lookup_mismatch", cause)
		}
		return s.settleIssue(ctx, op, owner, lookup.Document)
	}

	if markErr := s.markSent(ctx, op, owner); markErr != nil {
		return Document{}, markErr
	}
	_, sendErr := s.gateway.Issue(ctx, in)
	if sendErr != nil {
		if providerErr, ok := errors.AsType[*providerError](sendErr); ok && providerErr.Code != 5070357 {
			category := "issue_provider_rejected_" + strconv.Itoa(providerErr.Code)
			return Document{}, s.reject(ctx, op, owner, category, sendErr)
		}
		return Document{}, s.retry(ctx, op, owner, "issue_send_ambiguous", sendErr)
	}
	// Issue's success reply has no itemisation. Never infer provider truth from a
	// header-only response; FetchIssue is the settlement authority.
	lookup, found, lookupErr = s.gateway.FetchIssue(ctx, op.ProviderKey)
	if lookupErr != nil {
		return Document{}, s.retry(ctx, op, owner, "issue_post_send_lookup_failed", lookupErr)
	}
	if !found {
		return Document{}, s.retry(ctx, op, owner, "issue_not_yet_visible", ErrPending)
	}
	if !issueMatches(in, lookup, false) {
		cause := fmt.Errorf("%w: provider Issue differs from frozen request", ErrPending)
		return Document{}, s.alarm(ctx, op, owner, "issue_lookup_mismatch", cause)
	}
	return s.settleIssue(ctx, op, owner, lookup.Document)
}

func (s *Store) processAllowance(ctx context.Context, op *operation, owner uuid.UUID) (Document, error) {
	in, valid := frozenAllowanceRequest(op)
	if !valid {
		cause := fmt.Errorf("%w: frozen Allowance operation is invalid", ErrRejected)
		return Document{}, s.alarm(ctx, op, owner, "frozen_allowance_request_invalid", cause)
	}
	all, err := s.gateway.FetchAllowances(ctx, in.InvoiceNumber, in.InvoiceDate)
	if err != nil {
		return Document{}, s.retry(ctx, op, owner, "allowance_lookup_failed", err)
	}
	knownRows, err := s.q.KnownAllowances(ctx, op.TargetDocumentID)
	if err != nil {
		return Document{}, s.retry(ctx, op, owner, "allowance_known_facts_failed", err)
	}
	known, invalidCategory := indexKnownAllowances(knownRows)
	if invalidCategory != "" {
		cause := fmt.Errorf("%w: local Allowance facts are invalid", ErrPending)
		return Document{}, s.alarm(ctx, op, owner, invalidCategory, cause)
	}
	unknown, err := s.unresolvedAllowances(ctx, op, owner, in.InvoiceNumber, all, known)
	if err != nil {
		return Document{}, err
	}
	switch len(unknown) {
	case 0:
		return s.sendAllowance(ctx, op, owner, in)
	case 1:
		return s.processAllowanceCandidate(ctx, op, owner, in, unknown[0])
	default:
		cause := fmt.Errorf("%w: multiple unknown provider allowances", ErrPending)
		return Document{}, s.alarm(ctx, op, owner, "allowance_multiple_unknown_candidates", cause)
	}
}

func frozenAllowanceRequest(op *operation) (request AllowanceRequest, valid bool) {
	issuedAt, err := time.Parse("2006-01-02", op.Request.InvoiceDate)
	if err != nil || op.ProviderKey != op.Request.InvoiceNumber ||
		op.AmountCents != op.Request.AmountCents || len(op.Request.Lines) != 1 {
		return AllowanceRequest{}, false
	}
	return AllowanceRequest{
		InvoiceNumber: op.Request.InvoiceNumber, InvoiceDate: issuedAt,
		CustomerName: op.Request.CustomerName, Email: op.Request.Email,
		AmountCents: op.Request.AmountCents, Lines: op.Request.Lines,
	}, true
}

func indexKnownAllowances(
	rows []db.KnownAllowancesRow,
) (known map[string]knownAllowance, invalidCategory string) {
	known = make(map[string]knownAllowance, len(rows))
	for i := range rows {
		row := &rows[i]
		lines, ok := knownAllowanceLines(
			row.Descriptions, row.Quantities, row.UnitPriceCents,
			row.LineAmountCents, row.TaxTypes,
		)
		if !ok {
			return nil, "allowance_known_document_malformed"
		}
		if _, duplicate := known[row.Number]; duplicate {
			return nil, "allowance_known_document_duplicate"
		}
		known[row.Number] = knownAllowance{
			ID: row.ID, Number: row.Number, AmountCents: row.AmountCents,
			Status: row.Status, IssuedAt: row.IssuedAt, Lines: lines,
		}
	}
	return known, ""
}

func (s *Store) unresolvedAllowances(
	ctx context.Context,
	op *operation,
	owner uuid.UUID,
	invoiceNumber string,
	all []AllowanceLookup,
	known map[string]knownAllowance,
) ([]AllowanceLookup, error) {
	unknown := make([]AllowanceLookup, 0, len(all))
	for i := range all {
		remote := all[i]
		local, exists := known[remote.Document.Number]
		if !exists {
			unknown = append(unknown, remote)
			continue
		}
		if !allowanceKnownFactsMatch(invoiceNumber, local, remote) {
			cause := fmt.Errorf("%w: provider Allowance differs from known local facts", ErrPending)
			return nil, s.alarm(ctx, op, owner, "allowance_known_document_mismatch", cause)
		}
		if err := s.reconcileKnownAllowance(ctx, op, owner, local, remote); err != nil {
			return nil, err
		}
	}
	return unknown, nil
}

func (s *Store) reconcileKnownAllowance(
	ctx context.Context,
	op *operation,
	owner uuid.UUID,
	local knownAllowance,
	remote AllowanceLookup,
) error {
	switch local.Status {
	case "issued":
		if !remote.Invalid {
			return nil
		}
		if op.Sends > 0 {
			cause := fmt.Errorf(
				"%w: provider invalidation would change a sent Allowance basis", ErrPending)
			return s.alarm(ctx, op, owner, "allowance_invalid_after_current_send", cause)
		}
		return s.reconcileKnownInvalidAllowance(ctx, op, owner, local, remote)
	case "voided":
		if remote.Invalid {
			return nil
		}
		cause := fmt.Errorf("%w: provider reports a locally voided Allowance active", ErrPending)
		return s.alarm(ctx, op, owner, "allowance_provider_reactivated", cause)
	default:
		cause := fmt.Errorf("%w: unknown local Allowance status", ErrPending)
		return s.alarm(ctx, op, owner, "allowance_known_document_status_unknown", cause)
	}
}

func (s *Store) processAllowanceCandidate(
	ctx context.Context,
	op *operation,
	owner uuid.UUID,
	in AllowanceRequest,
	remote AllowanceLookup,
) (Document, error) {
	if op.Sends == 0 {
		// The pre-send stamp is the attribution boundary. Without it, this
		// candidate predates (or was created outside) this operation and must
		// never be filed under the current actor/request merely because its
		// amount happens to match.
		cause := fmt.Errorf("%w: provider Allowance has no send attribution", ErrPending)
		return Document{}, s.alarm(
			ctx, op, owner, "allowance_candidate_without_send_evidence", cause,
		)
	}
	if !allowanceRequestFactsMatch(in, remote) {
		cause := fmt.Errorf("%w: provider Allowance differs from frozen request", ErrPending)
		return Document{}, s.alarm(ctx, op, owner, "allowance_lookup_mismatch", cause)
	}
	if remote.Invalid {
		return Document{}, s.recordUnknownInvalidAllowance(ctx, op, owner, remote)
	}
	return s.settleAllowance(ctx, op, owner, remote.Document)
}

func (s *Store) sendAllowance(
	ctx context.Context,
	op *operation,
	owner uuid.UUID,
	in AllowanceRequest,
) (Document, error) {
	if op.Sends > op.ResendAuthorizations {
		// The list can lag a successful write. An empty authoritative read is not
		// proof that the earlier ambiguous send created nothing, so keep polling
		// this same frozen operation. A fresh audited operator authorization is
		// the only state which permits one more provider call.
		return Document{}, s.retry(ctx, op, owner,
			"allowance_not_yet_visible", ErrPending)
	}

	if markErr := s.markSent(ctx, op, owner); markErr != nil {
		return Document{}, markErr
	}
	doc, sendErr := s.gateway.Allowance(ctx, in)
	if sendErr != nil {
		if providerErr, ok := errors.AsType[*providerError](sendErr); ok {
			category := "allowance_provider_rejected_" + strconv.Itoa(providerErr.Code)
			return Document{}, s.reject(ctx, op, owner, category, sendErr)
		}
		return Document{}, s.retry(ctx, op, owner, "allowance_send_ambiguous", sendErr)
	}
	if doc.AmountCents != in.AmountCents || !slices.Equal(doc.Lines, in.Lines) {
		cause := fmt.Errorf("%w: provider Allowance response is inconsistent", ErrPending)
		return Document{}, s.alarm(ctx, op, owner, "allowance_success_mismatch", cause)
	}
	return s.settleAllowance(ctx, op, owner, doc)
}

func (s *Store) reconcileKnownInvalidAllowance(
	ctx context.Context,
	op *operation,
	owner uuid.UUID,
	local knownAllowance,
	remote AllowanceLookup,
) error {
	ctx, cancel := filingContext(ctx)
	defer cancel()
	descriptions, quantities, unitPrices, amounts := invoiceLineArrays(remote.Document.Lines)
	refrozen, err := s.q.ReconcileInvalidInvoiceAllowance(
		ctx, db.ReconcileInvalidInvoiceAllowanceParams{
			OperationID: op.ID, LeaseOwner: owner, DocumentID: local.ID,
			InvoiceNumber: remote.InvoiceNumber, AllowanceNumber: remote.Document.Number,
			IssuedAt: remote.Document.IssuedAt, AmountCents: remote.Document.AmountCents,
			Descriptions: descriptions, Quantities: quantities,
			UnitPriceCents: unitPrices, LineAmountCents: amounts,
		},
	)
	if err != nil {
		switch constraintName(err) {
		case "invoice_allowance_invalidation_original",
			"invoice_allowance_invalidation_header",
			"invoice_allowance_invalidation_lines",
			"invoice_allowance_invalidation_amount":
			cause := fmt.Errorf("%w: provider invalidation contradicts local allowance facts",
				ErrPending)
			return s.alarm(ctx, op, owner, "allowance_invalid_reconcile_contradiction", cause)
		default:
			return s.retry(ctx, op, owner, "allowance_invalid_reconcile_failed", err)
		}
	}
	if refrozen <= 0 {
		return s.retry(ctx, op, owner, "allowance_invalid_reconcile_lost_lease", ErrPending)
	}
	// The door already released this same operation due-now with its refrozen
	// amount. Returning pending keeps this pass from sending a different request
	// than the one loaded before the atomic state transition.
	return fmt.Errorf("%w: provider-invalid Allowance refrozen at %d cents",
		ErrPending, refrozen)
}

func (s *Store) recordUnknownInvalidAllowance(
	ctx context.Context,
	op *operation,
	owner uuid.UUID,
	remote AllowanceLookup,
) error {
	ctx, cancel := filingContext(ctx)
	defer cancel()
	descriptions, quantities, unitPrices, amounts := invoiceLineArrays(remote.Document.Lines)
	documentID, err := s.q.RecordInvalidInvoiceAllowance(
		ctx, db.RecordInvalidInvoiceAllowanceParams{
			OperationID: op.ID, LeaseOwner: owner,
			InvoiceNumber: remote.InvoiceNumber, AllowanceNumber: remote.Document.Number,
			IssuedAt: remote.Document.IssuedAt, AmountCents: remote.Document.AmountCents,
			Descriptions: descriptions, Quantities: quantities,
			UnitPriceCents: unitPrices, LineAmountCents: amounts,
		},
	)
	if err != nil {
		if constraintName(err) != "" {
			cause := fmt.Errorf("%w: invalid provider Allowance contradicts local facts",
				ErrPending)
			return s.alarm(ctx, op, owner, "allowance_invalid_record_contradiction", cause)
		}
		return s.retry(ctx, op, owner, "allowance_invalid_record_failed", err)
	}
	if documentID == uuid.Nil {
		return s.retry(ctx, op, owner, "allowance_invalid_record_lost_lease", ErrPending)
	}
	return fmt.Errorf("%w: provider Allowance %s was authoritatively invalidated",
		ErrRejected, remote.Document.Number)
}

func (s *Store) processVoid(ctx context.Context, op *operation, owner uuid.UUID) (Document, error) {
	issuedAt, valid := frozenVoidDate(op)
	if !valid {
		cause := fmt.Errorf("%w: frozen Void operation is invalid", ErrRejected)
		return Document{}, s.alarm(ctx, op, owner, "frozen_void_request_invalid", cause)
	}
	lookup, found, err := s.gateway.FetchIssue(ctx, op.Request.RelateNumber)
	if err != nil {
		return Document{}, s.retry(ctx, op, owner, "void_lookup_failed", err)
	}
	expected := IssueRequest{
		OrderNumber: op.Request.RelateNumber, AmountCents: op.Request.AmountCents,
		Lines: op.Request.Lines,
	}
	if !voidLookupMatches(op, expected, lookup, found) {
		cause := fmt.Errorf("%w: provider invoice cannot be matched to Void operation", ErrPending)
		return Document{}, s.alarm(ctx, op, owner, "void_lookup_mismatch", cause)
	}
	if lookup.Invalid {
		return s.settleVoid(ctx, op, owner)
	}
	// Invalid has no request idempotency key, but repeating the exact operation
	// cannot create a second tax document: it only drives this one invoice toward
	// the same invalid state. That makes retrying after mark-before-call crash
	// safe. Every pass first reads FetchIssue above, so an already propagated call
	// settles without another send.
	if markErr := s.markSent(ctx, op, owner); markErr != nil {
		return Document{}, markErr
	}
	if sendErr := s.gateway.Void(
		ctx, op.Request.InvoiceNumber, issuedAt, op.Request.Reason,
	); sendErr != nil {
		return Document{}, s.handleVoidSendError(ctx, op, owner, sendErr)
	}
	return s.settleVoid(ctx, op, owner)
}

func frozenVoidDate(op *operation) (issuedAt time.Time, valid bool) {
	issuedAt, err := time.Parse("2006-01-02", op.Request.InvoiceDate)
	if err != nil || op.ProviderKey != op.Request.InvoiceNumber ||
		op.Request.RelateNumber == "" || strings.TrimSpace(op.Request.Reason) == "" {
		return time.Time{}, false
	}
	return issuedAt, true
}

func voidLookupMatches(
	op *operation, expected IssueRequest, lookup IssueLookup, found bool,
) bool {
	return found && issueMatches(expected, lookup, true) &&
		lookup.Document.Number == op.Request.InvoiceNumber &&
		lookup.Document.ProviderRef == op.Request.RandomNumber
}

func (s *Store) handleVoidSendError(
	ctx context.Context, op *operation, owner uuid.UUID, cause error,
) error {
	if errors.Is(cause, errProviderIdentity) {
		mismatch := fmt.Errorf("%w: Invalid success identity differs from frozen request",
			ErrPending)
		return s.alarm(ctx, op, owner, "void_success_identity_mismatch", mismatch)
	}
	if providerErr, ok := errors.AsType[*providerError](cause); ok &&
		providerErr != nil && op.Sends == 0 {
		// This was the first provider call and ECPay returned a decrypted,
		// operation-level failure. No earlier ambiguous send can still surface,
		// so release the claim for a corrected, newly attributed operation.
		return s.reject(ctx, op, owner, "void_provider_rejected", cause)
	}
	// Transport/envelope/decode failures do not say whether Invalid ran. If a
	// prior send was ambiguous, even a later provider rejection can be racing
	// its propagation; retain this operation and reconcile provider truth.
	return s.retry(ctx, op, owner, "void_send_needs_lookup", cause)
}

func (s *Store) operationDocument(ctx context.Context, operationID uuid.UUID) (Document, error) {
	row, err := s.q.InvoiceOperationDocument(ctx, operationID)
	if err != nil {
		return Document{}, err
	}
	return Document{
		ID: row.ID.String(), Kind: row.Kind, Number: row.Number,
		AmountCents: row.AmountCents, Status: row.Status,
		ProviderRef: row.ProviderRef, IssuedAt: row.IssuedAt,
	}, nil
}

func (s *Store) settleIssue(
	ctx context.Context, op *operation, owner uuid.UUID, doc Document,
) (Document, error) {
	ctx, cancel := filingContext(ctx)
	defer cancel()
	descriptions, quantities, unitPrices, amounts := invoiceLineArrays(doc.Lines)
	documentID, err := s.q.SettleInvoiceIssue(ctx, db.SettleInvoiceIssueParams{
		OperationID: op.ID, LeaseOwner: owner, Number: doc.Number,
		RandomNumber: doc.ProviderRef, IssuedAt: doc.IssuedAt,
		Descriptions: descriptions, Quantities: quantities,
		UnitPriceCents: unitPrices, AmountCents: amounts,
	})
	if err != nil || documentID == uuid.Nil {
		return Document{}, s.retry(ctx, op, owner, "issue_local_settlement_failed", err)
	}
	doc.ID = documentID.String()
	return doc, nil
}

func (s *Store) settleAllowance(
	ctx context.Context, op *operation, owner uuid.UUID, doc Document,
) (Document, error) {
	ctx, cancel := filingContext(ctx)
	defer cancel()
	descriptions, quantities, unitPrices, amounts := invoiceLineArrays(doc.Lines)
	documentID, err := s.q.SettleInvoiceAllowance(ctx, db.SettleInvoiceAllowanceParams{
		OperationID: op.ID, LeaseOwner: owner, Number: doc.Number,
		IssuedAt: doc.IssuedAt, Descriptions: descriptions, Quantities: quantities,
		UnitPriceCents: unitPrices, AmountCents: amounts,
	})
	if err != nil || documentID == uuid.Nil {
		return Document{}, s.retry(ctx, op, owner, "allowance_local_settlement_failed", err)
	}
	doc.ID = documentID.String()
	return doc, nil
}

func (s *Store) settleVoid(ctx context.Context, op *operation, owner uuid.UUID) (Document, error) {
	ctx, cancel := filingContext(ctx)
	defer cancel()
	documentID, err := s.q.SettleInvoiceVoid(ctx, db.SettleInvoiceVoidParams{
		OperationID: op.ID, LeaseOwner: owner,
	})
	if err != nil || documentID == uuid.Nil {
		return Document{}, s.retry(ctx, op, owner, "void_local_settlement_failed", err)
	}
	return s.operationDocument(ctx, op.ID)
}

func (s *Store) markSent(ctx context.Context, op *operation, owner uuid.UUID) error {
	marked, err := s.q.MarkInvoiceOperationSent(ctx, db.MarkInvoiceOperationSentParams{
		OperationID: op.ID, LeaseOwner: owner,
	})
	if err != nil {
		return fmt.Errorf("stamp invoice operation %s before provider call: %w", op.ID, err)
	}
	if !marked {
		return fmt.Errorf("%w: lease for invoice operation %s was lost", ErrPending, op.ID)
	}
	return nil
}

func (s *Store) retry(
	ctx context.Context, op *operation, owner uuid.UUID, category string, cause error,
) error {
	ctx, cancel := filingContext(ctx)
	defer cancel()
	// Provider outages and propagation lag stay pending under a capped backoff
	// and never exhaust: only explicit identity/fact mismatches enter the
	// terminal attention state.
	rescheduled, err := s.q.RescheduleInvoiceOperation(ctx, db.RescheduleInvoiceOperationParams{
		OperationID: op.ID, LeaseOwner: owner, LastError: category,
		Backoff: interval(reconcileBackoff(op.Reconciles)),
	})
	if cause == nil {
		cause = ErrPending
	}
	pending := fmt.Errorf("%w: %s: %w", ErrPending, category, cause)
	if err != nil {
		return errors.Join(pending,
			fmt.Errorf("reschedule invoice operation %s: %w", op.ID, err))
	}
	if !rescheduled {
		return errors.Join(pending,
			fmt.Errorf("%w: lease for invoice operation %s was lost while rescheduling",
				ErrPending, op.ID))
	}
	return pending
}

func (s *Store) alarm(
	ctx context.Context, op *operation, owner uuid.UUID, category string, cause error,
) error {
	ctx, cancel := filingContext(ctx)
	defer cancel()
	alarmed, err := s.q.AlarmInvoiceOperation(ctx, db.AlarmInvoiceOperationParams{
		OperationID: op.ID, LeaseOwner: owner, LastError: category,
	})
	if err != nil {
		return errors.Join(cause, fmt.Errorf("alarm invoice operation %s: %w", op.ID, err))
	}
	if !alarmed {
		return errors.Join(cause,
			fmt.Errorf("%w: lease for invoice operation %s was lost while alarming",
				ErrPending, op.ID))
	}
	return cause
}

func (s *Store) reject(
	ctx context.Context, op *operation, owner uuid.UUID, category string, cause error,
) error {
	ctx, cancel := filingContext(ctx)
	defer cancel()
	rejected, err := s.q.RejectInvoiceOperation(ctx, db.RejectInvoiceOperationParams{
		OperationID: op.ID, LeaseOwner: owner, LastError: category,
	})
	if err != nil {
		return errors.Join(cause, fmt.Errorf("reject invoice operation %s: %w", op.ID, err))
	}
	if !rejected {
		return errors.Join(cause,
			fmt.Errorf("%w: lease for invoice operation %s was lost while rejecting",
				ErrPending, op.ID))
	}
	return cause
}

// ReconcileResult contains only non-PII operator coordinates. Category is a
// local allowlisted label persisted by the state machine, never provider text.
type ReconcileResult struct {
	Worked      bool
	OperationID uuid.UUID
	OrderID     uuid.UUID
	Category    string
}

// ReconcileOnce safely claims and processes one due operation across replicas.
func (s *Store) ReconcileOnce(ctx context.Context) (ReconcileResult, error) {
	owner := uuid.New()
	operationID, err := s.q.LeaseInvoiceOperation(ctx, db.LeaseInvoiceOperationParams{
		OperationID: uuid.Nil, LeaseOwner: owner, LeaseFor: interval(operationLease),
	})
	if err != nil || operationID == uuid.Nil {
		return ReconcileResult{}, err
	}
	result := ReconcileResult{Worked: true, OperationID: operationID}
	if op, loadErr := s.operation(ctx, operationID); loadErr == nil {
		result.OrderID = op.OrderID
	}
	_, err = s.processLeased(ctx, operationID, owner)
	if err != nil {
		if op, loadErr := s.operation(context.WithoutCancel(ctx), operationID); loadErr == nil {
			result.OrderID = op.OrderID
			result.Category = op.LastError
		}
		if result.Category == "" {
			result.Category = "operation_failed"
		}
	}
	return result, err
}

// ReconcileForever polls durable invoice operations until shutdown. Logs carry
// only local categories, never frozen PII, provider payloads or credentials.
func (s *Store) ReconcileForever(ctx context.Context, log *slog.Logger) {
	if log == nil {
		panic("invoice: ReconcileForever requires a logger")
	}
	ticker := time.NewTicker(reconcilePoll)
	defer ticker.Stop()
	for {
		result, err := s.ReconcileOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			log.WarnContext(ctx, "invoice reconciliation deferred",
				"operation", result.OperationID, "order", result.OrderID,
				"category", result.Category)
		}
		if result.Worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func issueMatches(expected IssueRequest, got IssueLookup, allowInvalid bool) bool {
	return got.RelateNumber == expected.OrderNumber &&
		(got.Issued || (allowInvalid && got.Invalid)) &&
		(allowInvalid || !got.Invalid) &&
		got.Document.AmountCents == expected.AmountCents &&
		slices.Equal(got.Document.Lines, expected.Lines)
}

func interval(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: d.Microseconds(), Valid: true}
}

func reconcileBackoff(attempt int32) time.Duration {
	shift := min(max(attempt-1, 0), 6)
	return min(15*time.Second*time.Duration(1<<shift), 15*time.Minute)
}

func constraintName(err error) string {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return ""
	}
	return pgErr.ConstraintName
}
