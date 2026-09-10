-- name: CreateContactMessage :one
INSERT INTO contact_messages (name, email, subject, order_ref, message)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, created_at;
