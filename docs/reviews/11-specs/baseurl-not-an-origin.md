# baseurl-not-an-origin

**Verdict** CONFIRMED · **Severity** medium · **Origin** external report (verified this round)

**Files** cmd/goen/main.go, internal/web/sitepath.go, internal/payment/stripe.go, internal/account/google.go, internal/email/notify.go, internal/email/newsletter.go, internal/email/orders.go, internal/site/sitemap.go, internal/ui/pages/jsonld.go


## Root cause

`checkProductionPosture`'s own doc comment (`cmd/goen/main.go:125-127`) states the contract it does not implement: "refuses a configuration that would serve the site with a security feature silently off. SecureCookies is the production signal." A `http://` base URL under Secure cookies is precisely a security feature silently off — TLS, for the links that carry account-recovery tokens — and the predicate underneath asks only `os.Getenv(...) == ""`. That is CLAUDE.md's mistake #31 exactly: "a comment that states the rule its own predicate does not implement", where the two halves are a comment and the line under it rather than two functions in different files.

Underneath that, the deeper cause is that "is this string a usable origin?" has no single definition in the repository, so it was answered three times by three people to three different standards — `!= ""`, `HasPrefix "http"` in `internal/payment`, `HasPrefix "http"` in `internal/account` — and never at all in the six mail-link builders, the sitemap and the JSON-LD. `HasPrefix "http"` is not a scheme test; it is a five-character string test that `httpx`, `httpfoo` and `httpsss` all pass. This is the shape CLAUDE.md keeps recording under `localized_name`, `committed_orders` and `order_amount_owed`: "the rule would otherwise be copied into [each caller], and the one that forgot would be whichever was written next."

The prior review round closed the absence question (M5: is it set?) and nobody asked the shape question (is it an origin?). Non-empty was mistaken for valid.


## Reproduction — the evidence this rests on

EXECUTED, three ways. No part of this is reasoning-only.

**1. The only check is non-emptiness.** `cmd/goen/main.go:141-145` is the whole of it:

```go
if cfg.SecureCookies && os.Getenv("GOEN_BASE_URL") == "" {
    return errors.New("GOEN_BASE_URL is required: it is the origin in every " +
        "link goen mails and every URL it gives Stripe, and guessing it from " +
        "the listen address produces URLs that only work on this machine")
}
```

`grep -rn "checkProductionPosture" cmd/goen/*_test.go` returns nothing — the function has no test at all.

**2. A full production-posture start accepts `httpx://` with a path and a query.** Built the real binary and ran it with the repo's own `.env` (which has `GOEN_STRIPE_SECRET_KEY` set, so `payment.NewGateway`'s validator actually ran), `GOEN_INSECURE_COOKIES` unset (SecureCookies = true), `GOEN_TOTP_KEY=probe-key`, `GOEN_BASE_URL='httpx://evil.example/deep/path?q=1'`:

```
level=INFO msg="goen serving" addr=127.0.0.1:9799
$ curl -s http://127.0.0.1:9799/sitemap.xml
  <loc>httpx://evil.example/deep/path?q=1/</loc>
  <loc>httpx://evil.example/deep/path?q=1/deals</loc>
```

It started clean and emitted that. `strings.HasPrefix(baseURL, "http")` at `internal/payment/stripe.go:36` matches `httpx` — the strongest existing check passes it. `url.Parse` in the same line practically never errors (it rejects only control characters and bad %-escapes).

**3. The mailed reset link takes the value verbatim.** `internal/email/notify.go:127` is `link := strings.TrimRight(n.BaseURL, "/") + "/reset?token=" + url.QueryEscape(p.Token)` with no validation anywhere in the package. Temporary test in `internal/email` (run, then deleted) driving `SendPasswordReset`:

```
base "http://shop.example"                -> http://shop.example/reset?token=SECRET-TOKEN
base "httpx://evil.example"               -> httpx://evil.example/reset?token=SECRET-TOKEN
base "shop.example"                       -> shop.example/reset?token=SECRET-TOKEN
base "https://ok@evil.example"            -> https://ok@evil.example/reset?token=SECRET-TOKEN
base "https://shop.example/deep/path?q=1" -> https://shop.example/deep/path?q=1/reset?token=SECRET-TOKEN
```

(The end-to-end `POST /forgot` on the running binary did enqueue and deliver `account.password_reset` — confirmed in `outbox_messages` — but `email.LogSender` deliberately never logs the body, so the link itself had to come from the unit-level probe.)

**Every validator that touches BaseURL, and what it accepts:**

