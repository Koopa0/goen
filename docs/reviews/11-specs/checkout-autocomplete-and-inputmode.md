# checkout-autocomplete-and-inputmode

**Verdict** CONFIRMED · **Severity** medium · **Origin** found in this round's own sweep (already established)

**Files** internal/ui/pages/cart.templ, internal/ui/pages/cart.go, internal/ui/pages/cart_templ.go, internal/ui/pages/account.templ, internal/ui/pages/account_templ.go, internal/ui/pages/autofill_test.go, internal/ui/layouts/header.templ, internal/ui/layouts/header_templ.go


## Root cause

A shared render helper that takes only the parameters its first call site happened to need. `field()` was written for "label + input + error paragraph" and every attribute that varies BY FIELD rather than by shape — autocomplete, inputmode, maxlength — had nowhere to live in its signature, so the one field that needed a token (#street) was written out longhand instead and got one, and the six that went through the helper got nothing. The helper made the omission invisible: there is no place in cart.templ where a reader can see a missing autocomplete, because there is no per-field markup to be missing it from.

That is the same shape as CLAUDE.md #20 ("a field each handler must remember to fill is a field that goes unfilled") one layer down, and the same shape as the aria-describedby sweep: "the checkout's own field() helper had it right from the day it was written and nothing held anything else to it" — here the helper is on the other side of the same asymmetry. Nothing in the repository asks whether an input tells the browser what it is, so uneven application had no way to become visible.


## Reproduction — the evidence this rests on

EXECUTED against the running dev server (not reasoned about).

Seeded a cart through the site's own form and fetched the checkout:

  VARIANT=$(psql "$GOEN_DATABASE_URL" -qtAc "SELECT v.id FROM product_variants v JOIN products p ON p.id=v.product_id WHERE p.status='active' AND v.is_active AND v.stock_quantity>v.safety_stock LIMIT 1")
  curl -s -c ck -b ck -X POST -d "variant=$VARIANT&quantity=1" http://127.0.0.1:9700/cart/items   -> 303
  curl -s -b ck http://127.0.0.1:9700/checkout                                                     -> 200, 15010 bytes

Rendered text-entry controls inside the checkout (radios/hidden stripped), verbatim:

  <input class="ui-input" id="email" type="email" name="email" value="" required aria-invalid="false">
  <input class="ui-input" id="name" type="text" name="name" value="" required aria-invalid="false">
  <input class="ui-input" id="phone" type="tel" name="phone" value="" required aria-invalid="false">
  <input class="ui-input" id="postal_code" type="text" name="postal_code" value="" required aria-invalid="false">
  <input class="ui-input" id="city" type="text" name="city" value="" required aria-invalid="false">
  <input class="ui-input" id="district" type="text" name="district" value="" required aria-invalid="false">
  <input class="ui-input" id="street" type="text" name="street" value="" required maxlength="200" autocomplete="street-address" aria-invalid="false">
  <input class="ui-input" id="coupon" name="coupon" maxlength="32" autocomplete="off" ...>

  grep -c inputmode  -> 0
  grep -o 'autocomplete="[^"]*"' | sort | uniq -c ->
     1 autocomplete="email"        (footer newsletter, layouts/footer.templ:97)
     1 autocomplete="off"          (#coupon)
     1 autocomplete="street-address" (#street)

Every cited line still says what is claimed. The mechanism is internal/ui/pages/cart.templ:522-541 — the shared helper

  templ field(name, label, kind, value string, v *CheckoutView, required bool) {
  	<div class="ui-field">
  		<label class="ui-label" for={ name }>{ label }</label>
  		<input
  			class="ui-input"
  			id={ name }
  			type={ kind }
  			name={ name }
  			value={ value }
  			required?={ required }
  			aria-invalid={ v.Invalid(name) }

— which emits no autocomplete, no inputmode, no maxlength. Its seven call sites are cart.templ:255 (email), :256 (name), :257 (phone), :282 (pickup_store_code), :306 (postal_code), :307 (city), :308 (district). #street (cart.templ:313-327) is written out longhand, which is exactly why it is the one field that got a token.

Also measured, and NOT in the summary — the pickup half of the same form:
  curl "http://127.0.0.1:9700/checkout?ship=ffff0002-0000-4000-8000-000000000002"
  -> #pickup_brand (select), #pickup_store_code, #pickup_store_name: no autocomplete on any of the three.

Two corrections to the summary's framing:

1. "the account address-book form ... is the same shape" is only half true. internal/ui/pages/account.templ:319/323/327/339 ALREADY carry autocomplete="name", "tel", "postal-code" + inputmode="numeric", and "street-address". Exactly two controls are missing a token — #addr-city (:331) and #addr-district (:335) — plus #addr-label (:315). The account form is 5/7 done; the checkout is 1/7. The uneven application is inside one page, not between two.

2. inputmode is likewise present-but-uneven, not absent from the codebase: admincoupon.templ:88/109/125/129/136/148, admincredit.templ:32, adminshipping.templ:58, product.templ:273, returns.templ:77, account.templ:327 and cart.templ:411 (#invoice_tax_id) all carry inputmode="numeric". The checkout's own 統編 field has it and the postal code beside it does not.

No recorded decision covers this: `grep -rn "autocomplete\|autofill\|inputmode\|keyboard" CLAUDE.md docs/*.md` returns nothing, and docs/roadmap.md does not mention it. Working tree verified clean (`git status --porcelain` empty).


## Blast radius

Every mobile customer, on the buying mainline, at the last screen before money moves — six required fields typed by hand that the phone already knows, and a 3-6 digit postal code that opens an alphabetic keyboard because #postal_code is type="text" with no inputmode (isPostalCode at internal/cart/cart.go:442-453 accepts digits only, 3-6 of them). Every order, not an edge case. The pickup form is worse in one respect and better in another: three more unfilled fields, but a numeric keypad there would be wrong (see the fix).

Silent by construction. Nothing is refused, nothing errors, no log line, no constraint fires — a shop reading /admin/reports sees only a checkout-completion fraction it has no way to explain, because CLAUDE.md records that "goen collects no traffic data, so what fraction of VISITORS bought is a number it cannot know". The one person who can see the defect is the customer, on their own phone, and they leave.

It is also an accessibility fact and not only a convenience one: WCAG 2.2 SC 1.3.5 Identify Input Purpose is exactly "the autocomplete token on a field collecting the user's own information", and it is the class of failure this repository already went after twice — mistake #36 (aria-describedby on 43 fields) and check-layout's seven DOM rules. Someone using a switch device or voice control pays the six manual entries in minutes, not seconds.


## Fix

Three edits, all in internal/ui/pages. No schema change, no handler change, no new dependency. Regenerate with `make templ` (`go tool templ generate -path internal/ui`) — that exact invocation and no other, per CLAUDE.md #22.

=== 1. internal/ui/pages/cart.go — one table, the single place that decides ===

Add an UNEXPORTED type and an unexported package-level map (unexported so TestEveryViewModelFieldIsAssigned, which walks exported structs only, does not adopt it as a view model):

	// fieldHints is what a browser needs in order to fill a control and which
	// keyboard to open for it. The checkout's inputs go through one helper, so
	// these cannot be written per field in the template — and templ fmt splits a
	// multi-attribute element across lines and separates an attribute from any
	// comment above it, so a per-field reason cannot survive a format there
	// either. They are Go, keyed on the field's own name: the same key the view
	// model's errors already use.
	type fieldHints struct {
		// Autocomplete is a WHATWG autofill token, or "off". Never empty: "no
		// token is right here" is a decision, and a decision is written down.
		Autocomplete string
		// InputMode is empty wherever the control's type already selects the
		// right keyboard.
		InputMode      string
		AutoCapitalize string
		SpellCheck     string // "false", or empty to leave the default
	}

	// checkoutHints is that table. Bare tokens rather than the "shipping "
	// prefixed forms: goen collects exactly one address on this page — billing
	// details are Stripe's, on Stripe's own page — so there is nothing to
	// disambiguate from, and the bare tokens are the ones every engine honours.
	var checkoutHints = map[string]fieldHints{
		"email": {Autocomplete: "email"},
		// One control, one column (order_private_data.recipient_name), and a
		// Chinese name is written 姓+名 with no separator: given-name/family-name
		// would need a rule for where the surname ends, which 複姓 (歐陽, 司徒)
		// makes undecidable from the string. "name" is also what
		// account.templ:319 and contact.templ:107 already use.
		"name": {Autocomplete: "name"},
		// type="tel" already opens the telephone keypad. inputmode="numeric"
		// would REMOVE + ( ) and space, which looksLikePhone accepts
		// (internal/cart/cart.go:428-440), so a customer typing +886 could not.
		"phone":       {Autocomplete: "tel"},
		"postal_code": {Autocomplete: "postal-code", InputMode: "numeric"},
		// 縣市 is the administrative area; 鄉鎮市區 is the locality below it.
		"city":     {Autocomplete: "address-level1"},
		"district": {Autocomplete: "address-level2"},
		// A store, not a person's address. "organization" or "address-level3"
		// would have a browser offer a home address for a 店名, and the shop
		// packs the parcel from these three fields.
		//
		// NO inputmode="numeric" on the code: it is ^[0-9A-Z]{1,10}$ and 149 of
		// 萊爾富's 1,350 stores lead with a LETTER. A numeric keypad is mistake
		// #28 relocated from the CHECK into the keyboard — the same 11% of one
		// chain, unreachable, one customer at a time, with nothing for the shop
		// to see.
		"pickup_store_code": {Autocomplete: "off", AutoCapitalize: "characters", SpellCheck: "false"},
	}

	// hintsFor is total: a control the table does not name renders no autofill
	// token, which is what TestEveryCheckoutFieldTellsTheBrowserWhatItIs
	// refuses.
	func hintsFor(name string) fieldHints { return checkoutHints[name] }

=== 2. internal/ui/pages/cart.templ ===

(a) Rewrite the helper at :522-541 to read the table. Bind once so the map is
    looked up a single time:

	templ field(name, label, kind, value string, v *CheckoutView, required bool) {
		{{ h := hintsFor(name) }}
		<div class="ui-field">
			<label class="ui-label" for={ name }>{ label }</label>
			<input
				class="ui-input"
				id={ name }
				type={ kind }
				name={ name }
				value={ value }
				required?={ required }
				autocomplete={ h.Autocomplete }
				if h.InputMode != "" {
					inputmode={ h.InputMode }
				}
				if h.AutoCapitalize != "" {
					autocapitalize={ h.AutoCapitalize }
				}
				if h.SpellCheck != "" {
					spellcheck={ h.SpellCheck }
				}
				aria-invalid={ v.Invalid(name) }
				if v.HasErr(name) {
					aria-describedby={ name + "-error" }
				}
			/>
			if v.HasErr(name) {
				<p class="ui-error-text" id={ name + "-error" }>{ v.Err(name) }</p>
			}
		</div>
	}

    Nothing at the seven call sites changes. That is the point of putting it here.

(b) The three controls the helper does not render, each gets a literal
    autocomplete="off":
      - :263  <select id="pickup_brand">      — no token names a convenience-store chain
      - :287  <input id="pickup_store_name">  — a 店名, not a person's address
      - :334  <textarea id="note">            — a free-text delivery note

(c) :313-327 #street keeps autocomplete="street-address" unchanged. The value IS
    the whole remainder of the address in one string (路/段/巷/弄/號/樓, 200 runes),
    not line 1 of several, so address-line1 would be the narrower claim; the token
    is already in the tree and in the account form, and changing it buys nothing
    measurable.

(d) #coupon (:477) and #invoice_carrier / #invoice_tax_id (:387/:409) keep
    autocomplete="off" — already correct, and named in the guard's exemption
    table below so they cannot be "tidied" into a token later.

=== 3. internal/ui/pages/account.templ — the address book, literal markup ===

	:331  id="addr-city"     → add autocomplete="address-level1"
	:335  id="addr-district" → add autocomplete="address-level2"
	:315  id="addr-label"    → add autocomplete="off"  (「家」/「公司」 is the
	      customer's own nickname for the row; "organization" would fill a company name)

	:319/:323/:327/:339 already carry name / tel / postal-code+numeric /
	street-address. Leave them exactly as they are — they are the precedent this
	change is bringing the checkout up to.

=== enterkeyhint: NOT added to the checkout, and this is the interesting half ===

Do not add enterkeyhint to any checkout control. TestEnterInTheCheckoutPlacesTheOrder
(internal/ui/pages/cart_test.go:632) locks a leading inert submit so that Enter in
ANY field places the order. enterkeyhint="next" would therefore label a key that
in fact charges the customer — a worse lie than the unlabelled default, and one
only a mobile customer would ever meet. Record that reason in a comment above
checkoutHints so it is refused rather than re-filed.

The one place enterkeyhint is honest is the header's sole-control GET form:
internal/ui/layouts/header.templ:51 `<input id="site-search" type="search" name="q">`
→ add enterkeyhint="search" and autocomplete="off" (a stored e-mail offered for a
product search). Optional, one line, outside the guard's corpus.

=== Explicitly out of scope ===

maxlength on the six helper-rendered fields. #email/#name have Go bounds
(maxEmailRunes 254, maxNameRunes 60, internal/cart/cart.go:288-296) but city,
district and phone have none, so any number typed into the template would be a
second rule the server does not enforce — a guess with the force of a limit,
which is #28's lesson. If it is wanted, it starts by naming the constants in
internal/cart, not in the template.

=== Verification ===

	make templ && make fmt-check && make templ-check && make lint && make test-race
and re-measure through the running server exactly as in the reproduction above:
`grep -o 'autocomplete="[^"]*"'` on /checkout must show seven tokens plus the
coupon's off, and `grep -c inputmode` must be 1.


## The lock, and how to see it fail first

New file internal/ui/pages/autofill_test.go, package `pages` (NOT pages_test — it reuses renderToString from cart_test.go:349 and the unexported checkoutHints). One test:

  func TestEveryCheckoutFieldTellsTheBrowserWhatItIs(t *testing.T)

CORPUS DERIVED, DECISIONS NAMED. The corpus is the rendered document, not a list:
render Checkout(CheckoutMeta(ctx), v) twice — once with Destination "" (address
half) and once with Destination "pickup_point" (pickup half) — plus
Account(AccountMeta(ctx), &AccountView{Addresses: nil}) for the address book. For
each, cut the form out the way cart_test.go:643-646 already does
(strings.Cut(html, `action="/checkout"`) then Cut(rest, "</form>")); for the
account use `action="/account/addresses"`. Scoping to the form is what keeps the
chrome (#site-search, #newsletter-email) out of it.

Match every text-entry control in the form with
`<(input|select|textarea)\b[^>]*>` and skip type=radio/checkbox/hidden/submit
(no autofill token applies to them — state that in a comment, do not exempt them
by id). Identify each by its id.

Three assertions:

 1. PRESENCE, derived: every control in the corpus carries an autocomplete
    attribute. No list. This is the half that catches a NEW checkout field the
    day it is added.

 2. TOKEN, named by identity: a table in the test
       want := map[string]string{
         "email": "email", "name": "name", "phone": "tel",
         "postal_code": "postal-code", "city": "address-level1",
         "district": "address-level2", "street": "street-address",
         "addr-name": "name", "addr-phone": "tel", "addr-postal": "postal-code",
         "addr-city": "address-level1", "addr-district": "address-level2",
         "addr-street": "street-address",
       }
    Every id in `want` must render with that exact token, AND — the fields_test.go
    rule, checked by identity rather than by count — every id in `want` must have
    been SEEN in the corpus, or the entry is stale and the test says so. Without
    that second direction an entry naming a control that no longer renders passes
    forever (fields_test.go:66-71 is the precedent, and CLAUDE.md records that
    TestEveryCategoryNameIsLocalized's first version compared totals and shipped
    two entries added on a guess).

 3. OFF, a named exemption list with a reason per entry, also checked by identity:
       offBecause := map[string]string{
         "coupon":            "a promotion code is not the customer's own data; a browser offering the last one is offering somebody else's",
         "invoice_carrier":   "a 手機條碼載具 is not an autofill category",
         "invoice_tax_id":    "no WHATWG token names a 統一編號",
         "pickup_brand":      "no token names a convenience-store chain, and a wrong chain is a parcel at the wrong counter",
         "pickup_store_code": "a store, not the customer's address",
         "pickup_store_name": "a store, not the customer's address",
         "note":              "a free-text delivery note; a stored address filled in here would be wrong",
         "addr-label":        "the customer's own nickname for the row",
       }
    Each must render exactly autocomplete="off"; each must have been seen.

 4. KEYBOARD, both directions:
       - #postal_code and #addr-postal MUST carry inputmode="numeric".
       - #pickup_store_code MUST NOT carry inputmode, and the failure message
         must name mistake #28 and the 149 萊爾富 codes: a guard that only asks
         for the presence of a good thing cannot refuse a plausible bad one, and
         "add numeric to the store code" is precisely what the next reader will
         try.
       - #phone MUST NOT carry inputmode="numeric" (it would drop +886).
       - No control in the checkout form carries enterkeyhint, citing
         TestEnterInTheCheckoutPlacesTheOrder: Enter there places the order, so
         "next" labels a key that charges the customer.

 5. A floor, the repo's standard guard against a regex that stopped matching:
    at least 10 controls examined across the three renders; t.Fatalf otherwise.

PROVING THE LOCK — run each, see red, restore. `make templ` after every .templ
edit (and only `go tool templ generate -path internal/ui`, #22); the Go-table
mutations need no regeneration, which is worth knowing while iterating.

 a. THE NATURAL RED, first: run the new test on the UNFIXED tree, before touching
    cart.go/cart.templ/account.templ. It must fail naming exactly #email, #name,
    #phone, #postal_code, #city, #district, #pickup_brand, #pickup_store_code,
    #pickup_store_name, #note, #addr-city, #addr-district, #addr-label and the
    missing inputmode on #postal_code. That is the defect, seen. Record the
    output in the PR.
 b. Presence half: delete the `autocomplete={ h.Autocomplete }` line from the
    helper, `make templ`, run → red on all seven helper-rendered fields.
 c. Token half: change "city" in checkoutHints to "address-level2" → red naming
    the wrong token. Proves assertion 2 is not satisfied by mere presence.
 d. Corpus half: delete the "district" entry from checkoutHints entirely → the
    helper renders autocomplete="" → red from assertion 1. Proves the table IS
    the source and a forgotten field cannot pass.
 e. Staleness half: add `"county": "address-level1"` to the test's `want` → red
    with "names a control the checkout does not render". Proves the identity
    check, not just the count.
 f. Keyboard, positive: drop InputMode from the postal_code entry → red.
 g. Keyboard, negative: add InputMode "numeric" to the pickup_store_code entry →
    red citing #28. This is the mutation most worth showing, because it is the
    one a future well-meaning change actually makes.

DO NOT also add this rule to scripts/check-layout.mjs. It visits /checkout and
/checkout?ship=PICKUP_SHIP (scripts/check-layout.mjs:84-91) so it would have a
subject, unlike the aria rule — but two homes for one claim is two places for it
to drift, and the aria precedent (internal/ui/pages/aria_test.go:26-31) puts the
coverage claim in Go deliberately. One sentence in the new test's doc comment
saying so is enough.

If CLAUDE.md gains a paragraph about this work, it must name the test exactly —
TestEveryNamedTestExists (internal/ui/pages/namedtests_test.go:15) resolves every
Test name mentioned in a .md/.go/.sql file against a func that exists.
