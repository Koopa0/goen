# no-compression

**Verdict** CONFIRMED · **Severity** high · **Origin** found in this round's own sweep (already established)

**Files** assets/assets.go, assets/precompress.go, assets/assets_test.go, internal/web/compress.go, internal/web/compress_test.go, cmd/goen/server.go, cmd/goen/server_test.go, internal/ui/pages/twofactor.templ, internal/ui/pages/reset.templ, internal/ui/pages/newsletter.templ


## Root cause

goen's deployment story is deliberately one binary with no reverse proxy (`ko build`, no proxy in docker-compose), and content negotiation for `Accept-Encoding` is the one job a reverse proxy usually does that was never re-homed into the binary when the proxy was designed out. The asset handler was built for *cache* correctness (a content digest, a one-year immutable `Cache-Control`) and the transfer-size question was never asked beside it; `web.Render` was built for *atomicity* (render into a buffer so a half-failed template cannot leave a truncated 200) and, having the whole body in memory already, hands it to `WriteTo` unencoded. Two correct halves, neither of which owns the wire format.


## Reproduction — the evidence this rests on

EXECUTED against the running dev server and the repo, not reasoned about.

1. No compression exists anywhere. `grep -rn "gzip\|Accept-Encoding\|Content-Encoding\|brotli\|compress/" --include="*.go" --include="*.templ" .` → zero hits outside tests. `grep -n "gzip\|zstd" Makefile` → zero hits. No proxy in the deployment story (`make image` is `ko build`, one binary).

2. Measured on the live server (curl, 2026-08-24):
   - `curl -sD - -o /dev/null http://127.0.0.1:9700/static/css/app/app.css -H 'Accept-Encoding: gzip, br'` → `HTTP/1.1 200`, `Content-Length: 82856`, `Content-Type: text/css; charset=utf-8`, **no `Content-Encoding`**, `Vary: Accept-Language, Cookie` (no `Accept-Encoding`), and **no `ETag` and no `Last-Modified`** — `embed.FS` reports a zero modtime, so `http.ServeContent` emits neither.
   - `curl -sD - -o /tmp/home.html http://127.0.0.1:9700/ -H 'Accept-Encoding: gzip, br'` → `Transfer-Encoding: chunked`, no `Content-Encoding`, body 29,693 bytes.

3. Measured compressibility with a throwaway Go program (`compress/gzip`, since deleted; scratchpad, never in the repo):
   ```
   level=1 home.html raw= 29693 gz= 6791   0.14 ms/op
   level=6 home.html raw= 29693 gz= 5947   0.15 ms/op
   level=6 app.css   raw= 82856 gz=17704   0.98 ms/op
   level=9 app.css   raw= 82856 gz=17615   1.35 ms/op
   ```
   The home page is 29,693 → 5,947 bytes (80% saved, 0.15 ms). `app.css` is 82,856 → 17,615.

4. The whole compressible embedded set (`assets/{brand,css,js}`, 21 files) is 226,504 bytes raw → 63,228 gzip -9. `assets/media` is 743,981 bytes of `.webp` (47 files) and must not be touched. So precompressing the entire embedded text set at start-up costs ~63 KB of retained heap and under 10 ms once.

5. Cited lines confirmed:
   - `assets/assets.go:206` `func Handler()`, `:218` `w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")` — the summary's "~line 218" is exact.
   - `assets/assets.go:206-225` serves via `http.FileServerFS(files)` after checking `digests[name]`.
   - `cmd/goen/server.go:334-344` is the whole middleware chain; `:341` `securityHeaders`, `:342` `requestLog`.
   - `cmd/goen/server.go:481` `w.Header().Add("Vary", "Accept-Language, Cookie")` in `withLocale`, applied to the whole mux — so asset responses already carry a `Vary`, and `Accept-Encoding` must be **added** to it, not set.
   - `internal/web/web.go:41-58` `Render` renders into a `bytes.Buffer` then `buf.WriteTo(w)` — a single `Write` call carrying the whole body, which is why a small-body threshold in the compressor is cheap and exact.
   - `cmd/goen/server.go:347-351` `crossOriginProtection` = `http.NewCrossOriginProtection().Handler(next)`; its comment says "which is why goen's forms carry no CSRF token". The summary is right that there is no CSRF token to protect from BREACH.

