# totp-key-derivation

**Verdict** CONFIRMED · **Severity** high · **Origin** found in this round's own sweep (already established)

**Files** internal/twofactor/crypt.go, internal/twofactor/store.go, internal/twofactor/twofactor.go, internal/twofactor/handler.go, internal/twofactor/twofactor_test.go, internal/twofactor/integration_test.go, internal/i18n/chrome_admin_twofactor.go, cmd/goen/main.go, cmd/goen/server.go, cmd/goen/main_test.go, README.md, .env.example, CLAUDE.md


## Root cause

A key was treated as a passphrase, and the documentation was written from the implementation rather than from the requirement. `sha256(x)` is a length-normaliser, not a key derivation function: it removes the "AES needs exactly 32 bytes" error at the cost of accepting inputs with far less than 256 bits of entropy, at one hash per guess. Because the normaliser accepts everything, there was nothing left to refuse at startup, and `.env.example` then documented the accident as a feature ("any length will do") beside the security claim it destroys.

It is CLAUDE.md mistake #31's shape from the configuration side: the sentence naming the guarantee ("this is what stops a database dump alone defeating the second factor") sits two lines under the sentence that voids it, and a reader stops at whichever they read first.


## Reproduction — the evidence this rests on

EVIDENCE CHECK (read, plus one measurement against the live dev database; no code changed, tree verified clean with `git status --porcelain` at the end).

1. `internal/twofactor/crypt.go:20-35` says exactly what the finding claims — line 24 is the derivation:
```go
func NewCipher(key string) *Cipher {
	if key == "" {
		return &Cipher{}
	}
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
```
One unsalted SHA-256 pass over an operator-supplied string, used directly as the AES-256-GCM key. `NewCipher` returns no error; its two failure paths panic and are commented "Unreachable".

2. `.env.example:140-143` is quoted correctly, and the two sentences really are two lines apart:
```
# Encrypts stored TOTP secrets with AES-256-GCM. The value is hashed to the key,
# so any length will do — `openssl rand -hex 32` is a good source. A
# staff_totp_credentials row is a password-equivalent, so this is what stops a
# database dump alone defeating the second factor.
```
"any length will do" is guidance that actively invites the weak case, on the same secret whose stated purpose is surviving a database dump.

3. `cmd/goen/main.go:128-140` (`checkProductionPosture`) tests emptiness and nothing else:
```go
	if cfg.TOTPKey == "" {
		if cfg.SecureCookies {
			return errors.New("GOEN_TOTP_KEY is required: ...")
		}
		log.Warn("GOEN_TOTP_KEY is not set; the back office has NO second factor", ...)
	}
```
No length floor, no decode, no format check. `cfg.TOTPKey` then travels as a raw string through `newServer` → `RouterConfig.TOTPKey` (`cmd/goen/server.go:56-57`, `:65`) → `twofactor.NewStore(adminPool, totpKey)` (`server.go:103`) → `NewCipher(key)` (`internal/twofactor/store.go:30`). `NewStore` is the only non-test caller of `NewCipher`.

WHAT THE SUMMARY DID NOT SAY, and matters to the fix:

a. The same claim is repeated in two more places that must be corrected with the code: `README.md:203` ("Encrypts staff second-factor secrets at rest. Empty disables enrolment" — no format stated) and `CLAUDE.md:2560` ("The secret is AES-256-GCM at rest, keyed by `GOEN_TOTP_KEY` … so the point is that a database dump alone does not defeat the factor").

b. This repository already refuses the same class of mistake for the OTHER secret in the binary. `internal/invoice/ecpay.go:50-53`:
```go
	// AES-128: ECPay's keys are exactly 16 bytes.
	if len(hashKey) != 16 || len(hashIV) != 16 {
		return nil, fmt.Errorf("invoice: the hash key and IV must be 16 bytes "+
			"(AES-128); got %d and %d", len(hashKey), len(hashIV))
	}
```
So "a key is a fixed number of bytes and a wrong one refuses to start" is an established in-tree pattern, and `GOEN_TOTP_KEY` is the one that departs from it. That is the precedent the fix follows, not a new idea.

