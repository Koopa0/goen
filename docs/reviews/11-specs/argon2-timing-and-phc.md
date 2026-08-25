# argon2-timing-and-phc

**Verdict** PARTLY · **Severity** medium · **Origin** external report (verified this round)

**Files** internal/account/store.go, internal/account/account.go, internal/account/account_test.go, internal/account/handler.go, cmd/goen/server.go, migrations/001_initial_schema.up.sql


## Root cause

The dummy path and the real path are two different functions with two different input contracts, and nothing narrows the input to the intersection. `burnHashTime` is `HashPassword`, bounded by `MaxPasswordBytes` because it must PRODUCE a storable hash; `VerifyPassword` is unbounded because it must READ one. Equal cost is asserted by a comment — "burnHashTime makes a wrong email cost the time a wrong password does" (store.go:83) — over an implementation that only holds inside that bound, and `burnHashTime` discards the very error that says it did nothing (`_, _ = HashPassword(password)`). No caller closes the gap: `SignIn` passes `r.PostFormValue("password")` through raw (handler.go:139 -> 148), while `PasswordError` (account.go:233-243) enforces `len(s) > MaxPasswordBytes` only on the registration and reset forms, which is why the asymmetry is invisible everywhere it is checked.

This is CLAUDE.md mistake #31 — "a comment that states the rule its own predicate does not implement" — with the comment and the failing line three lines apart, and #13's shape underneath it: two individually correct halves that disagree, which every guard in this repository is blind to because they all ask what is ABSENT.

(b)'s root cause is separate and minor: `VerifyPassword` trusts the stored string's parameters because the only producer is trusted, and states that nowhere.


## Reproduction — the evidence this rests on

(a) CONFIRMED — executed twice, against the running dev server and in-process.