6. **One thing the summary did not say, and it changes the security section.** There ARE secrets in response bodies:
   - `internal/ui/pages/twofactor.templ:39` renders the TOTP shared secret (`v.SecretGroups()`) and `:45` the `otpauth://` URI — a password-equivalent.
   - `internal/ui/pages/reset.templ:54` `<input type="hidden" name="token" value={ v.Token }/>` — a live single-use password-reset token.
   - `internal/ui/pages/newsletter.templ:16` — the unsubscribe token, which CLAUDE.md records as the one secret that never expires.
   None of those three pages reflects attacker-controlled input, so BREACH has no oracle today; the spec below opts them out anyway, because the defence is three lines and the reflection analysis has to be redone by every future editor of those templates.

7. No recorded decision covers this. `grep -rni "compress|gzip|brotli" CLAUDE.md docs/roadmap.md README.md docs/decisions/*.md` → zero hits. `go list -m all | wc -l` → 103, matching CLAUDE.md's stated graph size, which is what forbids a brotli dependency.

`git status --porcelain` empty at start and at finish.


## Blast radius

Every visitor, every request, silently. First paint is gated on `assets/css/app/app.css` (82,856 bytes) plus the design-system sheets — 226 KB of render-blocking text served raw where 63 KB would do. Every HTML page ships ~5x its necessary bytes (home: 29,693 vs 5,947). On a Taiwanese mobile connection this is roughly a second of added time-to-first-paint on a cold visit, and it is paid again on every page view for the HTML because HTML is not cacheable. Nothing in the repository can see it: `make check-layout` measures geometry in a browser and never asks about bytes; there is no observability (CLAUDE.md: "Observability is deferred… every growth gate queued in this file triggers on a number nothing in production will emit"). It is the one performance defect that costs on the very first request a shop ever serves.


## Fix

Two paths, because the two bodies have different economics. gzip only — `compress/gzip`, no new module. Brotli is refused in writing below.

════════════════════════════════════════════
PART A — embedded assets: precompress at start-up
════════════════════════════════════════════
NEW FILE `assets/precompress.go` (package `assets`; same package, so the single-file-package rule does not apply).

A1. Allowlist, not a denylist — the `MediaWidths` doctrine, fail-closed:
```go
// compressible are the embedded extensions worth a gzip representation.
//
// An ALLOWLIST rather than a list of things to skip: a .webp added to the
// media directory tomorrow must default to "leave it alone", because gzipping
// an already-compressed image spends CPU to make the response LARGER, and a
// denylist is the shape that gets that wrong by omission.
var compressible = map[string]bool{
    ".css": true, ".js": true, ".svg": true,
    ".json": true, ".xml": true, ".txt": true, ".map": true,
}

// minPrecompress is the size below which gzip's own 18-byte envelope and the
// second round trip through the negotiation branch are not worth it.
const minPrecompress = 1024
```

A2. A parallel map filled by the SAME walk that fills `digests`, so an asset can never have a digest and no encoding decision. Extend `index()` in `assets/assets.go` (currently lines 66-93) — it already reads every file's bytes through `io.Copy` into a hasher; read into a `bytes.Buffer` instead, hash from the buffer, and gzip from the same buffer. One read per file, not two.
```go
// gzipped holds the gzip representation of every embedded asset worth one.
// Built once at start-up at BestCompression: the CPU is paid on a cold binary,
// never on a request, which is what makes this strictly better than a
// streaming compressor for a file whose bytes cannot change.
var gzipped = map[string][]byte{}
```
Precompress with `gzip.NewWriterLevel(&out, gzip.BestCompression)`. Store the result **only if `out.Len() < len(raw)`** — a file that does not shrink gets no entry and is served identity forever. Log nothing; a failure from `gzip.Writer` on a `bytes.Buffer` cannot happen, so return the error out of `index()` and let the existing `init()` panic carry it (assets already panics at start-up on a missing required file; a compressor that cannot compress is the same class of impossible).