c. BLAST RADIUS CORRECTION (honest, and it does not weaken the finding). Measured against the running dev database:
```
$ psql "$GOEN_DATABASE_URL" -c "select count(*), count(confirmed_at) from staff_totp_credentials;"
 count | count
-------+-------
     0 |     0
```
and `GOEN_TOTP_KEY` in the local `.env` is empty (len=0). CLAUDE.md records goen as undeployed ("goen has not been deployed"). So there is no live compromise today: what is confirmed is a weak derivation plus documentation that instructs the operator into it, and zero enrolled rows anywhere. That fact is what makes the breaking change cheap — see the migration section of the fix.

d. `crypt.go` is the only place `crypto/sha256` is used for this purpose in the package; `store.go` imports it separately for session digests, so removing the import from `crypt.go` is safe and must not touch `store.go`.

e. `NewCipher` is called with a string in 4 unit-test sites (`twofactor_test.go:116,152,167,179`) and `NewStore` with a string in 19 integration sites (`integration_test.go`, all through `const testKey = "a-key-only-this-test-uses"` at line 26, plus a literal `"a-different-key"` at :267 and `""` at :332). Those are the call sites a signature change touches.


## Blast radius

Every staff TOTP secret in `staff_totp_credentials`, which CLAUDE.md itself calls "a password-equivalent". An attacker holding a database dump (the exact threat `.env.example:142-143` names) plus a guess at the operator's passphrase recovers every secret at one SHA-256 per candidate — no salt, no iteration count, and one guess breaks EVERY row at once because the key is global. With the secrets, the attacker generates valid codes indefinitely and the back-office step-up gate — the only thing between a stolen password and `/admin` — is defeated silently, leaving `last_step` looking like ordinary staff activity.

The reach is: the whole back office. `/admin/credit` grants store credit, `/admin/orders` corrects addresses and voids invoices, `/admin/customers` is a page of personal data, `/admin/staff` promotes accounts and removes second factors.

Frequency: it fires exactly when an operator follows the documentation. `.env.example:140-141` tells them "any length will do", so a memorable passphrase is the compliant answer, not the careless one. Silent: nothing at startup, in a log, or on any page distinguishes a 32-byte random key from `hunter2`; both produce a working `/admin/2fa`.

Today, zero rows and no deployment (measured above), so this is a latent defect and not an incident. It becomes live on the first enrolment in the first TLS deployment — which is also the last cheap moment to change the derivation.


## Fix

DECISION: require exactly 32 bytes of decoded key material (hex or base64) and refuse anything else at startup. NO passphrase path, no KDF.

Why this and not argon2id-over-a-passphrase, in this repository:
- The pattern is already in the tree for the sibling secret: `internal/invoice/ecpay.go:50-53` refuses a hash key that is not exactly 16 bytes, with a startup error naming the length it got. Two secrets in one binary, one of which accepts "any length will do", is the inconsistency.
- The house style is to refuse a half-configuration rather than degrade: a Stripe secret without a webhook secret refuses to start; `GOEN_BASE_URL` is required once cookies are Secure; a mistyped `GOEN_TRUSTED_PROXIES` is fatal ("a mistaken CIDR is fatal rather than a warning: it would otherwise keep the collapsed single-bucket behaviour it is configuring its way out of"). A KDF path is precisely "accepted, but weaker", which is the shape CLAUDE.md refuses everywhere else.
- A real KDF needs a SALT stable across restarts and across replicas. Stored beside the key in the same environment it defends nothing against the stated threat (a database dump takes the table, not the env). Derived from a compile-time constant it is a hardcoded salt: it stops a generic rainbow table and does nothing against a wordlist aimed at goen. So the KDF buys a slowdown factor with a second parameter set to misconfigure, a second migration story, and an operator choice that has no right answer.
- Cost to the operator is one command that is ALREADY printed in `.env.example`: `openssl rand -hex 32`.
- argon2id is already a dependency (`internal/account/account.go:15`), so this is not dependency avoidance — it is refusing to offer a weaker option.

--- 1. NEW PARSER, in the package that owns the format: `internal/twofactor/crypt.go`

