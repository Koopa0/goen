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

-- name: MarkSessionVerified :exec
UPDATE sessions SET totp_verified_at = now() WHERE token_hash = $1;

-- name: SessionTOTPVerified :one
SELECT (s.totp_verified_at IS NOT NULL
        AND s.totp_verified_at > now() - @max_age::interval)::boolean AS verified
FROM sessions s WHERE s.token_hash = $1;
