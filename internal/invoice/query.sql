-- What an order needs to become an invoice.
--
-- The preference is what the customer chose at checkout — 會員載具, 手機條碼載具
-- or 公司統編 — and until this query nothing read it for anything but showing a
-- staff member what to do by hand.
--
-- The amount is order_amount_owed's numerator rather than the net: a 統一發票
-- records the SALE, and store credit is how the customer paid rather than a
-- reduction in what was sold. An order settled entirely from credit still had a
-- price and still owes a tax document for it.
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
       -- Only a COMMITTED order gets an invoice. Issuing for a checkout nobody
       -- paid for files a tax document for a sale that did not happen, and
       -- voiding it is a correction with the 財政部 rather than a delete.
       (o.id IN (SELECT id FROM committed_orders))::boolean AS committed,
       -- How many invoices this order has already had, live or voided.
       --
       -- RelateNumber is ECPay's own idempotency key and they refuse a repeat
       -- (RtnCode 5070357) — which the staging API taught, by refusing the
       -- REISSUE after a void. The order number alone is therefore not enough:
       -- a wrong invoice is voided and a correct one issued in its place, and
       -- the second attempt has to be distinguishable from the first.
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

-- File an issued document.
--
-- Written AFTER the provider accepted it, because the number is theirs to
-- allocate: a row written first would carry a number goen invented, and
-- invoice_documents_number_present has no way to tell the two apart.
--
-- The reverse ordering — provider first, row second — has the failure the refund
-- path already documents: a document filed with the 加值中心 and absent here.
-- That is the recoverable direction, because /admin/orders shows it and ECPay's
-- own console can be queried. A row with no document is not: it claims a tax
-- filing that does not exist.
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

-- What one filed document says was sold.
--
-- The itemisation is the whole reason invoice_document_lines exists rather than
-- a single amount: a 統一發票 states what was bought, and a shop reconciling one
-- against an order needs to compare lines rather than a total. Without this read
-- the lines were written and shown to nobody — the half-a-door shape this
-- repository keeps finding, and TestEveryTableIsRead is what caught it here.
-- name: InvoiceDocumentLines :many
SELECT l.document_id, l.id, l.description, l.quantity, l.unit_price_cents,
       l.amount_cents, l.tax_type
FROM invoice_document_lines l
WHERE l.document_id = ANY(@document_ids::uuid[])
ORDER BY l.document_id, l.position, l.id;

-- The live invoice of an order, if it has one.
--
-- `status <> 'voided'` matches invoice_documents_one_active_invoice_per_order,
-- so this returns the row that index guarantees is unique — a voided invoice
-- frees the slot for a corrected reissue and must not be found here.
-- name: LiveInvoice :one
SELECT d.id, d.number, d.amount_cents, coalesce(d.provider_ref, '')::text AS provider_ref,
       d.issued_at
FROM invoice_documents d
JOIN orders o ON o.id = d.order_id
WHERE o.order_number = @order_number::text
  AND d.kind = 'invoice' AND d.status <> 'voided';

-- Void a filed invoice.
--
-- :execrows, because `status <> 'voided'` in this WHERE clause is the only place
-- the question is asked under a lock — two staff members voiding one invoice
-- both pass a read taken before the transaction. Zero rows is "somebody voided
-- it first", which is a sentence a caller can act on rather than a second call
-- to the 加值中心 for a document that is already cancelled.
-- name: VoidInvoiceDocument :execrows
UPDATE invoice_documents
SET status = 'voided', voided_at = now()
WHERE id = @id AND status <> 'voided';