```go
// KeyBytes is the AES-256 key length GOEN_TOTP_KEY must decode to.
const KeyBytes = 32

// ParseKey decodes a configured GOEN_TOTP_KEY into raw key material. An empty
// value yields (nil, nil): a deployment with no key, which NewCipher turns
// into a disabled Cipher.
//
// It is a KEY and not a passphrase. A single unsalted SHA-256 over a memorable
// phrase is not a key derivation function — it costs an offline attacker one
// hash per wordlist entry against every row in staff_totp_credentials at once,
// which is the dump this encryption exists to survive. So anything that is not
// exactly KeyBytes bytes of hex or base64 is refused here, at startup, rather
// than normalised into a key that looks like one.
func ParseKey(value string) ([]byte, error)
```
Behaviour:
- `strings.TrimSpace(value)` first (a key pasted out of a file carries a newline); `"" → (nil, nil)`.
- Decode attempts, first that yields exactly `KeyBytes` bytes wins: `hex.DecodeString` (accept upper and lower case — `encoding/hex` already does), then `base64.StdEncoding`, `base64.RawStdEncoding`, `base64.URLEncoding`, `base64.RawURLEncoding`.
- Refuse a key whose 32 bytes are all identical (`0000…`, a repeated-byte paste). This is the ONLY randomness rule: it is decidable, which is the standard `check-layout`'s rules are held to ("every rule is decidable … so a failure is a fact rather than an opinion"). Do not add an entropy estimator — 32 bytes is not enough sample to make one a fact.
- Errors (i18n-exempt: a startup failure, no visitor and no locale — follow `ecpay.go:44-45`'s comment form):
  - decoded to n != 32 bytes: `fmt.Errorf("twofactor: GOEN_TOTP_KEY must be %d random bytes as hex or base64 — generate one with `openssl rand -hex 32`; the configured value decoded to %d bytes", KeyBytes, n)`
  - decoded by nothing: `errors.New("twofactor: GOEN_TOTP_KEY is neither hex nor base64 — it is a key, not a passphrase; generate one with `openssl rand -hex 32`")`
  - all-identical: `errors.New("twofactor: GOEN_TOTP_KEY decoded to 32 identical bytes, which is a placeholder rather than a key; generate one with `openssl rand -hex 32`")`
- NO error may contain the value, or a startup log leaks the key. This is a hard rule for the implementer and a test asserts it.

--- 2. `NewCipher` takes bytes, not a string (`crypt.go:18-35`)

```go
// NewCipher derives a Cipher from key material produced by ParseKey. A nil or
// empty key yields a disabled Cipher rather than an error, which is what a
// deployment with no key gets.
func NewCipher(key []byte) *Cipher {
	if len(key) == 0 {
		return &Cipher{}
	}
	if len(key) != KeyBytes {
		// Unreachable: ParseKey is the only door and it is called at startup.
		panic("twofactor: NewCipher needs a key from ParseKey")
	}
	block, err := aes.NewCipher(key)
	...
```
Delete the `crypto/sha256` import from `crypt.go` ONLY (`store.go` uses it for session digests). The panic is the same class as `NewStore`'s existing nil-pool panic and fires at wiring time, before the port is bound. Do NOT make it return a disabled Cipher — silently-off is the failure mode this change exists to delete.

--- 3. `NewStore` takes bytes (`internal/twofactor/store.go:26-31`)

`func NewStore(pool *pgxpool.Pool, key []byte) *Store`. Body unchanged apart from the type.

--- 4. Wiring: the router receives PARSED material, so an unvalidated string cannot reach the cipher by type

`cmd/goen/server.go:56-57`:
```go
	// TOTPKey is the parsed 32-byte key from twofactor.ParseKey; nil disables
	// enrolment. A []byte and not the raw environment string: the format is
	// checked once, at startup, and the type is what stops a second caller
	// deciding it again.
	TOTPKey []byte
```
`server.go:65` and `:103` need no shape change.

`cmd/goen/main.go`: keep `config.TOTPKey string` (config is what the environment said) and add an unexported `totpKey []byte` beside it. Parse inside `checkProductionPosture` (pointer receiver, already), because that function's own doc comment is "refuses a configuration that would serve the site with a security feature silently off" and it already owns the empty-key branch — keeping both halves of one rule in one function is CLAUDE.md #13/#30/#31's lesson:
```go
	if cfg.TOTPKey == "" {
		... existing refuse-or-warn, unchanged ...
	} else {
		key, keyErr := twofactor.ParseKey(cfg.TOTPKey)
		if keyErr != nil {
			return fmt.Errorf("GOEN_TOTP_KEY: %w", keyErr)
		}
		cfg.totpKey = key
	}
```
The format refusal is NOT gated on `cfg.SecureCookies`. A developer who enrols under a passphrase locally would meet a different failure in production; the format is a fact about the key, not about the posture. Say so in the comment.

`newServer` (`main.go:177`) passes `TOTPKey: cfg.totpKey`. `checkProductionPosture` is already called (main.go:313) before `newServer`, so the ordering holds; do not move the call.

--- 5. Make a key mismatch LEGIBLE instead of a mystery lockout (the one behavioural addition, and it is load-bearing for the migration)

Today `Cipher.Open` failing at `store.go:196-198` surfaces through `Verify` → `handler.go:82-85` as `/admin/verify?bad=1`, i.e. "the code is not valid". A staff member whose row was sealed under a different key is told they typed the wrong digits, forever.

- `internal/twofactor/twofactor.go`, in the existing `var (...)` block:
```go
	// ErrSecretUnreadable is a stored credential that does not decrypt: the
	// key changed, or the row was tampered with. It is never a wrong code —
	// nothing the staff member types can fix it, so telling them it is one
	// sends them to the wrong place. Another admin must remove the factor.
	ErrSecretUnreadable = errors.New("twofactor: the stored secret does not open under the configured key")
```
- `store.go:196-198`: `return uuid.UUID{}, nil, 0, fmt.Errorf("%w: %w", ErrSecretUnreadable, err)`.
- `handler.go:82-85`: branch before the generic case —
```go
		if errors.Is(err, ErrSecretUnreadable) {
			h.log.ErrorContext(r.Context(), "totp secret does not open; GOEN_TOTP_KEY does not match the key these rows were sealed under",
				"set", "GOEN_TOTP_KEY")
			http.Redirect(w, r, "/admin/verify?stale=1", http.StatusSeeOther)
			return
		}
```
- `handler.go:163-176` `noticeFor`: add `case r.URL.Query().Get("stale") == "1": return i18n.T(r.Context(), i18n.KeyTOTPSecretUnreadable)`.
- `internal/i18n/chrome_admin_twofactor.go`: ONE declaration, both locales in one `key()` call (the repo's one-declaration rule; `exhaustruct` is enabled for `i18n.Message` and will refuse a missing locale at the line):
```go
	KeyTOTPSecretUnreadable = key("admin.totp.secret_unreadable", Message{
		ZhHant: "這組驗證器已無法讀取(加密金鑰已更換)。請另一位管理員在 /admin/staff 移除後重新設定。",
		En:     "This authenticator can no longer be read (the encryption key changed). Ask another admin to remove it at /admin/staff, then enrol again.",
	})
```
This discloses nothing: the path is reached only for an already-authenticated staff session failing on its OWN credential.

--- 6. BREAKING CHANGE — what happens to existing rows, exactly

Every `staff_totp_credentials.secret_encrypted` in existence was sealed under `sha256(GOEN_TOTP_KEY)`. After this change the SAME environment value produces a DIFFERENT AES key (its decoded bytes, not their hash) — including for an operator who already used `openssl rand -hex 32`. So **every existing row fails `Cipher.Open`**, `Verify` fails, and no enrolled staff member can step up. If every admin is enrolled, nobody can reach `/admin/staff` to remove a credential and SQL against production is the only exit — which is exactly the state `/admin/staff` was built to eliminate ("an admin who lost their phone was locked out permanently and the only fix was SQL against production").

In fact, measured: zero rows exist and CLAUDE.md records goen as undeployed. The migration is empty today, which is why the clean break is affordable and why it must land before the first TLS deployment.

Operator procedure, to be written into `.env.example` and README with the change:
1. Generate the key: `openssl rand -hex 32`.
2. **While the OLD binary is still running**, remove each enrolled factor at `/admin/staff` (that path already ends the holder's sessions). If the back office is already unreachable, `DELETE FROM staff_totp_credentials;` as the owning role.
3. Restart with the new key. Each staff member re-enrols at `/admin/2fa`. Step 2 must come first because `Store.Begin` refuses an already-confirmed credential with `ErrEnrolled`.

NO `GOEN_TOTP_KEY_LEGACY` compatibility path. It is a second key that lives in the environment forever, it re-admits the derivation this change deletes, and removing it later needs a rewrap job (a fifteenth `SECURITY DEFINER` writer, or a hand-run migration over encrypted rows). With zero rows in existence the clean break costs nothing and the compatibility path costs permanently.

--- 7. DOCUMENTATION, corrected in the same commit

`.env.example`, replace lines 140-143 (keep 145-151 as they are):
```
# Encrypts stored TOTP secrets with AES-256-GCM. It must be 32 random bytes,
# given as hex or base64: `openssl rand -hex 32`. It is a KEY and not a
# passphrase — goen refuses to start on anything that does not decode to
# exactly 32 bytes, because a single unsalted hash over a memorable phrase is
# not a key derivation function: it hands an offline attacker every row in
# staff_totp_credentials for the price of a wordlist. A row there is a
# password-equivalent, so this is what stops a database dump alone defeating
# the second factor.
#
# CHANGING IT makes every enrolled credential unreadable. Remove the factors at
# /admin/staff first, then restart with the new key and have staff re-enrol.
```

`README.md:203`, the table cell becomes: "32 random bytes as hex or base64 (`openssl rand -hex 32`), encrypting staff second-factor secrets at rest. Empty disables enrolment; a value that is not 32 bytes refuses to start".
`README.md:209` currently reads "Three of these fail in ways worth naming:" over three bullets. Add a fourth bullet — "`GOEN_TOTP_KEY` refuses a value that is not 32 decoded bytes. It is a key, not a passphrase, and a hash of a passphrase is not a key derivation function." — and change "Three" to "Four". This repository has a guard whose whole subject is a stated count going stale (`TestTheStatedSchemaTotalsAreTheRealOnes`); do not leave the word "Three" over four bullets.

`CLAUDE.md:2560`, after "The secret is AES-256-GCM at rest, keyed by `GOEN_TOTP_KEY`", add: "That key is 32 RAW bytes and a passphrase is refused at startup. It used to be `sha256(whatever was configured)` under a `.env.example` line reading 'any length will do' — a length-normaliser sold as a key derivation function, two lines above the sentence claiming a database dump alone does not defeat the factor. `ecpay.go` had refused a wrong-length key since it was written; the second secret in the same binary accepted anything."

--- 8. Call sites to update (compile-driven, complete list)
- `internal/twofactor/twofactor_test.go:116,152,167,179` — string → `[]byte`; :167's "a different key" must be genuinely different 32 bytes; :179 becomes `NewCipher(nil)`.
- `internal/twofactor/integration_test.go:26` — `const testKey = "a-key-only-this-test-uses"` becomes a `var` holding 32 parsed bytes; :267's `"a-different-key"` becomes a second 32-byte value; :332's `""` becomes `nil`. The other 17 `NewStore(pool, testKey)` sites are unchanged by name.
- No other production caller exists; `internal/db` (sqlc) and `*_templ.go` are untouched.

Run `make verify` (fmt-check → templ-check → squawk → sqlc-check → vet → lint → integration-build-check → test-race), and `make test-integration` for the twofactor suite. No migration and no schema change: this is a key-format change, not a data-model one.


## The lock, and how to see it fail first

Four new tests, all in-package (no test-only interface — `.claude/rules/` forbids one, and none is needed since everything here is a pure function or a config method). Every lock below is proven by mutation per `.claude/rules/testing.md:165` and CLAUDE.md mistake #6; record mutation → red output → restore → green in the PR thread.

T1. `internal/twofactor/twofactor_test.go` — `TestOnlyThirtyTwoRandomBytesIsAKey`, a table over `ParseKey`:
ACCEPT (assert `err == nil` AND that the returned bytes equal `hex.DecodeString` of the literal — NOT merely that err is nil; an implementation returning `sha256(value)` passes an err-only assertion, which is the whole defect):
  - a fixed 64-char hex literal (write it out; do not generate at run time — a case that changes per run is not a fixed fact),
  - the same literal upper-cased,
  - the same literal with a trailing "\n" and surrounding spaces,
  - `base64.StdEncoding` of those same 32 bytes (44 chars, one `=`),
  - `base64.RawStdEncoding` of them (43 chars),
  - `base64.RawURLEncoding` of a 32-byte value containing `-`/`_` producing characters.
REFUSE (assert a non-nil error):
  - `"a-key-from-the-environment"` (the string today's tests use),
  - `"correct horse battery staple"`,
  - `"replace_me"`,
  - 62 hex chars (31 bytes) and 66 hex chars (33 bytes),
  - `strings.Repeat("0", 64)` — the all-identical rule,
  - a 64-char string that is neither valid hex nor valid base64.
DISABLED: `ParseKey("")` returns `(nil, nil)` — assert both, and separately that `NewCipher(nil).Enabled()` is false.
LEAK: for every refused case assert `!strings.Contains(err.Error(), input)` — a startup error must never carry the key.

T2. `internal/twofactor/twofactor_test.go` — `TestTheKeyIsTheDecodedBytesAndNotAHashOfThem`. This is the test that names the defect. Seal a secret with `NewCipher(mustParse(t, hexKey))`. Then build two ciphers by hand in-package (the `aead` field is reachable from the same package, so no exported hook is needed): one from `aes.NewCipher(decodedBytes)` + `cipher.NewGCM` — it MUST open the sealed value; one from `sha256.Sum256([]byte(hexKey))` — it MUST NOT. Asserting only the second half is not enough: without the first, a broken parser that returns garbage also passes.

T3. `cmd/goen/main_test.go` (new file, `package main`) — `TestStartupRefusesATOTPKeyThatIsNotAKey`, table over `config` calling `cfg.checkProductionPosture(slog.New(slog.DiscardHandler))`:
  - `{TOTPKey: "", SecureCookies: true}` → error (existing rule, kept as a regression case),
  - `{TOTPKey: "", SecureCookies: false}` → nil, and `cfg.totpKey == nil`,
  - `{TOTPKey: "a-key-from-the-environment", SecureCookies: true}` → error,
  - `{TOTPKey: "a-key-from-the-environment", SecureCookies: false}` → **error too** — the format rule is not gated on posture, and this row is what stops someone reintroducing the gate,
  - `{TOTPKey: <valid 64-hex>, SecureCookies: false}` → nil and `len(cfg.totpKey) == 32`,
  - `{TOTPKey: <valid 64-hex>, SecureCookies: true}` → nil, with `t.Setenv("GOEN_BASE_URL", "https://goen.example")` because the same function refuses a missing base URL under Secure cookies — without the Setenv this row fails for the wrong reason and proves nothing.
Assert every returned error mentions `GOEN_TOTP_KEY` and contains none of the offending value.

T4. `internal/twofactor/integration_test.go` — `TestACredentialSealedUnderAnotherKeyIsNotAWrongCode`: enrol and confirm through a `Store` built on key A, then build a second `Store` on the same pool with key B (32 different bytes) and call `Verify` with a code computed from the real secret. Assert `errors.Is(err, ErrSecretUnreadable)` and NOT `errors.Is(err, ErrBadCode)`. This is what makes the migration lockout legible rather than a mystery; it also covers the `handler.go` branch's precondition. Bind the assertion to the sentinel, not to the error text (mistake #32: matching on message text is forbidden here).

MUTATIONS — each must be seen to apply before the run (CLAUDE.md false-green mode #3: "the mutation must be seen to apply, not assumed"; confirm with `grep -n` on the target file, not with a count on a pattern):
  M1 — in `ParseKey`, replace the whole body after the empty check with `sum := sha256.Sum256([]byte(value)); return sum[:], nil` (i.e. restore today's behaviour). Expected: T1's refuse rows RED, T1's accept rows RED on the byte-equality assertion, T2 RED, T3's two passphrase rows RED. If T1's accept rows stay GREEN, the byte-equality assertion was not written and T1 is decoration.
  M2 — delete the `twofactor.ParseKey` call from `checkProductionPosture` and assign `cfg.totpKey = []byte(cfg.TOTPKey)`. Expected: T3 RED only. T1/T2 staying green here is CORRECT and expected — record that explicitly so a reviewer does not read it as a weak lock.
  M3 — remove the all-identical refusal. Expected: T1's `strings.Repeat("0", 64)` row RED. If it stays green, delete that row rather than keeping a rule nothing holds.
  M4 — in `store.go`, drop the `ErrSecretUnreadable` wrap and return the raw error. Expected: T4 RED.

WHAT THESE TESTS MUST NOT CLAIM. `subtle.ConstantTimeCompare` at `internal/twofactor/totp.go:84` is documented as un-provable by behaviour — "the one property no behavioural test can prove — a timing test would be flaky and worse than the guarantee by construction — and the mutation is recorded green rather than dressed up". No test in this change may assert on timing or claim to cover it, and the PR must not list it as newly locked. Equally, T1–T4 prove that the DERIVATION is a key rather than a hash of a phrase; they cannot and do not prove that the operator's key is random. That limit is why the all-identical rule is the only randomness check: it is decidable, and an entropy estimator over 32 bytes would be an opinion dressed as a gate.