A3. `Handler()` (`assets/assets.go:206-225`) grows a negotiation branch. Rewrite the body of the inner `HandlerFunc`, keeping the existing digest lookup and `Cache-Control` logic **exactly as they are**:
```go
name := strings.TrimPrefix(r.URL.Path, "/")
digest, known := digests[name]
if !known { http.NotFound(w, r); return }

if r.URL.Query().Get("v") == digest {
    w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
} else {
    w.Header().Set("Cache-Control", "no-cache")
}
// Two representations of one asset are two entities, so they carry two
// validators and the response says the encoding is what it varied on.
// Add, not Set: withLocale has already put Accept-Language and Cookie there.
w.Header().Add("Vary", "Accept-Encoding")

if body, ok := gzipped[name]; ok && acceptsGzip(r) {
    etag := `"` + digest + `-gz"`
    if r.Header.Get("If-None-Match") == etag {
        w.Header().Set("ETag", etag)
        w.WriteHeader(http.StatusNotModified)
        return
    }
    w.Header().Set("ETag", etag)
    w.Header().Set("Content-Type", contentType(name))
    w.Header().Set("Content-Encoding", "gzip")
    w.Header().Set("Content-Length", strconv.Itoa(len(body)))
    if r.Method == http.MethodHead { w.WriteHeader(http.StatusOK); return }
    if _, err := w.Write(body); err != nil {
        slog.Warn("assets: write gzip body", "name", name, "error", err)
    }
    return
}
w.Header().Set("ETag", `"`+digest+`"`)
fileServer.ServeHTTP(w, r)
```
Notes the implementer must not skip:
 - **`Content-Type` must be set explicitly on the gzip branch.** `http.FileServerFS` derives it from the extension; writing the gzip bytes directly would let `http.DetectContentType` sniff the gzip magic and answer `application/x-gzip`, and `X-Content-Type-Options: nosniff` (set at `cmd/goen/server.go:399`) would then make the browser refuse the stylesheet. Use `mime.TypeByExtension(path.Ext(name))`, falling back to `"application/octet-stream"`; add the `; charset=utf-8` suffix for the text types the way `net/http` does.
 - **No `Accept-Ranges` on the gzip branch.** Ranges over a compressed entity are legal but useless for CSS/JS, and `fileServer` is what was advertising them. Identity keeps them; gzip does not. That asymmetry is correct — they are different representations.
 - **The ETag suffix is not cosmetic.** RFC 9110 §8.8.3: an entity-tag identifies the *selected representation*. One tag across both encodings makes a cache that stored the gzip body answer an identity request 304 with a validator for bytes it does not have.
 - `acceptsGzip(r)` must parse properly, not `strings.Contains`: split `Accept-Encoding` on `,`, trim, split each token on `;`, and refuse a token whose `q=` parses to 0. `Accept-Encoding: gzip;q=0` means "not gzip" and `Contains` reads it as yes.

A4. `Vary` is now added by two places (`withLocale` and `Handler`). Both use `Add`; two `Vary` header lines are equivalent to one comma list per RFC 9110 §5.3, so no coordination is needed.

════════════════════════════════════════════
PART B — dynamic HTML: streaming gzip middleware
════════════════════════════════════════════
NEW FILE `internal/web/compress.go`. This belongs in `internal/web`, whose own doc says it "holds the HTTP concerns every goen feature shares"; `cmd/goen` is wiring only and this has invariants that need their own test file.

