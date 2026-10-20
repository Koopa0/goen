-- lower(email) matches the unique index. verified is what a password sign-in
-- turns on: until the address is proved, the password may be whoever
-- registered it rather than whoever reads the mailbox.
-- name: UserByEmail :one
SELECT id, email, password_hash, full_name, role,
       (email_verified_at IS NOT NULL)::boolean AS verified
FROM users WHERE lower(email) = lower($1);

-- A forgotten-password request is this one INSERT whatever the address: the
-- account is looked up inside it and names nobody when there is none, so a
-- known address costs the request exactly what an unknown one does. The token
-- is issued later, off the request, from the account id alone.
-- name: EnqueuePasswordResetRequest :exec
INSERT INTO outbox_messages (topic, dedupe_key, payload)
SELECT @topic::text, @dedupe_key::text,
       jsonb_build_object('user_id', u.id, 'locale', @locale::text)
FROM (VALUES (true)) AS request (queued)
LEFT JOIN users u ON lower(u.email) = lower(@email::text);

-- A reset token and its outbox message are created while this lock is held.
-- FOR UPDATE serializes a replacement request against another issuance and
-- against erase_user, so only one unused token remains, and either both reset
-- records commit first and erasure purges them, or erasure wins and this
-- returns no row.
-- name: UserForPasswordReset :one
SELECT id, email
FROM users
WHERE id = @user_id::uuid
FOR UPDATE;

-- name: UserByID :one
SELECT id, email, full_name, phone, role, created_at,
       (password_hash IS NOT NULL)::boolean AS has_password
FROM users WHERE id = $1;