| Consumer | Check | Accepts |
|---|---|---|
| `cmd/goen/main.go:141` `checkProductionPosture` | `!= ""` | everything non-empty |
| `internal/payment/stripe.go:36` `NewGateway` | `url.Parse` + `HasPrefix "http"` | `httpx://`, userinfo, path, query — and **skipped entirely** when `secretKey == ""` (line 29-31 returns `&Gateway{baseURL: baseURL}` unvalidated) |
| `internal/account/google.go:59` `NewGoogle` | `HasPrefix "http"` | same; skipped when both Google vars empty |
| `internal/email/notify.go`, `newsletter.go`, `orders.go` (6 link builders) | none | everything |
| `internal/site/sitemap.go:29` | none | everything |
| `internal/ui/pages/jsonld.go:11,42` | none | everything |

`web.SitePath` is **not** in this path — it validates customer-supplied redirect targets and hero CTA hrefs, never BaseURL.

**What each consumer does with a malformed value — nothing does worse than a broken link, with one exception that is not a broken link at all:**
- Stripe `success_url`/`cancel_url` (`stripe.go:115-116`): Stripe validates URLs server-side, so a session create with `httpx://` is rejected — checkout fails loudly.
- Google `redirect_uri` (`google.go:65`): must exactly match a console-registered URI — sign-in fails loudly.
- Sitemap: `encoding/xml` encodes it; JSON-LD: `encoding/json` encodes it. **No injection in either** — I checked both; the garbage comes out escaped, as the `<loc>` output above shows.
- **`http://` with SecureCookies=true is the exception: it does not fail at all.** The link works, and a single-use account-recovery token crosses the wire in a cleartext request line — readable on-path and recorded in every proxy/CDN access log before any HSTS or 301 to HTTPS can act, because the redirect comes *after* the request carrying the token.

**Not a recorded decision.** `CLAUDE.md` and `docs/roadmap.md` say nothing about BaseURL shape. The nearest record is `docs/reviews/07-codex-round6-dispositions.md:35,468` — finding M5, "`GOEN_BASE_URL` defaults against its own doc", closed by adding exactly the emptiness check at line 141. That fix addressed *absence*; shape was never in scope. README.md:215 warns only about the default pointing at `127.0.0.1`.


## Blast radius

Operator-caused, not attacker-triggerable: `GOEN_BASE_URL` comes from the environment and nothing a visitor sends can influence it. This is hardening, not an exploit, and the honest framing is that a deployment has to be misconfigured first.

What raises it above pure ergonomics is the asymmetry in how the failures present. Four of the five malformations fail loudly and immediately — Stripe rejects the return URL so nobody can pay, Google rejects the redirect URI so nobody can sign in, a scheme-less or path-bearing value produces a visibly dead link. An operator finds those within minutes of the first order.

`http://` under SecureCookies=true is the one that does not fail. The site is served over TLS (that is what Secure cookies mean), the mailed links work, and nothing anywhere logs a complaint — while every `/reset?token=`, `/verify?token=`, `/newsletter/confirm?token=` and `/newsletter/unsubscribe?token=` link goen mails carries a live single-use credential in a cleartext request line. Password reset is the worst of the four: `internal/account`'s reset ends every session and sets a new password, so the token is the whole account. It is exposed to anyone on the path and written into every intermediary's access log, which outlives the token's one-hour expiry by however long logs are retained. An HSTS header or a 301 at the edge does not help — the token has already been sent by the time either is seen.

Affected population: every recipient of every transactional email, for as long as the misconfiguration stands. Silent to the shop and to the customer both.

A second, non-security cost worth naming because it is the likeliest mistake in practice: a base URL with a path or query silently concatenates into nonsense — `https://shop.example/deep/path?q=1/reset?token=…`, reproduced above. That is a broken recovery link for every customer, with no error anywhere on the server side.


## Fix

Give "is this string a usable origin?" ONE definition, in the package that already owns this repository's URL-shape vocabulary, then make all three existing checks read it.

**1. New: `web.SiteOrigin` in `internal/web/sitepath.go`**, beside `SitePath` and written in its style (same file, same comment idiom naming the case that a prefix check cannot decide). `internal/web` is a leaf — `go list -deps ./internal/web | grep koopa0` returns only itself — and both `internal/payment` and `internal/account` already import it, so there is no cycle.

```go
// SiteOrigin reports whether raw is an origin goen can build absolute links
// from, and returns it canonicalised with no trailing slash. HasPrefix(raw,
// "http") cannot do it: "httpx://" passes a five-character string test and is
// not a scheme, and "https://ok@evil.example" carries userinfo a mail client
// renders as the destination.
func SiteOrigin(raw string) (origin, scheme string, ok bool)
```