B1. Public surface, two functions:
```go
// Compress answers a client that asked for gzip with gzip, for the response
// types where it pays. It negotiates on Accept-Encoding, so a client that did
// not ask gets exactly the bytes it got before.
func Compress(next http.Handler) http.Handler

// NoCompress marks this response as one that must go out uncompressed, for a
// body that carries a secret. Call it BEFORE the first Write.
func NoCompress(w http.ResponseWriter)
```

B2. Type allowlist, keyed on the `Content-Type` the handler set, matched on the media type with parameters stripped:
```go
var compressibleTypes = map[string]bool{
    "text/html": true, "text/css": true, "text/plain": true,
    "text/javascript": true, "application/javascript": true,
    "application/json": true, "application/xml": true, "text/xml": true,
    "image/svg+xml": true,
}
```
`/sitemap.xml` sets `application/xml; charset=utf-8` (`internal/site/sitemap.go:69`) and `/robots.txt` sets `text/plain; charset=utf-8` (`:103`); both are covered. `internal/media/handler.go:98` sets `image/webp`/`image/png`/`image/jpeg` — none is on the list, so uploaded images pass through untouched.

B3. The wrapper (`type compressor struct{...}` implementing `http.ResponseWriter`). Decision is made **lazily, at the first `Write` or `WriteHeader`**, because that is the first moment `Content-Type` is known. Rules, each of which a reviewer will ask about:
 - **Refuse if the client did not ask.** Parse `Accept-Encoding` with the same `acceptsGzip` logic as A3 — put it in `internal/web` and have `assets` keep its own copy (assets must not import a feature package; it already spells out `uploadedDigest` rather than importing `internal/media`, and `TestAnUploadedKeyResolvesToTheMediaHandler` is the precedent for holding two copies together with a test rather than a comment).
 - **Refuse if `Content-Encoding` is already set.** This is the single line that stops Part A's precompressed assets being gzipped twice. Without it every stylesheet is a gzip stream inside a gzip stream and no browser can read it.
 - **Refuse on a status with no body**: `< 200`, `204`, `304`.
 - **Refuse below `minCompress = 1024` bytes.** Buffer the first writes until the total passes the threshold; on `Close()` with the total still under it, flush the buffer raw. Because `Render` does one `WriteTo` (`internal/web/web.go:57`), a page hits the decision on its first `Write` and the buffer never grows beyond one call in practice. This keeps the 303 redirect bodies, the `/promo/dismiss` empties and the small `422` htmx fragments identical to today.
 - **Refuse if `NoCompress` was called.** Mechanism: `NoCompress` sets a private response header `X-Goen-No-Compress: 1`; the compressor reads it at decision time and **deletes it unconditionally**, whether or not it compressed, so the marker can never reach a client. A response header is the only channel that runs handler→middleware in this direction; a context value cannot, because the middleware wraps from outside and never sees the handler's context edits.
 - **When it does compress:** `Header().Del("Content-Length")` (the length is now wrong and a client that believes it truncates the page), `Header().Del("Accept-Ranges")`, `Header().Set("Content-Encoding", "gzip")`, `Header().Add("Vary", "Accept-Encoding")`, and if an `ETag` is present rewrite it from `"x"` to `"x-gz"` (same representation rule as A3; nothing sets an ETag on a compressible response today, so this is a guard against the next one).
 - **Ordering:** every header mutation above must happen before the underlying `WriteHeader`, i.e. inside the lazy decision, not after.
 - **`Close()` after `next.ServeHTTP` returns**, in the middleware, not deferred inside the handler — a gzip stream that is never closed loses its trailing block and the last bytes of the page.
 - **`Unwrap() http.ResponseWriter`** returning the wrapped writer, so `http.ResponseController` reaches the real one. `cmd/goen/server.go:463` already relies on this for `statusRecorder` and documents why. Also implement `Flush()` (flush the gzip writer, then flush through the controller).
 - **`sync.Pool` of `*gzip.Writer`** created at `gzip.DefaultCompression` (level 6 — measured 5,947 bytes for the home page at 0.15 ms; level 1 saves 0.01 ms and costs 844 bytes, level 9 saves 31 bytes and costs 0.02 ms). `Reset(w)` on take, `Close()` then put back. Never put back a writer whose `Close` errored.

