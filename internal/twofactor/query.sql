-- The credential for one staff member.
-- name: TOTPCredential :one
SELECT secret_encrypted, confirmed_at, last_step
FROM staff_totp_credentials WHERE user_id = $1;

-- Start or restart enrolment, but never REPLACE a factor that has been proved.
--
-- ON CONFLICT overwrites an UNCONFIRMED row, which is what restarting means:
-- somebody who mistyped the secret into their app tries again, and nothing has
-- been proved yet, so there is nothing to protect.
--
-- A CONFIRMED row is refused, and that is the whole guard. This route needs only
-- an ordinary signed-in session, so without the WHERE clause a stolen PASSWORD
-- was enough to take the back office: sign in, enrol the attacker's own
-- authenticator over the real one, confirm it, and the session is step-up
-- verified. The second factor became a formality the password already cleared.
-- Store.Remove names that exact threat as the reason an admin cannot drop their
-- OWN factor — and this path let one skip the removal entirely.
--
-- Recovery is therefore what it always said it was: another admin removes the
-- credential at /admin/staff, which clears confirmed_at and reopens this door.
-- The guard is in the statement rather than in Go because a read-then-write is a
-- race two concurrent enrolments both win.
-- name: BeginTOTPEnrolment :execrows
INSERT INTO staff_totp_credentials (user_id, secret_encrypted)
VALUES (@user_id, @secret_encrypted)
ON CONFLICT (user_id) DO UPDATE
SET secret_encrypted = excluded.secret_encrypted,
    confirmed_at = NULL,
    last_step = NULL,
    created_at = now()
WHERE staff_totp_credentials.confirmed_at IS NULL;

-- Confirm enrolment and record the step in ONE statement.
--
-- Two statements would leave a window in which the credential is confirmed and
-- its step is not recorded, and the code just proved could be replayed through
-- it. The WHERE clause is the same guard the verification uses, so a concurrent
-- second submission of the same code updates zero rows.
-- name: ConfirmTOTP :execrows
UPDATE staff_totp_credentials
SET confirmed_at = now(), last_step = @step::bigint
WHERE user_id = @user_id
  AND (last_step IS NULL OR last_step < @step::bigint);

-- Record an accepted code.
--
-- The step guard is IN the statement, not in Go. Two requests replaying one
-- code concurrently both read the same last_step and both pass a check made in
-- the application; only one can win here.
-- name: RecordTOTPStep :execrows
UPDATE staff_totp_credentials
SET last_step = @step::bigint
WHERE user_id = @user_id
  AND confirmed_at IS NOT NULL
  AND (last_step IS NULL OR last_step < @step::bigint);

-- name: RemoveTOTP :execrows
DELETE FROM staff_totp_credentials WHERE user_id = @user_id;

-- Mark this session as having proved a second factor.
-- name: MarkSessionVerified :exec
UPDATE sessions SET totp_verified_at = now() WHERE token_hash = $1;

-- Whether a session's proof is still current.
-- name: SessionTOTPVerified :one
SELECT (s.totp_verified_at IS NOT NULL
        AND s.totp_verified_at > now() - @max_age::interval)::boolean AS verified
FROM sessions s WHERE s.token_hash = $1;

-- Every staff account and whether it has a confirmed credential, for the page
-- that shows who is protected.
-- name: StaffTOTPStatus :many
SELECT u.id, u.email, coalesce(u.full_name, '') AS full_name, u.role,
       (c.confirmed_at IS NOT NULL)::boolean AS enrolled
FROM users u
LEFT JOIN staff_totp_credentials c ON c.user_id = u.id
WHERE u.role IN ('admin', 'staff')
ORDER BY u.email;

-- Create a staff account with NO password.
--
-- Deliberately: an admin who typed a colleague's password would know it, and a
-- generated one has to be delivered somehow. The colleague sets their own
-- through /forgot, which already ends every session and invalidates every other
-- token — so the account is unusable until the person who owns the mailbox
-- proves they own it.
--
-- ON CONFLICT so an existing CUSTOMER can be promoted rather than refused: a
-- shop hiring somebody who already shops there is the common case, and telling
-- the admin "that email is taken" would be an answer they cannot act on.
-- name: UpsertStaff :one
INSERT INTO users (email, full_name, role)
VALUES (@email, nullif(@full_name::text, ''), @role)
ON CONFLICT (lower(email)) DO UPDATE
SET role = EXCLUDED.role,
    full_name = coalesce(nullif(EXCLUDED.full_name, ''), users.full_name)
RETURNING id;

-- Take somebody's back-office access away.
--
-- Their role goes back to 'customer' rather than the row being deleted: they
-- may have placed orders, written reviews and answered questions, and erase_user
-- is the only door that removes a person — for a colleague who has left, the
-- shop wants the history and not the access.
-- name: RevokeStaff :execrows
UPDATE users SET role = 'customer' WHERE id = $1 AND role IN ('staff', 'admin');

-- How many admins remain, for the guard that stops the last one being removed.
-- name: CountAdmins :one
SELECT count(*)::bigint FROM users WHERE role = 'admin';

-- Whether an address belongs to the account asking.
--
-- UpsertStaff resolves an account BY ADDRESS, so submitting your own address is
-- submitting your own row — and its DO UPDATE sets the role. That is how the
-- promotion worked, and comparing ids after the write would be comparing them to
-- a row the write had already changed.
--
-- Folded, because users_email_key is unique on lower(email): two addresses
-- differing only in case are one mailbox, and a check that missed that would be
-- bypassed by pressing shift.
-- name: UserHasEmail :one
SELECT EXISTS (
    SELECT 1 FROM users WHERE id = @id AND lower(email) = lower(@email::text)
);

-- Every session belonging to somebody whose access was just revoked.
--
-- Without this a revoked colleague keeps whatever session they had until it
-- expires, which is up to its full lifetime of back-office access after being
-- told they no longer have any.
-- name: EndStaffSessions :exec
DELETE FROM sessions WHERE user_id = $1;
