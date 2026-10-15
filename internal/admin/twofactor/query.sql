-- name: TOTPCredential :one
SELECT secret_encrypted, confirmed_at, last_step
FROM staff_totp_credentials WHERE user_id = $1;

-- Start or restart enrolment, but never replace a factor that has been proved.
-- The WHERE clause is the guard, in the statement because a read-then-write is
-- a race two concurrent enrolments both win.
-- name: BeginTOTPEnrolment :execrows
INSERT INTO staff_totp_credentials (user_id, secret_encrypted, mailed_code_digest)
VALUES (@user_id, @secret_encrypted, @mailed_code_digest)
ON CONFLICT (user_id) DO UPDATE
SET secret_encrypted = excluded.secret_encrypted,
    mailed_code_digest = excluded.mailed_code_digest,
    confirmed_at = NULL,
    last_step = NULL,
    created_at = now()
WHERE staff_totp_credentials.confirmed_at IS NULL;

-- Confirm enrolment and record the step in ONE statement: two would leave a
-- window in which the credential is confirmed and the code just proved is
-- still replayable. The secret is matched too: enrolment restarted since the
-- code was checked has replaced it with one no code has proved. The mailed code
-- is matched here too, so it is spent by the write that accepts it.
-- name: ConfirmTOTP :execrows
UPDATE staff_totp_credentials
SET confirmed_at = now(), last_step = @step::bigint, mailed_code_digest = NULL
WHERE user_id = @user_id
  AND secret_encrypted = @secret_encrypted
  AND mailed_code_digest = @mailed_code_digest
  AND created_at > now() - @mailed_code_ttl::interval
  AND (last_step IS NULL OR last_step < @step::bigint);

-- Charge one attempt against a confirmed credential before its code is
-- checked, and read the credential the code is checked against. The cap is in
-- the WHERE clause, so concurrent attempts cannot all pass a count read
-- separately: this UPDATE holds the row until the attempt's transaction ends,
-- and no row comes back once failed_attempts has reached the cap. users is
-- joined for the address a warning about the attempt goes to.
-- name: ChargeTOTPAttempt :one
UPDATE staff_totp_credentials AS c
SET failed_attempts = c.failed_attempts + 1
FROM users AS u
WHERE c.user_id = @user_id
  AND u.id = c.user_id
  AND c.confirmed_at IS NOT NULL
  AND c.failed_attempts < @max_failures::integer
RETURNING c.secret_encrypted, c.last_step, c.failed_attempts, u.email;

-- Record an accepted code and clear the attempts charged since the last one.
-- The step guard is in the statement: two requests replaying one code both
-- pass a check made in Go, and only one can win here. A secret the current key
-- did not open arrives re-sealed under it and is written by the same statement
-- that accepts the code; NULL leaves it alone.
-- name: RecordTOTPStep :execrows
UPDATE staff_totp_credentials
SET last_step = @step::bigint,
    failed_attempts = 0,
    secret_encrypted = coalesce(sqlc.narg(resealed)::bytea, secret_encrypted)
WHERE user_id = @user_id
  AND confirmed_at IS NOT NULL
  AND (last_step IS NULL OR last_step < @step::bigint);

-- name: MarkSessionVerified :exec
UPDATE sessions SET totp_verified_at = now() WHERE digest = $1;

-- name: SessionTOTPVerified :one
SELECT (s.totp_verified_at IS NOT NULL
        AND s.totp_verified_at > now() - @max_age::interval)::boolean AS verified
FROM sessions s WHERE s.digest = $1;
