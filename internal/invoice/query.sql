-- What an order needs to become an invoice. The amount is order_amount_owed's
-- numerator rather than the net: an invoice records the SALE, and store credit
-- is how the customer paid rather than a reduction in what was sold.
-- name: InvoiceSubject :one
SELECT o.id,
       o.order_number,
       coalesce(pd.recipient_name, '')::text AS customer_name,
       coalesce(pd.email, '')::text AS email,
       coalesce(ip.invoice_type, 'member_carrier')::text AS invoice_type,
       coalesce(ip.carrier_code, '')::text AS carrier_code,
       coalesce(ip.tax_id, '')::text AS tax_id,
       (coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
                  WHERE ol.order_id = o.id), 0)
        - o.discount_cents + o.shipping_cents + o.tax_cents)::bigint AS total_cents,
       -- Both halves separately, because the itemisation has to reconstruct the
       -- header rather than infer it. Deriving the delivery line as
       -- total - sum(lines) makes it shipping MINUS discount: a document that
       -- states a carriage charge nobody paid when the discount is smaller, and
       -- one ECPay refuses outright (5000022) when it is larger — which is every
       -- discounted order that also qualified for 免運.
       o.shipping_cents,
       o.discount_cents,
       -- Only a COMMITTED order gets an invoice: a checkout nobody paid for is
       -- not a sale, and undoing a filed document is a tax correction.
       (o.id IN (SELECT id FROM committed_orders))::boolean AS committed,
       -- How many invoices this order has already had, live or voided. ECPay
       -- refuse a repeated RelateNumber (RtnCode 5070357), so a reissue after a
       -- void has to be distinguishable from the first attempt.
       (SELECT count(*) FROM invoice_documents d
        WHERE d.order_id = o.id AND d.kind = 'invoice')::integer AS attempt
FROM orders o
LEFT JOIN order_private_data pd ON pd.order_id = o.id
LEFT JOIN invoice_preferences ip ON ip.order_id = o.id
WHERE o.order_number = @order_number::text;

-- The lines that go on it, as the ORDER recorded them rather than as the
-- catalogue reads today.
-- name: InvoiceSubjectLines :many
SELECT ol.product_name, ol.variant_label, ol.quantity, ol.unit_price_cents,
       (ol.unit_price_cents * ol.quantity)::bigint AS amount_cents
FROM order_lines ol
WHERE ol.order_id = @order_id
ORDER BY ol.position, ol.id;

-- File an issued document, AFTER the provider accepted it: the number is theirs
-- to allocate, and invoice_documents_number_present cannot tell an invented one
-- from a real one.
-- name: RecordInvoiceDocument :one
INSERT INTO invoice_documents (order_id, kind, original_id, number, amount_cents, provider_ref, issued_at)
VALUES (@order_id, @kind::text, sqlc.narg(original_id)::uuid, @number::text,
        @amount_cents::bigint, nullif(@provider_ref::text, ''), @issued_at)
RETURNING id;

-- The lines of an issued document, filed with it.
-- name: RecordInvoiceLine :exec
INSERT INTO invoice_document_lines
    (document_id, description, quantity, unit_price_cents, amount_cents, tax_type, position)
VALUES (@document_id, @description::text, @quantity::integer,
        @unit_price_cents::bigint, @amount_cents::bigint, 'taxable', @position::integer);

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

-- Void a filed invoice. :execrows, because `status <> 'voided'` here is the only
-- place the question is asked under a lock: two staff members voiding one
-- invoice both pass a read taken before the transaction.
-- name: VoidInvoiceDocument :execrows
UPDATE invoice_documents
SET status = 'voided', voided_at = now()
WHERE id = @id AND status <> 'voided';

-- Claim a filing BEFORE the provider is asked. The unique index on request_key
-- is what makes a second press — or a retry after a timeout — refusable here
-- rather than at the 財政部, where the damage is a second 折讓 against one
-- refund. The number is blank because allocating one is the provider's job.
-- name: ClaimInvoiceDocument :one
INSERT INTO invoice_documents
    (order_id, kind, original_id, number, amount_cents, request_key, status)
VALUES (@order_id, @kind::text, sqlc.narg(original_id)::uuid, '',
        @amount_cents::bigint, @request_key::text, 'pending')
RETURNING id;

-- Settle a claim with what the provider allocated.
-- name: SettleInvoiceDocument :execrows
UPDATE invoice_documents
SET number = @number::text, provider_ref = nullif(@provider_ref::text, ''),
    issued_at = @issued_at, status = 'issued'
WHERE id = @id AND status = 'pending';

-- What the CARD has sent back on this order. The credit half is
-- OrderCreditPosition's `returned`, which is the one definition of that figure
-- and the reason this query does not sum the ledger itself.
-- Both sources, from the one view: a refund is paid to the card, to store
-- credit, or split, and an allowance may not relieve more than the sum.
-- name: RefundedForOrder :one
SELECT (card_cents + credit_cents)::bigint AS refunded_cents
FROM order_refunds
WHERE order_number = @order_number::text;

-- What this order has already had relieved, live documents only.
-- name: AllowedTotalForOrder :one
SELECT coalesce(sum(d.amount_cents), 0)::bigint AS allowed_cents
FROM invoice_documents d
JOIN orders o ON o.id = d.order_id
WHERE o.order_number = @order_number::text
  AND d.kind = 'allowance' AND d.status <> 'voided';

-- name: ReleaseInvoiceClaim :execrows
-- A claim the provider REFUSED. ECPay answering with a business verdict proves
-- nothing was filed, so the reservation is the only thing left and holding it
-- would lock that refund out of ever being relieved. A transport failure is a
-- different case and keeps its claim: whether the 加值中心 has the document is
-- not knowable from here, and clearing it would let the next press file twice.
DELETE FROM invoice_documents
WHERE id = $1 AND status = 'pending';
