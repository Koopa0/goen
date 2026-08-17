-- name: TOTPCredential :one
SELECT secret_encrypted, confirmed_at, last_step
FROM staff_totp_credentials WHERE user_id = $1;

-- Start or restart enrolment, but never replace a factor that has been proved.
-- The WHERE clause is the guard, in the statement because a read-then-write is
-- a race two concurrent enrolments both win.
-- name: BeginTOTPEnrolment :execrows
INSERT INTO staff_totp_credentials (user_id, secret_encrypted)
VALUES (@user_id, @secret_encrypted)
ON CONFLICT (user_id) DO UPDATE
SET secret_encrypted = excluded.secret_encrypted,
    confirmed_at = NULL,
    last_step = NULL,
    created_at = now()
WHERE staff_totp_credentials.confirmed_at IS NULL;

-- Confirm enrolment and record the step in ONE statement: two would leave a
-- window in which the credential is confirmed and the code just proved is
-- still replayable.
-- name: ConfirmTOTP :execrows
UPDATE staff_totp_credentials
SET confirmed_at = now(), last_step = @step::bigint
WHERE user_id = @user_id
  AND (last_step IS NULL OR last_step < @step::bigint);

-- Record an accepted code. The step guard is in the statement: two requests
-- replaying one code both pass a check made in Go, and only one can win here.
-- name: RecordTOTPStep :execrows
UPDATE staff_totp_credentials
SET last_step = @step::bigint
WHERE user_id = @user_id
  AND confirmed_at IS NOT NULL
  AND (last_step IS NULL OR last_step < @step::bigint);

-- name: RemoveTOTP :execrows
DELETE FROM staff_totp_credentials WHERE user_id = @user_id;

-- name: MarkSessionVerified :exec
UPDATE sessions SET totp_verified_at = now() WHERE token_hash = $1;

-- name: SessionTOTPVerified :one
SELECT (s.totp_verified_at IS NOT NULL
        AND s.totp_verified_at > now() - @max_age::interval)::boolean AS verified
FROM sessions s WHERE s.token_hash = $1;

-- name: StaffTOTPStatus :many
SELECT u.id, u.email, coalesce(u.full_name, '') AS full_name, u.role,
       (c.confirmed_at IS NOT NULL)::boolean AS enrolled
FROM users u
LEFT JOIN staff_totp_credentials c ON c.user_id = u.id
WHERE u.role IN ('admin', 'staff')
ORDER BY u.email;

-- Create a staff account with NO password; they set their own through /forgot,
-- which is the one path that proves they own the mailbox. ON CONFLICT so an
-- existing customer is promoted rather than refused.
-- name: UpsertStaff :one
INSERT INTO users (email, full_name, role)
VALUES (@email, nullif(@full_name::text, ''), @role)
ON CONFLICT (lower(email)) DO UPDATE
SET role = EXCLUDED.role,
    full_name = coalesce(nullif(EXCLUDED.full_name, ''), users.full_name)
RETURNING id;

-- Take somebody's back-office access away. The role goes back to 'customer'
-- rather than the row being deleted: erase_user is the only door that removes
-- a person.
-- name: RevokeStaff :execrows
UPDATE users SET role = 'customer' WHERE id = $1 AND role IN ('staff', 'admin');

-- name: CountAdmins :one
SELECT count(*)::bigint FROM users WHERE role = 'admin';

-- Whether an address belongs to the account asking. Folded, because
-- users_email_key is unique on lower(email): two addresses differing only in
-- case are one mailbox, and a literal comparison is bypassed by pressing shift.
-- name: UserHasEmail :one
SELECT EXISTS (
    SELECT 1 FROM users WHERE id = @id AND lower(email) = lower(@email::text)
);

-- name: EndStaffSessions :exec
DELETE FROM sessions WHERE user_id = $1;