The claim's file:line is slightly off and the correction matters. `internal/account/account.go:57-59` is `HashPassword`, not `Authenticate`:

    func HashPassword(password string) (string, error) {
        if len(password) > MaxPasswordBytes {          // account.go:58, MaxPasswordBytes = 512 (account.go:44)
            return "", errors.New("account: password too long to hash")

`Authenticate` reaches it only through the dummy path (`internal/account/store.go:60-81`):

    row, err := s.q.UserByEmail(ctx, email)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            burnHashTime(password)                      // store.go:64
            return User{}, ErrBadCredentials
    ...
    if !row.PasswordHash.Valid { burnHashTime(password); return User{}, ErrBadCredentials }   // store.go:69-72
    if !VerifyPassword(row.PasswordHash.String, password) { return User{}, ErrBadCredentials } // store.go:74

    // burnHashTime makes a wrong email cost the time a wrong password does.
    func burnHashTime(password string) { _, _ = HashPassword(password) }   // store.go:83-86

So the order is: DB read FIRST, then the branch. `burnHashTime` -> `HashPassword` returns at once for 513 bytes; `VerifyPassword` (account.go:75-101) has NO length bound and runs argon2 unconditionally. The two paths are not the same path.

Measured over the running server (registered timing-probe@goen.invalid via POST /register, then deleted it):

    PW=$(python3 -c "print('a'*513)")
    curl -w "code=%{http_code} t=%{time_total}" -X POST http://127.0.0.1:9700/signin --data-urlencode "email=$e" --data-urlencode "password=$PW"

    timing-probe@goen.invalid   code=422 t=0.031532     nobody-xyz-1@goen.invalid  code=422 t=0.002004
    timing-probe@goen.invalid   code=422 t=0.031205     nobody-xyz-2@goen.invalid  code=422 t=0.002059
    timing-probe@goen.invalid   code=422 t=0.029623     nobody-xyz-3@goen.invalid  code=422 t=0.001747
    timing-probe@goen.invalid   code=422 t=0.030030     nobody-xyz-4@goen.invalid  code=422 t=0.001690

4/4 vs 4/4, no overlap, ~17x. Control at 16 bytes (same requests, legal length) — the dummy path works exactly as its comment claims:

    timing-probe@goen.invalid   code=422 t=0.031335     nobody-xyz-5@goen.invalid  code=422 t=0.032327
    timing-probe@goen.invalid   code=422 t=0.029983     nobody-xyz-6@goen.invalid  code=422 t=0.030391

In-process (temporary test, since deleted): burnHashTime(513)=75ns vs VerifyPassword(513)=28.016483ms; at 512 bytes both ~28-31ms. The oracle exists only above the bound. It is observable, not theoretical.

(b) REFUTED as a reachable hole; the mechanism is real and unreachable. Executed:
- Unbounded parse is real. account.go:82-88 does `fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads)` and passes the results straight to `argon2.IDKey` (account.go:99). Only `len(want)` is bounded (account.go:95-96). Confirmed by temporary test: `m=65536,t=0,p=4` -> PANIC "argon2: number of rounds too small"; `t=3,p=0` -> PANIC "argon2: parallelism degree too low" (x/crypto/argon2 `deriveKey` panics on time<1 and threads<1); `m=1048576` (1 GiB) is honoured verbatim and took 644.4ms in one call.
- But nothing attacker-controlled can reach it. The only three writers of `users.password_hash` are store.go:41 (`Register`), store.go:189 (`ChangePassword`) and reset.go:77 (`Reset`), and all three write `HashPassword`'s output, which is the compile-time constants m=65536,t=3,p=4 (account.go:64-68). `CreateUserFromIdentity` (store.go:661) writes NULL; `secure_promoted_account` (migrations/001:3439) writes NULL.
- The back office cannot write it at all: migrations/001_initial_schema.up.sql:3755-3757 `REVOKE INSERT, UPDATE ON users FROM admin;` then grants only `(email, full_name, role)` / `(role, full_name)`. `store` holds the column (001:4554-4556) but `store` is the application, which only ever writes HashPassword output.
- So a hostile PHC needs a direct write as `goen`/`store_svc` — and that actor can instead set the hash to a password they know and sign in as anyone, which strictly dominates a panic. The panic is also caught: `recoverPanic` (cmd/goen/server.go:421-430) turns it into a 500, not a process death.


## Blast radius

(a) Unauthenticated, remote, no session, silent. One POST /signin with a 513-byte password tells an attacker whether an address has a usable password account: ~1.7-2.1ms means no such account (or a Google-only account, since `!row.PasswordHash.Valid` at store.go:69-72 also takes burnHashTime), ~29.6-31.5ms means an account with a password. So it discloses HOW somebody signs in as well as whether they exist. Bounded only by the per-IP limiter (cmd/goen/server.go:66-68, `Every: 3s, Burst: 20`, wired at server.go:194) — about 1,200 addresses/hour/IP sustained. The per-account limiter (handler.go:141-146, keyed `"account:"+email`) NEVER fires against enumeration, because every probe uses a different address and gets a fresh bucket; I saw it fire only when I hit one address six times. CLAUDE.md records the limiter state is per process, so N replicas allow N times the rate, and distributed probing scales linearly. Every probe is an ordinary 422 — indistinguishable in the logs from a mistyped password — so the shop never sees it. What it yields is the customer list of a storefront: which addresses shop here, feeding credential stuffing and targeted phishing at exactly those people. This contradicts a goal the repo states twice: `ErrBadCredentials`' own comment (account.go:26) "one error for both, or the form enumerates accounts", and CLAUDE.md:1660 "`/forgot` answers IDENTICALLY whether or not the address belongs to anybody — it is otherwise a way to enumerate who has an account."

(b) Nobody, at present. It is defence in depth against a future writer of `users.password_hash` that does not go through `HashPassword`. Worst case even then is a recovered panic (500) or one large allocation per request, on a path that already requires database write access.


## Fix

FIX (a) — one guard, in `Store.Authenticate`, `internal/account/store.go:60`, as the FIRST statement, before `s.q.UserByEmail`:

    func (s *Store) Authenticate(ctx context.Context, email, password string) (User, error) {
        // Refused BEFORE the read, or the two outcomes are distinguishable: at this
        // length burnHashTime's own HashPassword returns instantly (account.go:58)
        // while VerifyPassword has no such bound, which made a wrong email 1.8ms and
        // a right one 30ms. Behaviour-preserving: no stored hash can have been made
        // from a password this long, because HashPassword refuses to make one.
        if len(password) > MaxPasswordBytes {
            return User{}, ErrBadCredentials
        }
        row, err := s.q.UserByEmail(ctx, email)
        ...

Why before the read and not after: after the read, the two branches still differ by the query itself, and the fast return still separates them. Before the read, both addresses take the identical path and no database work happens either.

The invariant this rests on, and it must be named in the comment: `store.go:41`, `store.go:189` and `reset.go:77` are the only writers of `users.password_hash` and all three go through `HashPassword`, which refuses >`MaxPasswordBytes` (account.go:57-59). If that bound is ever removed from `HashPassword`, this early return becomes wrong and must be removed with it.

It MUST NOT be done in `SignIn` (handler.go:148). `Authenticate` has three callers — handler.go:148 (sign-in), handler.go:480 (ChangeEmail re-verify) and handler.go:588 (ChangePassword re-verify) — and a guard in one handler leaves the same oracle in the other two. Fix it in the store, once.

It MUST NOT be done by making `burnHashTime` hash a legal-length substitute while leaving the read in place: that spends 64 MiB and ~28ms per probe on input that can never succeed, which is precisely the memory-exhaustion surface `internal/ratelimit`'s package doc exists to bound ("argon2id runs at 64 MiB a hash, so the limiter must run BEFORE the hash or it defends nothing").

Optional, same commit or the next, NOT a fix for the finding: `burnHashTime` (store.go:83-86) is a function whose entire purpose is to spend time and which silently spends none when `HashPassword` errors. Having it hash a fixed-length substitute when the input is out of bounds makes it unable to no-op for any future caller. Keep the store guard regardless — that is what closes the hole.

FIX (b) — hardening only, and it must be recorded as hardening rather than as closing a finding, since no path reaches it. In `VerifyPassword`, `internal/account/account.go`, immediately after the Sscanf at line 82-88 and before `argon2.IDKey` at line 99:

    // The only producer is HashPassword, so these are its constants; bounded
    // anyway, because a row this function cannot survive is a 500 for the
    // customer and x/crypto panics on time<1 or threads<1.
    if time < 1 || time > 8*argonTime || threads < 1 || memory > 8*argonMemory {
        return false
    }

Return false, never an error: `VerifyPassword` has one caller and its contract is a boolean, and an unparseable hash already returns false at four other points in the same function.


## The lock, and how to see it fail first

LOCK FOR (a) — deterministic, no wall clock. A timing assertion must NOT be the lock: CLAUDE.md already rules on that shape for the constant-time compare in `internal/twofactor` ("a timing test would be flaky and worse than the guarantee by construction"). The measurements above belong in the commit message as evidence.

Assert instead that the guard runs BEFORE the database read, which is exactly what makes the two paths identical. `pgxpool.New` does not dial until a query runs, so a pool pointing at nothing distinguishes the two orders with no database, no fixture and no interface (respecting "an interface introduced so a test can substitute something" — none is added).

Add to `internal/account/account_test.go` (already `package account`, so `NewStore` and `MaxPasswordBytes` are in scope):

    // A password longer than MaxPasswordBytes is refused before the read, or a
    // 513-byte probe separates "this address has an account" from "it does not":
    // burnHashTime returns instantly at that length and VerifyPassword does not.
    func TestAnOverLongPasswordIsRefusedBeforeTheRead(t *testing.T) {
        pool, err := pgxpool.New(t.Context(), "postgres://nobody@127.0.0.1:1/nothing")
        if err != nil { t.Fatal(err) }
        t.Cleanup(pool.Close)
        _, err = NewStore(pool).Authenticate(t.Context(), "anyone@example.com",
            strings.Repeat("a", MaxPasswordBytes+1))
        if !errors.Is(err, ErrBadCredentials) {
            t.Fatalf("Authenticate reached the database for an over-long password: %v", err)
        }
    }

HOW TO SEE IT FAIL FIRST — the mutation is already executed, against the unfixed tree. I ran exactly this call on HEAD (guard absent, which IS the mutation) via a temporary file and it returned:

    Authenticate(513-byte, dead pool) -> read user: context canceled   (is ErrBadCredentials: false)

so the assertion is RED today and goes GREEN only once the guard is in place. After applying the fix, re-prove by deleting the four added lines from `Authenticate` and re-running: it must report `read user: ...` again. Deleting the guard from `SignIn` instead of the store must ALSO leave it red — that is what stops the fix being made one handler at a time.

SECOND, behaviour preservation, and state it as that rather than as the lock (it passes today): an integration case in `internal/account/integration_test.go` (`//go:build integration`, `package account_test`) that seeds a user with a password, then calls `Authenticate` with a `MaxPasswordBytes+1` password for that address and for an address with no row, and asserts both return `ErrBadCredentials`. It proves the early return did not change any outcome; it cannot see the defect, and must not be presented as if it could.

LOCK FOR (b): none required, because nothing reaches it. If the hardening is applied anyway, the honest test is a table over `VerifyPassword` asserting `false` (not a panic) for `t=0`, `p=0` and `m=4294967295`, with the mutation being removal of the bound check — measured today, `t=0` and `p=0` panic and the recovered panic is what the test would catch. Do not add an integration test that plants a hostile PHC row: `admin` cannot write the column and `store` writing one is not a state the application can produce, which is CLAUDE.md's "a fixture for a state the application cannot produce" (mistake #31's tail, #26 from the other side).