B4. Wiring — ONE line in `cmd/goen/server.go`, between the current line 341 and 342:
```go
	handler = securityHeaders(handler)
	handler = web.Compress(handler)          // NEW
	handler = requestLog(handler, log)
```
This places the compressor **inside** `requestLog` (so `statusRecorder` still records the status; the compressor's `Unwrap` keeps `ResponseController` working through it) and **inside** `recoverPanic` (so the panic fallback at `cmd/goen/server.go:434` writes to the raw writer — a panic mid-page already produced a truncated body before this change and still does; it must not additionally produce a gzip member the browser silently drops). It wraps the mux, so `/static/` goes through it too, and the `Content-Encoding` guard in B3 is what stops the double compression there.

B5. `web.NoCompress(w)` call sites — exactly three, each at the top of the handler that renders a secret, before `web.Render`:
 - the TOTP enrolment render in `internal/twofactor` (the handler behind `internal/ui/pages/twofactor.templ`, which prints `v.SecretGroups()` at `:39` and `v.URI` at `:45`);
 - the `/reset` GET render in `internal/account` (`internal/ui/pages/reset.templ:54` embeds the live token);
 - the newsletter confirm/unsubscribe render in `internal/newsletter` (`internal/ui/pages/newsletter.templ:16`).
Each call carries a one-line comment naming the secret and the reason. The reason to state, because it is the part a reviewer will challenge: **CRIME does not apply** (Go's TLS never compresses), and **BREACH needs a reflection channel** — attacker-controlled input in the same response as the secret, observed over many requests — which none of these three pages has, and goen has no CSRF token in any body because `crossOriginProtection` (`cmd/goen/server.go:347-351`) uses `Sec-Fetch-Site`. The opt-out is taken anyway because it costs three lines and three uncompressed pages nobody loads in bulk, while the alternative is an argument that has to be re-made by whoever next adds an echoed field beside a token. This is the same call CLAUDE.md records for the constant-time compare: cheap defence, mutation recorded honestly.

════════════════════════════════════════════
REFUSED IN WRITING — brotli
════════════════════════════════════════════
Do not add it. `compress/*` ships no brotli, so it means `github.com/andybalholm/brotli` joining a module graph CLAUDE.md pins at 103 and repeatedly refuses to grow ("Measured before adding: +6 modules"; "the module graph is still 103"). The measured win is `app.css` 17,688 gzip → 14,896 brotli = 2,792 bytes, paid **once per client per year** because the asset carries `max-age=31536000, immutable`. The seam if it is ever wanted is one more map beside `gzipped` and one more branch in `acceptsGzip`; Part A's shape is deliberately built to take it without a rewrite.

════════════════════════════════════════════
NOT IN SCOPE, and why
════════════════════════════════════════════
`internal/media` stays untouched: its bodies are `.webp`/`.png`/`.jpeg`, already compressed, and it sets `Content-Length` at `handler.go:99` which the type allowlist protects. Minifying `app.css` is a separate question and is refused here — CLAUDE.md pins "Plain CSS with `oklch` tokens — there is no Tailwind and no CSS build step", and gzip already recovers most of what minification would.

Run `make verify` and then `make check-layout` (Chrome sends `Accept-Encoding: gzip`, so the layout probe exercises Part A end to end; every geometry assertion must still pass, which is what proves the browser is decoding the stylesheet rather than refusing it on `nosniff`).


## The lock, and how to see it fail first

Every lock below names the mutation that must be seen to go RED first, and the mutation must be seen to APPLY (CLAUDE.md false-green mode #3: "The mutation must be seen to apply, not assumed" — check the edit landed, do not trust a grep count).

── `assets/assets_test.go` (unit, no Docker) ──

1. `TestAnAssetIsServedPrecompressed`
   `GET assets.URL(assets.AppCSS)` with `Accept-Encoding: gzip`. Assert: `Content-Encoding: gzip`; `Content-Type` starts `text/css`; `Vary` contains `Accept-Encoding`; `Content-Length` equals `len(res.Body.Bytes())`; and **gunzip the body and assert it contains `.goen-header__bar`** — the same independent literal `TestHandlerServesRequestedAsset` already uses at `assets_test.go:129`, for the reason its comment gives ("not a symbol the handler shares, so a handler that served the wrong file still fails here").
   MUTATIONS: (a) delete `w.Header().Set("Content-Encoding", "gzip")` → red on the header AND the browser-visible failure; (b) write `raw` instead of `body` on the gzip branch → gunzip fails → red; (c) drop the explicit `Content-Type` set so sniffing produces `application/x-gzip` → red.

2. `TestAnAssetWithoutAcceptEncodingIsIdentity`
   Same URL, **no** `Accept-Encoding` header. Assert no `Content-Encoding`, `Content-Length: 82856`, body contains `.goen-header__bar` directly.
   MUTATION: serve gzip unconditionally → red. This test is also what keeps the existing `TestHandlerServesRequestedAsset` and `TestHandlerRefusesLongCacheWithoutMatchingVersion` meaningful — `httptest.NewRequest` sets no `Accept-Encoding`, so they must stay green untouched.

3. `TestGzipIsRefusedWhenTheClientSaysQZero`
   `Accept-Encoding: gzip;q=0` → identity.
   MUTATION: implement `acceptsGzip` as `strings.Contains(ae, "gzip")` → red. Without this case the naive implementation passes every other test in the file.

4. `TestAnImageIsNotPrecompressed`
   `GET assets.URL(assets.HomeHeroImage)` with `Accept-Encoding: gzip`. Assert no `Content-Encoding` and the body is byte-identical to the embedded file.
   MUTATION: add `".webp": true` to `compressible` → red.

5. `TestEveryPrecompressibleAssetRoundTrips` — **corpus derived from the catalogue, not written by hand**, which is this repository's standing rule for coverage guards. Range over the `digests` map; for each name whose extension is in `compressible` and whose raw size is ≥ `minPrecompress`, assert `gzipped[name]` exists, gunzips byte-for-byte to `files.ReadFile(name)`, and is strictly smaller than the raw. A new stylesheet added to `assets/css/` is covered the moment it is embedded.
   MUTATION: in `index()`, store the gzip of the *previous* file (off-by-one) → red on the byte comparison, not merely on a count.

6. `TestAnAssetETagDistinguishesEncodings`
   Fetch identity and gzip; assert the two `ETag`s differ. Then replay each one as `If-None-Match` **against its own encoding** and assert `304` with a body of length 0, and replay the gzip tag against an identity request and assert `200`.
   MUTATION: emit `"`+digest+`"` on both branches → red on the last assertion (a cache would answer an identity request 304 for bytes it holds only compressed).

── `internal/web/compress_test.go` (unit, no Docker) ──

Each case wires `web.Compress(http.HandlerFunc(...))` around a handler that sets a `Content-Type` and writes a fixed body, and drives it with `httptest`.

7. `TestHTMLIsCompressedForAClientThatAcceptsIt` — 30 KB of `text/html`, `Accept-Encoding: gzip`. Assert `Content-Encoding: gzip`, `Vary` contains `Accept-Encoding`, gunzip == original, and `Content-Length` is absent.
   MUTATIONS: (a) never compress → red; (b) leave a stale `Content-Length` the handler set → red (this is the mutation that proves the truncation guard, so the handler in this case MUST set `Content-Length` explicitly).

8. `TestAClientThatDoesNotAskGetsIdentity` — no `Accept-Encoding` → bytes identical to what the handler wrote, no `Content-Encoding`.

9. `TestAResponseThatAlreadyCarriesAnEncodingIsLeftAlone` — handler sets `Content-Encoding: gzip` and writes real gzip bytes. Assert the response body gunzips **once** to the plaintext, not to a gzip stream.
   MUTATION: remove the `Content-Encoding` guard → the body gunzips to gzip magic → red. **This is the lock on the Part A ↔ Part B seam** and the single most important case in the file: without the guard every stylesheet on the site is double-compressed and unreadable, and every geometry check in `make check-layout` fails at once.

10. `TestAnImageResponseIsNotCompressed` — `Content-Type: image/webp` → identity.
    MUTATION: replace the type allowlist with "compress unless image/*" → still green; replace it with "compress everything" → red. Use the second.

11. `TestASmallResponseIsNotCompressed` — 200 bytes of `text/html` → identity, byte-identical.
    MUTATION: set `minCompress = 0` → red.

12. `TestAStatusWithNoBodyEmitsNoGzipEnvelope` — table over `204` and `304`: handler calls `WriteHeader` and writes nothing. Assert `len(body) == 0` and no `Content-Encoding`.
    MUTATION: drop the status check → an 18-byte empty-gzip envelope appears under a 204 → red.

13. `TestAPageCarryingASecretIsNotCompressed` — handler calls `web.NoCompress(w)` then writes 30 KB of `text/html`. Assert identity, AND assert `res.Header().Get("X-Goen-No-Compress") == ""`.
    MUTATIONS: (a) ignore the marker → red on the encoding; (b) delete only on the compressed branch → red on the header leaking to the client. Both must be run: (b) is invisible to the first assertion.

14. `TestTheFlusherAndTheStatusRecorderReachThroughTheCompressor` — nest exactly as production does: a `statusRecorder`-shaped outer writer, `web.Compress` inside it, and a handler that calls `http.NewResponseController(w).Flush()` mid-body and then `WriteHeader`-less writes. Assert `Flush` returns nil and the outer recorder observed 200.
    MUTATION: delete `Unwrap()` → `ResponseController.Flush` returns `http.ErrNotSupported` → red.

── `cmd/goen/server_test.go` (unit, no pool — follow the existing pattern at `:12`, which builds a bare mux plus ONE middleware rather than the whole router) ──

15. `TestAnAssetIsNotCompressedTwiceByTheChain`
    ```go
    handler := web.Compress(securityHeaders(assets.Handler()))
    ```
    `GET /static/css/app/app.css?v=<digest>` with `Accept-Encoding: gzip`. Assert exactly one `Content-Encoding: gzip` header value, that gunzipping once yields `.goen-header__bar`, and that `Cache-Control` is still `public, max-age=31536000, immutable` — the existing behaviour at `assets/assets.go:218` must survive the change.
    MUTATION: remove the `Content-Encoding` guard from `web.Compress` → red.

── the three opt-out sites ──

16. In each owning feature's existing handler test file (`internal/twofactor`, `internal/account`, `internal/newsletter`), one case per site: drive the handler through `web.Compress` with `Accept-Encoding: gzip` and a body large enough to clear `minCompress`, and assert no `Content-Encoding`.
    MUTATION: delete the `web.NoCompress(w)` line from that one handler → that one test red, the other two still green. Run all three separately; a single shared mutation would not prove the three call sites are three independent locks.

── the whole-system check ──

17. `make verify` (fmt-check → templ-check → squawk → sqlc-check → vet → lint → integration-build-check → test-race), then `make check-layout` with a running `make run`. The layout probe drives real Chrome, which sends `Accept-Encoding: gzip` — so every one of its 85 viewport rows is an end-to-end assertion that the browser decoded the stylesheet under `nosniff`. Record both as PASS with `cmd && echo PASS || echo FAIL`, never a pipe (mistake #5). Re-run `make verify` twice before believing it (mistake #22).
