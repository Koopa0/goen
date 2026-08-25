# ratelimit-unbounded

**Verdict** CONFIRMED · **Severity** high · **Origin** external report (verified this round)

**Files** internal/ratelimit/ratelimit.go, internal/account/handler.go, internal/account/reset_handler.go, internal/ratelimit/ratelimit_test.go, cmd/goen/server.go, internal/twofactor/handler.go, internal/product/handler.go, internal/contact/handler_integration_test.go, internal/cart/integration_test.go


## Root cause

`Config.TTL`'s own comment (ratelimit.go:23-26) states the invariant the code does not implement:

    // TTL is how long an idle key is kept. It bounds memory: without it the map
    // grows with every distinct IP, which is a slower version of the attack
    // this package exists to stop.

TTL bounds the NUMBER of live keys to (insertion rate x TTL). It says nothing about the SIZE of a key, and nothing caps the product. The author reasoned about IP-keyed limiters — where a key is at most 45 bytes and its cardinality is bounded by the attacker's address space — and the conclusion was then inherited by the two limiters whose keys are raw form fields. This is CLAUDE.md's mistake #31/#34 exactly: a comment naming the rule sitting directly above code that does not implement it, written correctly under a constraint (IP keys) that later lifted (email keys) with nothing re-reading it.

Two design errors underneath, and they are separable:

1. The package accepts a key it does not bound, from callers it does not control — while its own doc comment (ratelimit.go:97-99) states the opposite principle for the other input it takes: "A header read on faith hands every attacker an unlimited supply of keys, so no header is read here." A form value read on faith is the same sentence. `ClientIP` was hardened; `Allow`'s key was not.

2. The sweep is triggered by the event it is meant to bound. `evictLocked` runs on every miss (ratelimit.go:62-65: "Swept here rather than on a ticker: this is the only moment the map can grow"), which is true and is exactly why it is the wrong trigger: work proportional to N, performed once per unit of N, is O(N^2). Worse, during a sustained attack the scan deletes nothing at all, because no entry is older than TTL until the attack has run for TTL.


## Reproduction — the evidence this rests on

EXECUTED, not argued — three measurements plus a live-server probe.

