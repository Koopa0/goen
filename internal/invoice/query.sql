-- Claim and freeze a request before any ECPay call. Replaying a still-active
-- operation returns the same id and never rebuilds the request from mutable PII.
-- name: ClaimInvoiceIssue :one
SELECT claim_invoice_issue(
    @order_number::text, @actor_user_id::uuid, @request_id::text
)::uuid AS operation_id;

-- Every document filed against one order, newest first.
-- name: InvoiceDocuments :many
SELECT d.id, d.kind, d.number, d.amount_cents, d.status,
       coalesce(d.provider_ref, '')::text AS provider_ref,
       d.issued_at, d.voided_at
FROM invoice_documents d
WHERE d.order_id = (SELECT id FROM orders WHERE order_number = @order_number::text)
ORDER BY d.issued_at DESC, d.id DESC;

-- What one filed document says was sold; a shop reconciling an invoice against
-- an order compares lines rather than totals.
-- name: InvoiceDocumentLines :many
SELECT l.document_id, l.id, l.description, l.quantity, l.unit_price_cents,
       l.amount_cents, l.tax_type
FROM invoice_document_lines l
WHERE l.document_id = ANY(@document_ids::uuid[])
ORDER BY l.document_id, l.position, l.id;

-- The live invoice of an order, if it has one. `status <> 'voided'` matches
-- invoice_documents_one_active_invoice_per_order, so the row is unique.
-- name: LiveInvoice :one
SELECT d.id, d.number, d.amount_cents, coalesce(d.provider_ref, '')::text AS provider_ref,
       d.issued_at
FROM invoice_documents d
JOIN orders o ON o.id = d.order_id
WHERE o.order_number = @order_number::text
  AND d.kind = 'invoice' AND d.status <> 'voided';

-- name: ClaimInvoiceAllowance :one
SELECT claim_invoice_allowance(
    @original_id::uuid,
    @operation_id::uuid,
    @actor_user_id::uuid,
    @request_id::text
)::uuid AS operation_id;

-- name: ClaimInvoiceVoid :one
SELECT claim_invoice_void(
    @document_id::uuid, @reason::text, @actor_user_id::uuid, @request_id::text
)::uuid AS operation_id;