Rules, all decidable and each closing a case reproduced above:
- `url.Parse` must succeed;
- `u.Scheme` must be exactly `"http"` or `"https"` — an equality test, never a prefix test (this is what refuses `httpx://` and scheme-less `shop.example`);
- `u.Host` must be non-empty;
- `u.User` must be nil (refuses userinfo);
- `u.Path` must be `""` or `"/"`, and `u.RawQuery` and `u.Fragment` must be empty — goen mounts at the root (`mux.HandleFunc("GET /{$}", …)` in `cmd/goen/server.go`), so a sub-path deployment is unsupported and a path here only produces the `?q=1/reset?token=` concatenation;
- return `strings.TrimRight(u.Scheme+"://"+u.Host, "/")` and the scheme.

It returns the scheme rather than deciding on it, because the http-vs-https judgement needs `SecureCookies`, which `internal/web` does not have. Do NOT put the TLS rule inside this function.

**2. `cmd/goen/main.go:141-145`, inside `checkProductionPosture`** — replace the emptiness test with:

```go
if cfg.SecureCookies && os.Getenv("GOEN_BASE_URL") == "" {
    return errors.New(<the existing message, unchanged>)
}
origin, scheme, ok := web.SiteOrigin(cfg.BaseURL)
if !ok {
    return fmt.Errorf("GOEN_BASE_URL %q is not an origin: it must be "+
        "scheme://host with no path, query or credentials, because goen "+
        "concatenates paths onto it to build every link it mails", cfg.BaseURL)
}
if cfg.SecureCookies && scheme != "https" {
    return fmt.Errorf("GOEN_BASE_URL %q is plain HTTP while cookies are "+
        "Secure: every password-reset and address-verification link goen "+
        "mails would carry a single-use token in cleartext. Use https, or "+
        "set GOEN_INSECURE_COOKIES=1 for local development", cfg.BaseURL)
}
cfg.BaseURL = origin
```

Both refusals are FATAL and not warnings, matching `trustedProxies`' stated reason at `main.go:149-151` ("a mistyped CIDR is fatal rather than a warning"). The `scheme != "https"` refusal MUST be gated on `cfg.SecureCookies` — the development default is `http://` + `GOEN_ADDR` (`main.go:111`) and `make run` must keep working. `checkProductionPosture` already takes a pointer receiver, so assigning `cfg.BaseURL` is in-place; do it, so the single canonical value is what reaches `newServer` and `workerDeps`.

Ordering: `checkProductionPosture` is called at `main.go:313`, *after* `payment.NewGateway` (line 250) and `account.NewGoogle` (line 264). Move the call to before those two so the operator gets the specific message rather than a Stripe one — or leave the order and accept that the gateways refuse first with their own (now equally strict) message. Moving it is cleaner and is a pure reordering of independent steps.

**3. `internal/payment/stripe.go:36`** — replace
`if _, err := url.Parse(baseURL); err != nil || !strings.HasPrefix(baseURL, "http") {`
with `origin, _, ok := web.SiteOrigin(baseURL); if !ok {`, keep the existing error message, and set `baseURL: origin` at line 42 in place of `strings.TrimRight(baseURL, "/")`. Drop the now-unused `net/url`/`strings` imports if nothing else in the file uses them. Leave the `secretKey == ""` early return at line 29-31 as it is: `Enabled()` is false there and no session is ever created, so the unvalidated field is unreachable — do not add a check that would make a keyless development start fail.

**4. `internal/account/google.go:59`** — same substitution, keep the existing message, and build `redirectURL` from the returned `origin` instead of `strings.TrimSuffix(baseURL, "/")` at line 65.

**Do NOT** add validation to `internal/email`, `internal/site/sitemap.go` or `internal/ui/pages/jsonld.go`. They receive an already-canonical value from the one edge that validates it; re-checking there is the second definition this fix exists to remove. Their `strings.TrimRight(n.BaseURL, "/")` calls become no-ops and may stay — deleting them is optional tidying, not part of the fix.

No schema change, no sqlc regeneration, no templ regeneration. `internal/ui/pages/jsonld.go` is hand-written Go, not templ output.


## The lock, and how to see it fail first

