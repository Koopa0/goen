# sitepath-triple-slash

**Verdict** CONFIRMED · **Severity** high · **Origin** found in this round's own sweep (already established)

**Files** internal/web/sitepath.go, internal/web/sitepath_test.go, internal/account/account.go, internal/account/account_test.go, internal/account/handler.go, internal/home/hero.go, internal/home/banner.go, internal/home/hero_test.go, internal/ui/pages/home.templ, internal/ui/layouts/banner.templ


## Root cause

One question — "is this string a path on this site, as a BROWSER will resolve it?" — has two independent implementations, and both answer it by proxy rather than by rule.

web.SitePath delegates to net/url, which implements RFC 3986. RFC 3986 says an authority begins with exactly two slashes and a third slash starts the path; WHATWG (what every browser actually implements) says the special-authority-ignore-slashes state skips ANY number of / and \ and then reads a host. So the function asks a standard no browser follows about a string only a browser will resolve. Delegating to url.Parse looked more rigorous than a prefix test, and that is why the sophisticated definition is the wrong one.

account.SafeNext re-derives the same rule by hand, refuses the whole //-prefixed class in one line, and is therefore correct — but it is a second definition, so the repository has no single place where this rule is true and the security of a call site depends on which of the two the author happened to import. That is the shape CLAUDE.md keeps recording — localized_name, committed_orders, store_credit_balances, order_amount_owed(order_id): "the rule would otherwise be copied into … and the one that forgot would be whichever was written next". Here the one that forgot was written FIRST and is the one on the storefront.


## Reproduction — the evidence this rests on

EXECUTED, not argued. Three levels: the predicate, Go's own resolver, and the live server.