(1) THE KEY IS AN UNBOUNDED ATTACKER-SUPPLIED STRING. `internal/account/handler.go:142`:

    email := r.PostFormValue("email")
    // Before Authenticate: argon2 at 64 MiB is the cost this limit protects.
    if retryAfter, ok := h.signinLimit.Allow("account:" + strings.ToLower(strings.TrimSpace(email))); !ok {

No validation of any kind runs between `PostFormValue` and `Allow`. Same shape at `internal/account/reset_handler.go:35`: `h.resetLimit.Allow("forgot:" + normaliseForLimit(email))`, where `normaliseForLimit` (reset_handler.go:99-101) is `strings.ToLower(strings.TrimSpace(email))` and nothing else. The only bound is `web.MaxFormBytes = 64 << 10` on the whole body (`internal/web/web.go:18,23`).

Confirmed against the RUNNING dev server — a 60 KiB address is accepted and keyed:

    $ POST /signin  email=('a'*60000)+"@example.com"  password=x
    status 422 bodylen 70792

422 (not 400/413) means it passed the limiter, created a ~60 KiB map key, and reached `Authenticate`.

By contrast `internal/newsletter/handler.go:43-52` — the one handler that got this right — runs `email.Clean` then `Validate(addr)` (`email.Max = 254`, internal/email/email.go:13) BEFORE `h.limit.Allow("newsletter:" + addr)`. The precedent exists in the tree; the two auth handlers do not follow it.

(2) THE PER-IP GUARD BOUNDS THE RATE BUT NOT THE TOTAL. 30 sequential POSTs to /signin with 30 distinct emails, against the live server:

    [422 x20, 429 x10]

So `authLimit` (Burst 20, Every 3s — cmd/goen/server.go:66-68) admits exactly 20 immediately, then 1 per 3s: 20 + 3600/3 = 1220 distinct account keys per IP per hour. `signinLimit`'s TTL is 1h (internal/account/handler.go:48-50), so none of them expires while the attack runs.

(3) MEASURED MEMORY, temporary probe in internal/ratelimit (now deleted), 1220 keys/IP-hour at 60 KiB each:

     1 attacking IP,  1220 keys of 61440 B: wall= 31.0ms  heapHeld=   76.4 MiB (65667 B/key)
     5 attacking IPs, 6100 keys of 61440 B: wall=247.1ms  heapHeld=  382.1 MiB (65683 B/key)
    20 attacking IPs,24400 keys of 61440 B: wall=  2.86s  heapHeld= 1528.4 MiB (65683 B/key)

ONE source IP pins 76 MiB of resident heap for a full hour, at a sustained upload cost of 1220 x 60 KiB / 3600s = ~21 KB/s. Five IPs: 382 MiB. The bytes are transient on the wire and resident for an hour — that is the amplification, ~3600:1 in time.

(4) MEASURED O(N^2), same probe, short keys, `Every: 1s, Burst: 1, TTL: 1h`:

    N=  1000  wall= 11.84ms  per-key= 11.8us  heapHeld=0.15 MiB (157 B/key)
    N=  5000  wall=100.10ms  per-key= 20.0us  heapHeld=0.89 MiB (187 B/key)
    N= 10000  wall=316.75ms  per-key= 31.7us  heapHeld=1.79 MiB (187 B/key)
    N= 20000  wall=  1.306s  per-key= 65.3us  heapHeld=3.59 MiB (188 B/key)
    N= 40000  wall=  5.162s  per-key=129.1us  heapHeld=7.16 MiB (187 B/key)

Textbook quadratic: N doubles, wall time 4x, per-key cost 2x. Cost of ONE insert into an already-large map, measured directly (map pre-populated, then a single `Allow` of a fresh key):

    one insert into a map of  5000 live keys:  32.1us
    one insert into a map of 20000 live keys: 133.0us
    one insert into a map of 40000 live keys: 274.3us
    one insert into a map of 80000 live keys: 497.8us

Every microsecond of that is spent inside `l.mu` (ratelimit.go:58-59), so it serialises against every other `Allow` on the same limiter. My first probe run, which included N=200000, had to be killed at the 2-minute timeout — that is the curve, not a hang.

Note the sweep does no useful work during an attack: `evictLocked` (ratelimit.go:82-88) only deletes entries older than TTL, and while keys are arriving continuously nothing is older than TTL for the first hour. So it is a full O(N) scan that frees zero for the entire attack window.

ADJUDICATION between the two prior readings. The "nit" reading is wrong and the P2 reading is right on severity but has the mechanism backwards in emphasis:
 - The O(N^2) half — the part both prior passes argued about — is REAL and measured, but it is the SECONDARY term. Reaching 24k keys needs ~20 source IPs (1220/IP/hour); at that size a new key costs ~165us of held mutex. Annoying, not fatal, and it needs distributed capability.
 - The BYTES-PER-KEY half is the sharp one and neither pass named it. It needs ONE IP, no botnet, no IPv6 trick: 76 MiB/hour/IP of pinned heap for 21 KB/s of upload. That is an ordinary single-host OOM path against a one-binary deployment, and it is silent — nothing logs the key, `/admin/health` has no counter for it, and the memory survives an hour past the last request.
So: CONFIRMED, with the weight moved from N^2 to bytes-per-key.

NOT a recorded decision. CLAUDE.md's only statement on limiter state is "The state is per process, so N replicas allow N times the rate. That is a weakening rather than a hole" — that is about rate ACCURACY, not memory. `docs/roadmap.md` and `docs/reviews/` carry no queued item for limiter map growth (only H14, the trusted-proxy fix, and M4, the contact-form limit — both closed).

Other limiter keys checked and found bounded: `totp:`+u.ID (twofactor/handler.go:76,145), `ask:`+u.ID (product/handler.go:209), `findorder:`+ClientIP (cart/handler.go:800), `oauth:`+ClientIP (account/handler.go:695), bare ClientIP (contact/handler.go:52, middleware.go). Those are UUIDs or IP literals. Only the two auth-email keys are attacker-shaped strings.

Working tree: my probe file is deleted; `git status --porcelain internal/ratelimit` is empty and `go test ./internal/ratelimit/` passes. Four untracked files remain that are NOT mine and that I deliberately did not touch, since sibling agents in this review appear to be mid-flight on them: internal/admin/zz_probe_integration_test.go, internal/returns/zz_probe_integration_test.go, migrations/002_probe.up.sql, migrations/002_probe.down.sql.


## Blast radius

One process-wide OOM kills the whole site, because goen is one binary: storefront, checkout, payment webhook receiver and /admin all die together. An in-flight Stripe webhook delivery meets a dead process and retries; a customer mid-checkout loses the session.

Cost to the attacker, measured: a single source IP, ~21 KB/s of sustained upload, no authentication, no account, no botnet. 76 MiB/hour/IP. Five IPs reach 382 MiB. Twenty reach 1.5 GiB and additionally start holding the sign-in mutex for ~165us per new key.

Silent in every direction. The key is never logged (`middleware.go` logs path and retry_after; `handler.go:143` logs "sign-in throttled by account" with no key). `/admin/health` derives its figures from outbox/reservation/projection work and has no limiter counter. `Limiter.Size()` exists but has no caller outside tests. The operator sees RSS climb and an eventual kill, with nothing naming the cause, and the memory persists a full hour after the last request — so by the time anyone looks, the traffic that caused it has stopped.

Frequency: reachable on every request to POST /signin and POST /forgot, both unauthenticated, both linked from the site footer/header. Not a race, not a rare path.

What is NOT in the radius: no data is disclosed, no money moves, no authorisation is bypassed. This is availability only. And the per-IP Guard does cap the rate, so it is a slow build-up over an hour rather than an instant kill — which is why it is high and not critical.


## Fix

Two independent changes. Do BOTH: (a) alone leaves the package trusting its callers, (b) alone leaves the two handlers keying on 64 KiB strings if any future caller forgets.

--- (a) internal/ratelimit/ratelimit.go — the package stops trusting its key ---

1. Add, next to the existing consts/types:

    // MaxKeyBytes bounds one key. A key is an IP, a UUID or a normalised email;
    // anything longer is a form field nobody validated, and a map entry sized by
    // an attacker is what TTL alone does not bound.
    const MaxKeyBytes = 128

2. In `Allow` (ratelimit.go:55), as the FIRST statement, before `now := time.Now()`:

    key = clampKey(key)

   and add:

    // clampKey keeps a long key distinct without keeping its bytes.
    func clampKey(key string) string {
        if len(key) <= MaxKeyBytes {
            return key
        }
        sum := sha256.Sum256([]byte(key))
        return key[:MaxKeyBytes-44] + base64.RawURLEncoding.EncodeToString(sum[:])
    }

   sha256 (not truncation) because two 64 KiB submissions differing only in their
   last byte MUST NOT collapse into one bucket — that would let one attacker's
   requests spend a victim's allowance. The readable prefix is kept so a future
   log line naming a key still says which limiter and which shape refused.
   Imports: `crypto/sha256`, `encoding/base64`.

3. Cap the key COUNT. Add to `Config`:

    // MaxKeys bounds how many keys are tracked. At the cap the least recently
    // seen key is dropped: a limiter that forgets an idle key allows one extra
    // burst, which is strictly better than a limiter the kernel kills.
    MaxKeys int

   Extend `New`'s validation (ratelimit.go:46-48) to `|| cfg.MaxKeys < 1` and
   update the panic string. Every construction site must then name its bound —
   cmd/goen/server.go:66,78,83,95; internal/account/handler.go:48,51;
   internal/twofactor/handler.go:37; internal/product/handler.go:37; and the test
   helpers in internal/cart/integration_test.go and
   internal/contact/handler_integration_test.go. Suggested production values:
   authLimit/findLimit/contactLimit/signupLimit (IP-keyed) 65536; signinLimit and
   resetLimit (email-keyed) 65536; twofactor and product (user-keyed) 8192.
   Do NOT default a zero MaxKeys inside New — CLAUDE.md's rule is that a number
   nobody states is a number that stops being measured, and New already refuses
   every other unset field.

4. Amortise the sweep so it is O(N) per sweep interval, not O(N) per insert. Add
   two unexported fields to `Limiter`:

    lastSweep time.Time
    sweeps    int // for a test to assert the sweep is amortised

   Rewrite the miss branch (ratelimit.go:61-68) as:

    b, found := l.buckets[key]
    if !found {
        // Swept on an interval, not on every miss: work proportional to N done
        // once per new key is O(N^2), and during a sustained arrival of new keys
        // nothing is yet older than TTL, so the scan frees nothing while it runs.
        if now.Sub(l.lastSweep) >= l.cfg.TTL/8 || len(l.buckets) >= l.cfg.MaxKeys {
            l.evictLocked(now)
            l.lastSweep = now
        }
        b = &bucket{limiter: rate.NewLimiter(rate.Every(l.cfg.Every), l.cfg.Burst)}
        l.buckets[key] = b
    }

5. Make `evictLocked` (ratelimit.go:82-88) enforce the cap in the SAME scan it
   already pays for — track the minimum `seen` and its key as it goes, increment
   `l.sweeps`, and after the loop, while `len(l.buckets) >= l.cfg.MaxKeys`, delete
   the oldest key found. One extra pass per over-cap insert is acceptable because
   the interval gate has already made sweeps rare.

MUST NOT: do not swap the map for `sync.Map` (forbidden by
.claude/rules/concurrency.md without profiling); do not move limiter state to
PostgreSQL (CLAUDE.md: "would put a write on the login path — its own
amplifier"); do not introduce an interface to make any of this testable
(.claude/rules/interfaces.md — tests are never a reason); do not add a background
ticker goroutine (the package has no lifecycle and `New` has no ctx, and
concurrency.md forbids a goroutine with no exit plan).

--- (b) internal/account — validate before keying, as newsletter already does ---

`internal/newsletter/handler.go:43-52` is the precedent: `email.Clean`, then
`Validate`, then `Allow`. Apply it to the two auth handlers.

6. internal/account/handler.go:141-142, in `SignIn`, between reading the form and
   calling the limiter: replace `strings.ToLower(strings.TrimSpace(email))` with
   `email.Clean(email)` (internal/email/email.go:17) and, if the cleaned address
   is longer than `email.Max` (254), answer 422 with the ordinary bad-credentials
   view and RETURN before touching the limiter. It must be the SAME 422 body the
   wrong-password branch renders (handler.go:154-159) — a distinct response here
   would tell a prober something about address shape, and `/forgot`'s
   indistinguishable-answer rule (CLAUDE.md) is the same reasoning.

7. internal/account/reset_handler.go:35 and 99-101: make `normaliseForLimit` call
   `email.Clean` and refuse anything over `email.Max` the same way — for `Forgot`
   the refusal must be the identical `303 /forgot?sent=1` the success path writes
   (reset_handler.go:52), or the endpoint becomes an oracle for address length.

An address over 254 bytes cannot belong to any row: `users.email` is bounded by
the schema and the SMTP forward-path limit that `email.Max` cites. Rejecting it
costs no legitimate customer anything.


## The lock, and how to see it fail first

Four locks, each with the mutation that must be SEEN to go red first. `internal/ratelimit/ratelimit_test.go` is `package ratelimit`, so it may read `l.buckets` and `l.sweeps` directly — `Size()` (ratelimit.go:90-91, "for a test to assert eviction happens") is the existing precedent for an unexported hook read by a same-package test. No new interface, no fake.

1. TestALongKeyIsNotStoredWhole (internal/ratelimit/ratelimit_test.go)
   `l := New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: 1000})`;
   `l.Allow("account:" + strings.Repeat("a", 64<<10))`; range `l.buckets` and
   assert every stored key is `<= MaxKeyBytes` long.
   MUTATION: delete the `key = clampKey(key)` line from `Allow`. Must go red with
   a stored key of 65544 bytes. Prove it applied by running the test and reading
   the failure message — do not assume, per CLAUDE.md's false-green mode #3.

2. TestTwoLongKeysStayDistinct (same file)
   Two 64 KiB keys identical except for their final byte; `Allow` each; assert
   `l.Size() == 2`.
   MUTATION: change `clampKey` to `return key[:MaxKeyBytes]` (truncate instead of
   hash). Must go red with `Size() == 1`. This is the lock that stops the fix
   from turning a memory bug into a shared-bucket bug — without it, the cheapest
   wrong fix passes test 1.

3. TestTheKeyCountIsCapped (same file)
   `New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: 100})`;
   insert 1000 distinct keys in a tight loop (well inside the TTL, so the TTL
   sweep can free nothing — that is the point); assert `l.Size() <= 100`.
   MUTATION: remove the over-cap deletion from `evictLocked`. Must go red with
   `Size() == 1000`. Note the fixture MUST stay inside the TTL: a fixture that
   let keys age past it would be freed by the sweep and would pass with the cap
   deleted — CLAUDE.md mistake #33's false-green, "a fixture that does not reach
   the state under test passes for a reason that has nothing to do with the fix".

4. TestTheSweepIsAmortised (same file)
   `New(Config{Every: time.Minute, Burst: 1, TTL: time.Hour, MaxKeys: 1 << 20})`;
   insert 10000 distinct keys; assert `l.sweeps <= 10`.
   MUTATION: remove the `now.Sub(l.lastSweep) >= l.cfg.TTL/8` gate so the sweep
   runs on every miss. Must go red with `l.sweeps == 10000`.
   Counting sweeps and NOT wall time is deliberate: a timing assertion would be
   flaky on a loaded machine and would be a worse lock than the guarantee it
   claims — the call CLAUDE.md records for the constant-time compare in
   internal/twofactor and the clock-skew fix in /admin/messages.

5. TestAnOverlongAddressIsRefusedIdentically (internal/account, handler test)
   Drive `SignIn` through an `httptest` request whose email field is 60 KiB and
   whose password is wrong; assert status 422 AND that the response body is
   byte-identical to the body produced by a normal wrong-password submission
   (same fixture, a 20-byte unknown address). Then the same shape for `Forgot`:
   a 60 KiB address must produce the identical `303 -> /forgot?sent=1`.
   MUTATION: delete the length check added in fix step 6/7. The status assertion
   alone will NOT go red — SignIn already answers 422 for an unknown address,
   which is exactly why it shipped — so the lock is the BODY comparison plus a
   direct assertion that the limiter never saw the long string: build the handler
   with a limiter of `MaxKeys: 10`, submit one overlong address, and assert
   `Size() == 0` for the account key. That assertion is red without the fix and
   green with it. An assertion coarser than the error it sits beside is not a
   weak lock, it is no lock (CLAUDE.md mistake #34).

Regression: `go test ./internal/ratelimit/ ./internal/account/ -count=1` and
`make verify`. The existing `TestIdleKeysAreEvicted` (ratelimit_test.go:80-95,
TTL 20ms, 100 keys, asserts Size drops to 1) must still pass with the interval
gate: `TTL/8` is 2.5ms there and the test sleeps past the full 20ms TTL before
inserting its next key, so the gate opens. Confirm that rather than assume it —
it is the one existing test the sweep change can break.