Two new tests, plus one existing test to re-check. The repository requires locks proven by mutation (CLAUDE.md mistake #6, `.claude/rules/testing.md`), and mistake #28 names the specific trap to avoid here: **"an accepting statement that both versions of a rule accept proves nothing about either"** — so every case must be one the OLD code accepts and the new code refuses, and the accept-cases must include one the old `HasPrefix "http"` would have rejected is impossible (it rejects almost nothing), so the accept-cases exist only to prove the rule is not simply "refuse everything".

**Test 1 — `TestSiteOriginRefusesWhatIsNotAnOrigin`, `internal/web/sitepath_test.go`** (unit, no build tag, beside the existing `SitePath` tests).

Table-driven, each row asserting `ok`, and `origin` where ok:

| input | want | why this row exists |
|---|---|---|
| `https://shop.example` | ok, `https://shop.example` | the correct value |
| `https://shop.example/` | ok, `https://shop.example` | trailing slash canonicalised |
| `http://127.0.0.1:9700` | ok (scheme `http`), `http://127.0.0.1:9700` | the dev default must survive |
| `httpx://evil.example` | REFUSED | the reproduced `HasPrefix` hole |
| `httpsss://evil.example` | REFUSED | same hole, other side |
| `shop.example` | REFUSED | no scheme |
| `https://ok@evil.example` | REFUSED | userinfo |
| `https://shop.example/deep/path` | REFUSED | path |
| `https://shop.example?q=1` | REFUSED | query |
| `https://` | REFUSED | empty host |
| `""` | REFUSED | empty |

**Mutation to prove it RED, and it must be seen to apply** (CLAUDE.md false-green mode #3 — "the mutation must be seen to apply, not assumed"): replace the scheme equality test with `strings.HasPrefix(u.Scheme, "http")` — i.e. restore exactly the old rule — and confirm the `httpx://` and `httpsss://` rows go red. Then separately delete the `u.User != nil` clause and confirm the userinfo row goes red; then the path/query clauses and confirm those rows go red. Each clause needs its own mutation, because a single mutation that reddens the whole table proves only that the function is called.

**Test 2 — `TestProductionPostureRefusesACleartextBaseURL`, new file `cmd/goen/main_test.go`** (package `main`; `checkProductionPosture` is unexported and there is currently NO test for it — this is the lock that was missing).

Construct `config` values directly and call `cfg.checkProductionPosture(slog.New(slog.DiscardHandler))`. Note the function reads `os.Getenv("GOEN_BASE_URL")` for the emptiness branch as well as `cfg.BaseURL`, so use `t.Setenv("GOEN_BASE_URL", …)` to keep the two consistent — and set `TOTPKey` non-empty in every row, or the first branch answers before the one under test is reached (this is the shape CLAUDE.md warns about under "a redundant check that makes the real one untestable").

Rows:
1. `{SecureCookies: true, TOTPKey: "k", BaseURL: "http://shop.example"}` → **error**, and the message must mention cleartext/https. This is THE row: it is the case the current code accepts silently, and it is the only malformation that does not otherwise fail loudly.
2. `{SecureCookies: true, TOTPKey: "k", BaseURL: "httpx://evil.example/deep/path?q=1"}` → error. This is the exact string I started the real binary with; it must now refuse.
3. `{SecureCookies: true, TOTPKey: "k", BaseURL: "https://shop.example/"}` → nil, and assert `cfg.BaseURL == "https://shop.example"` — this locks the in-place canonicalisation, without which the fix silently does half its job.
4. `{SecureCookies: false, TOTPKey: "", BaseURL: "http://127.0.0.1:9700"}` → nil. **This row is the one that fails if somebody writes the https rule ungated**, and without it `make run` breaks in a way no test would catch.

**Mutations, each seen to apply:**
- Delete the `cfg.SecureCookies && scheme != "https"` block → row 1 goes green-to-red. Confirm row 1 is the ONLY row that flips; if row 2 also flips, the rows are not testing distinct clauses.
- Delete the `!ok` block → row 2 flips.
- Delete `cfg.BaseURL = origin` → row 3 flips. (Assert the string, not merely that no error came back — CLAUDE.md mistake #34: "an assertion coarser than the error it is meant to catch is not a weak lock, it is no lock.")
- Change the https test to fire unconditionally (drop `cfg.SecureCookies &&`) → row 4 flips.

**Existing test to re-check, not to change blindly:** `cmd/goen/integration_test.go:77` constructs `email.Notifier{… BaseURL: "https://goen.test"}`, which passes the new rule unchanged. `internal/payment/stripe_http_test.go:72` sets `baseURL: "https://goen.example"` on the struct directly, bypassing `NewGateway`, so it is unaffected. Confirm both still pass rather than assuming it — a fix that closes one hole routinely opens the next (`.claude/rules/review-process.md`).

**Not lockable, and say so rather than dressing it up:** that a cleartext link actually leaks the token to an on-path observer is a property of the network, not of this code, and no test in this repository can assert it. The tests above lock the refusal; the leak is the argument for why the refusal is worth having, and belongs in the commit message, not in an assertion.