-- One durable operation and its frozen request. Nil target/result UUIDs avoid a
-- nullable UUID at the Go state-machine boundary; kind/status say which applies.
-- name: InvoiceOperation :one
SELECT op.id, op.order_id, op.kind,
       coalesce(op.target_document_id,
                '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS target_document_id,
       coalesce(op.result_document_id,
                '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS result_document_id,
       op.provider_key, op.amount_cents, op.request_payload,
       op.actor_id_snapshot, op.request_id, op.status,
       op.reconcile_attempts, op.send_attempts, op.resend_authorizations,
       coalesce(op.last_error, '')::text AS last_error,
       op.created_at, op.updated_at
FROM invoice_operations op
WHERE op.id = @operation_id::uuid;

-- name: InvoiceOperationDocument :one
SELECT d.id, d.kind, d.number, d.amount_cents, d.status,
       coalesce(d.provider_ref, '')::text AS provider_ref, d.issued_at
FROM invoice_operations op
JOIN invoice_documents d ON d.id = op.result_document_id
WHERE op.id = @operation_id::uuid AND op.status = 'succeeded';

-- The exact-id form is used by a request handler; uuid.Nil atomically picks the
-- oldest due operation for a background replica.
-- name: LeaseInvoiceOperation :one
SELECT lease_invoice_operation(
    @operation_id::uuid, @lease_owner::uuid, @lease_for::interval
)::uuid AS operation_id;

-- name: MarkInvoiceOperationSent :one
SELECT mark_invoice_operation_sent(
    @operation_id::uuid, @lease_owner::uuid
)::boolean AS marked;

-- name: RescheduleInvoiceOperation :one
SELECT reschedule_invoice_operation(
    @operation_id::uuid, @lease_owner::uuid, @last_error::text, @backoff::interval
)::boolean AS rescheduled;

-- name: AlarmInvoiceOperation :one
SELECT alarm_invoice_operation(
    @operation_id::uuid, @lease_owner::uuid, @last_error::text
)::boolean AS alarmed;

-- name: RejectInvoiceOperation :one
SELECT reject_invoice_operation(
    @operation_id::uuid, @lease_owner::uuid, @last_error::text
)::boolean AS rejected;

-- name: SettleInvoiceIssue :one
SELECT settle_invoice_issue(
    @operation_id::uuid,
    @lease_owner::uuid,
    @number::text,
    @random_number::text,
    @issued_at,
    @descriptions::text[],
    @quantities::integer[],
    @unit_price_cents::bigint[],
    @amount_cents::bigint[]
)::uuid AS document_id;

-- name: SettleInvoiceAllowance :one
SELECT settle_invoice_allowance(
    @operation_id::uuid,
    @lease_owner::uuid,
    @number::text,
    @issued_at,
    @descriptions::text[],
    @quantities::integer[],
    @unit_price_cents::bigint[],
    @amount_cents::bigint[]
)::uuid AS document_id;

-- name: SettleInvoiceVoid :one
SELECT settle_invoice_void(
    @operation_id::uuid, @lease_owner::uuid
)::uuid AS document_id;

-- Complete immutable local facts for every allowance represented against the
-- original invoice. A provider row is "known" only when its invoice/allowance
-- identity, timestamp, money and every line agree with these facts; comparing
-- the number alone can hide a provider-side invalidation or misattribute a
-- different document to the current operation.
-- name: KnownAllowances :many
SELECT d.id, d.number, d.amount_cents, d.status, d.issued_at,
       ARRAY(SELECT l.description FROM invoice_document_lines l
             WHERE l.document_id = d.id ORDER BY l.position)::text[] AS descriptions,
       ARRAY(SELECT l.quantity FROM invoice_document_lines l
             WHERE l.document_id = d.id ORDER BY l.position)::integer[] AS quantities,
       ARRAY(SELECT l.unit_price_cents FROM invoice_document_lines l
             WHERE l.document_id = d.id ORDER BY l.position)::bigint[] AS unit_price_cents,
       ARRAY(SELECT l.amount_cents FROM invoice_document_lines l
             WHERE l.document_id = d.id ORDER BY l.position)::bigint[] AS line_amount_cents,
       ARRAY(SELECT l.tax_type FROM invoice_document_lines l
             WHERE l.document_id = d.id ORDER BY l.position)::text[] AS tax_types
FROM invoice_documents d
WHERE d.original_id = @original_id::uuid AND d.kind = 'allowance'
ORDER BY d.issued_at, d.id;

-- Atomically accept an exact authoritative provider invalidation, void the old
-- local allowance, and refreeze the still-unsent replacement operation from
-- current settled refunds. The operation id and its audit identity are kept.
-- name: ReconcileInvalidInvoiceAllowance :one
SELECT reconcile_invalid_invoice_allowance(
    @operation_id::uuid,
    @lease_owner::uuid,
    @document_id::uuid,
    @invoice_number::text,
    @allowance_number::text,
    @issued_at,
    @amount_cents::bigint,
    @descriptions::text[],
    @quantities::integer[],
    @unit_price_cents::bigint[],
    @line_amount_cents::bigint[]
)::bigint AS refrozen_amount_cents;

-- A sent operation can be recovered after ECPay issued and then invalidated its
-- document before local settlement. Persist that exact provider history as a
-- voided document, then reject/release the operation so a new claim can derive
-- the still-unrelieved amount.
-- name: RecordInvalidInvoiceAllowance :one
SELECT record_invalid_invoice_allowance(
    @operation_id::uuid,
    @lease_owner::uuid,
    @invoice_number::text,
    @allowance_number::text,
    @issued_at,
    @amount_cents::bigint,
    @descriptions::text[],
    @quantities::integer[],
    @unit_price_cents::bigint[],
    @line_amount_cents::bigint[]
)::uuid AS document_id;