-- A registration is this one statement whether or not the address is taken:
-- created says which, and user_id names the account either way. users_email_key
-- decides, so two registrations of one address make one account. user_id is the
-- nil UUID only when a concurrent registration committed the address after this
-- statement's snapshot was taken, and then there is nobody to write to.
-- name: CreateUserUnlessRegistered :one
WITH created AS (
    INSERT INTO users (email, password_hash, full_name)
    VALUES (@email::text, @password_hash::text, @full_name)
    ON CONFLICT ((lower(email))) DO NOTHING
    RETURNING id
)
SELECT coalesce(c.id, u.id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS user_id,
       (c.id IS NOT NULL)::boolean AS created
FROM (VALUES (true)) AS attempt (made)
LEFT JOIN created c ON true
LEFT JOIN users u ON lower(u.email) = lower(@email::text);

-- Born proved: the address is the operator's, published beside the password,
-- so no link is mailed for it.
-- name: CreateVerifiedUser :exec
INSERT INTO users (email, password_hash, email_verified_at)
VALUES (@email::text, @password_hash::text, now());

-- Asking again for a registration link is this one INSERT whatever the
-- address: the account is looked up inside it, and names nobody unless it is
-- registered and still unproved, so an address with no account, or one already
-- proved, costs the request what any other does.
-- name: EnqueueRegistrationResend :exec
INSERT INTO outbox_messages (topic, dedupe_key, payload)
SELECT @topic::text, @dedupe_key::text,
       jsonb_build_object('user_id', u.id, 'created', true, 'locale', @locale::text, 'next', @next::text)
FROM (VALUES (true)) AS request (queued)
LEFT JOIN users u ON lower(u.email) = lower(@email::text) AND u.email_verified_at IS NULL;

-- The registrant's half of completing a registration: the password chosen when
-- the account was made.
-- name: RegistrationCredential :one
SELECT password_hash FROM users WHERE id = $1;

-- name: RegistrationAccount :one
SELECT email, full_name, (email_verified_at IS NOT NULL)::boolean AS verified
FROM users WHERE id = $1;

-- Spending a reset link proves the address it was mailed to, which is the
-- account's current one: every reset token dies when the address changes.
-- name: ProveEmailByReset :exec
UPDATE users SET email_verified_at = now()
WHERE id = $1 AND email_verified_at IS NULL;

-- name: SetPasswordHash :exec
UPDATE users SET password_hash = $2 WHERE id = $1;

-- name: TouchLastLogin :exec
UPDATE users SET last_login_at = now() WHERE id = $1;

-- The expiry is checked here, so an expired session is dead before anything sweeps it.
-- name: SessionUser :one
SELECT u.id, u.email, u.full_name, u.role
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.digest = $1 AND s.expires_at > now();

-- Recency is measured on the database's clock, the one that stamped created_at
-- and that the expiry check above reads. A missing or expired session is not recent.
-- name: SessionCreatedSince :one
SELECT s.created_at > now() - @max_age::interval AS recent
FROM sessions s
WHERE s.digest = @digest AND s.expires_at > now();

-- The expiry is computed from the DATABASE's clock, which is what every read
-- and retention sweep compares it with.
-- name: CreateSession :exec
INSERT INTO sessions (digest, user_id, user_agent, ip, expires_at)
VALUES (@digest, @user_id, @user_agent, @ip, now() + @ttl::interval);

-- name: DeleteSession :exec
DELETE FROM sessions WHERE digest = $1;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = $1;

-- name: DeleteOtherUserSessions :exec
DELETE FROM sessions
WHERE user_id = @user_id AND digest <> @current_digest::bytea;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= now();

-- A remembered browser counts only for the account the form names, so one
-- signed in to X spends nobody's budget when it guesses at Y, and an address
-- with no account finds nothing either way.
-- name: KnownDeviceForAddress :one
SELECT d.digest
FROM known_devices d
JOIN users u ON u.id = d.user_id
WHERE d.digest = @digest AND lower(u.email) = lower(@email::text) AND d.expires_at > now();

-- name: CountKnownDeviceMiss :one
UPDATE known_devices SET misses = misses + 1
WHERE digest = $1
RETURNING misses;

-- name: DeleteKnownDevice :exec
DELETE FROM known_devices WHERE digest = $1;

-- Taken before a browser is remembered, so two sign-ins to one account cannot
-- each count the other's row out of the newest and leave more than the limit.
-- NO KEY: the rows that reference the account still insert meanwhile.
-- name: LockUserForKnownDevices :one
SELECT id FROM users WHERE id = @user_id::uuid FOR NO KEY UPDATE;

-- The expiry is computed from the DATABASE's clock, which is what reads it back.
-- name: CreateKnownDevice :exec
INSERT INTO known_devices (digest, user_id, expires_at)
VALUES (@digest, @user_id, now() + @ttl::interval);

-- Keeps the newest @keep of an account's browsers. The digest breaks a tie in
-- created_at, so the cut is the same however the rows are read.
-- name: DeleteSurplusKnownDevices :exec
DELETE FROM known_devices d
WHERE d.user_id = @user_id::uuid
  AND d.digest NOT IN (SELECT k.digest FROM known_devices k
                       WHERE k.user_id = @user_id::uuid
                       ORDER BY k.created_at DESC, k.digest DESC
                       LIMIT @keep::integer);

-- name: DeleteUserKnownDevices :exec
DELETE FROM known_devices WHERE user_id = $1;

-- name: DeleteOtherUserKnownDevices :exec
DELETE FROM known_devices
WHERE user_id = @user_id AND digest <> @current_digest::bytea;

-- name: DeleteExpiredKnownDevices :exec
DELETE FROM known_devices WHERE expires_at <= now();

-- A grace period rather than now(), so support can still answer "did they ask
-- for a reset yesterday?".
-- name: DeleteDeadResetTokens :exec
DELETE FROM password_reset_tokens
WHERE (used_at IS NOT NULL OR expires_at <= now())
  AND created_at < now() - sqlc.arg(grace)::interval;

-- The expiry is computed from the DATABASE's clock, which is what reads it back.
-- name: CreatePasswordResetToken :exec
INSERT INTO password_reset_tokens (digest, user_id, expires_at)
VALUES (@digest, @user_id, now() + @ttl::interval);

-- name: PasswordResetToken :one
SELECT user_id FROM password_reset_tokens
WHERE digest = $1 AND used_at IS NULL AND expires_at > now();

-- Completion takes the account row before it spends the token. erase_user uses
-- the same user-before-token order, so the two operations cannot deadlock. The
-- exclusive row lock also serializes different live tokens for one account;
-- otherwise two KEY SHARE holders can deadlock while both upgrade to write the
-- password, or let the later writer silently replace the earlier reset.
-- name: LockUserForPasswordReset :one
SELECT id FROM users WHERE id = @user_id::uuid FOR UPDATE;

-- used_at IS NULL is in the WHERE, so two requests carrying one token cannot both
-- win, and the caller runs this in the SAME transaction as the password change.
-- name: SpendPasswordResetToken :one
UPDATE password_reset_tokens SET used_at = now()
WHERE digest = $1 AND used_at IS NULL AND expires_at > now()
RETURNING user_id;

-- A replacement link, a completed reset, or a verified mailbox must leave no
-- unused token that could still spend.
-- name: InvalidateResetTokens :exec
UPDATE password_reset_tokens SET used_at = now()
WHERE user_id = $1 AND used_at IS NULL;

-- Once an account moves to a new mailbox, a queued reset for its old address is
-- both dead authority and retained PII. Keep this query operation-specific: an
-- address change has no authority to remove other outbox topics.
-- name: DeletePasswordResetMessagesForEmail :exec
DELETE FROM outbox_messages
WHERE topic = 'account.password_reset'
  AND lower(coalesce(payload ->> 'email', '')) =
      lower(@email::text);

-- name: EraseUser :exec
SELECT erase_user($1);

-- Delete the queued copy before replacing its verification row. Both statements
-- run after the user lock in the same transaction, so every surviving message
-- has a surviving digest that erasure can identify without claiming the mailbox.
-- name: DeleteOutstandingEmailVerificationMessage :exec
DELETE FROM outbox_messages m
USING email_verifications v
WHERE v.user_id = @user_id::uuid
  AND m.topic = 'account.email_verify'
  AND m.dedupe_key = 'verify:' || encode(v.digest, 'hex');

-- Replaces any earlier request, so a mailbox holds one live link.
-- name: RequestEmailVerification :exec
INSERT INTO email_verifications (user_id, email, digest, expires_at)
VALUES (@user_id, @email::text, @digest, now() + @ttl::interval)
ON CONFLICT (user_id) DO UPDATE
    SET email      = EXCLUDED.email,
        digest     = EXCLUDED.digest,
        expires_at = EXCLUDED.expires_at,
        created_at = now();

-- Spent BY THIS STATEMENT: several requests carrying one token reach it and one wins.
-- name: SpendEmailVerification :one
DELETE FROM email_verifications
WHERE digest = $1 AND expires_at > now()
RETURNING user_id, email;

-- A plain lookup names the account to lock before SpendEmailVerification takes
-- the token row. erase_user uses the same user-before-token order.
-- name: EmailVerificationToken :one
SELECT user_id, email FROM email_verifications
WHERE digest = $1 AND expires_at > now();

-- verified is read under the lock: a link that proves the address of an
-- account whose address was never proved is the one that completes a
-- registration.
-- name: LockUserForEmailVerification :one
SELECT id, email, (email_verified_at IS NOT NULL)::boolean AS verified, role
FROM users WHERE id = @user_id::uuid FOR UPDATE;

-- One statement, because an address goen has proved and one goen is using must
-- not be able to disagree. users_email_key catches an address taken in between.
-- name: SetVerifiedEmail :exec
UPDATE users SET email = @email::text, email_verified_at = now()
WHERE id = @user_id;

-- Read by the outbox worker, never by the request, so asking to move to an
-- address costs the same whether or not it has an account. Not the guard:
-- users_email_key is, because an address can be taken in between.
-- name: OtherAccountAtAddress :one
SELECT email, full_name FROM users
WHERE lower(email) = lower(@email::text) AND id <> @user_id;

-- Keyed on the SUBJECT: a Google account can change address, and a released
-- Workspace address can be reassigned to somebody else.
-- name: LockGoogleSubject :exec
SELECT pg_advisory_xact_lock(hashtextextended('google:' || @subject::text, 0));

-- name: UserByGoogleSubject :one
SELECT u.id, u.email, u.full_name, u.role
FROM user_identities i
JOIN users u ON u.id = i.user_id
WHERE i.provider = 'google' AND i.provider_subject = @subject::text;

-- verified is what linking turns on: an unverified address may belong to whoever
-- registered it rather than to whoever reads the mailbox.
-- name: UserForOAuthLink :one
SELECT id, email, full_name, role,
       (email_verified_at IS NOT NULL)::boolean AS verified,
       (password_hash IS NOT NULL)::boolean AS has_password
FROM users WHERE lower(email) = lower(@email::text);

-- No password hash, which is legal; the CALLER checks the provider's verified
-- claim before email_verified_at is set from it.
-- name: CreateUserFromIdentity :one
INSERT INTO users (email, full_name, email_verified_at)
VALUES (@email::text, nullif(@full_name::text, ''), now())
RETURNING id, email, full_name, role;

-- ON CONFLICT DO NOTHING: two tabs finishing one sign-in are one link.
-- name: LinkIdentity :execrows
INSERT INTO user_identities (user_id, provider, provider_subject)
VALUES (@user_id, 'google', @subject::text)
ON CONFLICT (provider, provider_subject) DO NOTHING;

-- :execrows: the caller refuses when the account has no password, and the row
-- count is how it learns whether the delete happened.
-- name: UnlinkIdentity :execrows
DELETE FROM user_identities
WHERE user_id = @user_id AND provider = @provider::text;