1. The cited lines say what the finding claims. internal/web/sitepath.go:11-26 is verbatim:

    func SitePath(raw string) (string, bool) {
        if raw == "" { return "", false }
        if strings.ContainsAny(raw, `\`) { return "", false }
        u, err := url.Parse(raw)
        if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil { return "", false }
        if !strings.HasPrefix(u.Path, "/") { return "", false }
        return u.String(), true
    }

The only rootedness test is strings.HasPrefix(u.Path, "/") on the DECODED path of a url.Parse that never saw an authority.

2. Ran that exact body as a throwaway program (scratchpad, deleted):

    "///evil.example/x"   ok=true  got="///evil.example/x"   Host="" Path="///evil.example/x"
    "////evil.example/x"  ok=true  got="////evil.example/x"  Host="" Path="////evil.example/x"
    "///"                 ok=true  got="///"
    "//evil.example"      ok=false                           Host="evil.example"
    Go RFC3986 resolve: base https://goen.example/checkout + "///evil.example/x" -> https://goen.example///evil.example/x

Go and every browser disagree about the same string, which is the finding: WHATWG's special-authority-ignore-slashes state consumes every run of / or \ after the first two and then reads an authority, giving https://evil.example/x.

3. LIVE end-to-end against the running dev server, which is stronger than the finding stated:

    psql: insert into promo_banners (id, message, cta_label, cta_href, is_active)
          values ('...deadbeef','PROBE','go','///evil.example/x', true);
    curl -s http://127.0.0.1:9700/ | grep goen-promo__link
    => goen-promo__link" href="///evil.example/x"

(row deleted afterwards; count where message='PROBE' = 0; git status --porcelain empty.)
That rendered href passed BOTH gates: internal/admin/banner.go:65 at write time and internal/home/banner.go:49 at read time. The read-time re-validation the repo already has does not help, because it calls the same broken predicate.

What the summary got right: banner.templ:12 is a live sink; the http.Redirect sinks are not exploitable (http.Redirect normalises); SafeNext's cruder strings.HasPrefix(next, "//") at internal/account/account.go:262 does refuse the triple slash, so the less sophisticated of the two definitions is the correct one.

Three things the summary MISSED, all of which the fix must cover:

a) There is a SECOND live sink and it is bigger. internal/home/hero.go:28-30 builds pages.CTA{Href: row.PrimaryCtaHref} straight from the database with NO SitePath call at read time, and internal/ui/pages/home.templ:27,32 render both CTAs through templ.SafeURL — the largest button on the storefront. The banner path at least attempts a read-time check; the hero path has none at all and depends entirely on internal/admin/hero.go:60,72.

b) strings.HasPrefix(u.Path, "/") reads the DECODED path, which is wrong in the other direction too: "/%2f%2fevil.example" decodes to Path="///evil.example" while the returned u.String() is "/%2f%2fevil.example", which browsers keep same-origin. Today it is accepted by luck, and any "fix" that tightens the check on u.Path would break a legitimate same-site URL. The predicate must read the RAW string.

c) Tab/CR/LF variants ("/\t/evil.example", "/\n/evil.example") are WHATWG-stripped to "//evil.example" and are currently refused only as a side effect of url.Parse erroring on ASCII control characters — an implicit dependency on net/url internals, not a stated rule.


## Blast radius

Whoever can write a CTA href reaches every storefront visitor. Two writers: /admin/home/banner and /admin/home (hero) — so a staff account, or anyone holding a staff session, or anything that writes those two tables (no CHECK constrains the column shape: promo_banners carries only promo_banners_cta_complete, hero_slides only the label/href-present pair; verified against the live catalogue with \d promo_banners). The resulting anchor sits ABOVE THE HEADER on every storefront page (promo strip) and is the hero's primary button on /. A click leaves goen for an attacker origin while the link's rendered text still reads as goen's own promotion — a phishing landing page reached from the shop's own home page, which is exactly the harm CLAUDE.md says the check exists to prevent: "an absolute URL there would send every visitor off-site from the home page".

Silent in every direction: nothing logs it, the admin form accepts it with no error, /admin/home displays it back as an ordinary path, and both TestSitePathRefusesAnythingButAPathOnThisSite and make check-layout stay green.

The redirect callers (internal/site/locale.go:22, internal/home/banner.go:113, internal/account/handler.go:549) are NOT exploitable — http.Redirect collapses the leading slashes — but they are one refactor away from being so (a move to w.Header().Set("Location", …), an htmx HX-Redirect header, or a meta refresh re-opens them), and the account package escapes today only because it happens to use a different, second implementation of the same rule.


## Fix

ONE definition, stated as a rule rather than delegated, plus the read-time gate the hero never had.

=== 1. Rewrite the predicate: internal/web/sitepath.go ===

Replace the body of SitePath (lines 11-26). Every clause is load-bearing; the comment must say which browser behaviour each one answers, because the next reader's instinct will be to "simplify" it back into url.Parse.

    // SitePath reports whether raw is a path on this site, as a BROWSER will
    // resolve it, and returns it cleaned.
    //
    // The rule is stated here rather than delegated to net/url, and that is the
    // point. net/url follows RFC 3986, where an authority is exactly two
    // slashes; browsers follow WHATWG, whose special-authority-ignore-slashes
    // state skips ANY run of "/" or "\" and then reads a host. So url.Parse
    // reports Host="" for "///evil.example/x" while every browser resolves it
    // to https://evil.example/x.
    func SitePath(raw string) (string, bool) {
        // (1) WHATWG strips U+0009, U+000A and U+000D from ANYWHERE in a URL
        //     before parsing, so "/<TAB>/evil.example" is "//evil.example" to a
        //     browser; it also trims leading C0-and-space, so " //evil" is too.
        //     Refuse every control character outright: none belongs in a link,
        //     and CR LF in a Location header is response splitting.
        if raw == "" || hasControl(raw) {
            return "", false
        }
        // (2) Rooted, tested on the RAW string. u.Path is percent-DECODED, so
        //     "/%2f%2fevil.example" arrives there as "///evil.example" although
        //     a browser keeps it same-origin: the decoded path answers a
        //     different question from the one asked.
        if raw[0] != '/' {
            return "", false
        }
        // (3) A second "/" or "\" is where a browser starts reading a host.
        //     Position 1 alone covers "//", "///", "////", "/\", "/\/" and every
        //     longer run, because the ignore-slashes state consumes the rest of
        //     the run for us.
        if len(raw) > 1 && (raw[1] == '/' || raw[1] == '\\') {
            return "", false
        }
        // (4) A backslash anywhere: a browser normalises \ to / in a
        //     special-scheme URL, so a later one can move a segment boundary.
        //     Refusing it costs nothing — no route here contains one.
        if strings.ContainsRune(raw, '\\') {
            return "", false
        }
        // (5) A scheme, an authority or userinfo means it was never a path.
        //     A backstop after (2)-(4), not the rule.
        u, err := url.Parse(raw)
        if err != nil || u.Scheme != "" || u.Opaque != "" || u.Host != "" || u.User != nil {
            return "", false
        }
        return u.String(), true
    }

Add an unexported hasControl to internal/web (unicode.IsControl over the runes) — copy the shape of internal/account/account.go:246-253. It is three lines and web must not import account (account imports web). Do NOT delete account.go's own hasControl: it is still used at account.go:214.

Invariant the implementation must hold and the test must assert: SitePath is CLOSED UNDER ITS OWN OUTPUT — for every accepted raw, SitePath(got) must return (got, true). If u.String() ever decoded %2f, the output would be a string the predicate refuses, and that output is what reaches the href.

=== 2. Delete the second definition ===

- Delete SafeNext (internal/account/account.go:255-269) entirely. grep -rn SafeNext finds only handler.go and account_test.go.
- Rewrite its six call sites in internal/account/handler.go as web.SitePathOr(x, "/account") — the package already imports internal/web:
    :117  Next: web.SitePathOr(r.URL.Query().Get("next"), "/account")
    :139  next := web.SitePathOr(r.PostFormValue("next"), "/account")
    :174  pages.AuthView{Next: web.SitePathOr(r.URL.Query().Get("next"), "/account")}
    :190  next := web.SitePathOr(r.PostFormValue("next"), "/account")
    :700  h.google.AuthorizeURL(web.SitePathOr(r.URL.Query().Get("next"), "/account"))
    :810  Next: web.SitePathOr(parts[2], "/account")
  Update the two //nolint:gosec // G710 comments at :164 and :219 to name web.SitePathOr instead of SafeNext.
- The fallback stays "/account" at all six sites: it is where a signed-in customer belongs and it is what SafeNext returned. Do not introduce a package constant — the repository's existing note is that "web.SitePath owns the rule; the FALLBACK is what differs per caller" (internal/account/wishlist_test.go:9).
- Behavioural deltas from the swap, all tightenings, all acceptable, and all to be reflected in the migrated test rows: a backslash anywhere (not just at position 1) now falls back; a control character anywhere now falls back (SafeNext already refused CR/LF); the returned string is re-encoded by u.String(), so "/ /x" becomes "/%20/x" — which is what belongs in an href and in the OAuth state anyway.
- Move the TestSafeNext table rows into the web table (below) and delete TestSafeNext (internal/account/account_test.go:134-155). Keep ONE account-level test that the sign-in redirect falls back to /account, so the fallback choice stays locked where it is made.

=== 3. Close the hero read path ===

internal/home/hero.go:28-30 puts row.PrimaryCtaHref and row.SecondaryCtaHref.String into pages.CTA with no validation; internal/ui/pages/home.templ:27,32 render both through templ.SafeURL. internal/home/banner.go:48-56 already shows the pattern:

    // Typed by a person and rendered into an href at the top of every page.
    href, ok := web.SitePath(row.CtaHref)
    if !ok { href = "" }

Apply the same to both hero CTAs with the banner's both-or-neither collapse: a refused href drops the button (blank the Label too) rather than rendering a dead one. For the PRIMARY href, a refusal must fall back to the whole pages.DefaultHero() CTA pair rather than render a headline with a hrefless button — hero_slides_primary_cta_present requires the pair, and a hero with no primary button is a broken home page. Decide it explicitly and comment it. Carry banner.go:48's one-line comment across.

=== 4. Do NOT add a CHECK constraint ===

State this in the commit message so it is not re-litigated: a cta_href ~ '^/([^/\\]|$)' CHECK on promo_banners and hero_slides would be a THIRD definition of a WHATWG rule, written in a language that cannot know it, and drifting from the Go one is the defect being fixed. The read-time re-validation in §3 is what closes the direct-SQL path, and it is the same single definition.

=== 5. Guard against a fourth definition ===

Add TestOnlySitePathDecidesASameSitePath in internal/web: walk the repository's *.go (excluding internal/db, *_templ.go, *_test.go and internal/web itself) and *.templ, and fail on any strings.HasPrefix( call whose second argument literal is "/", "//" or `/\`. Derive the corpus from the tree, never from a hand-written list — the rule every guard in this repository follows. Tests are excluded for the reason TestEveryViewModelFieldIsAssigned excludes them: this guard's own table would otherwise satisfy it, and a rule re-implemented in a test is the defect rather than the cure.


## The lock, and how to see it fail first

All of this lives in internal/web/sitepath_test.go (package web, no build tag, runs under make verify). No new dependency.

=== A. The table, and how to assert WHATWG rather than Go ===

The existing table asserts the Go-side predicate against itself. Replace the assertion with a browser oracle written into the test, so a case cannot pass merely because net/url agrees with the code:

    // whatwgOrigin resolves ref against a fixed base the way a BROWSER does,
    // not the way RFC 3986 does — url.ResolveReference is the standard the code
    // under test must NOT be measured by. It implements only the states that
    // decide origin:
    //   1. remove every U+0009/U+000A/U+000D from ref (WHATWG strips, not errors)
    //   2. trim leading and trailing C0-and-space
    //   3. if ref[0] is '/' or '\' and ref[1] is '/' or '\', skip the whole run
    //      of '/' and '\' (special-authority-ignore-slashes) and read the host
    //      up to the next '/', '\', '?' or '#'
    //   4. otherwise the origin is the base's
    // Percent-encoding is deliberately NOT decoded: a browser does not decode
    // before parsing, which is why "/%2f%2fevil.example" stays same-origin.
    func whatwgOrigin(ref string) string   // "goen.example", or the foreign host

Pin that helper to a real engine in a comment above it so the oracle is not itself a guess — these are the measured values of new URL(ref, 'https://goen.example/checkout').href in node:

    "///evil.example/x"    -> https://evil.example/x
    "////evil.example/x"   -> https://evil.example/x
    "/\/evil.example/x"    -> https://evil.example/x
    "/<TAB>/evil.example"  -> https://evil.example/
    "/%2f%2fevil.example"  -> https://goen.example/%2f%2fevil.example
    "/ /evil.example"      -> https://goen.example/%20/evil.example

Then, for EVERY row, accepted or refused:

    got, ok := SitePath(tt.raw)
    if ok {
        if whatwgOrigin(tt.raw) != "goen.example" { t.Errorf("accepted %q, which a browser resolves off-site", tt.raw) }
        if whatwgOrigin(got) != "goen.example"    { t.Errorf("returned %q, which a browser resolves off-site", got) }
        if g2, ok2 := SitePath(got); !ok2 || g2 != got { t.Errorf("output %q is not itself accepted", got) }
    }

The table — every existing row KEPT, so the fix cannot regress a legitimate path:

  accepted (want == raw unless noted):
    /deals · /search?q=abc · /faq#shipping · / · /p/%E6%89%8B%E6%A9%9F
    /account · /account/orders · /cart?x=1                (migrated from TestSafeNext)
    /a?b=//c · /a#//b                                     (a // inside query or fragment is not an authority)
    /%2f%2fevil.example                                   (encoded slashes stay same-origin; want == raw, encoding preserved)
    /%5C%5Cevil.example                                   (encoded backslashes, same reason)
    "/ /evil.example" -> want "/%20/evil.example"         (space escaped, origin unchanged)

  refused (want ""):
    "" · //evil.example · //evil.example/x
    ///evil.example/x                                     <- THE FINDING
    ////evil.example/x · /////x · ///                     <- triple, quadruple, bare run
    /\evil.example · /\/evil.example · //\evil.example · \\evil.example · /a\b
    "/\t/evil.example" · "/\n/evil.example" · "/\r/evil.example"   <- WHATWG strips these to "//"
    " //evil.example" · "\t//evil.example"                        <- leading C0-and-space is trimmed
    ///goen.example@evil.example/                                 <- userinfo, triple-slash form
    https://evil.example · http://evil.example/deals · //goen.example@evil.example/
    https://goen.example@evil.example/ · javascript:alert(1) · data:text/html,<script>alert(1)</script>
    deals · ../etc · "/deals\n/evil" · "/x\r\nSet-Cookie: a=b"    (migrated from TestSafeNext)

Use explicit "\t"/"\n"/"\r" escapes in the source and name each row the way the current table does. Extend the file's header comment ("Each refusal below is a real bypass of the naive check, not a hypothetical") to say the same of the SOPHISTICATED check.

=== B. Prove the lock RED before the fix (rules/testing.md, "Locks Are Proven by Mutation") ===

Order matters, because this repository has already recorded a false green in this exact area and its own note is "the mutation must be seen to apply, not assumed".

1. Write the table and the oracle FIRST, against the UNCHANGED sitepath.go. Run go test ./internal/web/. It must fail, and the transcript must name the rows: ///evil.example/x, ////evil.example/x, /////x, ///, ///goen.example@evil.example/. Record separately that the three tab/LF/CR rows PASS today for the wrong reason — url.Parse("/\t/evil.example") returns an error, measured — which is why clause (1) of the fix is explicit rather than inherited.
2. Apply the fix. The table goes green.
3. Mutate each clause one at a time and record which row dies. A clause with no row that dies is a clause with no lock:
   - delete clause (3) (raw[1]=='/'||raw[1]=='\\') -> the whole triple/quadruple block goes red.
   - revert clause (2) to strings.HasPrefix(u.Path, "/") -> the same block goes red. This is the exact mutation back to the shipped code and is the finding's own lock.
   - delete clause (1) hasControl -> the tab/LF/CR rows go red. If they do NOT (url.Parse still erroring), say so in the transcript and keep the clause anyway, with that fact in its comment: an inherited refusal is not a stated rule.
   - delete clause (4) -> /a\b goes red.
   - return u.Path instead of u.String() -> the closed-under-output assertion on /%2f%2fevil.example goes red. That assertion has no equivalent in the current suite and is what guards the encoded-slash direction.

=== C. The consolidation lock ===

TestOnlySitePathDecidesASameSitePath (fix §5). Prove it by mutation too: re-add SafeNext's strings.HasPrefix(next, "//") line to internal/account/account.go, watch the test name that file:line, then delete it again. Without that step the guard is a grep nobody has watched fail.

=== D. The hero and banner read paths ===

In internal/home: build a hero row whose PrimaryCtaHref is ///evil.example/x and assert the resulting pages.Hero carries the DefaultHero CTA pair, not the row's; and a second case where only SecondaryCtaHref is poisoned, asserting the secondary button is absent while the primary survives. Mutation: delete the web.SitePath call added in fix §3 and both must go red.

Mirror it for the banner with the same value — that case exercises the call ALREADY PRESENT at internal/home/banner.go:49, so it must be seen RED against the unfixed sitepath.go and green after. That is the one test that proves the fix is in the predicate and not in a new caller-side patch.

=== E. Regression evidence, end to end ===

Re-run the live probe from the reproduction after the fix: insert the same promo_banners row, curl -s http://127.0.0.1:9700/ | grep goen-promo__link, and the anchor must be gone — href refused, label blanked, so the non-CTA <span> branch at internal/ui/layouts/banner.templ:24 renders instead. Delete the row and confirm the table is clean. This is the only step that proves the whole chain (write gate, read gate, template) rather than the predicate alone, and it is how the defect was demonstrated in the first place.

=== F. Gates ===

make verify twice (mistake #22: a gate that passes on the output of its own previous run), and make test-integration if D lands in an integration file. No templ regeneration is needed — no .templ file changes.
