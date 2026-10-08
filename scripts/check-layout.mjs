// Layout conformance for the storefront, measured over the Chrome DevTools
// Protocol.
//
// Why this exists as a driven browser rather than a screenshot: Chrome on macOS
// refuses to open a window narrower than ~500px, so `--window-size=375` renders
// at 500 and crops. Overflow it shows is fake and overflow it hides is missed.
// Emulation.setDeviceMetricsOverride sets the layout viewport for real, and
// every assertion below reads a box off the live layout.
//
// Usage: make check-layout   (needs Chrome and a server on GOEN_URL)

import { readFileSync } from 'node:fs';
import { AXE_OPTIONS, WCAG_TAGS, WCAG_LEVEL, gatesAccessibility, wcagRuleExclusion } from './wcag-gate.mjs';
import { contrastRatio, measureControlBoundary } from './control-boundary.mjs';
import { fieldFaults } from './field-faults.mjs';
import { measureChooserStates, measureSwatchState } from './forced-colours.mjs';

const LAYOUT_DIR = process.env.LAYOUT_DIR || '.layout-chrome';
const CDP_PORT = Number(process.env.CDP_PORT || 9222);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700/').replace(/\/$/, '');

// axe-core runs once per route at the end of this file, over the same CDP
// session everything else uses.
//
// Its source is read HERE, before Chrome is contacted, so an absent file stops
// the run in a second rather than after ten minutes of measuring. A gate that
// quietly skips its own audit when an input is missing is not a gate.
//
// It is never a <script src>: the site sends `script-src 'self'`, and relaxing
// that so the checker could get in would mean auditing a page no visitor is
// served. A CDP evaluation runs outside the page's CSP and leaves the document
// exactly as a visitor receives it.
const AXE_SOURCE = process.env.AXE_SOURCE || `${LAYOUT_DIR}/axe.min.js`;
const AXE_BASELINE = process.env.AXE_BASELINE || 'scripts/axe-baseline.json';

// One width. Every rule asked for below is a property of the document rather
// than of the fold, and the widths are already covered by the geometry
// assertions above.
const AXE_WIDTH = { width: 1440, height: 900 };

let axeSource;
try {
  axeSource = readFileSync(AXE_SOURCE, 'utf8');
} catch (err) {
  console.error(`axe-core is not readable at ${AXE_SOURCE}: ${err.message}\n` +
    'make check-layout fetches it at the Makefile\'s AXE_CORE_VERSION and verifies ' +
    'its digest; run this check through make rather than by hand.');
  process.exit(2);
}

// The accessibility debt already in the tree, as route -> rule ids. A pair that
// is not listed fails the run; a pair listed that no longer fires fails it too,
// so this file can only shrink. Rule ids and not selectors, because the markup
// under a rule changes every week and a stale selector would fail a run for a
// reason that has nothing to do with accessibility.
let axeBaselineFile;
try {
  axeBaselineFile = JSON.parse(readFileSync(AXE_BASELINE, 'utf8'));
} catch (err) {
  console.error(`the axe baseline at ${AXE_BASELINE} did not parse: ${err.message}`);
  process.exit(2);
}
const axeBaseline = axeBaselineFile.routes || {};

// The same file, for the 320px and 200%-text pass at the end of this file: route
// -> the checks that fail there today. It is read here so an unparsable file
// stops the run before the measuring starts.
const REFLOW_BASELINE = process.env.REFLOW_BASELINE || 'scripts/reflow-baseline.json';
let reflowBaselineFile;
try {
  reflowBaselineFile = JSON.parse(readFileSync(REFLOW_BASELINE, 'utf8'));
} catch (err) {
  console.error(`the reflow baseline at ${REFLOW_BASELINE} did not parse: ${err.message}`);
  process.exit(2);
}
const reflowBaseline = reflowBaselineFile.routes || {};

// What the two artboards fold into. The seeded six departments are square
// photographs, three to a line on a phone and six from 768; the
// seeded campaign has four products and a wide first photograph, so its row is
// the lead tile (a full row under 1024, then 2 + 1 beside it) and the campaign
// card. Column counts are read off the rendered
// boxes — how many children share the top row — not off the CSS, so a rule that
// stops applying is caught rather than a rule that stops existing.
const EXPECTED = [
  { label: '375 (artboard)', width: 375, height: 812, cats: 3, tiles: 1, hero: 'stacked' },
  { label: '768 (md)', width: 768, height: 1024, cats: 6, tiles: 1, hero: 'stacked' },
  { label: '1024 (lg)', width: 1024, height: 900, cats: 6, tiles: 3, hero: 'side-by-side' },
  { label: '1440 (artboard)', width: 1440, height: 900, cats: 6, tiles: 3, hero: 'side-by-side' },
];

// Every page that renders a document, at a phone width and at the artboard.
//
// A browser measurement is the only way an overflow or a 30px tap target is
// found. Back-office tables are deliberately wider than a phone and scroll
// inside their own box; only this says whether the PAGE stayed put.
const PAGES = [
  { label: 'campaign 375', width: 375, height: 812, path: '/s/layout-campaign', marker: '.goen-tiles__grid .goen-tile' },
  { label: 'campaign 1440', width: 1440, height: 900, path: '/s/layout-campaign', marker: '.goen-tiles__grid .goen-tile' },
  { label: 'about 375', width: 375, height: 812, path: '/about', marker: '.about' },
  { label: 'about 1440', width: 1440, height: 900, path: '/about', marker: '.about' },
  { label: 'contact 375', width: 375, height: 812, path: '/contact', marker: 'form' },
  { label: 'contact 1440', width: 1440, height: 900, path: '/contact', marker: 'form' },
  { label: 'faq 375', width: 375, height: 812, path: '/faq', marker: '.goen-doc' },
  { label: 'find order 375', width: 375, height: 812, path: '/orders/find', marker: '.goen-auth__form' },
  { label: 'find order 1440', width: 1440, height: 900, path: '/orders/find', marker: '.goen-auth__form' },
  { label: 'verify 375', width: 375, height: 812, path: '/verify?token=demo', marker: '.notice__form' },
  { label: 'verify 1440', width: 1440, height: 900, path: '/verify?token=demo', marker: '.notice__form' },
  { label: 'newsletter confirm 375', width: 375, height: 812, path: '/newsletter/confirm?token=demo', marker: '.notice__form' },
  { label: 'newsletter confirm 1440', width: 1440, height: 900, path: '/newsletter/confirm?token=demo', marker: '.notice__form' },
  { label: 'newsletter unsubscribe 375', width: 375, height: 812, path: '/newsletter/unsubscribe?token=demo', marker: '.notice__form' },
  { label: 'newsletter unsubscribe 1440', width: 1440, height: 900, path: '/newsletter/unsubscribe?token=demo', marker: '.notice__form' },
  { label: 'faq 1440', width: 1440, height: 900, path: '/faq', marker: '.goen-doc' },
  { label: 'shipping 375', width: 375, height: 812, path: '/shipping', marker: '.goen-doc' },
  { label: 'shipping 1440', width: 1440, height: 900, path: '/shipping', marker: '.goen-doc' },
  { label: 'returns 375', width: 375, height: 812, path: '/returns', marker: '.goen-doc' },
  { label: 'returns 1440', width: 1440, height: 900, path: '/returns', marker: '.goen-doc' },
  { label: 'warranty-policy 375', width: 375, height: 812, path: '/warranty', marker: '.goen-doc' },
  { label: 'warranty-policy 1440', width: 1440, height: 900, path: '/warranty', marker: '.goen-doc' },
  { label: 'privacy 375', width: 375, height: 812, path: '/privacy', marker: '.goen-doc' },
  { label: 'privacy 1440', width: 1440, height: 900, path: '/privacy', marker: '.goen-doc' },
  { label: 'terms 375', width: 375, height: 812, path: '/terms', marker: '.goen-doc' },
  { label: 'terms 1440', width: 1440, height: 900, path: '/terms', marker: '.goen-doc' },
  { label: 'payment-policy 375', width: 375, height: 812, path: '/payment', marker: '.goen-doc' },
  { label: 'payment-policy 1440', width: 1440, height: 900, path: '/payment', marker: '.goen-doc' },
  { label: 'signin 375', width: 375, height: 812, path: '/signin', marker: '.goen-auth__form' },
  { label: 'signin 1440', width: 1440, height: 900, path: '/signin', marker: '.goen-auth__form' },
  { label: 'register 375', width: 375, height: 812, path: '/register', marker: '.goen-auth__form' },
  { label: 'register 1440', width: 1440, height: 900, path: '/register', marker: '.goen-auth__form' },
  { label: 'search 375', width: 375, height: 812, path: '/search?q=pixelight', marker: '.goen-listing' },
  { label: 'search 1440', width: 1440, height: 900, path: '/search?q=pixelight', marker: '.goen-listing' },
  // A query of only NBSP is the empty prompt, not a no-results heading for an
  // invisible term. Measuring /search?q=pixelight never visits that state.
  { label: 'search nbsp 375', width: 375, height: 812, path: '/search?q=%C2%A0', marker: '.ui-empty' },
  { label: 'search nbsp 1440', width: 1440, height: 900, path: '/search?q=%C2%A0', marker: '.ui-empty' },
  // The same Unicode trim at the edges of a real term: heading and ILIKE both
  // see "pixelight". The grid is the surface that term produces.
  { label: 'search edge-trim 375', width: 375, height: 812, path: '/search?q=%C2%A0%E3%80%80pixelight%E2%80%83', marker: '.goen-listing' },
  { label: 'search edge-trim 1440', width: 1440, height: 900, path: '/search?q=%C2%A0%E3%80%80pixelight%E2%80%83', marker: '.goen-listing' },
  { label: 'deals 375', width: 375, height: 812, path: '/deals', marker: '.goen-listing' },
  { label: 'deals 1440', width: 1440, height: 900, path: '/deals', marker: '.goen-listing' },
  { label: 'pdp 375', width: 375, height: 812, path: '/p/PRODUCT_SLUG', marker: '.goen-pdp' },
  { label: 'pdp 1440', width: 1440, height: 900, path: '/p/PRODUCT_SLUG', marker: '.goen-pdp' },
];

// The comparison TABLE. Enough() needs two columns; one p= is the too-few
// empty state, and .goen-compare wraps that state too. A marker on the
// wrapper measures chrome and calls the table covered.
// COMPARE_SLUG is a second active seed product. table: true is what says
// this row measured columns, the sticky first cell, and (at 375) overflow
// inside the scroll box — not merely that a marker existed.
//
// The one-product empty state is a different page. It cannot stand in for
// the table.
const COMPARE = [
  { label: 'compare 375', width: 375, height: 812, path: '/compare?p=PRODUCT_SLUG&p=COMPARE_SLUG', marker: '.goen-compare__table', table: true },
  { label: 'compare 1440', width: 1440, height: 900, path: '/compare?p=PRODUCT_SLUG&p=COMPARE_SLUG', marker: '.goen-compare__table', table: true },
  { label: 'compare one 375', width: 375, height: 812, path: '/compare?p=PRODUCT_SLUG', marker: '.ui-empty' },
  { label: 'compare one 1440', width: 1440, height: 900, path: '/compare?p=PRODUCT_SLUG', marker: '.ui-empty' },
];

const CART = [
  { label: 'cart 375', width: 375, height: 812, path: '/cart' },
  { label: 'cart 1440', width: 1440, height: 900, path: '/cart' },
  { label: 'checkout 375', width: 375, height: 812, path: '/checkout' },
  { label: 'checkout 1440', width: 1440, height: 900, path: '/checkout' },
  // The checkout with 超商取貨 chosen. It is a different form — a chain to
  // choose rather than a street address — so a layout row for the default
  // method measures only half the page. PICKUP_SHIP is the version id
  // scripts/check-layout.sql reads from the database, and the marker insists
  // the chain chooser is there. Checkout offers 超商取貨 only where the store
  // map is configured, so the server under test must have GOEN_ECPAY_LOGISTICS
  // set.
  { label: 'pickup 375', width: 375, height: 812, path: '/checkout?ship=PICKUP_SHIP', marker: 'input[name=pickup_chain]' },
  { label: 'pickup 1440', width: 1440, height: 900, path: '/checkout?ship=PICKUP_SHIP', marker: 'input[name=pickup_chain]' },
  // The payment page. PLACED_ORDER is the NUMBER of the order
  // scripts/check-layout.sql places; PLACED_TOKEN, set as a cookie above, is
  // the browser's proof that it placed it. Two facts, two variables — without either the page is the 404 a
  // stranger gets and the check measures nothing, which is why the probe below
  // insists the heading is there.
  // The promotional strip. It sits above the header IN FLOW, so what is
  // measured is that it is one row at both widths and that its controls meet
  // the touch minimum — a two-line banner changes the page's height between
  // one promotion and the next.
  { label: 'promo 375', width: 375, height: 812, path: '/', marker: '.goen-promo' },
  { label: 'promo 1440', width: 1440, height: 900, path: '/', marker: '.goen-promo' },
  { label: 'pay 375', width: 375, height: 812, path: '/orders/PLACED_ORDER/pay', marker: '.goen-pay' },
  { label: 'pay 768', width: 768, height: 1024, path: '/orders/PLACED_ORDER/pay', marker: '.goen-pay' },
  { label: 'pay 1440', width: 1440, height: 900, path: '/orders/PLACED_ORDER/pay', marker: '.goen-pay' },
  // The way back in. These need no fixture at all — which is the point: they
  // are the two pages a locked-out customer reaches, so anything that stops
  // them rendering stops the account being recoverable.
  { label: 'forgot 375', width: 375, height: 812, path: '/forgot', marker: '.goen-auth__form' },
  { label: 'forgot 1440', width: 1440, height: 900, path: '/forgot', marker: '.goen-auth__form' },
  { label: 'reset 375', width: 375, height: 812, path: '/reset?token=layoutcheck', marker: '.goen-auth__form' },
  { label: 'reset 1440', width: 1440, height: 900, path: '/reset?token=layoutcheck', marker: '.goen-auth__form' },
];

// The listing page. Its filters are a toolbar open above the grid from lg and a
// collapsed disclosure above it below; either way they sit over the results, so
// the rail is always 'stacked'. What lg changes is whether the controls show
// (`toolbar`), which the desktop probes below assert.
const LISTING = [
  { label: 'listing 375', width: 375, height: 812, rail: 'stacked' },
  { label: 'listing 768', width: 768, height: 1024, rail: 'stacked' },
  { label: 'listing 1024', width: 1024, height: 900, rail: 'stacked', toolbar: true },
  { label: 'listing 1440', width: 1440, height: 900, rail: 'stacked', toolbar: true },
];

// English category names in the desktop header compete with the search field.
// A row that never sets goen_locale and never asks the field's width cannot
// see a crush. Home and listing at 1024/1440 are the surfaces that show the bar.
const HEADER_EN = [
  { label: 'header en 1024', width: 1024, height: 900, path: '/' },
  { label: 'header en 1440', width: 1440, height: 900, path: '/' },
  { label: 'listing header en 1024', width: 1024, height: 900, path: '/c/phones' },
  { label: 'listing header en 1440', width: 1440, height: 900, path: '/c/phones' },
  // Staff have the widest bar: the wordmark, the heart and "Back office" all show from 1024.
  { label: 'header en staff 1024', width: 1024, height: 900, path: '/', staff: true },
  { label: 'header en staff 1440', width: 1440, height: 900, path: '/', staff: true },
];

// The back office. Needs a staff session, which scripts/check-layout.sql
// provides through ADMIN_TOKEN; without one these are skipped rather than
// silently measuring a sign-in page.
const ADMIN = [
  { label: 'admin 375', width: 375, height: 812, path: '/admin', marker: '.goen-spark' },
  { label: 'admin 1440', width: 1440, height: 900, path: '/admin', marker: '.goen-spark' },
  { label: 'admin stock 375', width: 375, height: 812, path: '/admin/stock' },
  { label: 'admin orders 375', width: 375, height: 812, path: '/admin/orders' },
  { label: 'admin picking 375', width: 375, height: 812, path: '/admin/orders/picking/slips', marker: '.goen-admin__slip' },
  { label: 'admin picking 1440', width: 1440, height: 900, path: '/admin/orders/picking/slips', marker: '.goen-admin__slip' },
  // The back-office pages with the widest tables.
  { label: 'admin products 375', width: 375, height: 812, path: '/admin/products', marker: '.goen-admin' },
  { label: 'admin products 1440', width: 1440, height: 900, path: '/admin/products', marker: '.goen-admin' },
  // The product EDIT page, the busiest form in the back office. The marker is
  // its 規格表 table: PRODUCT_SLUG comes from the seed and has three specs, so a
  // row without it would measure the empty state.
  { label: 'admin product 375', width: 375, height: 812, path: '/admin/products/PRODUCT_SLUG', marker: '.ui-table' },
  { label: 'admin product 1440', width: 1440, height: 900, path: '/admin/products/PRODUCT_SLUG', marker: '.ui-table' },
  // The sales and reviews card in its drawn state: scripts/check-layout.sql gives
  // PRODUCT_SLUG paid orders on nine shop days and five visible reviews, so a row
  // missing either drawing fails.
  { label: 'admin product standing 320', width: 320, height: 568, path: '/admin/products/PRODUCT_SLUG',
    marker: '#sec-standing:has(.goen-chart__frame--columns):has(.goen-spread)' },
  { label: 'admin product standing 375', width: 375, height: 812, path: '/admin/products/PRODUCT_SLUG',
    marker: '#sec-standing:has(.goen-chart__frame--columns):has(.goen-spread)' },
  { label: 'admin product standing 1440', width: 1440, height: 900, path: '/admin/products/PRODUCT_SLUG',
    marker: '#sec-standing:has(.goen-chart__frame--columns):has(.goen-spread)' },
  // .goen-report__rows--returned .goen-chartbar: scripts/check-layout.sql gives the
  // report two best sellers, one at four digits, paid orders on seven shop days with a period total of seven digits (the running totals' .goen-chart), and a return of a quarter of the
  // second's units, so the row waits for the returned-products bar, the one with
  // the most text; without it the page measured is the one-sentence state.
  // .goen-chart__frame--columns:has(.goen-chart__strip): the paid-orders columns
  // with a campaign over them. Seven paid days give the full drawing, and the
  // running campaign layout-campaign (scripts/check-layout.sql:44, now - 3 days to
  // now + 1 day) is bracketed with its "until ..." name, which has to fit at 320.
  // .goen-chartrangebar: the same script adds ten paid orders on one SKU with a
  // ledger that starts twenty days back, so its row carries the range bar, the range text
  // and the triangle with its text alternative; without it the rows measured say only that sales are too few.
  { label: 'admin reports 320', width: 320, height: 568, path: '/admin/reports', marker: '.goen-admin:has(.goen-report__rows--returned .goen-chartbar):has(.goen-chart)' },
  { label: 'admin reports 375', width: 375, height: 812, path: '/admin/reports', marker: '.goen-admin:has(.goen-report__rows--returned .goen-chartbar):has(.goen-chart)' },
  { label: 'admin reports 1440', width: 1440, height: 900, path: '/admin/reports', marker: '.goen-admin:has(.goen-report__rows--returned .goen-chartbar):has(.goen-chart)' },
  { label: 'admin reports columns 320', width: 320, height: 568, path: '/admin/reports', marker: '.goen-chart__frame--columns:has(.goen-chart__strip)' },
  { label: 'admin reports columns 375', width: 375, height: 812, path: '/admin/reports', marker: '.goen-chart__frame--columns:has(.goen-chart__strip)' },
  { label: 'admin reports columns 1440', width: 1440, height: 900, path: '/admin/reports', marker: '.goen-chart__frame--columns:has(.goen-chart__strip)' },
  // .goen-report__departments .goen-chartbar: the two best sellers are in two
  // departments, so the row waits for the department bars; without them it
  // measured the page with one department or none.
  { label: 'admin reports departments 320', width: 320, height: 568, path: '/admin/reports', marker: '.goen-report__departments .goen-chartbar' },
  { label: 'admin reports departments 375', width: 375, height: 812, path: '/admin/reports', marker: '.goen-report__departments .goen-chartbar' },
  { label: 'admin reports departments 1440', width: 1440, height: 900, path: '/admin/reports', marker: '.goen-report__departments .goen-chartbar' },
  { label: 'admin reports stock 320', width: 320, height: 568, path: '/admin/reports', marker: '.goen-chartrangebar' },
  { label: 'admin reports stock 375', width: 375, height: 812, path: '/admin/reports', marker: '.goen-chartrangebar' },
  { label: 'admin reports stock 1440', width: 1440, height: 900, path: '/admin/reports', marker: '.goen-chartrangebar' },
  { label: 'admin audit 375', width: 375, height: 812, path: '/admin/audit', marker: '.goen-admin__auditrow' },
  { label: 'admin audit 1440', width: 1440, height: 900, path: '/admin/audit', marker: '.goen-admin__auditrow' },
  // .goen-chartmeter: scripts/check-layout.sql adds a coupon with a total limit,
  // so a row without a meter measured the list of uncapped coupons.
  { label: 'admin coupons 375', width: 375, height: 812, path: '/admin/coupons', marker: '.goen-chartmeter' },
  { label: 'admin coupons 1440', width: 1440, height: 900, path: '/admin/coupons', marker: '.goen-chartmeter' },
  { label: 'admin credit 375', width: 375, height: 812, path: '/admin/credit', marker: '.goen-admin' },
  { label: 'admin credit 1440', width: 1440, height: 900, path: '/admin/credit', marker: '.goen-admin' },
  // .ui-table and not .goen-health: the status list is always present, so a
  // marker on it measures the HEALTHY page — chrome and nothing else — while
  // the alarm tables an operator has to act on go unrendered.
  // scripts/check-layout.sql seeds an unreconciled payment and an alarmed 折讓
  // claim for exactly this.
  { label: 'admin health 375', width: 375, height: 812, path: '/admin/health', marker: '.ui-table' },
  { label: 'admin health 1440', width: 1440, height: 900, path: '/admin/health', marker: '.ui-table' },
  // ONE order in full, which is where every invoice control lives: issue, void
  // and the 折讓 form. The list had rows and the detail page had none, so no
  // browser had ever rendered a form on the page that files a tax document.
  // INVOICE_ORDER is the order scripts/check-layout.sql refunds and then
  // files against — the unpaid guest order PLACED_ORDER cannot carry a
  // compensation, so a 折讓 on that page would violate invoice_allowance_valid.
  // The stranded claim is an invoice_operations row, which is what puts the
  // health alarm on /admin/health; the form here needs the refunded room.
  { label: 'admin order 375', width: 375, height: 812, path: '/admin/orders/INVOICE_ORDER', marker: '.goen-admin__form' },
  { label: 'admin order 1440', width: 1440, height: 900, path: '/admin/orders/INVOICE_ORDER', marker: '.goen-admin__form' },
  // The three notices a void or 折讓 can land on. The order page without a
  // query flag never renders them, so the rows above measure the form and
  // miss the copy.
  { label: 'admin order voidreason 375', width: 375, height: 812, path: '/admin/orders/INVOICE_ORDER?voidreason=1', marker: '.ui-alert' },
  { label: 'admin order voidreason 1440', width: 1440, height: 900, path: '/admin/orders/INVOICE_ORDER?voidreason=1', marker: '.ui-alert' },
  { label: 'admin order voidfailed 375', width: 375, height: 812, path: '/admin/orders/INVOICE_ORDER?voidfailed=1', marker: '.ui-alert' },
  { label: 'admin order voidfailed 1440', width: 1440, height: 900, path: '/admin/orders/INVOICE_ORDER?voidfailed=1', marker: '.ui-alert' },
  { label: 'admin order allowfailed 375', width: 375, height: 812, path: '/admin/orders/INVOICE_ORDER?allowfailed=1', marker: '.ui-alert' },
  { label: 'admin order allowfailed 1440', width: 1440, height: 900, path: '/admin/orders/INVOICE_ORDER?allowfailed=1', marker: '.ui-alert' },
  { label: 'admin home 375', width: 375, height: 812, path: '/admin/home', marker: '.goen-admin' },
  { label: 'admin home 1440', width: 1440, height: 900, path: '/admin/home', marker: '.goen-admin' },
  { label: 'admin questions 375', width: 375, height: 812, path: '/admin/questions', marker: '.goen-admin__questions' },
  { label: 'admin reviews 375', width: 375, height: 812, path: '/admin/reviews', marker: '.goen-admin' },
  { label: 'admin reviews 1440', width: 1440, height: 900, path: '/admin/reviews', marker: '.goen-admin' },
  // The customer pages. Both need a fixture and neither is measured without one:
  // /admin/customers lists NOTHING until somebody searches, so a row against the
  // bare path would measure a search box and call the page covered. CUSTOMER_ID
  // is the customer scripts/check-layout.sql seeds and places the return
  // fixture's orders for, so the detail page has its stats and its order table
  // on screen rather than the empty state.
  // The FAQ page. Its marker is the LIST rather than .goen-admin, because the seed
  // populates faq_entries and the page's two halves are a form and that list — a row
  // that passed on the chrome alone would measure the form and call the page covered.
  // One variant's stock ledger. Markered on the TABLE: a variant whose ledger is
  // empty renders a hint instead, so the seed posts its stock through
  // record_inventory_movement.
  { label: 'admin movements 375', width: 375, height: 812, path: '/admin/stock/PXL-9P-1-1', marker: '.ui-table' },
  { label: 'admin movements 1440', width: 1440, height: 900, path: '/admin/stock/PXL-9P-1-1', marker: '.ui-table' },
  { label: 'admin faq 375', width: 375, height: 812, path: '/admin/faq', marker: '.goen-admin__coupons' },
  { label: 'admin faq 1440', width: 1440, height: 900, path: '/admin/faq', marker: '.goen-admin__coupons' },
  { label: 'admin customers 375', width: 375, height: 812, path: '/admin/customers?q=layout', marker: '.ui-table' },
  { label: 'admin customers 1440', width: 1440, height: 900, path: '/admin/customers?q=layout', marker: '.ui-table' },
  { label: 'admin customer 375', width: 375, height: 812, path: '/admin/customers/CUSTOMER_ID', marker: '.goen-chartmeter' },
  { label: 'admin customer 1440', width: 1440, height: 900, path: '/admin/customers/CUSTOMER_ID', marker: '.goen-chartmeter' },
  { label: 'admin messages 375', width: 375, height: 812, path: '/admin/messages', marker: '.goen-admin__returns' },
  { label: 'admin messages 1440', width: 1440, height: 900, path: '/admin/messages', marker: '.goen-admin__returns' },
  { label: 'admin newsletter 375', width: 375, height: 812, path: '/admin/newsletter', marker: '.goen-admin' },
  { label: 'admin newsletter 1440', width: 1440, height: 900, path: '/admin/newsletter', marker: '.goen-admin' },
  { label: 'admin questions 1440', width: 1440, height: 900, path: '/admin/questions', marker: '.goen-admin__questions' },
  // /admin/warranty lists NOTHING until somebody searches — the /admin/customers
  // rule, because these rows carry a customer's name beside what they own. So the
  // row searches for the serial scripts/check-layout.sql registered, and the marker
  // is the table that exists only when the search found it. A row against the bare
  // path would measure a search box and report a checked page.
  { label: 'admin warranty 375', width: 375, height: 812, path: '/admin/warranty?q=LAYOUT_SERIAL', marker: '.goen-admin__warranties' },
  { label: 'admin warranty 1440', width: 1440, height: 900, path: '/admin/warranty?q=LAYOUT_SERIAL', marker: '.goen-admin__warranties' },
  { label: 'admin returns 375', width: 375, height: 812, path: '/admin/returns', marker: '.goen-admin__returns' },
  { label: 'admin returns 1440', width: 1440, height: 900, path: '/admin/returns', marker: '.goen-admin__returns' },
  { label: 'admin taxonomy 375', width: 375, height: 812, path: '/admin/taxonomy', marker: '.goen-admin' },
  { label: 'admin taxonomy 1440', width: 1440, height: 900, path: '/admin/taxonomy', marker: '.goen-admin' },
  // .goen-chart__frame--columns:has(.goen-chart__window): the editor of the running
  // campaign layout-campaign, whose start scripts/check-layout.sql:44 puts three
  // days back. The same script pays four units of its first product on each of the
  // seven days before today, so the card has the grey days before under their
  // "Before" line, the campaign days bracketed with their "until ..." name beside
  // it, and the table; without them the page measured is the sentence.
  { label: 'admin campaign results 320', width: 320, height: 568, path: '/admin/campaigns/layout-campaign', marker: '.goen-chart__frame--columns:has(.goen-chart__window)' },
  { label: 'admin campaign results 375', width: 375, height: 812, path: '/admin/campaigns/layout-campaign', marker: '.goen-chart__frame--columns:has(.goen-chart__window)' },
  { label: 'admin campaign results 1440', width: 1440, height: 900, path: '/admin/campaigns/layout-campaign', marker: '.goen-chart__frame--columns:has(.goen-chart__window)' },
  { label: 'admin campaigns 375', width: 375, height: 812, path: '/admin/campaigns', marker: '.goen-admin' },
  { label: 'admin campaigns 1440', width: 1440, height: 900, path: '/admin/campaigns', marker: '.goen-admin' },
  { label: 'admin shipping 375', width: 375, height: 812, path: '/admin/shipping', marker: '.goen-admin' },
  { label: 'admin shipping 1440', width: 1440, height: 900, path: '/admin/shipping', marker: '.goen-admin' },
  { label: 'admin tiers 375', width: 375, height: 812, path: '/admin/tiers', marker: '.goen-admin' },
  { label: 'admin tiers 1440', width: 1440, height: 900, path: '/admin/tiers', marker: '.goen-admin' },
  { label: 'admin staff 375', width: 375, height: 812, path: '/admin/staff', marker: '.goen-admin' },
  { label: 'admin staff 1440', width: 1440, height: 900, path: '/admin/staff', marker: '.goen-admin' },
];

// Signed-in customer surfaces. CUST_TOKEN is the session
// scripts/check-layout.sql mints; RETURN_FORM_ORDER is a delivered order with
// no return filed yet; INVOICE_ORDER is the refunded order the return fixture
// decided.
const ACCOUNT_BADFORM = [
  { label: 'points badform 375', width: 375, height: 812, locale: 'zh-Hant',
    notice: '這份兌換表單已過期，請重新送出。' },
  { label: 'points badform 1440', width: 1440, height: 900, locale: 'zh-Hant',
    notice: '這份兌換表單已過期，請重新送出。' },
  { label: 'points badform en 375', width: 375, height: 812, locale: 'en',
    notice: 'That redemption form expired. Submit it again.' },
  { label: 'points badform en 1440', width: 1440, height: 900, locale: 'en',
    notice: 'That redemption form expired. Submit it again.' },
];

const ACCOUNT_PAGES = [
  // The order history lives on /account, not a separate /account/orders list.
  // .goen-account__orders is absent until the customer owns an order.
  { label: 'account orders 375', width: 375, height: 812, path: '/account',
    marker: '.goen-account__orders' },
  { label: 'account orders 1440', width: 1440, height: 900, path: '/account',
    marker: '.goen-account__orders' },
  // Canonical order detail. INVOICE_ORDER is delivered, refunded and
  // owned by the signed-in customer — not the guest PLACED_ORDER cookie.
  { label: 'order detail 375', width: 375, height: 812,
    path: '/orders/INVOICE_ORDER', marker: '.goen-order__lines' },
  { label: 'order detail 1440', width: 1440, height: 900,
    path: '/orders/INVOICE_ORDER', marker: '.goen-order__lines' },
  { label: 'return form 375', width: 375, height: 812,
    path: '/orders/RETURN_FORM_ORDER/return', marker: '.goen-returns__form' },
  { label: 'return form 1440', width: 1440, height: 900,
    path: '/orders/RETURN_FORM_ORDER/return', marker: '.goen-returns__form' },
  // The redeem field and its max both come from spendable hundreds the fixture
  // funded — a bare .goen-account marker would pass on balance alone.
  { label: 'points 375', width: 375, height: 812, path: '/account/points',
    marker: '#points', points: true },
  { label: 'points 1440', width: 1440, height: 900, path: '/account/points',
    marker: '#points', points: true },
  // Each saved product must be one direct grid list item with its card and
  // remove form inside it and matching slugs. A global tile plus a global form
  // is the wrong topology and must not pass.
  { label: 'wishlist 375', width: 375, height: 812, path: '/account/wishlist',
    marker: '.goen-tiles__grid > li.goen-wish', wishlist: true },
  { label: 'wishlist 1440', width: 1440, height: 900, path: '/account/wishlist',
    marker: '.goen-tiles__grid > li.goen-wish', wishlist: true },
  { label: 'warranty 375', width: 375, height: 812, path: '/account/warranty',
    marker: '.goen-warranty__item' },
  { label: 'warranty 1440', width: 1440, height: 900, path: '/account/warranty',
    marker: '.goen-warranty__item' },
];

const MIN_TAP = 44; // the smallest comfortable touch target, in CSS px

// The narrowest a product card may be once the viewport is wide enough for the
// filter toolbar to open above the grid. Two-up on a 375px phone gives 166px and
// that is correct; the defect is a card that is no wider at 1024 than it is on
// a phone, which is a column count that stopped fitting. Only checked where
// the row has `toolbar`.
const MIN_CARD = 200;

// The narrowest the desktop search field may be once English names are in
// the bar. Below this the input is a sliver: the icon still shows and a
// visitor cannot type.
const MIN_SEARCH = 120;

let nextId = 1;
const pending = new Map();

// timeoutMs is a parameter because one call is not like the others: axe.run on
// a back-office table takes longer than every probe in this file put together,
// and capping it at the default would report a slow audit as a dead socket.
function send(ws, method, params = {}, timeoutMs = 30000) {
  const id = nextId++;
  ws.send(JSON.stringify({ id, method, params }));
  return new Promise((resolve, reject) => {
    // Cleared on every answer: an armed timer keeps node alive, and the audit's
    // 120 s one would hold the process that long after the check has finished.
    const timer = setTimeout(() => {
      if (pending.delete(id)) reject(new Error(`${method} timed out`));
    }, timeoutMs);
    pending.set(id, { resolve, reject, timer });
  });
}

async function pageSocket() {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    try {
      const list = await (await fetch(`http://127.0.0.1:${CDP_PORT}/json/list`)).json();
      const page = list.find((t) => t.type === 'page');
      if (page) return page.webSocketDebuggerUrl;
    } catch {
      /* Chrome is not listening yet */
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`no Chrome page target on port ${CDP_PORT}\n${chromeStartupReport()}`);
}

// The browser is started by the Makefile with its output in a file, so a start
// that never opens a page can be told from a slow one.
function chromeStartupReport() {
  const lines = [];
  try {
    const pid = Number(readFileSync(`${LAYOUT_DIR}/pid`, 'utf8'));
    try {
      process.kill(pid, 0);
      lines.push(`Chrome (pid ${pid}) is still running`);
    } catch {
      lines.push(`Chrome (pid ${pid}) has exited`);
    }
    const output = readFileSync('chrome.log', 'utf8').trimEnd().split('\n');
    lines.push(`Chrome output, last ${Math.min(output.length, 40)} lines:`, ...output.slice(-40));
  } catch {
    lines.push('no Chrome pid or chrome.log');
  }
  return lines.join('\n');
}

// Runs in the page and returns plain data only.
// The accessibility invariants a BROWSER can decide, and only a browser can.
//
// These are not style: each is a defect that makes the page unusable for somebody
// and invisible to everybody else. A control with no accessible name is announced as
// "button"; a skipped heading level breaks the outline a screen-reader user navigates
// by; an image with no alt is either announced as its filename or silently dropped.
//
// Deliberately NOT a full audit — no contrast, no ARIA semantics, nothing needing a
// judgement call. Every rule here is decidable from the DOM, so a failure is a fact
// rather than an opinion, which is what makes it safe to gate a build on.
const ACCESSIBILITY = `
    // Exactly one h1: it is the page's name, and a screen reader's "jump to the
    // heading" lands there. Two is an argument about which page this is; none is a
    // document with no title at all.
    h1Count: document.querySelectorAll('h1').length,
    // A skipped LEVEL (h2 → h4) breaks the outline. Reported as the first offender
    // rather than a count, because the fix is always at one place.
    headingSkip: (() => {
      let last = 0, bad = '';
      for (const h of document.querySelectorAll('h1,h2,h3,h4,h5,h6')) {
        const level = +h.tagName[1];
        if (last && level > last + 1 && !bad) bad = 'h' + last + ' → h' + level + ': ' + h.textContent.trim().slice(0, 30);
        last = level;
      }
      return bad;
    })(),
    // An image with no alt attribute at all. alt="" is CORRECT for decoration and is
    // not flagged: the rule is that somebody decided, not that every image speaks.
    imagesWithoutAlt: [...document.querySelectorAll('img:not([alt])')]
      .slice(0, 3).map((e) => e.getAttribute('src') || '(no src)'),
    // A field rule whose pattern the browser cannot compile is ignored without a
    // word, and the field it guards is never refused. The v flag is what the
    // pattern attribute is compiled with.
    uncompiledRulePatterns: [...document.querySelectorAll('[data-rule][pattern]')].filter((f) => {
      try { new RegExp('^(?:' + f.getAttribute('pattern') + ')$', 'v'); return false; } catch (e) { return true; }
    }).slice(0, 3).map((f) => f.getAttribute('data-rule') + ' #' + f.id),
    // A control nobody can name. A label[for], an aria-label, an aria-labelledby, a
    // wrapping label, or a title — any of them is a decision; none is a control
    // announced as "edit text, blank".
    unnamedControls: [...document.querySelectorAll('input:not([type=hidden]), select, textarea')]
      .filter((e) => !(
        (e.id && document.querySelector('label[for="' + CSS.escape(e.id) + '"]')) ||
        e.getAttribute('aria-label') || e.getAttribute('aria-labelledby') ||
        e.closest('label') || e.getAttribute('title')
      ))
      .slice(0, 3).map((e) => e.tagName.toLowerCase() + '#' + (e.id || e.name || '?')),
    // A link or button announced as nothing. Text, an aria-label, or an image with
    // alt text inside it all count.
    unnamedTargets: [...document.querySelectorAll('a[href], button')]
      .filter((e) => !(
        e.textContent.trim() || e.getAttribute('aria-label') ||
        e.getAttribute('aria-labelledby') ||
        [...e.querySelectorAll('img')].some((i) => i.getAttribute('alt')) ||
        [...e.querySelectorAll('svg')].some((sv) => sv.getAttribute('aria-label') || sv.querySelector('title'))
      ))
      .slice(0, 3).map((e) => e.tagName.toLowerCase() + '.' + String(e.className || '').split(' ')[0]),
    // A POSITIVE tabindex reorders the whole document's focus order, not just its
    // own — which is why it is a defect rather than a preference.
    positiveTabindex: [...document.querySelectorAll('[tabindex]')]
      .filter((e) => +e.getAttribute('tabindex') > 0).length,
    // A field marked invalid whose error text is not ATTACHED to it. The message
    // is in the DOM either way, but without aria-describedby pointing at it a
    // screen reader announces "invalid" and never says why — so the one person
    // who cannot see the red paragraph is the one told least. Only fields the
    // server has actually refused are asked: aria-invalid="false" is a decision,
    // and a valid field needs nothing attached.
    //
    // The referenced element must be the ERROR and not merely something that
    // exists. A password field already described by its own hint satisfied the
    // first version of this rule while announcing "invalid" and then reading out
    // the rules it had just broken, with the refusal itself never spoken.
    unexplainedInvalids: [...document.querySelectorAll('[aria-invalid="true"]')]
      .filter((e) => {
        const ids = (e.getAttribute('aria-describedby') || '').split(/\s+/).filter(Boolean);
        return !ids.some((id) => {
          const t = document.getElementById(id);
          return t && (t.getAttribute('role') === 'alert' ||
            t.classList.contains('ui-error-text') || t.classList.contains('ui-alert--error'));
        });
      })
      .slice(0, 3).map((e) => e.tagName.toLowerCase() + '#' + (e.id || e.name || '?')),
    // <html lang> is what decides the voice a screen reader reads the page in. goen
    // stamps it from the locale cookie before the first byte; an empty one would
    // announce Chinese copy in an English voice.
    lang: document.documentElement.getAttribute('lang') || '',
`;

const PROBE = `(() => {
  const de = document.documentElement;
  const cols = (sel) => {
    const items = [...document.querySelectorAll(sel)];
    if (!items.length) return 0;
    const top = Math.min(...items.map((e) => e.getBoundingClientRect().top));
    return items.filter((e) => Math.abs(e.getBoundingClientRect().top - top) < 1).length;
  };
  // Only the right edge: the skip link is parked off-canvas at left:-9999px by
  // design and creates no scrollable area in LTR, so flagging it would bury the
  // real cause.
  // documentElement.scrollWidth counts content inside a scrolling descendant, so
  // an intentionally scrollable table reads as page overflow when it is not.
  // body.scrollWidth is what actually widens the page — verified by trying to
  // scroll: with a 640px table in an overflow:auto box, documentElement said 573
  // and window.scrollX stayed 0.
  const pageWidth = document.body.scrollWidth;
  // An element with a scrolling ancestor is clipped by it and cannot widen the
  // page, so naming it would bury the real cause.
  const clipped = (e) => {
    let p = e.parentElement;
    while (p && p !== document.body) {
      const o = getComputedStyle(p).overflowX;
      if (o === 'auto' || o === 'hidden' || o === 'scroll') return true;
      p = p.parentElement;
    }
    return false;
  };
  const overflowing = [...document.querySelectorAll('body *')]
    .filter((e) => { const r = e.getBoundingClientRect(); return r.width > 0 && r.right > de.clientWidth + 0.5 && !clipped(e); })
    .slice(0, 6)
    .map((e) => e.tagName.toLowerCase() + '.' + String(e.className || '').split(' ')[0]);

  // A slide with no photograph is colour and type and has no media to compare.
  const photoSlide = document.querySelector('.goen-hero__slide:has(.goen-hero__media)');
  const body = photoSlide.querySelector('.goen-hero__body').getBoundingClientRect();
  const media = photoSlide.querySelector('.goen-hero__media').getBoundingClientRect();

  // The content column has to line up across the three landmarks, or the page
  // reads as three documents stacked.
  const edges = (sel) => {
    const e = document.querySelector(sel); if (!e) return null;
    const r = e.getBoundingClientRect(), cs = getComputedStyle(e);
    return [ +(r.left + parseFloat(cs.paddingLeft)).toFixed(1),
             +(r.right - parseFloat(cs.paddingRight)).toFixed(1) ];
  };

  const taps = [...document.querySelectorAll('.goen-cat, .goen-hero__actions .ui-btn')]
    .map((e) => e.getBoundingClientRect().height);

  // Every rendered <img> must have laid out with real pixels. A 404 leaves a
  // zero-size box in Chrome, so this catches artwork referenced but not served.
  const images = [...document.querySelectorAll('main img')].map((e) => ({
    src: e.getAttribute('src'),
    ok: e.naturalWidth > 0 && e.naturalHeight > 0,
  }));

  return {
    viewportWidth: de.clientWidth,
    scrollWidth: pageWidth,
    overflowing,
    heroSplit: Math.abs(body.y - media.y) < 2 ? 'side-by-side' : 'stacked',
    cats: cols('.goen-cats__grid > li'),
    tiles: cols('.goen-tiles__grid > li'),
    header: edges('.goen-header__bar'),
    main: edges('.goen-home'),
    footer: edges('.goen-footer__grid'),
    minTap: taps.length ? Math.min(...taps) : 0,
    images,
    ${ACCESSIBILITY}
  };
})()`;

// settled waits for the document to finish loading, rather than guessing.
//
// A fixed sleep is a race the check loses on a cold server: the probe runs
// against about:blank or a half-built document and throws a TypeError instead
// of reporting the page as not ready. Polls readyState with a ceiling; a page
// that never completes is a real failure and says so.
// Every route this run actually visited, in the order it first saw them, as
// route -> the URL that reached it. The axe pass at the end reads this rather
// than a second list of paths: a list would drift from the tables above, and
// the point of the audit is that it covers what the gate covers.
const visited = new Map();

// A route key has to mean the same thing next week. The fixtures mint a
// customer id, an order number carrying today's date and a warranty serial from
// that number on every run, so a key taken verbatim would name a page that
// does not exist tomorrow and the baseline would be stale on the run after the
// one that wrote it.
const PER_RUN = [
  [/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi, '{id}'],
  [/GO-\d{6}-\d{6}/g, '{order}'],
  [/LAYOUTSN\d+/g, '{serial}'],
];

const routeOf = (url) => {
  let route = (url.startsWith(ORIGIN) ? url.slice(ORIGIN.length) : url) || '/';
  for (const [fixture, name] of PER_RUN) route = route.replace(fixture, name);
  return route;
};

const DUPLICATE_TRANSITION_NAMES = `(() => {
  const count = new Map();
  for (const e of document.querySelectorAll('*')) {
    const name = getComputedStyle(e).viewTransitionName;
    if (!name || name === 'none' || name === 'match-element' || !e.getClientRects().length) continue;
    count.set(name, (count.get(name) || 0) + 1);
  }
  return [...count].filter(([, n]) => n > 1).map(([name, n]) => name + ' x' + n);
})()`;

const settled = async (ws, label, url) => {
  for (let i = 0; i < 50; i++) {
    const { result } = await send(ws, 'Runtime.evaluate', {
      // The URL as well as readyState, and that is the whole trick: immediately
      // after Page.navigate the OLD document is still there and still 'complete',
      // so polling readyState alone returns instantly and the probe measures the
      // previous page — or catches this one mid-teardown and reads null off a
      // querySelector.
      expression: 'document.readyState + " " + location.href', returnByValue: true,
    });
    const [state, href] = String(result.value).split(' ');
    if (state === 'complete' && href === url) {
      // One frame more, so layout and web fonts have applied before anything is
      // measured — the geometry assertions are the reason this check exists.
      await new Promise((r) => setTimeout(r, 250));
      // A view-transition-name used twice on one page makes the browser skip
      // the whole transition without a word, so a duplicate is a failure here
      // on every page this run opens, at whatever width it opened it.
      const dup = await send(ws, 'Runtime.evaluate', { expression: DUPLICATE_TRANSITION_NAMES, returnByValue: true });
      if (Array.isArray(dup.result?.value) && dup.result.value.length) {
        fail(label, `view-transition-name used more than once on ${routeOf(url)}: ${dup.result.value.join(', ')}`);
      }
      const route = routeOf(url);
      if (!visited.has(route)) visited.set(route, url);
      return;
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  console.error(`\n${label}: the page never finished loading`);
  process.exit(2);
};

const failures = [];
const fail = (where, msg) => failures.push(`${where}: ${msg}`);

// Wait for every <img> in main to have FINISHED fetching, before the probe reads
// naturalWidth off it.
//
// "Not yet" and "not served" are different facts and naturalWidth tells them
// apart only once the fetch has settled. readyState complete does not wait for
// images, so on a cold cache the first page of a run is measured mid-download:
//
// This is a correction to the probe, not a weaker assertion. `complete` is true
// the moment the fetch settles WHETHER OR NOT it succeeded, so an image the
// server does not serve still arrives here with naturalWidth 0 and still fails,
// without waiting: only a slow byte is given time, never a missing one. The
// ceiling exists so an image that never starts fetching cannot hang the run —
// the probe then reports it by src, which is the failure that was wanted.
//
// The ceiling is generous because reaching it is not the normal cost: an image
// that is served arrives long before, and one that is NOT served is `complete`
// on the first poll and returns immediately. Only an image that never starts
// fetching waits the whole way.
//
// And "never starts fetching" is what a tile below the fold does. The home
// page's product tiles carry loading="lazy", which is right for a shopper: a
// phone should not pay for eight images to read a hero. A driven viewport never
// scrolls, so the deepest row stays outside the distance Chrome starts a lazy
// fetch at, sits at complete=false for the whole ceiling, and is then reported
// as an image the server does not serve.
//
// So walk the document through in viewport-height steps first, which is what a
// shopper's thumb does, and come back to the top before anything is measured —
// every geometry assertion below reads a box at scroll 0.
//
// This too is a correction rather than a weaker assertion: after the walk the
// fetch has been asked for, so the poll below is once again deciding whether a
// byte ARRIVED. An image the server does not serve is still `complete` with
// naturalWidth 0 and still fails, on the first poll and without waiting.
const imagesFetched = async (label) => {
  await send(ws, 'Runtime.evaluate', {
    // Two frames per step, because the lazy fetch is scheduled off the frame
    // that the scroll produced, not off the scroll call. The step ceiling is
    // there because scrollHeight GROWS as the images it is being walked for
    // arrive, and a loop bounded only by it can chase its own tail.
    expression: `(async () => {
      const frame = () => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
      const step = window.innerHeight;
      for (let y = 0; y < document.documentElement.scrollHeight && y < step * 60; y += step) {
        window.scrollTo(0, y);
        await frame();
      }
      window.scrollTo(0, 0);
      await frame();
    })()`,
    awaitPromise: true,
  });
  for (let i = 0; i < 150; i++) {
    const { result } = await send(ws, 'Runtime.evaluate', {
      expression: `[...document.querySelectorAll('main img')].filter((img) => !img.complete).length`,
      returnByValue: true,
    });
    if (result.value === 0) return;
    await new Promise((r) => setTimeout(r, 100));
  }
  fail(label, 'an image in main never finished fetching within 15s');
};

// checkAccessibility reports the invariants the probe measured.
//
// Called for every row of every table. These are properties of a PAGE rather than of
// a width — but measured at each width anyway, because a responsive layout can hide
// a control at one and reveal an unlabelled one at another.
const checkAccessibility = (at, got) => {
  if (got.h1Count !== 1) {
    fail(at, `the page has ${got.h1Count} <h1> elements, want exactly 1 — it is the ` +
      `page's name, and "jump to the heading" lands there`);
  }
  if (got.headingSkip) {
    fail(at, `heading level skipped (${got.headingSkip}) — the outline is what a ` +
      `screen-reader user navigates by`);
  }
  for (const src of got.imagesWithoutAlt || []) {
    fail(at, `image has no alt attribute: ${src} — alt="" is correct for decoration, ` +
      `absent means nobody decided`);
  }
  for (const f of got.uncompiledRulePatterns || []) {
    fail(at, `field rule pattern does not compile under the v flag: ${f} — the browser ignores it silently`);
  }
  for (const c of got.unnamedControls || []) {
    fail(at, `form control with no accessible name: ${c} — announced as "edit text, blank"`);
  }
  for (const c of got.unnamedTargets || []) {
    fail(at, `link or button with no accessible name: ${c}`);
  }
  for (const c of got.unexplainedInvalids || []) {
    fail(at, `${c} is marked invalid with no error text attached — ` +
      'aria-describedby must name an element that exists');
  }
  if (got.positiveTabindex > 0) {
    fail(at, `${got.positiveTabindex} elements carry a positive tabindex, which ` +
      `reorders the whole document's focus order`);
  }
  if (!got.lang) {
    fail(at, `<html> has no lang — it decides the voice a screen reader reads in`);
  }
};

const ws = new WebSocket(await pageSocket());
await new Promise((r) => (ws.onopen = r));
ws.onmessage = (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.id && pending.has(msg.id)) {
    const { resolve, reject, timer } = pending.get(msg.id);
    pending.delete(msg.id);
    clearTimeout(timer);
    msg.error ? reject(new Error(JSON.stringify(msg.error))) : resolve(msg.result);
  }
};

await send(ws, 'Page.enable');

// The cart pages need the browser to carry the cart cookie
// scripts/check-layout.sql minted. Without it /cart is empty and its check
// would measure nothing.
if (process.env.CART_TOKEN) {
  await send(ws, 'Network.enable');
  await send(ws, 'Network.setCookie', {
    name: 'goen_cart', value: process.env.CART_TOKEN, domain: '127.0.0.1', path: '/',
  });
}

// The payment page is gated on the browser having placed the order, because an
// order number is a guessable per-day counter. Without this cookie every pay
// check would measure the 404 page and pass.
//
// The cookie carries a TOKEN and the URL carries the NUMBER; the number alone
// is not proof, and reading the token into the URL would name an order that
// does not exist.
if (process.env.PLACED_TOKEN) {
  await send(ws, 'Network.enable');
  await send(ws, 'Network.setCookie', {
    name: 'goen_placed', value: process.env.PLACED_TOKEN, domain: '127.0.0.1', path: '/',
  });
}

for (const want of EXPECTED) {
  await send(ws, 'Emulation.setDeviceMetricsOverride', {
    width: want.width,
    height: want.height,
    deviceScaleFactor: 1,
    mobile: want.width < 768,
  });
  const target = ORIGIN + '/';
  await send(ws, 'Page.navigate', { url: target });
  await settled(ws, want.label, target);
  await imagesFetched(want.label);

  const evaluated = await send(ws, 'Runtime.evaluate', { expression: PROBE, returnByValue: true });
  // A probe that THREW comes back with exceptionDetails and no value, and reading
  // `.h1Count` off undefined twenty lines later reports a TypeError in the checker
  // rather than the mistake in the probe. Say what actually happened.
  if (evaluated.exceptionDetails || !evaluated.result || evaluated.result.value === undefined) {
    console.error(`\n${want.label}: the probe did not run — ` +
      (evaluated.exceptionDetails?.exception?.description || JSON.stringify(evaluated).slice(0, 400)));
    process.exit(2);
  }
  const got = evaluated.result.value;
  const at = want.label;
  checkAccessibility(at, got);

  if (got.scrollWidth > got.viewportWidth) {
    fail(at, `page scrolls horizontally (${got.scrollWidth} > ${got.viewportWidth})` +
      (got.overflowing.length ? ` — widest: ${got.overflowing.join(', ')}` : ''));
  }
  if (got.cats !== want.cats) fail(at, `category grid has ${got.cats} columns, want ${want.cats}`);
  if (got.tiles !== want.tiles) fail(at, `product grid has ${got.tiles} columns, want ${want.tiles}`);
  if (got.heroSplit !== want.hero) fail(at, `hero is ${got.heroSplit}, want ${want.hero}`);
  if (got.minTap < MIN_TAP) fail(at, `smallest tap target is ${got.minTap}px, want >= ${MIN_TAP}`);

  for (const [name, edge] of [['header', got.header], ['footer', got.footer]]) {
    if (!edge || !got.main) continue;
    if (Math.abs(edge[0] - got.main[0]) > 1 || Math.abs(edge[1] - got.main[1]) > 1) {
      fail(at, `${name} content column [${edge}] does not line up with main [${got.main}]`);
    }
  }
  for (const img of got.images) {
    if (!img.ok) fail(at, `image did not load: ${img.src}`);
  }

  const mark = failures.length ? '' : ' ok';
  console.log(`${at.padEnd(16)} scrollW=${got.scrollWidth}/${got.viewportWidth} ` +
    `cats=${got.cats} tiles=${got.tiles} hero=${got.heroSplit} tap=${got.minTap}${mark}`);
}

// The period track (WCAG 1.4.4, 1.4.10, 1.4.11). At 320px, at 200% text and in
// forced colours every track lies inside the viewport and draws its cells as one
// row of equal parts, joined but for the 4px after a mark, so its length still
// reads as the share of the span. Elapsed stays apart from to come: by colour,
// and in forced colours, where the colours are the system's, by thickness. The
// span after a mark (the return window's goodwill days) is drawn apart from the
// days before it: dashes under a transparent border, in forced colours too,
// where a dashed border paints solid on a short cell. A dash is a gradient
// whose first stop is under 100%, repeated on a tile a few px wide or narrower
// than the cell, so a solid gradient does not pass; today among the dashes is
// cut by a mask of the same kind. A dashed cell and today's
// half-filled one draw their line as a gradient. A label a track draws lies
// inside it and the viewport and overlaps no other. A route with no period drawn fails, because a track that
// is not drawn cannot be measured and the check would pass on nothing.
const PERIOD_PROBE = `(() => {
  const vw = document.documentElement.clientWidth;
  const forced = matchMedia('(forced-colors: active)').matches;
  const periods = [...document.querySelectorAll('.ui-period')].filter((e) => e.getClientRects().length > 0);
  const problems = [];
  const tracks = [];
  let extras = 0;
  const clear = (colour) => colour === 'transparent' || /^rgba\\(.*,\\s*0\\)$/.test(colour);
  const drawn = (c) => c.width + 'px ' + c.style + ' ' + c.colour + (c.image === 'none' ? '' : ' over ' + c.image + ' at ' + c.size)
    + (c.mask === 'none' ? '' : ' masked by ' + c.mask + ' at ' + c.maskSize);
  const tiled = (image, size, cellWidth) => {
    const stop = /\\)\\s+(\\d+(?:\\.\\d+)?)%/.exec(image);
    const across = (size || '').split(' ')[0];
    return /gradient/.test(image) && !!stop && Number(stop[1]) < 100
      && /px$/.test(across) && parseFloat(across) > 0 && (parseFloat(across) <= 16 || parseFloat(across) < cellWidth);
  };
  periods.forEach((period, n) => {
    period.closest('.goen-hero__slide')?.scrollIntoView({ inline: 'start', block: 'nearest', behavior: 'instant' });
    const box = period.getBoundingClientRect();
    const at = 'period ' + n + ' [' + box.left.toFixed(1) + ',' + box.right.toFixed(1) + ']';
    tracks.push(box.left.toFixed(1) + '+' + box.width.toFixed(1) + 'x' + box.height.toFixed(1));
    if (box.left < -0.5 || box.right > vw + 0.5) problems.push(at + ' lies outside the viewport ' + vw);
    const cells = [...period.children].map((e) => {
      const s = getComputedStyle(e);
      return {
        r: e.getBoundingClientRect(), filled: e.hasAttribute('data-cell'), mark: e.hasAttribute('data-mark'),
        today: e.getAttribute('data-cell') === 'today', extra: e.getAttribute('data-span') === 'extra',
        width: parseFloat(s.borderBottomWidth) || 0, style: s.borderBottomStyle, colour: s.borderBottomColor,
        image: s.backgroundImage, size: s.backgroundSize,
        mask: s.maskImage || s.webkitMaskImage || 'none', maskSize: s.maskSize || s.webkitMaskSize || '',
      };
    });
    if (cells.length === 0) problems.push(at + ' has no cells');
    cells.forEach((c, i) => {
      if (c.r.height <= 0 || c.width < 2 || c.style === 'none' || c.style === 'hidden'
        || (clear(c.colour) && !/gradient/.test(c.image))) {
        problems.push(at + ' cell ' + i + ' draws no line (' + drawn(c) + ')');
      }
      if (c.r.left < box.left - 0.5 || c.r.right > box.right + 0.5) {
        problems.push(at + ' cell ' + i + ' [' + c.r.left.toFixed(1) + ',' + c.r.right.toFixed(1) + '] lies outside its track');
      }
      const next = cells[i + 1];
      if (!next) return;
      const gap = next.r.left - c.r.right;
      const want = c.mark ? 4 : 0;
      if (Math.abs(gap - want) > 0.5) {
        problems.push(at + ' cells ' + i + ' and ' + (i + 1) + ' are ' + gap.toFixed(1) + 'px apart, want ' + want);
      }
      if (Math.abs(next.r.top + next.r.bottom - c.r.top - c.r.bottom) > 2) {
        problems.push(at + ' cell ' + (i + 1) + ' leaves the row');
      }
    });
    const widths = cells.map((c) => c.r.width);
    if (widths.length && Math.max(...widths) - Math.min(...widths) > 1) {
      problems.push(at + ' cells range from ' + Math.min(...widths).toFixed(1) + ' to ' + Math.max(...widths).toFixed(1) +
        'px wide, so the track no longer draws the span to scale');
    }
    for (const [part, span] of [['', cells.filter((c) => !c.extra)], [' after its mark', cells.filter((c) => c.extra)]]) {
      const filled = span.filter((c) => c.filled && !c.today);
      const ahead = span.filter((c) => !c.filled);
      if (filled.length && ahead.length && (forced ? filled[0].width <= ahead[0].width
        : filled[0].colour + filled[0].image === ahead[0].colour + ahead[0].image)) {
        problems.push(at + part + ' draws elapsed and to come alike (' + drawn(filled[0]) + ' against ' + drawn(ahead[0]) + ')');
      }
    }
    cells.forEach((c, i) => {
      if (!c.extra) return;
      extras++;
      const before = cells.find((d) => !d.extra && !d.today && d.filled === c.filled);
      const dashed = clear(c.colour) && (c.mask === 'none'
        ? tiled(c.image, c.size, c.r.width) : /gradient/.test(c.image) && tiled(c.mask, c.maskSize, c.r.width));
      if (!dashed || (before && drawn(before) === drawn(c))) {
        problems.push(at + ' cell ' + i + ' after the mark is drawn like the days before it (' + drawn(c) + ')');
      }
    });
    const labels = [...period.querySelectorAll('b, small')]
      .filter((e) => e.getClientRects().length > 0)
      .map((e) => ({ text: e.textContent, r: e.getBoundingClientRect() }));
    for (const { text, r } of labels) {
      if (r.left < Math.max(0, box.left) - 0.5 || r.right > Math.min(vw, box.right) + 0.5) {
        problems.push(at + ' label "' + text + '" [' + r.left.toFixed(1) + ',' + r.right.toFixed(1) + '] lies outside its period or the viewport ' + vw);
      }
    }
    for (let i = 0; i < labels.length; i++) {
      for (let j = i + 1; j < labels.length; j++) {
        const a = labels[i].r, b = labels[j].r;
        if (a.left < b.right - 0.5 && b.left < a.right - 0.5 && a.top < b.bottom - 0.5 && b.top < a.bottom - 0.5) {
          problems.push(at + ' labels "' + labels[i].text + '" and "' + labels[j].text + '" overlap');
        }
      }
    }
  });
  return { periods: periods.length, forced, tracks, extras, problems };
})()`;

// extra says the route draws a span after a mark, so a pass with none fails.
async function periodPass(route, extra = false) {
  for (const [name, fontSize, forced] of [['320', '', false], ['320 at 200% text', '200%', false], ['320 forced colours', '', true]]) {
    const at = 'period track ' + route + ' ' + name;
    await send(ws, 'Emulation.setDeviceMetricsOverride', { width: 320, height: 800, deviceScaleFactor: 1, mobile: true });
    if (forced) await send(ws, 'Emulation.setEmulatedMedia', { features: [{ name: 'forced-colors', value: 'active' }] });
    try {
      const target = ORIGIN + route;
      await send(ws, 'Page.navigate', { url: target });
      await settled(ws, at, target);
      await send(ws, 'Runtime.evaluate', { expression: `document.documentElement.style.fontSize = ${JSON.stringify(fontSize)}` });
      const got = (await send(ws, 'Runtime.evaluate', { expression: PERIOD_PROBE, returnByValue: true })).result?.value;
      if (!got) {
        fail(at, 'the period probe did not run');
        continue;
      }
      if (forced && !got.forced) fail(at, 'forced colours did not activate');
      if (got.periods === 0) fail(at, 'no .ui-period on the page: the period is not drawn');
      if (extra && got.extras === 0) fail(at, 'no cell after a mark: the goodwill days are not drawn');
      for (const problem of got.problems) fail(at, problem);
      console.log(at.padEnd(48) + ' periods=' + got.periods + ' extra=' + got.extras + ' ' + got.tracks.join(' ') +
        (got.problems.length || got.periods === 0 || (extra && got.extras === 0) ? '' : ' ok'));
    } finally {
      if (forced) await send(ws, 'Emulation.setEmulatedMedia', { features: [] });
    }
  }
}

for (const route of ['/', '/s/layout-campaign']) await periodPass(route);

// Whether the filter shell exposes its form and a control. On desktop a closed
// <details> keeps ::details-content at content-visibility:hidden until the
// stylesheet opens it; display:flex on the form alone is not enough.
const FILTER_SHELL_PROBE = `(() => {
  const shell = document.querySelector('.goen-filters__shell');
  const form = document.querySelector('.goen-filters');
  const input = document.querySelector('.goen-filters #sort');
  const summary = document.querySelector('.goen-filters__shell-summary');
  const formRect = form ? form.getBoundingClientRect() : null;
  const inputRect = input ? input.getBoundingClientRect() : null;
  const summaryStyle = summary ? getComputedStyle(summary) : null;
  let contentVisibility = null;
  if (shell) {
    try {
      contentVisibility = getComputedStyle(shell, '::details-content').contentVisibility;
    } catch (_) {
      contentVisibility = null;
    }
  }
  return {
    open: shell ? shell.open : null,
    summaryDisplay: summaryStyle ? summaryStyle.display : null,
    summaryVisible: !!(summary && summary.getBoundingClientRect().height > 0),
    formVisible: !!(formRect && formRect.height > 0 && formRect.width > 0),
    inputVisible: !!(inputRect && inputRect.height > 0 && inputRect.width > 0),
    contentVisibility,
    products: document.querySelectorAll('.goen-tiles__grid > li').length,
    viewport: document.documentElement.clientWidth,
  };
})()`;

const assertDesktopFiltersVisible = (at, got) => {
  if (!got.formVisible) {
    fail(at, `desktop filter form is not visible — ${JSON.stringify(got)}`);
  }
  if (!got.inputVisible) {
    fail(at, `desktop filter controls are not visible — ${JSON.stringify(got)}`);
  }
  if (got.contentVisibility === 'hidden') {
    fail(at, `desktop ::details-content is still hidden — ${JSON.stringify(got)}`);
  }
};

const LISTING_LAYOUT_PROBE = `(() => {
  const filters = document.querySelector('.goen-listing__filters');
  const filterForm = document.querySelector('.goen-filters');
  const results = document.querySelector('.goen-listing__results');
  const card = document.querySelector('.goen-tiles__grid > li');
  const layout = document.querySelector('.goen-listing__layout');
  if (!filterForm || !results || !layout) {
    return { ok: false, why: 'listing layout landmarks missing' };
  }
  const rail = (filters || filterForm).getBoundingClientRect();
  // The column the results are in: it also holds the applied-filter chips
  // above them, which would otherwise push the results below the rail's top.
  const resultsRect = (document.querySelector('.goen-listing__main') || results).getBoundingClientRect();
  const cardRect = card ? card.getBoundingClientRect() : null;
  return {
    ok: true,
    rail: Math.abs(rail.y - resultsRect.y) < 2 ? 'beside' : 'stacked',
    filterX: +rail.x.toFixed(1),
    filterW: +rail.width.toFixed(1),
    resultsX: +resultsRect.x.toFixed(1),
    resultsW: +resultsRect.width.toFixed(1),
    cardW: cardRect ? +cardRect.width.toFixed(1) : 0,
    layoutChildren: [...layout.children].map((e) => String(e.className || '').split(' ')[0]),
    resultsBelowFilter: resultsRect.top > rail.bottom - 2,
  };
})()`;

const assertDesktopResultsLayout = (at, got) => {
  if (got.threw || !got.ok) {
    fail(at, got.why || 'listing layout probe failed');
    return;
  }
  if (got.rail !== 'stacked' || !got.resultsBelowFilter) {
    fail(at, `results are not below the filter toolbar — ${JSON.stringify(got)}`);
  }
  if (got.layoutChildren.length !== 2) {
    fail(at, `layout has ${got.layoutChildren.length} direct children, want 2 — ${got.layoutChildren}`);
  }
  if (got.cardW > 0 && got.cardW < MIN_CARD) {
    fail(at, `product card is ${got.cardW}px wide, want >= ${MIN_CARD} — ${JSON.stringify(got)}`);
  }
};

const assertMobileResultsLayout = (at, got) => {
  if (got.threw || !got.ok) {
    fail(at, got.why || 'listing layout probe failed');
    return;
  }
  if (got.rail !== 'stacked') {
    fail(at, `mobile results are not stacked under the filter rail — ${JSON.stringify(got)}`);
  }
  if (got.layoutChildren.length !== 2) {
    fail(at, `layout has ${got.layoutChildren.length} direct children, want 2 — ${got.layoutChildren}`);
  }
};

const evalPage = async (expression) => {
  const evaluated = await send(ws, 'Runtime.evaluate', {
    expression, returnByValue: true, awaitPromise: true,
  });
  if (evaluated.exceptionDetails || !evaluated.result || evaluated.result.value === undefined) {
    return {
      threw: true,
      why: evaluated.exceptionDetails?.exception?.description || JSON.stringify(evaluated).slice(0, 400),
    };
  }
  return evaluated.result.value;
};

// The listing page. Its own probe: no hero and no category grid, but a filter
// rail whose position is the fold, and the same no-overflow and shared-gutter
// rules the home is held to.
const LISTING_PROBE = `(() => {
  const de = document.documentElement;
  const clipped = (e) => {
    let p = e.parentElement;
    while (p && p !== document.body) {
      const o = getComputedStyle(p).overflowX;
      if (o === 'auto' || o === 'hidden' || o === 'scroll') return true;
      p = p.parentElement;
    }
    return false;
  };
  const rail = (document.querySelector('.goen-listing__filters')
    || document.querySelector('.goen-filters')).getBoundingClientRect();
  const results = document.querySelector('.goen-listing__results').getBoundingClientRect();
  const cols = (sel) => {
    const items = [...document.querySelectorAll(sel)];
    if (!items.length) return 0;
    const top = Math.min(...items.map((e) => e.getBoundingClientRect().top));
    return items.filter((e) => Math.abs(e.getBoundingClientRect().top - top) < 1).length;
  };
  const edges = (sel) => {
    const e = document.querySelector(sel); if (!e) return null;
    const r = e.getBoundingClientRect(), cs = getComputedStyle(e);
    return [ +(r.left + parseFloat(cs.paddingLeft)).toFixed(1),
             +(r.right - parseFloat(cs.paddingRight)).toFixed(1) ];
  };
  const taps = [...document.querySelectorAll(
    '.goen-filters__shell-summary, .goen-filters__option, .goen-filters__apply, .goen-filters #sort, .goen-filters .ui-input:not([type=checkbox])',
  )].map((e) => e.getBoundingClientRect().height).filter((h) => h > 0);
  return {
    viewportWidth: de.clientWidth,
    scrollWidth: document.body.scrollWidth,
    overflowing: [...document.querySelectorAll('body *')]
      .filter((e) => { const r = e.getBoundingClientRect(); return r.width > 0 && r.right > de.clientWidth + 0.5 && !clipped(e); })
      .slice(0, 6).map((e) => e.tagName.toLowerCase() + '.' + String(e.className || '').split(' ')[0]),
    rail: Math.abs(rail.y - results.y) < 2 ? 'beside' : 'stacked',
    tiles: cols('.goen-tiles__grid > li'),
    // A column count copied from the home page once produced 156px cards here —
    // narrower than the same card on a 375px phone. Column count alone would not
    // have caught that; the width is what the visitor sees.
    cardWidth: (() => {
      const c = document.querySelector('.goen-tiles__grid > li');
      return c ? +c.getBoundingClientRect().width.toFixed(1) : 0;
    })(),
    header: edges('.goen-header__bar'),
    main: edges('.goen-listing'),
    minTap: taps.length ? +Math.min(...taps).toFixed(1) : 0,
    viewportHeight: window.innerHeight,
    shellOpen: document.querySelector('.goen-filters__shell')?.open ?? null,
    firstTileTop: (() => {
      const t = document.querySelector('.goen-tiles__grid > li');
      return t ? +t.getBoundingClientRect().top.toFixed(1) : null;
    })(),
    firstTileInView: (() => {
      const t = document.querySelector('.goen-tiles__grid > li');
      if (!t) return false;
      const r = t.getBoundingClientRect();
      const img = t.querySelector('img');
      const name = t.querySelector('.ui-product__name, .goen-tile__name, h3');
      const price = t.querySelector('.ui-price, .goen-tile__price');
      return r.top < window.innerHeight && r.bottom > 0
        && !!(img && img.getBoundingClientRect().height > 0)
        && !!(name && name.textContent.trim())
        && !!(price && price.textContent.trim());
    })(),
    ${ACCESSIBILITY}
  };
})()`;

for (const want of LISTING) {
  await send(ws, 'Emulation.setDeviceMetricsOverride', {
    width: want.width,
    height: want.height,
    deviceScaleFactor: 1,
    mobile: want.width < 768,
  });
  const target = ORIGIN + '/c/phones';
  await send(ws, 'Page.navigate', { url: target });
  await settled(ws, want.label, target);

  const { result } = await send(ws, 'Runtime.evaluate', { expression: LISTING_PROBE, returnByValue: true });
  const got = result.value;
  const at = want.label;
  checkAccessibility(at, got);

  if (got.scrollWidth > got.viewportWidth) {
    fail(at, `page scrolls horizontally (${got.scrollWidth} > ${got.viewportWidth})` +
      (got.overflowing.length ? ` — widest: ${got.overflowing.join(', ')}` : ''));
  }
  if (got.rail !== want.rail) fail(at, `filter rail is ${got.rail}, want ${want.rail}`);
  if (want.width === 375) {
    if (got.shellOpen) fail(at, 'filter shell is open by default on mobile');
    if (got.firstTileTop !== null && got.firstTileTop >= got.viewportHeight) {
      fail(at, `first product starts at ${got.firstTileTop}px, below the ${got.viewportHeight}px viewport`);
    }
    if (got.firstTileTop !== null && !got.firstTileInView) {
      fail(at, 'the first product tile is not fully visible on entry');
    }
    const filters = await evalPage(FILTER_SHELL_PROBE);
    if (filters.threw) fail(at, `filter visibility probe failed — ${filters.why}`);
    if (filters.formVisible) fail(at, 'filter form is visible while the shell is collapsed on mobile');
  }
  if (want.toolbar) {
    const filters = await evalPage(FILTER_SHELL_PROBE);
    if (filters.threw) fail(at, `filter visibility probe failed — ${filters.why}`);
    assertDesktopFiltersVisible(at, filters);
  }
  if (got.minTap < MIN_TAP) fail(at, `smallest filter control is ${got.minTap}px, want >= ${MIN_TAP}`);
  if (want.toolbar && got.cardWidth > 0 && got.cardWidth < MIN_CARD) {
    fail(at, `product card is ${got.cardWidth}px wide, want >= ${MIN_CARD} — ` +
      `too many columns for the space the grid has`);
  }
  if (got.header && got.main &&
      (Math.abs(got.header[0] - got.main[0]) > 1 || Math.abs(got.header[1] - got.main[1]) > 1)) {
    fail(at, `header content column [${got.header}] does not line up with the listing [${got.main}]`);
  }

  console.log(`${at.padEnd(16)} scrollW=${got.scrollWidth}/${got.viewportWidth} ` +
    `rail=${got.rail} tiles=${got.tiles} tap=${got.minTap}`);
}

// The seeded /c/audio page at 375px: products must stay in the first viewport,
// the shell must expand on demand, and a filtered reload must focus results.
const LISTING_AUDIO = [
  { label: 'listing audio zh 375', width: 375, height: 812, path: '/c/audio', locale: 'zh-Hant' },
  { label: 'listing audio en 375', width: 375, height: 812, path: '/c/audio', locale: 'en' },
  { label: 'listing audio zh 1440', width: 1440, height: 900, path: '/c/audio', locale: 'zh-Hant', rail: 'stacked', toolbar: true },
  { label: 'listing audio en 1440', width: 1440, height: 900, path: '/c/audio', locale: 'en', rail: 'stacked', toolbar: true },
];

for (const want of LISTING_AUDIO) {
  if (want.locale) {
    await send(ws, 'Network.setCookie', {
      name: 'goen_locale', value: want.locale, domain: '127.0.0.1', path: '/',
    });
  }
  await send(ws, 'Emulation.setDeviceMetricsOverride', {
    width: want.width,
    height: want.height,
    deviceScaleFactor: 1,
    mobile: want.width < 768,
  });
  const target = ORIGIN + want.path;
  await send(ws, 'Page.navigate', { url: target });
  await settled(ws, want.label, target);

  const { result } = await send(ws, 'Runtime.evaluate', { expression: LISTING_PROBE, returnByValue: true });
  const got = result.value;
  const at = want.label;
  checkAccessibility(at, got);

  if (got.scrollWidth > got.viewportWidth) {
    fail(at, `page scrolls horizontally (${got.scrollWidth} > ${got.viewportWidth})` +
      (got.overflowing.length ? ` — widest: ${got.overflowing.join(', ')}` : ''));
  }
  if (want.rail && got.rail !== want.rail) {
    fail(at, `filter rail is ${got.rail}, want ${want.rail}`);
  }
  if (want.width === 375) {
    if (got.shellOpen) fail(at, 'filter shell is open by default on mobile');
    if (got.firstTileTop !== null && got.firstTileTop >= got.viewportHeight) {
      fail(at, `first product starts at ${got.firstTileTop}px, below the ${got.viewportHeight}px viewport`);
    }
    if (!got.firstTileInView) fail(at, 'the first product is not visible on entry');
    const filters = await evalPage(FILTER_SHELL_PROBE);
    if (filters.threw) fail(at, `filter visibility probe failed — ${filters.why}`);
    if (filters.formVisible) fail(at, 'filter form is visible while the shell is collapsed on mobile');
  }
  if (want.toolbar) {
    const filters = await evalPage(FILTER_SHELL_PROBE);
    if (filters.threw) fail(at, `filter visibility probe failed — ${filters.why}`);
    assertDesktopFiltersVisible(at, filters);
  }
  console.log(`${at.padEnd(24)} scrollW=${got.scrollWidth}/${got.viewportWidth} ` +
    `rail=${got.rail} tileTop=${got.firstTileTop} shell=${got.shellOpen}`);
}

const proveListingFilterJourney = async (label, locale) => {
  if (locale) {
    await send(ws, 'Network.setCookie', {
      name: 'goen_locale', value: locale, domain: '127.0.0.1', path: '/',
    });
  }
  await send(ws, 'Emulation.setDeviceMetricsOverride', {
    width: 375, height: 812, deviceScaleFactor: 1, mobile: true,
  });
  const start = `${ORIGIN}/c/audio`;
  await send(ws, 'Page.navigate', { url: start });
  await settled(ws, `${label} entry`, start);

  const collapsed = await evalPage(`(() => {
    const shell = document.querySelector('.goen-filters__shell');
    const tile = document.querySelector('.goen-tiles__grid > li');
    if (!shell || !tile) return { ok: false, why: 'shell or product tile missing' };
    const top = tile.getBoundingClientRect().top;
    return {
      ok: true,
      shellOpen: shell.open,
      tileTop: +top.toFixed(1),
      inView: top < window.innerHeight,
    };
  })()`);
  if (collapsed.threw || !collapsed.ok) {
    fail(label, collapsed.why || 'collapsed entry probe failed');
    return;
  }
  if (collapsed.shellOpen) fail(label, 'filter shell is open before the visitor asks');
  if (!collapsed.inView) {
    fail(label, `first product starts at ${collapsed.tileTop}px, outside the viewport`);
  }

  const expanded = await evalPage(`(() => {
    const shell = document.querySelector('.goen-filters__shell');
    const summary = document.querySelector('.goen-filters__shell-summary');
    if (!shell || !summary) return { ok: false, why: 'shell summary missing' };
    summary.click();
    return { ok: true, shellOpen: shell.open };
  })()`);
  if (expanded.threw || !expanded.ok) {
    fail(label, expanded.why || 'expanded probe failed');
    return;
  }
  if (!expanded.shellOpen) fail(label, 'filter shell did not open after the summary was activated');

  // With scripting on a changed filter applies itself and the stylesheet hides
  // the button; with scripting off the button is the only way to apply, so it
  // must show once the shell opens. The page is reloaded because the shell above
  // was opened by script.
  await send(ws, 'Emulation.setScriptExecutionDisabled', { value: true });
  const noScriptTarget = `${ORIGIN}/c/audio`;
  await send(ws, 'Page.navigate', { url: noScriptTarget });
  await settled(ws, `${label} no script`, noScriptTarget);
  const noScript = await evalPage(`(() => {
    const summary = document.querySelector('.goen-filters__shell-summary');
    if (!summary) return { ok: false, why: 'shell summary missing with scripting off' };
    summary.click();
    const apply = document.querySelector('.goen-filters__apply');
    const rect = apply ? apply.getBoundingClientRect() : null;
    return { ok: true, applyVisible: !!(rect && rect.height > 0 && rect.bottom > 0) };
  })()`);
  await send(ws, 'Emulation.setScriptExecutionDisabled', { value: false });
  if (noScript.threw || !noScript.ok) {
    fail(label, noScript.why || 'no-script apply probe failed');
    return;
  }
  if (!noScript.applyVisible) fail(label, 'apply control is not visible with scripting off after expanding the shell');

  const filtered = `${ORIGIN}/c/audio?in_stock=1#listing-results`;
  await send(ws, 'Page.navigate', { url: filtered });
  await settled(ws, `${label} filtered`, filtered);

  const landed = await evalPage(`(() => {
    const results = document.getElementById('listing-results');
    const applied = document.querySelector('.goen-filters__applied');
    const clear = document.querySelector('.goen-filters__applied .ui-filterbar__clear');
    const shell = document.querySelector('.goen-filters__shell');
    return {
      ok: true,
      focused: document.activeElement === results,
      hasApplied: !!applied,
      hasClear: !!clear,
      shellOpen: shell ? shell.open : null,
      href: location.href,
    };
  })()`);
  if (landed.threw || !landed.ok) {
    fail(label, landed.why || 'filtered landing probe failed');
    return;
  }
  if (!landed.focused) fail(label, 'filtered reload did not focus #listing-results');
  if (!landed.hasApplied) fail(label, 'filtered reload shows no applied-filter summary');
  if (!landed.hasClear) fail(label, 'filtered reload offers no clear-all control');
  if (landed.shellOpen) fail(label, 'filter shell stayed open after a filtered reload');

  const filteredLayout = await evalPage(LISTING_LAYOUT_PROBE);
  assertMobileResultsLayout(`${label} filtered`, filteredLayout);
  // MIN_CARD is deliberately NOT asserted here. It asks whether a column count
  // still fits the grid's columns, which is a question only the desktop layout
  // can answer — its own declaration says
  // "Only checked where the row has `toolbar`", and the two call sites that honour
  // that are assertDesktopResultsLayout and the `want.toolbar` guard
  // on the LISTING rows. This journey runs at 375, where the filters are stacked and
  // EXPECTED requires two columns; two columns in a 343px content area is a
  // 163.5px card, so asserting 200 here contradicts the artboard the same file
  // declares. Adding it back makes the two assertions unsatisfiable together.

  const cleared = await evalPage(`(() => {
    const clear = document.querySelector('.goen-filters__applied .ui-filterbar__clear');
    if (!clear) return { ok: false, why: 'clear-all link missing after filter' };
    clear.click();
    return { ok: true };
  })()`);
  if (cleared.threw || !cleared.ok) {
    fail(label, cleared.why || 'clear-all navigation did not start');
    return;
  }
  await settled(ws, `${label} cleared`, `${ORIGIN}/c/audio`);
  const afterClear = await evalPage(LISTING_LAYOUT_PROBE);
  assertMobileResultsLayout(`${label} cleared`, afterClear);
  const clearedProbe = await evalPage(`(() => ({
    hasApplied: !!document.querySelector('.goen-filters__applied'),
    href: location.href,
  }))()`);
  if (clearedProbe.hasApplied) fail(label, 'clear-all left the applied-filter summary visible');
  if (clearedProbe.href.includes('in_stock=')) fail(label, 'clear-all left filter query parameters in the URL');

  console.log(`${label.padEnd(24)} collapsed tileTop=${collapsed.tileTop} filtered focus clear ok`);
};

for (const locale of ['zh-Hant', 'en']) {
  await proveListingFilterJourney(`listing audio journey ${locale}`, locale);
}

const proveListingDesktopResize = async (label, locale) => {
  if (locale) {
    await send(ws, 'Network.setCookie', {
      name: 'goen_locale', value: locale, domain: '127.0.0.1', path: '/',
    });
  }
  const loadDesktop = async (scriptingOff) => {
    await send(ws, 'Emulation.setScriptExecutionDisabled', { value: scriptingOff });
    await send(ws, 'Emulation.setDeviceMetricsOverride', {
      width: 1440, height: 900, deviceScaleFactor: 1, mobile: false,
    });
    const target = `${ORIGIN}/c/audio`;
    await send(ws, 'Page.navigate', { url: target });
    await settled(ws, `${label} desktop load`, target);
    const got = await evalPage(FILTER_SHELL_PROBE);
    if (got.threw) fail(label, got.why || 'desktop filter probe failed');
    assertDesktopFiltersVisible(`${label} desktop`, got);
    const layout = await evalPage(LISTING_LAYOUT_PROBE);
    assertDesktopResultsLayout(`${label} desktop`, layout);
    return got;
  };

  await loadDesktop(false);

  // With scripting on, ticking a filter updates the results in place: the URL
  // follows, the chips appear, the region is a new node, the box keeps focus,
  // the page does not scroll and the announced count is the page's own.
  const live = await evalPage(`(async () => {
    const box = document.querySelector('.goen-filters input[name=in_stock]');
    if (!box) return { ok: false, why: 'stock filter missing' };
    const before = document.getElementById('listing-results');
    // Extra facets can put stock below the viewport. Bring the control into
    // view before measuring whether the results update itself moves the page.
    box.closest('details').open = true;
    box.scrollIntoView({ block: 'center', behavior: 'instant' });
    box.focus({ preventScroll: true });
    const y = window.scrollY;
    box.click();
    const deadline = Date.now() + 3000;
    while (Date.now() < deadline) {
      if (location.search.includes('in_stock=1') && document.getElementById('listing-results') !== before
        && document.querySelector('#filters-applied .goen-filters__chip')) break;
      await new Promise((r) => setTimeout(r, 50));
    }
    const count = document.querySelector('.goen-listing__count');
    const replaced = document.getElementById('listing-results') !== before;
    const focusKept = document.activeElement === box;
    const scrollAfter = window.scrollY;
    const scrolled = scrollAfter !== y;
    // The cross is nine pixels wide; what is pressable is the 44px square around
    // its centre, hit-tested at the square's four corners.
    // An open filter panel overlays the applied row by design; close it so the
    // link's own target is what is measured.
    document.querySelectorAll('.goen-filters__group[open]').forEach((group) => group.removeAttribute('open'));
    const remove = document.querySelector('#filters-applied .goen-filters__chip-remove');
    const missed = [];
    if (remove) {
      remove.scrollIntoView({ block: 'center' });
      // The update is a view transition, and while one runs the page itself is
      // what a point hits: wait until the link's own centre is pressable.
      let r = remove.getBoundingClientRect();
      for (let waited = 0; waited < 3000; waited += 50) {
        r = remove.getBoundingClientRect();
        if (remove.contains(document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2))) break;
        await new Promise((done) => setTimeout(done, 50));
      }
      const cx = r.left + r.width / 2;
      const cy = r.top + r.height / 2;
      for (const [dx, dy] of [[-21, -21], [21, -21], [-21, 21], [21, 21]]) {
        const hit = document.elementFromPoint(cx + dx, cy + dy);
        if (!hit || !remove.contains(hit)) missed.push(dx + ',' + dy);
      }
    }
    return {
      ok: true,
      search: location.search,
      chip: !!document.querySelector('#filters-applied .goen-filters__chip'),
      removeMissed: remove ? missed : null,
      replaced,
      focusKept,
      scrolled,
      scrollBefore: y,
      scrollAfter,
      status: (document.getElementById('listing-status')?.textContent || '').trim(),
      count: count ? count.textContent.trim() : null,
    };
  })()`);
  if (live.threw || !live.ok) {
    fail(label, live.why || 'live filter probe failed');
  } else {
    if (!live.search.includes('in_stock=1')) fail(label, `ticking the stock filter did not update the URL — ${JSON.stringify(live)}`);
    if (!live.chip) fail(label, `no active-filter chip after the update — ${JSON.stringify(live)}`);
    if (live.removeMissed === null) fail(label, 'the active-filter chip has no remove link');
    else if (live.removeMissed.length) fail(label, `the chip's remove link is not pressable across 44x44px; corners that miss it: ${live.removeMissed.join(' ')}`);
    if (!live.replaced) fail(label, 'the results region was not replaced by the update');
    if (!live.focusKept) fail(label, 'focus left the filter box after the update');
    if (live.scrolled) fail(label, `the page scrolled after the update: ${live.scrollBefore} -> ${live.scrollAfter}`);
    if (!live.count || live.status !== live.count) fail(label, `the announced count "${live.status}" is not the page's "${live.count}"`);
  }

  // The applied chips are the results' own heading: above them, in their column.
  const placed = await evalPage(`(() => {
    const applied = document.querySelector('#filters-applied .goen-filters__applied');
    const results = document.getElementById('listing-results');
    if (!applied || !results) return { ok: false, why: 'applied chips or results missing after the filter' };
    const a = applied.getBoundingClientRect();
    const r = results.getBoundingClientRect();
    return { ok: true, aBottom: a.bottom, aLeft: a.left, rTop: r.top, rLeft: r.left };
  })()`);
  if (placed.threw || !placed.ok) fail(label, placed.why || 'chip placement probe failed');
  else if (placed.aBottom > placed.rTop + 1 || Math.abs(placed.aLeft - placed.rLeft) > 1) {
    fail(label, `the applied chips are not above the results — ${JSON.stringify(placed)}`);
  }

  await loadDesktop(true);
  await send(ws, 'Emulation.setScriptExecutionDisabled', { value: false });

  const submitted = await evalPage(`(() => {
    const box = document.querySelector('.goen-filters input[name=in_stock]');
    const form = document.querySelector('.goen-filters');
    if (!box || !form) return { ok: false, why: 'desktop filter controls missing before submit' };
    box.checked = true;
    form.requestSubmit();
    return { ok: true };
  })()`);
  if (submitted.threw || !submitted.ok) {
    fail(label, submitted.why || 'desktop filter submit did not start');
    return;
  }
  // readyState as well as the URL, for the reason `settled` gives: location.href
  // is the new document's the moment the navigation commits, which is before that
  // document has run anything. The landing probe below reads activeElement, and
  // a URL match alone hands it a document still at readyState "loading".
  //
  // `settled` itself is not used here because it records the URL as a route for
  // the accessibility pass at the end of this file, and this href carries the
  // empty price and sort fields the form submits — a second spelling of a page
  // that pass already audits.
  const filteredHref = await (async () => {
    for (let i = 0; i < 50; i++) {
      const { result } = await send(ws, 'Runtime.evaluate', {
        expression: 'document.readyState + " " + location.href', returnByValue: true,
      });
      const [state, href] = String(result.value || '').split(' ');
      if (state === 'complete' && href.includes('in_stock=1') && href.includes('#listing-results')) {
        return href;
      }
      await new Promise((r) => setTimeout(r, 100));
    }
    return '';
  })();
  if (!filteredHref) {
    fail(label, 'desktop filter submit did not land on in_stock=1#listing-results');
    return;
  }

  // autofocus is flushed when the new document first updates its rendering, and
  // readyState complete is not that moment: a cross-document view transition
  // holds the first render until the old page's snapshot is ready, so the focus
  // a keyboard visitor gets can arrive a frame or two after the load event.
  // Give it a ceiling rather than one reading — a landing that never focuses
  // still exhausts the poll and still fails.
  //
  // The element has to EXIST for this to be true. Reading
  // `document.activeElement === document.getElementById(id)` on a page without
  // the region is null === null, which is how a missing results region would
  // have passed as a focused one.
  const focusReached = await (async () => {
    for (let i = 0; i < 30; i++) {
      const { result } = await send(ws, 'Runtime.evaluate', {
        expression: `(() => {
          const results = document.getElementById('listing-results');
          return !!results && document.activeElement === results;
        })()`,
        returnByValue: true,
      });
      if (result.value === true) return true;
      await new Promise((r) => setTimeout(r, 100));
    }
    return false;
  })();

  const afterFilter = await evalPage(`(() => {
    const applied = document.querySelector('.goen-filters__applied');
    const clear = document.querySelector('.goen-filters__applied .ui-filterbar__clear');
    const box = document.querySelector('.goen-filters input[name=in_stock]');
    return {
      ok: true,
      hasApplied: !!applied,
      hasClear: !!clear,
      stockChecked: !!(box && box.checked),
    };
  })()`);
  if (afterFilter.threw || !afterFilter.ok) {
    fail(label, afterFilter.why || 'desktop filtered landing probe failed');
    return;
  }
  if (!focusReached) fail(label, 'desktop filtered reload did not focus #listing-results');
  if (!afterFilter.hasApplied) fail(label, 'desktop filtered reload shows no applied-filter summary');
  if (!afterFilter.hasClear) fail(label, 'desktop filtered reload offers no clear-all control');
  if (!afterFilter.stockChecked) fail(label, 'desktop filtered reload lost the in_stock checkbox state');

  const filteredLayout = await evalPage(LISTING_LAYOUT_PROBE);
  assertDesktopResultsLayout(`${label} filtered`, filteredLayout);

  const cleared = await evalPage(`(() => {
    const clear = document.querySelector('.goen-filters__applied .ui-filterbar__clear');
    if (!clear) return { ok: false, why: 'clear-all link missing after desktop filter' };
    clear.click();
    return { ok: true };
  })()`);
  if (cleared.threw || !cleared.ok) {
    fail(label, cleared.why || 'desktop clear-all navigation did not start');
    return;
  }
  await settled(ws, `${label} cleared`, `${ORIGIN}/c/audio`);
  const afterClear = await evalPage(LISTING_LAYOUT_PROBE);
  assertDesktopResultsLayout(`${label} cleared`, afterClear);
  const clearedProbe = await evalPage(`(() => ({
    hasApplied: !!document.querySelector('.goen-filters__applied'),
    href: location.href,
  }))()`);
  if (clearedProbe.hasApplied) fail(label, 'desktop clear-all left the applied-filter summary visible');

  await send(ws, 'Emulation.setDeviceMetricsOverride', {
    width: 375, height: 812, deviceScaleFactor: 1, mobile: true,
  });
  await new Promise((r) => setTimeout(r, 250));
  const mobileCollapsed = await evalPage(FILTER_SHELL_PROBE);
  if (mobileCollapsed.threw) fail(label, mobileCollapsed.why || 'mobile resize probe failed');
  if (mobileCollapsed.open) fail(label, 'filter shell is open after resize to mobile');
  if (mobileCollapsed.formVisible) {
    fail(label, 'filter form is visible while the shell is closed on mobile after resize');
  }

  const expanded = await evalPage(`(() => {
    const summary = document.querySelector('.goen-filters__shell-summary');
    if (!summary) return { ok: false, why: 'mobile summary missing after resize' };
    summary.click();
    const form = document.querySelector('.goen-filters');
    const formRect = form ? form.getBoundingClientRect() : null;
    return {
      ok: true,
      formVisible: !!(formRect && formRect.height > 0 && formRect.width > 0),
    };
  })()`);
  if (expanded.threw || !expanded.ok) {
    fail(label, expanded.why || 'mobile expand after resize failed');
    return;
  }
  if (!expanded.formVisible) fail(label, 'filter form did not open after summary click on mobile');

  await send(ws, 'Emulation.setDeviceMetricsOverride', {
    width: 1440, height: 900, deviceScaleFactor: 1, mobile: false,
  });
  await new Promise((r) => setTimeout(r, 250));
  const desktopAgain = await evalPage(FILTER_SHELL_PROBE);
  if (desktopAgain.threw) fail(label, desktopAgain.why || 'desktop re-expand probe failed');
  assertDesktopFiltersVisible(`${label} after resize`, desktopAgain);

  console.log(`${label.padEnd(24)} desktop submit filter clear resize ok`);
};

for (const locale of ['zh-Hant', 'en']) {
  await proveListingDesktopResize(`listing audio desktop ${locale}`, locale);
}

// The category drawer hangs below the header and is the only category
// navigation on a phone. Paint order is invisible to axe, so each link is hit
// tested: whatever sits at its centre has to be the link, or a tap lands on the
// photo underneath and counts as an outside click.
//
// The drawer's content fades in and leaves content-visibility:hidden only when
// that transition ends, and a hidden box is not hit by anything, so the probe
// waits it out before it measures.
const DRAWER_PROBE = `(async () => {
  const menu = document.querySelector('.goen-header__menu');
  if (!menu) return { ok: false, why: 'the header menu is missing' };
  menu.open = true;
  await new Promise((done) => setTimeout(done, 1000));
  const links = [...menu.querySelectorAll('.goen-header__drawer a')];
  const drawerBottom = menu.querySelector('.goen-header__drawer').getBoundingClientRect().bottom;
  // The drawer scrolls on its own (the account links sit below the departments),
  // and a point outside the viewport hits nothing: bring each link in before it
  // is tested, so what is measured is what is painted over it and not how far
  // down it is.
  const hitOf = (a) => {
    a.scrollIntoView({ block: 'nearest' });
    const r = a.getBoundingClientRect();
    return document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
  };
  const covered = [];
  for (const a of links) {
    const hit = hitOf(a);
    if (!hit || !a.contains(hit)) {
      covered.push(a.textContent.trim() + ' under ' + (hit ? hit.tagName + '.' + String(hit.className).split(' ')[0] : 'nothing'));
    }
  }
  return { ok: true, links: links.length, covered, drawerBottom, viewport: window.innerHeight };
})()`;

for (const want of [
  { label: 'drawer home 375', width: 375, height: 812, path: '/' },
  { label: 'drawer pdp 375', width: 375, height: 812, path: '/p/PRODUCT_SLUG' },
  { label: 'drawer home 768', width: 768, height: 1024, path: '/' },
  { label: 'drawer pdp 768', width: 768, height: 1024, path: '/p/PRODUCT_SLUG' },
]) {
  await send(ws, 'Emulation.setDeviceMetricsOverride', {
    width: want.width, height: want.height, deviceScaleFactor: 1, mobile: want.width < 768,
  });
  const target = ORIGIN + want.path.replace('PRODUCT_SLUG', process.env.PRODUCT_SLUG || '');
  await send(ws, 'Page.navigate', { url: target });
  await settled(ws, want.label, target);
  const got = await evalPage(DRAWER_PROBE);
  if (got.threw || !got.ok) {
    fail(want.label, `the drawer probe did not run — ${got.why}`);
    continue;
  }
  if (!got.links) fail(want.label, 'the open drawer lists no links — this check proved nothing');
  if (got.covered.length) {
    fail(want.label, `${got.covered.length} of ${got.links} drawer links are painted over: ${got.covered.join(', ')}`);
  }
  if (got.drawerBottom > got.viewport + 0.5) {
    fail(want.label, `the open drawer ends at ${got.drawerBottom}px, below the ${got.viewport}px viewport, so what is under the fold cannot be reached`);
  }
  console.log(`${want.label.padEnd(24)} links=${got.links} covered=${got.covered.length}${got.covered.length ? '' : ' ok'}`);
}

const HEADER_EN_PROBE = `(() => {
  const de = document.documentElement;
  const clipped = (e) => {
    let p = e.parentElement;
    while (p && p !== document.body) {
      const o = getComputedStyle(p).overflowX;
      if (o === 'auto' || o === 'hidden' || o === 'scroll') return true;
      p = p.parentElement;
    }
    return false;
  };
  const search = document.querySelector('.goen-header__search');
  return {
    viewportWidth: de.clientWidth,
    scrollWidth: document.body.scrollWidth,
    overflowing: [...document.querySelectorAll('body *')]
      .filter((e) => { const r = e.getBoundingClientRect(); return r.width > 0 && r.right > de.clientWidth + 0.5 && !clipped(e); })
      .slice(0, 6).map((e) => e.tagName.toLowerCase() + '.' + String(e.className || '').split(' ')[0]),
    searchWidth: search ? search.clientWidth : 0,
    ${ACCESSIBILITY}
  };
})()`;

await send(ws, 'Network.enable');
for (const want of HEADER_EN) {
  if (want.staff && !process.env.ADMIN_TOKEN) {
    console.log(`${want.label.padEnd(24)} skipped (no ADMIN_TOKEN)`);
    continue;
  }
  await send(ws, 'Network.setCookie', {
    name: 'goen_locale', value: 'en', domain: '127.0.0.1', path: '/',
  });
  if (want.staff) {
    await send(ws, 'Network.setCookie', {
      name: 'goen_session', value: process.env.ADMIN_TOKEN, domain: '127.0.0.1', path: '/',
    });
  } else {
    await send(ws, 'Network.deleteCookies', { name: 'goen_session', domain: '127.0.0.1', path: '/' });
  }
  await send(ws, 'Emulation.setDeviceMetricsOverride', {
    width: want.width, height: want.height, deviceScaleFactor: 1, mobile: false,
  });
  const target = ORIGIN + want.path;
  await send(ws, 'Page.navigate', { url: target });
  await settled(ws, want.label, target);

  const evaluated = await send(ws, 'Runtime.evaluate', {
    expression: HEADER_EN_PROBE, returnByValue: true,
  });
  if (evaluated.exceptionDetails || !evaluated.result || evaluated.result.value === undefined) {
    fail(want.label, 'the probe did not run — ' +
      (evaluated.exceptionDetails?.exception?.description || JSON.stringify(evaluated).slice(0, 400)));
    continue;
  }
  const got = evaluated.result.value;
  const at = want.label;

  if (!got.searchWidth) {
    fail(at, 'the header search field did not render — this check proved nothing');
    continue;
  }
  if (got.lang !== 'en') {
    fail(at, `<html lang> is ${JSON.stringify(got.lang)}, want "en"`);
  }
  checkAccessibility(at, got);
  if (got.searchWidth < MIN_SEARCH) {
    fail(at, `header search is ${got.searchWidth}px wide, want >= ${MIN_SEARCH}`);
  }
  if (got.scrollWidth > got.viewportWidth) {
    fail(at, `page scrolls horizontally (${got.scrollWidth} > ${got.viewportWidth})` +
      (got.overflowing.length ? ` — widest: ${got.overflowing.join(', ')}` : ''));
  }
  console.log(`${at.padEnd(24)} scrollW=${got.scrollWidth}/${got.viewportWidth} ` +
    `lang=${got.lang} search=${got.searchWidth}`);
}
await send(ws, 'Network.deleteCookies', {
  name: 'goen_locale', domain: '127.0.0.1', path: '/',
});
await send(ws, 'Network.deleteCookies', { name: 'goen_session', domain: '127.0.0.1', path: '/' });

// The cart pages. Their probe measures the CONTROLS: a cart is a page of
// buttons and number inputs, and the defect this caught on its first run was a
// block button whose padding pushed it past the viewport at 375.
const CART_PROBE = `(() => {
  const de = document.documentElement;
  const clipped = (e) => {
    let p = e.parentElement;
    while (p && p !== document.body) {
      const o = getComputedStyle(p).overflowX;
      if (o === 'auto' || o === 'hidden' || o === 'scroll') return true;
      p = p.parentElement;
    }
    return false;
  };
  // Every control a finger has to hit. a.goen-btn is here because goen's own
  // button class replaces .ui-btn surface by surface: a rebuilt page whose one
  // action is a link would otherwise be measured as having no controls at all,
  // which is this sweep going blind rather than the page having nothing to hit.
  // A button element carrying it is already matched by the element name.
  //
  // The radio INPUT is intentionally small —
  // its label is the target — so the label is measured where one wraps it.
  // A control that is aria-hidden AND out of the tab order is a target for
  // nobody: no pointer user can see it and no keyboard user can reach it. The
  // checkout's default submit button is one — it exists so Enter places the
  // order rather than pressing a 更新 button above it. Both attributes are
  // required, because either one alone is a defect rather than an intention.
  const targets = [...document.querySelectorAll('button, .ui-btn, a.goen-btn, input[type=number], label.goen-checkout__ship, a.goen-checkout__ship')]
    .filter((e) => !(e.getAttribute('aria-hidden') === 'true' && e.getAttribute('tabindex') === '-1'))
    .filter((e) => e.getBoundingClientRect().width > 0 && !e.closest('.goen-footer, .goen-header'))
    .map((e) => +e.getBoundingClientRect().height.toFixed(1));
  return {
    viewportWidth: de.clientWidth,
    scrollWidth: document.body.scrollWidth,
    overflowing: [...document.querySelectorAll('body *')]
      .filter((e) => { const r = e.getBoundingClientRect(); return r.width > 0 && r.right > de.clientWidth + 0.5 && !clipped(e); })
      .slice(0, 4).map((e) => e.tagName.toLowerCase() + '.' + String(e.className || '').split(' ')[0]),
    minTap: targets.length ? +Math.min(...targets).toFixed(1) : 0,
    controls: targets.length,
    // __MARKER__ is substituted per page. A page behind an access gate renders
    // the 404 to a browser without the right cookie, and a 404 has a header and
    // a footer — so "there are controls" does not prove the right page loaded.
    marker: __MARKER__ ? !!document.querySelector(__MARKER__) : true,
    ${ACCESSIBILITY}
  };
})()`;

// Hold one real checkout update at the fetch boundary, inspect its pending
// state, then release it and require the same request to settle successfully.
async function proveCheckoutRequestFeedback(label) {
  const result = await evalPage(`(async () => {
    const choice = document.querySelector('input[name="shipping"]:not(:checked)');
    const form = choice?.form;
    if (!choice || !form) return { ok: false, why: 'no alternate shipping choice' };
    const originalFetch = window.fetch;
    let release;
    const held = new Promise(resolve => { release = resolve; });
    let started;
    const issued = new Promise(resolve => { started = resolve; });
    window.fetch = async (...args) => { started(); await held; return originalFetch(...args); };
    let finish;
    const done = new Promise(resolve => { finish = resolve; });
    const source = choice.closest('[hx-post]');
    if (!source) { window.fetch = originalFetch; return { ok: false, why: 'choice has no request source' }; }
    let watched;
    const watchRequest = (event) => {
      const ctx = event.detail?.ctx;
      if (ctx?.request?.form !== form || (ctx.sourceElement !== source && !source.contains(ctx.sourceElement))) return;
      watched = ctx;
    };
    // On document: the source is detached by the swap before it could hear this.
    const watchFinish = (event) => { if (watched && event.detail?.ctx === watched) finish(); };
    document.addEventListener('htmx:before:request', watchRequest);
    document.addEventListener('htmx:finally:request', watchFinish);
    try {
      choice.click();
      await Promise.race([issued, new Promise((_, reject) => setTimeout(() => reject(new Error('checkout request did not start')), 5000))]);
      const busy = form.getAttribute('aria-busy') === 'true' && !!form.querySelector('[aria-disabled="true"]');
      release();
      await Promise.race([done, new Promise((_, reject) => setTimeout(() => reject(new Error('checkout request did not finish')), 15000))]);
      // The original form: the swap replaces it, and the copy in the page is not the one that was held.
      const cleared = !form.hasAttribute('data-request-pending') && form.getAttribute('aria-busy') !== 'true' && !form.querySelector('[aria-disabled="true"]');
      return { ok: busy && cleared, busy, cleared };
    } finally { release(); document.removeEventListener('htmx:before:request', watchRequest); document.removeEventListener('htmx:finally:request', watchFinish); window.fetch = originalFetch; }
  })()`);
  if (!result.ok) fail(label, 'request feedback: ' + JSON.stringify(result));
}

// A choice re-renders the checkout's form under a customer who is half way down
// it, and the page has to stay where they were looking. The choice is centred,
// pressed as a tap presses it, and the position read once the swap and its
// cross-fade are over. value picks the option; without it, any unchosen one.
async function proveChoiceKeepsScroll(label, name, value) {
  const tolerance = 4;
  const input = `input[name="${name}"]` + (value ? `[value="${value}"]` : ':not(:checked)');
  const at = await evalPage(`(async () => {
    const frames = () => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
    const idle = async () => {
      if (document.activeViewTransition) await document.activeViewTransition.finished.catch(() => {});
      await Promise.all(document.getAnimations()
        .filter((a) => String((a.effect && a.effect.pseudoElement) || '').startsWith('::view-transition'))
        .map((a) => a.finished.catch(() => {})));
      await frames();
    };
    const choice = document.querySelector(${JSON.stringify(input)});
    const tile = choice && choice.closest('label');
    const region = document.getElementById('checkout-region');
    if (!tile || !region) return { ok: false };
    await idle();
    tile.scrollIntoView({ block: 'center', behavior: 'instant' });
    await frames();
    window.__choiceSwap = {
      region, frames, idle,
      swapped: new Promise((resolve) => document.addEventListener('htmx:after:settle', resolve, { once: true })),
    };
    const box = tile.getBoundingClientRect();
    return {
      ok: true, value: choice.value, x: box.left + box.width / 2, y: box.top + box.height / 2,
      start: scrollY, bottom: document.documentElement.scrollHeight - innerHeight,
    };
  })()`);
  if (at.threw || !at.ok) {
    fail(label, `no ${name} choice to press`);
    return;
  }
  if (at.start <= tolerance || at.bottom - at.start <= tolerance) {
    fail(label, `the ${name} choice cannot be held half way down the page (scrollY ${at.start} of ${at.bottom}), so its position proves nothing`);
    return;
  }
  for (const type of ['mousePressed', 'mouseReleased']) {
    await send(ws, 'Input.dispatchMouseEvent', { type, x: at.x, y: at.y, button: 'left', clickCount: 1 });
  }
  const got = await evalPage(`(async () => {
    const watch = window.__choiceSwap;
    const late = new Promise((r) => setTimeout(r, 10000, 'late'));
    if (await Promise.race([watch.swapped, late]) === 'late') return { ok: false, why: 'nothing swapped within 10s' };
    // The swap runs inside the view transition's update; its cross-fade starts after.
    await watch.frames();
    await watch.idle();
    const region = document.getElementById('checkout-region');
    if (!region || region === watch.region) return { ok: false, why: 'the region was not replaced' };
    if (!region.querySelector(${JSON.stringify(`input[name="${name}"][value="${at.value}"]:checked`)})) {
      return { ok: false, why: 'the new region does not hold the choice' };
    }
    return { ok: true, end: scrollY, bottom: document.documentElement.scrollHeight - innerHeight };
  })()`);
  if (got.threw || !got.ok) {
    fail(label, `choosing ${name}=${at.value} did not swap the checkout: ${got.why}`);
    return;
  }
  console.log(`${label.padEnd(16)} ${name}=${at.value} scrollY ${at.start} -> ${got.end} (bottom ${got.bottom})`);
  if (Math.abs(got.end - at.start) > tolerance) {
    fail(label, `choosing ${name}=${at.value} moved the page from scrollY ${at.start} to ${got.end} ` +
      `(bottom ${got.bottom}), want it within ${tolerance}px of where it was`);
  }
}

// Exercise the checkout's actual inputs: native validity and the blur feedback
// must agree before an order can leave this form.
async function checkoutConstraintFeedback(label) {
  const initial = await evalPage(`(() => {
    const field = document.getElementById('postal_code');
    const message = document.getElementById('postal_code-error');
    if (!field || !message) return { ok: false };
    field.focus();
    field.select();
    return { ok: true, hidden: getComputedStyle(message).display === 'none' };
  })()`);
  if (!initial.ok || !initial.hidden) {
    fail(label, 'checkout postal constraint feedback is missing or starts visible');
    return;
  }
  await send(ws, 'Input.insertText', { text: 'abc' });
  await send(ws, 'Input.dispatchKeyEvent', { type: 'keyDown', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 });
  await send(ws, 'Input.dispatchKeyEvent', { type: 'keyUp', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 });
  const checked = await evalPage(`(() => {
    const field = document.getElementById('postal_code');
    const message = document.getElementById('postal_code-error');
    const refused = !field.validity.valid && field.getAttribute('aria-invalid') === 'true' && getComputedStyle(message).display !== 'none';
    field.value = '001';
    field.dispatchEvent(new Event('input', { bubbles: true }));
    const recovered = field.validity.valid && field.getAttribute('aria-invalid') !== 'true' && getComputedStyle(message).display === 'none';
    field.value = '110 ';
    const trailingSpace = field.validity.valid;
    const lengths = ['city', 'district'].every(id => {
      const input = document.getElementById(id);
      if (!input) return false;
      const saved = input.value;
      input.value = String.fromCodePoint(0x20000).repeat(20);
      const twenty = input.validity.valid;
      input.value += ' ';
      const padded = input.validity.valid;
      input.value = String.fromCodePoint(0x20000).repeat(21);
      const twentyOne = input.validity.valid;
      input.value = saved;
      return twenty && padded && !twentyOne;
    });
    return { ok: true, refused, recovered, trailingSpace, lengths };
  })()`);
  if (!checked.ok || !checked.refused || !checked.recovered || !checked.trailingSpace || !checked.lengths) {
    fail(label, 'checkout constraints failed: ' + JSON.stringify(checked));
  }
}

for (const want of [...CART, ...PAGES]) {
  await send(ws, 'Emulation.setDeviceMetricsOverride', {
    width: want.width, height: want.height, deviceScaleFactor: 1, mobile: want.width < 768,
  });
  const target = ORIGIN + want.path
    .replace('PLACED_ORDER', process.env.PLACED_ORDER || '')
    .replace('PICKUP_SHIP', process.env.PICKUP_SHIP || '')
    .replace('PRODUCT_SLUG', process.env.PRODUCT_SLUG || '')
    .replace('CUSTOMER_ID', process.env.CUSTOMER_ID || '');
  await send(ws, 'Page.navigate', { url: target });
  await settled(ws, want.label, target);

  const { result } = await send(ws, 'Runtime.evaluate', {
    expression: CART_PROBE.replaceAll('__MARKER__', JSON.stringify(want.marker || null)),
    returnByValue: true,
  });
  const got = result.value;
  const at = want.label;
  checkAccessibility(at, got);

  if (!got.marker) {
    fail(at, `the page did not render (${want.marker} is absent) — this check proved nothing`);
    continue;
  }
  if (got.scrollWidth > got.viewportWidth) {
    fail(at, `page scrolls horizontally (${got.scrollWidth} > ${got.viewportWidth})` +
      (got.overflowing.length ? ` — widest: ${got.overflowing.join(', ')}` : ''));
  }
  if (got.controls === 0) {
    fail(at, 'no controls on the page — the cart is empty, so this check proved nothing');
  } else if (got.minTap < MIN_TAP) {
    fail(at, `smallest control is ${got.minTap}px, want >= ${MIN_TAP}`);
  }
  console.log(`${at.padEnd(16)} scrollW=${got.scrollWidth}/${got.viewportWidth} ` +
    `controls=${got.controls} tap=${got.minTap}`);
  if (want.path === '/checkout') await checkoutConstraintFeedback(at);
  if (want.path === '/checkout') await proveCheckoutRequestFeedback(at);
  if (want.path === '/checkout') await proveChoiceKeepsScroll(at, 'invoice_type');
  if (want.marker === 'input[name=pickup_chain]') await proveChoiceKeepsScroll(at, 'pickup_chain', 'seven_eleven');
}

// Stepping to a bound must leave keyboard focus on the button that was pressed:
// disabling a focused button sends focus to the body.
const STEPPER_FOCUS_PROBE = `(() => {
  const up = document.querySelector('.goen-stepper__step[data-stepper-step="1"]');
  const field = document.querySelector('.goen-stepper__value');
  if (!up || !field || up.disabled || field.disabled || up.getBoundingClientRect().width === 0) {
    return { ok: true, skipped: true };
  }
  up.focus();
  for (let i = 0; i < 100 && up.getAttribute('aria-disabled') !== 'true'; i++) up.click();
  return {
    ok: true, skipped: false, value: field.value, max: field.max,
    atBound: up.getAttribute('aria-disabled') === 'true',
    focused: document.activeElement === up,
    active: document.activeElement ? document.activeElement.tagName : 'none',
  };
})()`;

{
  const label = 'stepper focus 375';
  await send(ws, 'Emulation.setDeviceMetricsOverride', { width: 375, height: 812, deviceScaleFactor: 1, mobile: true });
  const target = ORIGIN + '/p/' + (process.env.PRODUCT_SLUG || '');
  await send(ws, 'Page.navigate', { url: target });
  await settled(ws, label, target);
  const got = await evalPage(STEPPER_FOCUS_PROBE);
  if (got.threw) fail(label, `the probe did not run — ${got.why}`);
  else if (got.skipped) console.log(`${label.padEnd(24)} stepper not offered on this product, skipped`);
  else {
    if (!got.atBound) fail(label, `the + button never reached its bound (value ${got.value}, max ${got.max})`);
    if (!got.focused) fail(label, `focus left the + button at the bound and sits on ${got.active}`);
    console.log(`${label.padEnd(24)} value=${got.value}/${got.max} focused=${got.focused}${got.focused ? ' ok' : ''}`);
  }
}

// A quantity typed above the stock is the server's to answer: the request goes
// out, the field takes back the quantity the cart kept (the stock), and the
// notice region says why. A native validation bubble would have swallowed it.
const CART_OVERTYPE_PROBE = `(async () => {
  const lineField = () => document.querySelector('.goen-line__controls .goen-stepper__value');
  const notice = () => (document.getElementById('cart-notices')?.textContent || '').trim();
  const until = async (done) => {
    const deadline = Date.now() + 8000;
    while (Date.now() < deadline && !done()) await new Promise((r) => setTimeout(r, 100));
    return done();
  };
  const type = (field, value) => {
    field.focus();
    field.value = String(value);
    field.dispatchEvent(new Event('input', { bubbles: true }));
    field.dispatchEvent(new Event('change', { bubbles: true }));
  };
  const field = lineField();
  if (!field) return { ok: false, why: 'the cart has no quantity field' };
  const max = Number(field.max);
  const original = Number(field.value);
  if (!max || max >= 999) return { ok: true, skipped: true };
  const noticeBefore = notice();
  type(field, max + 5);
  const settled = await until(() => lineField()?.value === String(max) && notice() !== '');
  const result = { ok: true, skipped: false, max, settled, value: lineField()?.value, noticeBefore, notice: notice() };
  // The cart is shared with the checks that follow: put the line back.
  type(lineField(), original);
  await until(() => lineField()?.value === String(original) && notice() === '');
  return result;
})()`;

{
  const label = 'cart overtype 375';
  await send(ws, 'Emulation.setDeviceMetricsOverride', { width: 375, height: 812, deviceScaleFactor: 1, mobile: true });
  const target = ORIGIN + '/cart';
  await send(ws, 'Page.navigate', { url: target });
  await settled(ws, label, target);
  const got = await evalPage(CART_OVERTYPE_PROBE);
  if (got.threw || !got.ok) fail(label, `the probe did not run — ${got.why}`);
  else if (got.skipped) console.log(`${label.padEnd(24)} no line with a stock bound, skipped`);
  else {
    if (got.noticeBefore !== '') fail(label, 'the cart already carried a notice, so this check proved nothing');
    if (got.value !== String(got.max)) fail(label, `typing ${got.max + 5} left the field at ${got.value}, want the stock ${got.max}`);
    if (got.notice === '') fail(label, 'no notice said why the quantity changed');
    console.log(`${label.padEnd(24)} value=${got.value}/${got.max} notice=${got.notice !== ''}`);
  }
}

// A department's panel is not part of the tab order until it is opened: with
// focus on the department's link its sub-links are not rendered, ArrowDown opens
// the panel and moves into it, and Escape closes it and returns to the link.
{
  const label = 'department panel 1440';
  await send(ws, 'Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  const target = ORIGIN + '/';
  await send(ws, 'Page.navigate', { url: target });
  await settled(ws, label, target);
  const key = async (name, code) => {
    for (const type of ['keyDown', 'keyUp']) {
      await send(ws, 'Input.dispatchKeyEvent', { type, key: name, code: name, windowsVirtualKeyCode: code });
    }
  };
  const state = () => evalPage(`(() => {
    const dept = document.querySelector('.goen-dept');
    const link = dept && dept.querySelector(':scope > a');
    const panel = dept && dept.querySelector('.goen-dept__panel');
    if (!link || !panel) return { skipped: true };
    return {
      skipped: false,
      shown: [...panel.querySelectorAll('a')].filter(a => a.getClientRects().length > 0).length,
      expanded: link.getAttribute('aria-expanded'),
      inPanel: panel.contains(document.activeElement),
      onLink: document.activeElement === link,
    };
  })()`);
  await evalPage(`(() => { const a = document.querySelector('.goen-dept > a'); if (a) a.focus(); })()`);
  const resting = await state();
  if (resting.skipped) console.log(`${label.padEnd(24)} no department panel on this catalogue, skipped`);
  else {
    if (!resting.onLink) fail(label, 'the department link did not take focus');
    if (resting.shown !== 0) fail(label, `${resting.shown} sub-links are rendered, so tab stops, with focus only on the department link`);
    await key('ArrowDown', 40);
    const opened = await state();
    if (!opened.inPanel || opened.expanded !== 'true' || opened.shown === 0) fail(label, 'ArrowDown did not open the panel and move into it: ' + JSON.stringify(opened));
    await key('Escape', 27);
    const closed = await state();
    if (!closed.onLink || closed.shown !== 0 || closed.expanded !== 'false') fail(label, 'Escape did not close the panel and return to the link: ' + JSON.stringify(closed));
    console.log(`${label.padEnd(24)} panel closed on focus, opens with ArrowDown, Escape returns ok`);
  }
}

// The served transition rules must honor reduced motion, including pseudo-
// elements and native details content that the global element override misses.
for (const motion of ['no-preference', 'reduce']) {
  await send(ws, 'Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: motion }] });
  await send(ws, 'Emulation.setDeviceMetricsOverride', { width: 375, height: 812, deviceScaleFactor: 1, mobile: true });
  await send(ws, 'Page.navigate', { url: ORIGIN + '/' });
  await settled(ws, 'motion ' + motion, ORIGIN + '/');
  const checked = await evalPage(`(async () => {
    const menu = document.querySelector('[data-menu]');
    if (!menu) return { ok: false, why: 'missing native menu' };
    menu.querySelector('summary').click();
    const duration = getComputedStyle(menu, '::details-content').transitionDuration;
    if (!document.startViewTransition) return { ok: true, supported: false, duration };
    const transition = document.startViewTransition(() => { document.body.dataset.motionProbe = 'changed'; });
    await transition.ready;
    const root = document.documentElement;
    const animation = getComputedStyle(root, '::view-transition-new(root)').animationName;
    const group = getComputedStyle(root, '::view-transition-group(root)').animationName;
    const seconds = parseFloat(getComputedStyle(root, '::view-transition-new(root)').animationDuration);
    transition.skipTransition();
    await transition.finished;
    return { ok: true, supported: true, animation, group, seconds, duration };
  })()`);
  if (!checked.ok) fail('motion ' + motion, JSON.stringify(checked));
  // Reduced motion keeps a plain dissolve of the base step: a hard cut between
  // two different layouts reads as more of a jump than the fade does. What it
  // may not keep is any group animation, which is what moves or resizes.
  if (checked.supported && checked.animation === 'none') fail('motion ' + motion, 'the page dissolve is switched off');
  if (checked.supported && motion === 'reduce') {
    if (checked.group !== 'none') fail('motion reduce', 'the root group still animates: ' + checked.group);
    if (!(checked.seconds <= 0.1201)) fail('motion reduce', 'the dissolve takes ' + checked.seconds + 's, want at most 0.12s');
  }
  if (motion === 'reduce' && checked.duration !== '0s') fail('motion reduce', 'menu still transitions: ' + checked.duration);
  await send(ws, 'Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  await send(ws, 'Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  const closed = await evalPage(`(() => { const menu = document.querySelector('[data-menu]'); return !menu.open && document.activeElement === menu.querySelector('summary'); })()`);
  if (closed !== true) fail('motion ' + motion, 'Escape did not close the menu and return focus');
}
await send(ws, 'Emulation.setEmulatedMedia', { features: [] });

// A photo clicked on one page is the same photo on the next. The new page's
// recorder is injected into every document the probe opens, because a
// cross-document transition can only be observed from inside the two pages:
// what each page named, whether the transition ran or was skipped, and which
// groups animated.
const TRANSITION_RECORDER = `(() => {
  const rec = (patch) => {
    try { sessionStorage.vt = JSON.stringify({ ...JSON.parse(sessionStorage.vt || '{}'), ...patch }); } catch {}
  };
  const named = () => [...document.querySelectorAll('*')].map((e) => e.style.viewTransitionName).filter(Boolean);
  const pseudo = (a) => String((a.effect && a.effect.pseudoElement) || '');
  addEventListener('pagereveal', (e) => {
    rec({ revealed: !!e.viewTransition, kind: navigation.activation && navigation.activation.navigationType, ready: null });
    if (!e.viewTransition) return;
    e.viewTransition.ready.then(() => {
      const running = document.getAnimations().filter((a) => pseudo(a).startsWith('::view-transition'));
      const moving = running.filter((a) => a.effect.getKeyframes().some((k) =>
        ['transform', 'translate', 'scale', 'rotate', 'width', 'height', 'inlineSize', 'blockSize', 'top', 'left'].some((p) => p in k)));
      rec({
        ready: 'resolved',
        newNames: named(),
        groups: running.map(pseudo).filter((p) => p.startsWith('::view-transition-group(')),
        durations: Object.fromEntries(running.map((a) => [pseudo(a), a.effect.getTiming().duration])),
        moving: moving.map(pseudo),
      });
    }, () => rec({ ready: 'rejected' }));
  });
  addEventListener('pageshow', (e) => rec({ persisted: e.persisted }));
  // After the transitions script's own listener, so the names it set are the ones read.
  document.addEventListener('DOMContentLoaded', () => addEventListener('pageswap', (e) => {
    if (e.viewTransition) rec({ oldNames: named(), swapped: true });
  }));
})()`;

const proveMorph = async (label, { from, link, name, viewport, reduce = false, back = null }) => {
  await send(ws, 'Emulation.setEmulatedMedia', { features: reduce ? [{ name: 'prefers-reduced-motion', value: 'reduce' }] : [] });
  await send(ws, 'Emulation.setDeviceMetricsOverride', { ...viewport, deviceScaleFactor: 1, mobile: viewport.width < 768 });
  await send(ws, 'Page.navigate', { url: ORIGIN + from });
  await settled(ws, label, ORIGIN + from);
  const href = await evalPage(`(() => {
    const withPhoto = [...document.querySelectorAll(${JSON.stringify(link)})].filter((x) => x.querySelector('img'));
    const a = ${back === 'hidden'} ? withPhoto.at(-1) : withPhoto[0];
    if (!a) return null;
    sessionStorage.vt = '{}';
    if (${back === 'visible'}) a.scrollIntoView({ block: 'center' });
    window.__morphLink = a;
    return a.getAttribute('href');
  })()`);
  if (typeof href !== 'string') { fail(label, `no ${link} with a photo on ${from}, so no navigation was made`); return; }
  if (back === 'hidden') {
    const where = await evalPage(`(() => { const r = window.__morphLink.querySelector('img').getBoundingClientRect(); return r.top >= innerHeight || r.bottom <= 0; })()`);
    if (where !== true) { fail(label, 'the card is inside the viewport, so this proved nothing about a card outside it'); return; }
  }
  await send(ws, 'Runtime.evaluate', { expression: 'window.__morphLink.click()' });
  await settled(ws, label, ORIGIN + href);
  const read = async (want) => {
    for (let i = 0; i < 30; i++) {
      const v = await evalPage(`JSON.parse(sessionStorage.vt || '{}')`);
      if (v && v.ready && (!want || v.kind === want)) return v;
      await new Promise((r) => setTimeout(r, 100));
    }
    return await evalPage(`JSON.parse(sessionStorage.vt || '{}')`);
  };
  let got = await read('push');
  if (!got.revealed) { fail(label, 'the arriving page had no view transition: ' + JSON.stringify(got)); return; }
  if (got.ready !== 'resolved') { fail(label, 'the transition was skipped: ' + JSON.stringify(got)); return; }
  if (got.swapped !== true) fail(label, 'the page being left never saw its own pageswap: ' + JSON.stringify(got));
  const photoGroups = (got.groups || []).filter((g) => /^::view-transition-group\((product|department)-photo\)$/.test(g));
  // The step each group takes is part of the design: the photo travels in the
  // move step, the page dissolves in the slow one, and reduced motion in the base one.
  const takes = (group, ms) => {
    const at = (got.durations || {})[group];
    if (at !== ms) fail(label, `${group} runs for ${at}ms, want ${ms}ms`);
  };
  takes('::view-transition-new(root)', reduce ? 120 : 200);
  if (!reduce) for (const g of photoGroups) takes(g, 280);
  if (reduce) {
    if (photoGroups.length || (got.oldNames || []).length || (got.newNames || []).length) fail(label, 'reduced motion still named a photo: ' + JSON.stringify(got));
    if ((got.moving || []).length) fail(label, 'reduced motion still runs animations that move, scale or resize: ' + got.moving.join(', '));
  } else {
    const want = JSON.stringify([name]);
    if (JSON.stringify(got.oldNames || []) !== want) fail(label, `the clicked page named ${JSON.stringify(got.oldNames)}, want only ${want}`);
    if (JSON.stringify(got.newNames || []) !== want) fail(label, `the arriving page named ${JSON.stringify(got.newNames)}, want only ${want}`);
    if (!photoGroups.length) fail(label, `no ::view-transition-group(${name}) animated: ` + JSON.stringify(got.groups));
  }
  let persisted;
  await new Promise((r) => setTimeout(r, 400));
  const left = await evalPage(`[...document.querySelectorAll('*')].filter((e) => e.style.viewTransitionName || e.style.viewTransitionClass).length`);
  if (left !== 0) fail(label, `${left} element(s) still carry a view-transition-name after the transition`);

  if (back) {
    await evalPage(`sessionStorage.vt = '{}'; true`);
    await send(ws, 'Runtime.evaluate', { expression: 'history.back()' });
    await settled(ws, label + ' back', ORIGIN + from);
    const returned = await read('traverse');
    if (returned.ready !== 'resolved') { fail(label + ' back', 'the way back had no completed transition: ' + JSON.stringify(returned)); return; }
    const backGroups = (returned.groups || []).filter((g) => /-photo\)$/.test(g));
    if (back === 'visible') {
      if (JSON.stringify(returned.newNames || []) !== JSON.stringify([name])) fail(label + ' back', `the card in view was not named ${name}: ` + JSON.stringify(returned));
      if (!backGroups.length) fail(label + ' back', 'the photo did not travel back to the card: ' + JSON.stringify(returned.groups));
    } else if ((returned.newNames || []).length || backGroups.length) {
      fail(label + ' back', 'a photo was sent to a card outside the viewport: ' + JSON.stringify(returned));
    }
    await new Promise((r) => setTimeout(r, 400));
    const after = await evalPage(`[...document.querySelectorAll('*')].filter((e) => e.style.viewTransitionName).length`);
    if (after !== 0) fail(label + ' back', `${after} element(s) still carry a view-transition-name after the way back`);
    persisted = (await evalPage(`JSON.parse(sessionStorage.vt || '{}')`)).persisted;
  }
  const how = persisted === undefined ? '' : persisted ? ' (restored from bfcache)' : ' (reloaded, not bfcache)';
  console.log(`${label.padEnd(24)} ${reduce ? 'reduced: dissolve only' : 'named ' + name + ', ran, cleared'}${back ? ', back ' + back + how : ''} ok`);
};

{
  const recorder = await send(ws, 'Page.addScriptToEvaluateOnNewDocument', { source: TRANSITION_RECORDER });
  const desk = { width: 1440, height: 900 };
  const tile = 'a.goen-tile[href^="/p/"]';
  await proveMorph('morph tile 1440', { from: '/c/phones', link: tile, name: 'product-photo', viewport: desk });
  await proveMorph('morph tile 375', { from: '/c/phones', link: tile, name: 'product-photo', viewport: { width: 375, height: 812 } });
  await proveMorph('morph department 1440', { from: '/', link: 'a.goen-cat[href^="/c/"]', name: 'department-photo', viewport: desk });
  await proveMorph('morph related 1440', { from: '/p/' + process.env.PRODUCT_SLUG, link: '.goen-pdp__related ' + tile, name: 'product-photo', viewport: desk });
  await proveMorph('morph back visible', { from: '/c/phones', link: tile, name: 'product-photo', viewport: desk, back: 'visible' });
  await proveMorph('morph back hidden', { from: '/c/phones', link: tile, name: 'product-photo', viewport: { width: 1440, height: 400 }, back: 'hidden' });
  await proveMorph('morph tile reduced', { from: '/c/phones', link: tile, name: 'product-photo', viewport: desk, reduce: true });
  await send(ws, 'Page.removeScriptToEvaluateOnNewDocument', { identifier: recorder.identifier });
  await send(ws, 'Emulation.setEmulatedMedia', { features: [] });
}

// The comparison table. CART_PROBE's marker-only check is not enough: the
// wrapper is present on the empty state, and the table's job is to scroll
// inside its own box while the first column stays put.
const COMPARE_PROBE = `(() => {
  const de = document.documentElement;
  const clipped = (e) => {
    let p = e.parentElement;
    while (p && p !== document.body) {
      const o = getComputedStyle(p).overflowX;
      if (o === 'auto' || o === 'hidden' || o === 'scroll') return true;
      p = p.parentElement;
    }
    return false;
  };
  const table = document.querySelector('.goen-compare__table');
  const scroller = document.querySelector('.goen-compare__scroll');
  const firstCol = document.querySelector('.goen-compare__table tbody th');
  const sticky = firstCol ? getComputedStyle(firstCol) : null;
  return {
    viewportWidth: de.clientWidth,
    scrollWidth: document.body.scrollWidth,
    overflowing: [...document.querySelectorAll('body *')]
      .filter((e) => { const r = e.getBoundingClientRect(); return r.width > 0 && r.right > de.clientWidth + 0.5 && !clipped(e); })
      .slice(0, 4).map((e) => e.tagName.toLowerCase() + '.' + String(e.className || '').split(' ')[0]),
    marker: __MARKER__ ? !!document.querySelector(__MARKER__) : true,
    productColumns: table ? table.querySelectorAll('thead .goen-compare__head').length : 0,
    tableScrolls: !!(scroller && scroller.scrollWidth > scroller.clientWidth + 0.5),
    stickyLeft: !!(sticky && sticky.position === 'sticky' && parseFloat(sticky.left) === 0),
    ${ACCESSIBILITY}
  };
})()`;

for (const want of COMPARE) {
  await send(ws, 'Emulation.setDeviceMetricsOverride', {
    width: want.width, height: want.height, deviceScaleFactor: 1, mobile: want.width < 768,
  });
  const target = ORIGIN + want.path
    .replace('COMPARE_SLUG', process.env.COMPARE_SLUG || '')
    .replace('PRODUCT_SLUG', process.env.PRODUCT_SLUG || '');
  await send(ws, 'Page.navigate', { url: target });
  await settled(ws, want.label, target);

  const evaluated = await send(ws, 'Runtime.evaluate', {
    expression: COMPARE_PROBE.replaceAll('__MARKER__', JSON.stringify(want.marker || null)),
    returnByValue: true,
  });
  if (evaluated.exceptionDetails || !evaluated.result || evaluated.result.value === undefined) {
    fail(want.label, 'the probe did not run — ' +
      (evaluated.exceptionDetails?.exception?.description || JSON.stringify(evaluated).slice(0, 400)));
    continue;
  }
  const got = evaluated.result.value;
  const at = want.label;

  if (!got.marker) {
    fail(at, `the page did not render (${want.marker} is absent) — this check proved nothing`);
    continue;
  }
  checkAccessibility(at, got);
  if (got.scrollWidth > got.viewportWidth) {
    fail(at, `page scrolls horizontally (${got.scrollWidth} > ${got.viewportWidth})` +
      (got.overflowing.length ? ` — widest: ${got.overflowing.join(', ')}` : ''));
  }
  if (want.table) {
    if (got.productColumns < 2) {
      fail(at, `comparison has ${got.productColumns} product columns, want at least 2`);
    }
    if (!got.stickyLeft) {
      fail(at, 'the first column is not sticky — a reader loses the row label when the table scrolls');
    }
    if (want.width === 375 && !got.tableScrolls) {
      fail(at, 'the table does not scroll inside its box at 375 — the columns were never wide enough to measure');
    }
  }
  console.log(`${at.padEnd(16)} scrollW=${got.scrollWidth}/${got.viewportWidth} ` +
    `cols=${got.productColumns} sticky=${got.stickyLeft} tableScroll=${got.tableScrolls}`);
}

// The back office. Its tables are deliberately wider than a phone and scroll
// inside their own box, so the assertion is that the PAGE does not scroll —
// which is what body.scrollWidth answers and documentElement.scrollWidth does
// not.
const ADMIN_PROBE = `(() => {
  const de = document.documentElement;
  const clipped = (e) => {
    let p = e.parentElement;
    while (p && p !== document.body) {
      const o = getComputedStyle(p).overflowX;
      if (o === 'auto' || o === 'hidden' || o === 'scroll') return true;
      p = p.parentElement;
    }
    return false;
  };
  const admin = document.querySelector('.goen-admin');
  if (!admin) return { missing: true };
  // And the row's own marker, substituted per page. .goen-admin is the back office
  // CHROME — it is there on an empty search, a 404 body and a page whose data
  // fixture never ran, so a check that only asks for it reports a measured page
  // where it measured a nav bar.
  if (__MARKER__ && !document.querySelector(__MARKER__)) return { noMarker: true };
  // Nav links, buttons and inputs. Table cells are not targets.
  const taps = [...document.querySelectorAll('.goen-admin .ui-navitem, .goen-admin button, .goen-admin input, .goen-admin .ui-filter')]
    .map((e) => e.getBoundingClientRect().height).filter((h) => h > 0);
  // A field is one column: its label is not squeezed beside the control, and no
  // child starts away from the field's left edge or sits on a sibling.
  const fieldFaults = (${fieldFaults.toString()})(document.querySelectorAll('.goen-admin__field'));
  return {
    viewportWidth: de.clientWidth,
    scrollWidth: document.body.scrollWidth,
    overflowing: [...document.querySelectorAll('body *')]
      .filter((e) => { const r = e.getBoundingClientRect(); return r.width > 0 && r.right > de.clientWidth + 0.5 && !clipped(e); })
      .slice(0, 4).map((e) => e.tagName.toLowerCase() + '.' + String(e.className || '').split(' ')[0]),
    minTap: taps.length ? +Math.min(...taps).toFixed(1) : 0,
    fieldFaults: fieldFaults.slice(0, 4),
    controls: taps.length,
    ${ACCESSIBILITY}
  };
})()`;

const POINTS_REDEEM_PROBE = `(() => {
  const redeemForm = document.querySelector('form.goen-qa__form');
  const redeemField = document.querySelector('#points');
  if (!redeemForm || !redeemField) return { noRedeem: true };
  const max = parseInt(redeemField.getAttribute('max') || '0', 10);
  if (!(max > 0)) return { noRedeemable: true };
  const taps = [...document.querySelectorAll('.goen-qa__form .ui-btn, .goen-qa__form .ui-input')]
    .map((e) => e.getBoundingClientRect().height).filter((h) => h > 0);
  return {
    redeemable: max,
    controls: taps.length,
    minTap: taps.length ? +Math.min(...taps).toFixed(1) : 0,
    taps,
  };
})()`;

const ACCOUNT_BADFORM_PROBE = `(() => {
  const de = document.documentElement;
  const clipped = (e) => {
    let p = e.parentElement;
    while (p && p !== document.body) {
      const o = getComputedStyle(p).overflowX;
      if (o === 'auto' || o === 'hidden' || o === 'scroll') return true;
      p = p.parentElement;
    }
    return false;
  };
  const notice = document.querySelector('.ui-alert--info');
  const redeem = ${POINTS_REDEEM_PROBE};
  if (redeem.noRedeem) return { noRedeem: true };
  if (redeem.noRedeemable) return { noRedeemable: true };
  return {
    viewportWidth: de.clientWidth,
    scrollWidth: document.body.scrollWidth,
    overflowing: [...document.querySelectorAll('body *')]
      .filter((e) => { const r = e.getBoundingClientRect(); return r.width > 0 && r.right > de.clientWidth + 0.5 && !clipped(e); })
      .slice(0, 4).map((e) => e.tagName.toLowerCase() + '.' + String(e.className || '').split(' ')[0]),
    minTap: redeem.minTap,
    controls: redeem.controls,
    redeemable: redeem.redeemable,
    notice: notice ? notice.textContent.trim() : '',
    ${ACCESSIBILITY}
  };
})()`;

const ACCOUNT_PAGE_PROBE = `(() => {
  const de = document.documentElement;
  const clipped = (e) => {
    let p = e.parentElement;
    while (p && p !== document.body) {
      const o = getComputedStyle(p).overflowX;
      if (o === 'auto' || o === 'hidden' || o === 'scroll') return true;
      p = p.parentElement;
    }
    return false;
  };
  if (__MARKER__ && !document.querySelector(__MARKER__)) return { noMarker: true };
  const taps = [...document.querySelectorAll(
    '.goen-account .ui-page-head .ui-btn, .goen-qa__form .ui-btn, .goen-qa__form .ui-input, ' +
    '.goen-order__cancel .ui-btn, .goen-order__cancel .goen-btn, ' +
    '.goen-returns__form .ui-btn, .goen-returns__form button, ' +
    '#points')]
    .map((e) => e.getBoundingClientRect().height).filter((h) => h > 0);
  return {
    viewportWidth: de.clientWidth,
    scrollWidth: document.body.scrollWidth,
    overflowing: [...document.querySelectorAll('body *')]
      .filter((e) => { const r = e.getBoundingClientRect(); return r.width > 0 && r.right > de.clientWidth + 0.5 && !clipped(e); })
      .slice(0, 4).map((e) => e.tagName.toLowerCase() + '.' + String(e.className || '').split(' ')[0]),
    minTap: taps.length ? +Math.min(...taps).toFixed(1) : 0,
    controls: taps.length,
    ${ACCESSIBILITY}
  };
})()`;

const POINTS_PAGE_PROBE = `(() => {
  const de = document.documentElement;
  const clipped = (e) => {
    let p = e.parentElement;
    while (p && p !== document.body) {
      const o = getComputedStyle(p).overflowX;
      if (o === 'auto' || o === 'hidden' || o === 'scroll') return true;
      p = p.parentElement;
    }
    return false;
  };
  const redeem = ${POINTS_REDEEM_PROBE};
  if (redeem.noRedeem) return { noRedeem: true };
  if (redeem.noRedeemable) return { noRedeemable: true };
  return {
    viewportWidth: de.clientWidth,
    scrollWidth: document.body.scrollWidth,
    overflowing: [...document.querySelectorAll('body *')]
      .filter((e) => { const r = e.getBoundingClientRect(); return r.width > 0 && r.right > de.clientWidth + 0.5 && !clipped(e); })
      .slice(0, 4).map((e) => e.tagName.toLowerCase() + '.' + String(e.className || '').split(' ')[0]),
    minTap: redeem.minTap,
    controls: redeem.controls,
    redeemable: redeem.redeemable,
    ${ACCESSIBILITY}
  };
})()`;

// Wishlist rows need each saved product as one direct grid list item: the card
// link and its native remove form live in the same li, with matching slugs.
// Tile already emits li.goen-tile__cell; wrapping @Tile in li.goen-wish leaves
// an empty outer item and parks the form as a direct ul child.
const WISHLIST_PAGE_PROBE = `(() => {
  const de = document.documentElement;
  const clipped = (e) => {
    let p = e.parentElement;
    while (p && p !== document.body) {
      const o = getComputedStyle(p).overflowX;
      if (o === 'auto' || o === 'hidden' || o === 'scroll') return true;
      p = p.parentElement;
    }
    return false;
  };
  const grid = document.querySelector('.goen-tiles__grid');
  if (!grid) return { noMarker: true };
  const slugFromCard = (item) => {
    const link = item.querySelector('a.goen-tile[href]');
    if (!link) return '';
    const m = (link.getAttribute('href') || '').match(/^\\/p\\/([^?#]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  };
  const slugFromForm = (item) => {
    const input = item.querySelector('form.goen-wish__remove input[name="slug"]');
    return input ? input.value : '';
  };
  const direct = [...grid.children];
  if (!direct.length) return { noMarker: true };
  const problems = [];
  const taps = [];
  let composed = 0;
  for (const child of direct) {
    if (child.tagName !== 'LI') {
      problems.push(child.tagName.toLowerCase() + ' is a direct grid child');
      continue;
    }
    if (!child.classList.contains('goen-wish')) {
      problems.push('grid li is not .goen-wish');
      continue;
    }
    if (child.querySelector('li')) {
      problems.push('grid li nests another list item');
      continue;
    }
    const card = child.querySelector('a.goen-tile');
    const form = child.querySelector('form.goen-wish__remove');
    if (!card) {
      problems.push('.goen-wish has no product card');
      continue;
    }
    if (!form) {
      problems.push('.goen-wish has no remove form');
      continue;
    }
    const cardSlug = slugFromCard(child);
    const formSlug = slugFromForm(child);
    if (!cardSlug || !formSlug || cardSlug !== formSlug) {
      problems.push('card slug and remove form slug do not match');
      continue;
    }
    const btn = form.querySelector('.ui-btn');
    if (btn) {
      const h = btn.getBoundingClientRect().height;
      if (h > 0) taps.push(h);
    }
    composed++;
  }
  return {
    viewportWidth: de.clientWidth,
    scrollWidth: document.body.scrollWidth,
    overflowing: [...document.querySelectorAll('body *')]
      .filter((e) => { const r = e.getBoundingClientRect(); return r.width > 0 && r.right > de.clientWidth + 0.5 && !clipped(e); })
      .slice(0, 4).map((e) => e.tagName.toLowerCase() + '.' + String(e.className || '').split(' ')[0]),
    items: direct.length,
    composed,
    problems,
    minTap: taps.length ? +Math.min(...taps).toFixed(1) : 0,
    controls: taps.length,
    ${ACCESSIBILITY}
  };
})()`;

if (process.env.CUST_TOKEN) {
  await send(ws, 'Network.enable');
  await send(ws, 'Network.setCookie', {
    name: 'goen_session', value: process.env.CUST_TOKEN, domain: '127.0.0.1', path: '/',
  });

  for (const want of ACCOUNT_BADFORM) {
    await send(ws, 'Network.setCookie', {
      name: 'goen_locale', value: want.locale, domain: '127.0.0.1', path: '/',
    });
    await send(ws, 'Emulation.setDeviceMetricsOverride', {
      width: want.width, height: want.height, deviceScaleFactor: 1, mobile: want.width < 768,
    });
    const target = ORIGIN + '/account/points?badform=1';
    await send(ws, 'Page.navigate', { url: target });
    await settled(ws, want.label, target);

    const evaluated = await send(ws, 'Runtime.evaluate', {
      expression: ACCOUNT_BADFORM_PROBE, returnByValue: true,
    });
    if (evaluated.exceptionDetails || !evaluated.result || evaluated.result.value === undefined) {
      fail(want.label, 'the probe did not run — ' +
        (evaluated.exceptionDetails?.exception?.description || JSON.stringify(evaluated).slice(0, 400)));
      continue;
    }
    const got = evaluated.result.value;
    const at = want.label;

    if (got.noRedeem) {
      fail(at, 'the points page rendered without form.goen-qa__form and #points — its fixture did not run, so this check proved nothing');
      continue;
    }
    if (got.noRedeemable) {
      fail(at, 'the points page has no redeemable hundreds — #points max is zero, so this check proved nothing');
      continue;
    }
    if (!got.notice) {
      fail(at, 'the expired-form notice did not render (.ui-alert--info is absent) — this check proved nothing');
      continue;
    }
    if (got.notice !== want.notice) {
      fail(at, `notice = ${JSON.stringify(got.notice)}, want ${JSON.stringify(want.notice)}`);
    }
    if (got.lang !== want.locale) {
      fail(at, `<html lang> is ${JSON.stringify(got.lang)}, want ${JSON.stringify(want.locale)}`);
    }
    checkAccessibility(at, got);
    if (got.scrollWidth > got.viewportWidth) {
      fail(at, `page scrolls horizontally (${got.scrollWidth} > ${got.viewportWidth})` +
        (got.overflowing.length ? ` — widest: ${got.overflowing.join(', ')}` : ''));
    }
    if (got.controls === 0) {
      fail(at, 'no redemption controls on the populated points page — this check proved nothing');
    } else if (got.minTap < MIN_TAP) {
      fail(at, `smallest redemption control is ${got.minTap}px, want >= ${MIN_TAP}`);
    }
    console.log(`${at.padEnd(24)} scrollW=${got.scrollWidth}/${got.viewportWidth} ` +
      `lang=${got.lang} controls=${got.controls} tap=${got.minTap || '-'} redeemable=${got.redeemable} notice=${JSON.stringify(got.notice)}`);
  }

  // A delivered order of the signed-in customer: the track of its right to cancel.
  await periodPass('/orders/' + (process.env.RETURN_FORM_ORDER || ''), true);

  for (const want of ACCOUNT_PAGES) {
    await send(ws, 'Emulation.setDeviceMetricsOverride', {
      width: want.width, height: want.height, deviceScaleFactor: 1, mobile: want.width < 768,
    });
    const target = ORIGIN + want.path
      .replace('INVOICE_ORDER', process.env.INVOICE_ORDER || '')
      .replace('RETURN_FORM_ORDER', process.env.RETURN_FORM_ORDER || '');
    await send(ws, 'Page.navigate', { url: target });
    await settled(ws, want.label, target);

    const probe = want.wishlist ? WISHLIST_PAGE_PROBE
      : want.points ? POINTS_PAGE_PROBE
        : ACCOUNT_PAGE_PROBE.replaceAll('__MARKER__', JSON.stringify(want.marker || null));
    const { result } = await send(ws, 'Runtime.evaluate', {
      expression: probe, returnByValue: true,
    });
    const got = result.value;
    const at = want.label;

    if (got.noMarker) {
      fail(at, want.wishlist
        ? 'the wishlist rendered without a product grid — its fixture did not run, so this check proved nothing'
        : `the page rendered without ${want.marker} — its fixture did not run, so this check proved nothing`);
      continue;
    }
    if (want.wishlist && got.composed < 1) {
      const detail = got.problems?.length ? ` — ${got.problems.join('; ')}` : '';
      fail(at, `wishlist has no composed saved-product row — card and remove form must share one grid list item with matching slugs${detail}`);
      continue;
    }
    if (want.wishlist && got.items !== got.composed) {
      fail(at, `wishlist grid has ${got.items} direct children but only ${got.composed} compose a card with its matching remove form`);
      continue;
    }
    if (got.noRedeem) {
      fail(at, 'the points page rendered without form.goen-qa__form and #points — its fixture did not run, so this check proved nothing');
      continue;
    }
    if (got.noRedeemable) {
      fail(at, 'the points page has no redeemable hundreds — #points max is zero, so this check proved nothing');
      continue;
    }
    checkAccessibility(at, got);
    if (got.scrollWidth > got.viewportWidth) {
      fail(at, `page scrolls horizontally (${got.scrollWidth} > ${got.viewportWidth})` +
        (got.overflowing.length ? ` — widest: ${got.overflowing.join(', ')}` : ''));
    }
    if ((want.wishlist || want.points) && got.controls === 0) {
      fail(at, want.wishlist
        ? 'no remove control on the populated wishlist — this check proved nothing'
        : 'no redemption controls on the populated points page — this check proved nothing');
    } else if (got.controls > 0 && got.minTap < MIN_TAP) {
      fail(at, `smallest control is ${got.minTap}px, want >= ${MIN_TAP}`);
    }
    const extra = want.points ? ` redeemable=${got.redeemable}`
      : want.wishlist ? ` items=${got.items} composed=${got.composed}` : '';
    console.log(`${at.padEnd(24)} scrollW=${got.scrollWidth}/${got.viewportWidth} ` +
      `controls=${got.controls} tap=${got.minTap || '-'}${extra}`);
  }

  // Hovering the nth star of the rating row previews exactly n, whatever is
  // already chosen. The row is offered to a customer whose order was delivered,
  // so the product is the one on the customer's delivered order.
  {
    const label = 'review stars hover';
    await send(ws, 'Network.setCookie', { name: 'goen_locale', value: 'zh-Hant', domain: '127.0.0.1', path: '/' });
    await send(ws, 'Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
    const product = ORIGIN + '/p/' + (process.env.REVIEW_SLUG || '');
    await send(ws, 'Page.navigate', { url: product });
    await settled(ws, label, product);
    const centres = await evalPage(`(() => {
      const row = document.querySelector('.goen-pdp__starrow');
      if (!row) return null;
      row.scrollIntoView({ block: 'center' });
      const inputs = [...row.querySelectorAll('input')];
      inputs[3].checked = true;
      return [...row.querySelectorAll('.goen-pdp__star')].map((e) => {
        const r = e.getBoundingClientRect();
        return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
      });
    })()`);
    if (!centres || centres.threw || centres.length !== 5) {
      fail(label, 'no rating row is offered to the customer on the product of the delivered order');
    } else {
      const lit = () => evalPage(`[...document.querySelectorAll('.goen-pdp__starrow .goen-pdp__starmark svg')]
        .filter((e) => getComputedStyle(e).fill !== 'none').length`);
      const hover = (at) => send(ws, 'Input.dispatchMouseEvent', { type: 'mouseMoved', x: at.x, y: at.y });
      for (const n of [1, 2, 3]) {
        await hover(centres[n - 1]);
        const got = await lit();
        if (got !== n) fail(label, `hovering star ${n} with 4 chosen lights ${got}, want ${n}`);
      }
      await hover({ x: 5, y: 5 });
      const rest = await lit();
      if (rest !== 4) fail(label, `with the pointer off the row the chosen 4 light ${rest}`);
      console.log(`${label.padEnd(24)} hover 1..3 lights 1..3, rest lights ${rest}`);
    }
  }
} else {
  console.log('account pages    skipped (no CUST_TOKEN)');
}

if (process.env.ADMIN_TOKEN) {
  await send(ws, 'Network.enable');
  await send(ws, 'Network.setCookie', {
    name: 'goen_session', value: process.env.ADMIN_TOKEN, domain: '127.0.0.1', path: '/',
  });

  // The staff session is checked ONCE, before the sweep, and the answer names
  // which of four things went wrong.
  //
  // Every admin row below tests `.goen-admin` and, when it is absent, said "the
  // staff session is not being accepted". That sentence describes ONE cause and
  // there are at least four, three of which are not a rejected cookie at all:
  //
  //   - the fixture wrote no session row;
  //   - the cookie is fine and GOEN_TOTP_KEY is set, so /admin redirects to the
  //     step-up challenge that this target cannot answer — it has no authenticator;
  //   - the session expired, or the server is running with secure cookies and is
  //     reading __Host-goen_session instead;
  //   - the back office is genuinely broken, which is the only one worth 48 lines
  //     of output.
  //
  // A run that failed all 34 admin rows with the single message and then passed
  // twice is what put this here: with one sentence for four causes, "not
  // deterministic" was the only reading available. Where the browser LANDS tells
  // them apart, because each cause redirects somewhere different.
  {
    const probe = ORIGIN + '/admin';
    await send(ws, 'Emulation.setDeviceMetricsOverride', {
      width: 1440, height: 900, deviceScaleFactor: 1, mobile: false,
    });
    await send(ws, 'Page.navigate', { url: probe });
    await settled(ws, 'admin session', probe);
    const { result } = await send(ws, 'Runtime.evaluate', {
      expression: `({ href: location.pathname + location.search,
                      admin: !!document.querySelector('.goen-admin'),
                      title: (document.querySelector('h1') || {}).textContent || '' })`,
      returnByValue: true,
    });
    const at = result.value;
    if (!at.admin) {
      // RequireStaff answers 404 rather than redirecting to /signin, deliberately:
      // it does not disclose that /admin exists to somebody who may not be staff.
      // So "still at /admin, no back-office chrome" IS the rejected-session case,
      // and reading it as an unexpected landing would send the next person looking
      // for a broken route. Verified by running this against a token no session
      // row matches.
      const why = at.href.startsWith('/admin/verify')
        ? 'the back office wants a SECOND FACTOR. GOEN_TOTP_KEY is set on the server, so the ' +
          'step-up gate is on and this check has no authenticator. Run it against a server ' +
          'started without that key, or give layout-check@goen.invalid a confirmed credential.'
        : at.href.startsWith('/admin')
          ? 'the session is not being accepted AS STAFF (RequireStaff answers 404 rather than ' +
            'redirecting). Either the fixture wrote no session row, or it has expired, or the ' +
            'user is not role=admin, or the server is running with secure cookies and reads ' +
            '__Host-goen_session while this check sets goen_session.'
          : `it landed on ${at.href} with h1 ${JSON.stringify(at.title)}, which is none of the ` +
            'causes this check knows about — worth reading before trusting the rest.';
      fail('admin session', `${why}\n    Not running the ${ADMIN.length} back-office rows: ` +
        'each would have reported this one cause as a failure of its own page.');
      // Each row is a failure of its own, so a surface that was not measured
      // is listed by name and never leaves the run smaller and green.
      for (const row of ADMIN) fail(row.label, 'not measured: the admin session was unusable (see "admin session" above)');
      ADMIN.length = 0;
    }
  }

  for (const want of ADMIN) {
    await send(ws, 'Emulation.setDeviceMetricsOverride', {
      width: want.width, height: want.height, deviceScaleFactor: 1, mobile: want.width < 768,
    });
    const target = ORIGIN + want.path
      .replace('PLACED_ORDER', process.env.PLACED_ORDER || '')
      .replace('INVOICE_ORDER', process.env.INVOICE_ORDER || '')
      .replace('CUSTOMER_ID', process.env.CUSTOMER_ID || '')
      .replace('LAYOUT_SERIAL', process.env.LAYOUT_SERIAL || '')
      .replace('PRODUCT_SLUG', process.env.PRODUCT_SLUG || '');
    await send(ws, 'Page.navigate', { url: target });
    await settled(ws, want.label, target);

    const { result } = await send(ws, 'Runtime.evaluate', {
      expression: ADMIN_PROBE.replaceAll('__MARKER__', JSON.stringify(want.marker || null)),
      returnByValue: true,
    });
    const got = result.value;
    const at = want.label;

    if (got.missing) {
      fail(at, 'the back office did not render — the staff session is not being accepted');
      continue;
    }
    if (got.noMarker) {
      fail(at, `the page rendered without ${want.marker} — its fixture did not run, so this check proved nothing`);
      continue;
    }
    // AFTER the two guards above, deliberately: a 404 body has headings and controls
    // of its own, so measuring it would report the wrong page's problems as this
    // page's.
    checkAccessibility(at, got);
    if (got.scrollWidth > got.viewportWidth) {
      fail(at, `page scrolls horizontally (${got.scrollWidth} > ${got.viewportWidth})` +
        (got.overflowing.length ? ` — widest: ${got.overflowing.join(', ')}` : ''));
    }
    if (got.fieldFaults.length) fail(at, `form field laid out wrong: ${got.fieldFaults.join("; ")}`);
    if (got.controls === 0) {
      fail(at, 'no controls found — the probe measured nothing');
    } else if (got.minTap < MIN_TAP) {
      fail(at, `smallest control is ${got.minTap}px, want >= ${MIN_TAP}`);
    }
    console.log(`${at.padEnd(16)} scrollW=${got.scrollWidth}/${got.viewportWidth} ` +
      `controls=${got.controls} tap=${got.minTap}`);
  }

  // The charts' hover readout: pointing at a day writes that row of the chart's
  // own table under the plot without moving anything, it stays while the
  // pointer is on it, Escape puts it away, and nothing overflows sideways.
  if (ADMIN.length) {
    for (const [width, height] of [[375, 812], [1440, 900]]) {
      const label = `admin chart readout ${width}`;
      await send(ws, 'Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: width < 768 });
      const target = ORIGIN + '/admin/reports';
      await send(ws, 'Page.navigate', { url: target });
      await settled(ws, label, target);
      const count = await evalPage('document.querySelectorAll(".goen-chart__hit").length ? document.querySelectorAll(".goen-chart").length : 0');
      if (!count || count.threw) {
        fail(label, 'the reports page rendered no chart with hit areas — its fixture did not run, so this check proved nothing');
        continue;
      }
      const mouse = (at) => send(ws, 'Input.dispatchMouseEvent', { type: 'mouseMoved', x: at.x, y: at.y });
      // pick is an expression over hits: the middle day, then the last, whose
      // line is the longest (the largest totals, the "up to" time, the campaign).
      const probe = (k, pick) => evalPage(`(() => {
          const fig = document.querySelectorAll('.goen-chart')[${k}];
          const hits = [...fig.querySelectorAll('.goen-chart__hit')];
          if (!hits.length) return { none: true };
          fig.scrollIntoView({ block: 'center' });
          const at = ${pick};
          const row = fig.querySelector('.goen-chart__table').tBodies[0].rows[Number(at.dataset.row)];
          const heads = [...fig.querySelector('.goen-chart__table').tHead.rows[0].cells];
          const series = [], notes = [];
          heads.forEach((h, i) => {
            const text = row.cells[i].textContent.trim();
            if (!text) return;
            if (h.dataset.readout === 'series') series.push(text + ' ' + h.textContent.trim());
            if (h.dataset.readout === 'note') notes.push(text);
          });
          const box = at.getBoundingClientRect();
          const frame = fig.querySelector('.goen-chart__frame').getBoundingClientRect();
          const readout = fig.querySelector('.goen-chart__readout');
          const line = readout ? readout.getBoundingClientRect() : null;
          return {
            hit: { x: box.left + box.width / 2, y: box.top + box.height / 2 },
            gap: { x: box.left + box.width / 2, y: frame.bottom + 1 },
            line: line && { x: line.left + line.width / 2, y: line.top + line.height / 2 },
            want: [...series, row.cells[0].textContent.trim(), ...notes].join(' · '),
            top: fig.querySelector('.goen-chart__data').getBoundingClientRect().top,
          };
        })()`);
      for (let k = 0; k < count; k++) {
        const chart = `${label} chart ${k + 1}`;
        const got = await probe(k, 'hits[hits.length >> 1]');
        if (got.none) continue;
        if (got.threw || !got.line) {
          fail(chart, 'no .goen-chart__readout in the figure');
          continue;
        }
        const read = () => evalPage(`(() => {
          const fig = document.querySelectorAll('.goen-chart')[${k}];
          return {
            text: fig.querySelector('.goen-chart__readout').textContent,
            top: fig.querySelector('.goen-chart__data').getBoundingClientRect().top,
            wide: document.documentElement.scrollWidth - document.documentElement.clientWidth,
          };
        })()`);
        await mouse({ x: 2, y: 2 });
        await mouse(got.hit);
        const hovered = await read();
        if (hovered.text !== got.want) fail(chart, `hovering a day reads "${hovered.text}", want the table row "${got.want}"`);
        if (Math.abs(hovered.top - got.top) > 0.5) fail(chart, `the table moved ${hovered.top - got.top}px when the readout appeared`);
        if (hovered.wide > 0) fail(chart, `the page scrolls sideways by ${hovered.wide}px with the readout showing`);
        // The first pixel under the plot is the readout's own padding while the
        // two touch. With a gap there it is the figure, and crossing it puts the
        // readout away before the pointer gets down to it.
        await mouse(got.gap);
        const below = await read();
        if (below.text !== got.want) fail(chart, `the readout went away 1px under the plot, on the way down to it: "${below.text}"`);
        await mouse(got.line);
        const onIt = await read();
        if (onIt.text !== got.want) fail(chart, `the readout went away when the pointer moved onto it: "${onIt.text}"`);
        for (const type of ['keyDown', 'keyUp']) {
          await send(ws, 'Input.dispatchKeyEvent', { type, key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
        }
        const gone = await read();
        if (gone.text !== '') fail(chart, `Escape left the readout showing "${gone.text}"`);
        // The last day reads the longest line, two lines on a narrow figure; the
        // height held for it is what keeps the table's toggle where it was.
        const last = await probe(k, 'hits[hits.length - 1]');
        if (last.threw || last.none) {
          fail(chart, 'could not measure the last day');
          continue;
        }
        await mouse({ x: 2, y: 2 });
        await mouse(last.hit);
        const longest = await read();
        if (longest.text !== last.want) fail(chart, `hovering the last day reads "${longest.text}", want the table row "${last.want}"`);
        if (Math.abs(longest.top - last.top) > 0.5) fail(chart, `the table moved ${longest.top - last.top}px when the last day's readout appeared`);
        if (longest.wide > 0) fail(chart, `the page scrolls sideways by ${longest.wide}px with the last day's readout showing`);
        await mouse({ x: 2, y: 2 });
        console.log(`${chart.padEnd(32)} reads "${got.want.slice(0, 40)}", last "${last.want.slice(0, 40)}"`);
      }
    }
  }

  // The order page is the packing slip: printed, the back office around it is
  // gone and the delivery block and the lines are what is left.
  if (ADMIN.length && process.env.INVOICE_ORDER) {
    const label = 'admin order print';
    const target = `${ORIGIN}/admin/orders/${process.env.INVOICE_ORDER}`;
    await send(ws, 'Emulation.setDeviceMetricsOverride', {
      width: 794, height: 1123, deviceScaleFactor: 1, mobile: false,
    });
    await send(ws, 'Emulation.setEmulatedMedia', { media: 'print' });
    try {
      await send(ws, 'Page.navigate', { url: target });
      await settled(ws, label, target);
      const printed = await evalPage(`(() => {
        const shown = (selector) => [...document.querySelectorAll(selector)]
          .filter((el) => getComputedStyle(el).display !== 'none' && el.getClientRects().length > 0).length;
        return {
          lines: shown('.goen-order__line'),
          delivery: shown('.ui-dl__row'),
          nav: shown('.goen-admin__nav'),
          bar: shown('.goen-adminbar'),
          forms: shown('.goen-admin__orderpanel form, .goen-admin__orderside form'),
        };
      })()`);
      if (printed.threw) {
        fail(label, `print probe did not run — ${printed.why}`);
      } else {
        if (printed.lines === 0) fail(label, 'no order lines are shown when printed');
        if (printed.delivery === 0) fail(label, 'the recipient and destination are not shown when printed');
        if (printed.nav || printed.bar) fail(label, 'the back-office bar or rail is still shown when printed');
        if (printed.forms) fail(label, `${printed.forms} action forms are still shown when printed`);
        console.log(`${label.padEnd(16)} lines=${printed.lines} delivery=${printed.delivery} nav=${printed.nav} forms=${printed.forms}`);
      }
    } finally {
      await send(ws, 'Emulation.setEmulatedMedia', { media: '' });
    }
  }

  if (ADMIN.length) {
    const label = 'admin batch print';
    const target = `${ORIGIN}/admin/orders/picking/slips`;
    await send(ws, 'Emulation.setDeviceMetricsOverride', {
      width: 794, height: 1123, deviceScaleFactor: 1, mobile: false,
    });
    await send(ws, 'Emulation.setEmulatedMedia', { media: 'print' });
    try {
      await send(ws, 'Page.navigate', { url: target });
      await settled(ws, label, target);
      const printed = await evalPage(`(() => {
        const slips = [...document.querySelectorAll('.goen-admin__slip')];
        const visible = (el) => getComputedStyle(el).display !== 'none' && el.getClientRects().length > 0;
        const controls = [...document.querySelectorAll('.goen-adminbar,.goen-admin__nav,.goen-admin__batchcontrols,form')].filter(visible);
        return {
          slips: slips.length,
          lines: slips.map((slip) => slip.querySelectorAll('.goen-order__line').length),
          delivery: slips.map((slip) => slip.querySelectorAll('.ui-dl__row').length),
          breaks: slips.map((slip) => getComputedStyle(slip).breakAfter),
          pickLists: document.querySelectorAll('.goen-admin__picklist').length,
          controls: controls.length,
          controlNames: controls.map((el) => el.tagName.toLowerCase() + '.' + el.className),
        };
      })()`);
      if (printed.threw) {
        fail(label, `print probe did not run — ${printed.why}`);
      } else {
        if (printed.slips < 2) fail(label, `only ${printed.slips} slips; the multi-order fixture did not run`);
        if (printed.lines.some((n) => n === 0) || printed.delivery.some((n) => n < 4)) fail(label, 'a slip lost its items or delivery');
        if (printed.breaks.slice(0, -1).some((value) => value !== 'page')) fail(label, 'a slip lacks its forced page break');
        if (printed.pickLists !== 1 || printed.controls) fail(label, `pickLists=${printed.pickLists} controls=${printed.controls}: ${printed.controlNames.join(", ")}`);
        const pdf = await send(ws, 'Page.printToPDF', { preferCSSPageSize: true, printBackground: true });
        // Chromium writes page dictionaries outside compressed content streams.
        const pages = (Buffer.from(pdf.data, 'base64').toString('latin1').match(/\/Type\s*\/Page\b/g) || []).length;
        if (pages !== printed.slips + 1) fail(label, `${pages} PDF pages, want ${printed.slips} slips plus the pick list`);
        console.log(`${label.padEnd(16)} slips=${printed.slips} PDF pages=${pages} controls=${printed.controls}`);
      }
    } finally {
      await send(ws, 'Emulation.setEmulatedMedia', { media: '' });
    }
  }
} else {
  console.log('admin           skipped (no ADMIN_TOKEN)');
}

// The footer newsletter and the contact panel both target themselves with
// outerHTML. htmx 4 swaps every status except 204/304, so a plain-text 429
// replaces the interactive surface with raw `429 …` and the visitor cannot
// retry. A handler test can only see the fragment; these rows spend the live
// limiter through the actual submit control and read the swapped DOM.
const RETRY_ZH = '請求過於頻繁，請稍後再試。';
const RETRY_EN = 'Too many requests. Please try again shortly.';
const namesRetry = (text) => String(text || '').includes(RETRY_ZH) || String(text || '').includes(RETRY_EN);

const openAt = async (label, path) => {
  await send(ws, 'Emulation.setDeviceMetricsOverride', {
    width: 1440, height: 900, deviceScaleFactor: 1, mobile: false,
  });
  const target = ORIGIN + path;
  await send(ws, 'Page.navigate', { url: target });
  await settled(ws, label, target);
};

// axe has no WCAG 1.4.11 rule, so measure the boundary or fill identifying each editable region.
// Native Tab must add a shape as well as changing the colour of the border.
const controlFocus = async (selector) => {
  const prepared = await evalPage(`(() => {
    const target = document.querySelector(${JSON.stringify(selector)});
    // Closed disclosure contents can keep layout boxes without being Tab stops.
    const controls = [...document.querySelectorAll('a[href], button, input, select, textarea, summary, [tabindex]')]
      .filter((el) => el.tabIndex >= 0 && !el.disabled && !el.closest('[inert]')
        && el.checkVisibility({ visibilityProperty: true }));
    const position = controls.indexOf(target);
    if (position < 1) return { error: 'control or its preceding Tab stop is missing' };
    if (controls.some((el) => el.tabIndex > 0)) return { error: 'positive tabindex needs an explicit focus-order check' };
    controls[position - 1].focus();
    return { ready: document.activeElement === controls[position - 1] };
  })()`);
  if (!prepared.ready) return { selector, error: prepared.error || prepared.why || 'preceding Tab stop could not focus' };
  await send(ws, 'Input.dispatchKeyEvent', { type: 'keyDown', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 });
  await send(ws, 'Input.dispatchKeyEvent', { type: 'keyUp', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 });
  return evalPage(`new Promise((resolve) => setTimeout(() => resolve((${measureControlBoundary.toString()})(${JSON.stringify([selector])}, ${contrastRatio.toString()}, true)[0]), 350))`);
};

const { cookies: boundaryCookies } = await send(ws, 'Network.getCookies', { urls: [ORIGIN] });
const boundarySession = boundaryCookies.find((c) => c.name === 'goen_session');
try {
  for (const locale of ['zh-Hant', 'en']) {
    await send(ws, 'Network.setCookie', { name: 'goen_locale', value: locale, domain: '127.0.0.1', path: '/' });
    for (const { path, selectors, widths, session } of [
      { path: '/contact', selectors: ['#contact-name', '#contact-subject', '#contact-message', '#site-search', '#newsletter-email'], widths: [1440] },
      { path: '/compare?p=' + encodeURIComponent(process.env.PRODUCT_SLUG || ''), selectors: ['#compare-q'], widths: [1440] },
      { path: '/c/phones', selectors: ['#sort'], widths: [1440] },
      { path: '/admin/credit', selectors: ['#credit-email'], widths: [375, 1440], session: process.env.ADMIN_TOKEN },
      { path: '/admin/orders/' + (process.env.RETURN_FORM_ORDER || ''), selectors: ['#staff-note', '#next-status'], widths: [375, 1440], session: process.env.ADMIN_TOKEN },
      { path: '/checkout', selectors: ['#address-book'], widths: [375, 1440], session: process.env.CUST_TOKEN },
    ]) {
      if ((path.startsWith('/admin/') || path === '/checkout') && !session) {
        fail('control boundary', path + ': authenticated fixture missing');
        continue;
      }
      if (session) await send(ws, 'Network.setCookie', { name: 'goen_session', value: session, domain: '127.0.0.1', path: '/' });
      for (const width of widths) {
        const label = 'control boundary ' + locale + ' ' + width + ' ' + path;
        await openAt(label, path);
        await send(ws, 'Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false });
        await evalPage('new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
        await send(ws, 'Input.dispatchMouseEvent', { type: 'mouseMoved', x: 0, y: 0 });
        const boundaries = await evalPage(`(${measureControlBoundary.toString()})(${JSON.stringify(selectors)}, ${contrastRatio.toString()})`);
        if (boundaries.threw || !Array.isArray(boundaries)) {
          fail(label, boundaries.why || 'boundary probe returned no measurements');
          continue;
        }
        for (const boundary of boundaries) {
          console.log(label + ' rest ' + JSON.stringify(boundary));
          if (boundary.error) {
            fail(label, boundary.selector + ': ' + boundary.error);
            continue;
          }
          if (Math.max(boundary.outlineContrast, boundary.borderContrast, boundary.fillContrast, boundary.underlineContrast, boundary.arrowContrast) < 3) {
            fail(label, boundary.selector + ': boundary, underline, arrow and fill all below 3:1');
          }
          const focused = await controlFocus(boundary.selector);
          console.log(label + ' focus ' + JSON.stringify(focused));
          if (focused.error || focused.threw || !focused.active || !focused.focusVisible || focused.outlineWidth < 2
              || focused.outlineWidth <= boundary.outlineWidth || focused.outlineContrast < 3) {
            fail(label, boundary.selector + ': native keyboard focus has no additional visible 2px ring');
          }
        }
      }
    }
  }
} finally {
  if (boundarySession) {
    await send(ws, 'Network.setCookie', { name: boundarySession.name, value: boundarySession.value, domain: boundarySession.domain, path: boundarySession.path });
  } else {
    await send(ws, 'Network.deleteCookies', { name: 'goen_session', domain: '127.0.0.1', path: '/' });
  }
  await send(ws, 'Network.setCookie', { name: 'goen_locale', value: 'zh-Hant', domain: '127.0.0.1', path: '/' });
}

// Selection must be visible after focus leaves the control; focus is a separate state.
const forcedColoursMissing = ['CUST_TOKEN', 'PICKUP_SHIP', 'COLOUR_SLUG'].filter((name) => !process.env[name]);
if (forcedColoursMissing.length === 0) {
  await send(ws, 'Network.setCookie', {
    name: 'goen_session', value: process.env.CUST_TOKEN, domain: '127.0.0.1', path: '/',
  });
  try {
    for (const palette of ['light', 'dark']) {
      await send(ws, 'Emulation.setEmulatedMedia', { features: [
        { name: 'forced-colors', value: 'active' },
        { name: 'prefers-color-scheme', value: palette },
      ] });
      for (const locale of ['zh-Hant', 'en']) {
        await send(ws, 'Network.setCookie', { name: 'goen_locale', value: locale, domain: '127.0.0.1', path: '/' });
        console.log('forced colours palette ' + palette + ' locale ' + locale);
        for (const [path, groups] of [['/checkout', ['invoice_type', 'shipping']],
          ['/checkout?ship=' + process.env.PICKUP_SHIP, ['pickup_chain']]]) {
          await openAt('forced colours chooser', path);
          for (const name of groups) {
            const got = await evalPage(`(${measureChooserStates.toString()})(${JSON.stringify(name)})`);
            console.log('forced colours chooser ' + JSON.stringify({ palette, locale, path, ...got }));
            if (got.threw || got.error || !got.forced || got.scheme !== palette) {
              fail('forced colours chooser', got.why || got.error || 'forced-colors did not activate');
            } else {
              for (const choice of got.results) {
                if (!choice.distinct && !choice.radioVisible) {
                  fail('forced colours chooser', name + '=' + choice.value + ' has no visible selected cue after blur');
                }
              }
            }
          }
        }
        await openAt('forced colours swatch', '/p/' + process.env.COLOUR_SLUG);
        const swatchURL = await evalPage(`document.querySelector('.goen-swatch:not(.goen-swatch--dot)')?.href || null`);
        if (!swatchURL || swatchURL.threw) {
          fail('forced colours swatch', 'no text variant choice was rendered');
        } else {
          await send(ws, 'Page.navigate', { url: swatchURL });
          await settled(ws, 'forced colours swatch chosen', swatchURL);
          const got = await evalPage(`(${measureSwatchState.toString()})()`);
          console.log('forced colours swatch ' + JSON.stringify({ palette, locale, ...got }));
          if (got.threw || got.error || !got.forced || got.scheme !== palette || !got.distinct) {
            fail('forced colours swatch', got.why || got.error || 'selected text swatch has no distinct visible cue');
          } else if (parseFloat(got.checked.outlineWidth) < 2 || got.checked.outlineStyle === 'none') {
            fail('forced colours swatch', palette + '/' + locale + ' selected ring is ' + got.checked.outlineWidth + ' ' + got.checked.outlineStyle + ', want at least 2px visible outline');
          } else if (!(got.contrast >= 3)) {
            fail('forced colours swatch', palette + '/' + locale + ' selected ring contrasts with Canvas at ' + got.contrast.toFixed(2) + ':1, want at least 3:1');
          }
        }
        const colourURL = await evalPage(`document.querySelector('.goen-swatch--dot')?.href || null`);
        if (!colourURL || colourURL.threw) {
          fail('forced colours colour swatch', 'no colour variant choice was rendered');
        } else {
          await send(ws, 'Page.navigate', { url: colourURL });
          await settled(ws, 'forced colours chosen colour', colourURL);
          const got = await evalPage(`(${measureSwatchState.toString()})(true)`);
          console.log('forced colours colour swatch ' + JSON.stringify({ palette, locale, ...got }));
          if (got.threw || got.error || !got.forced || got.scheme !== palette || !got.distinct) {
            fail('forced colours colour swatch', got.why || got.error || 'selected colour swatch has no distinct visible cue');
          } else if (parseFloat(got.checked.outlineWidth) < 2 || got.checked.outlineStyle === 'none') {
            fail('forced colours colour swatch', palette + '/' + locale + ' selected ring is ' + got.checked.outlineWidth + ' ' + got.checked.outlineStyle + ', want at least 2px visible outline');
          } else if (!(got.contrast >= 3)) {
            fail('forced colours colour swatch', palette + '/' + locale + ' selected ring contrasts with Canvas at ' + got.contrast.toFixed(2) + ':1, want at least 3:1');
          }
        }
        await openAt('forced colours language', '/contact');
        const language = await evalPage(`(() => {
          const menu = document.querySelector('.goen-langmenu');
          if (!menu) return { error: 'language menu missing' };
          menu.open = true;
          const selected = menu.querySelector('[aria-pressed="true"]');
          const other = menu.querySelector('[aria-pressed="false"]');
          const current = menu.querySelector('.goen-langmenu__current');
          return { forced: matchMedia('(forced-colors: active)').matches,
            current: current?.textContent.trim(), selected: selected?.getAttribute('lang'),
            selectedIcons: selected?.querySelectorAll('svg').length,
            otherIcons: other?.querySelectorAll('svg').length,
            selectedWeight: selected && getComputedStyle(selected).fontWeight,
            otherWeight: other && getComputedStyle(other).fontWeight };
        })()`);
        console.log('forced colours language ' + JSON.stringify(language));
        if (language.threw || language.error || !language.forced || !language.current
          || !(language.selectedIcons > language.otherIcons || language.selectedWeight !== language.otherWeight)) {
          fail('forced colours language', language.why || language.error || 'current language has no structural cue');
        }
      }
    }
  } finally {
    await send(ws, 'Emulation.setEmulatedMedia', { features: [] });
    await send(ws, 'Network.setCookie', { name: 'goen_locale', value: 'zh-Hant', domain: '127.0.0.1', path: '/' });
    if (process.env.ADMIN_TOKEN) {
      await send(ws, 'Network.setCookie', {
        name: 'goen_session', value: process.env.ADMIN_TOKEN, domain: '127.0.0.1', path: '/',
      });
    }
  }
} else if (process.env.CUST_TOKEN) {
  fail('forced colours fixtures', forcedColoursMissing.join(', ') + ' unset — run it through make check-layout');
} else {
  console.log('forced colours skipped (no CUST_TOKEN)');
}

const proveUsable = async (at, fieldSel, formSel) => {
  const got = await evalPage(`(() => {
    const form = document.querySelector(${JSON.stringify(formSel)});
    const field = document.querySelector(${JSON.stringify(fieldSel)});
    if (!form || form.tagName !== 'FORM' || !field) {
      return { form: !!form, field: !!field };
    }
    const typed = 'still-usable@example.com';
    field.focus();
    field.value = typed;
    field.dispatchEvent(new Event('input', { bubbles: true }));
    const submit = form.querySelector('button[type=submit]');
    return {
      form: true,
      field: true,
      hxPost: form.getAttribute('hx-post') || '',
      typed: field.value,
      enabled: !field.disabled && !field.readOnly,
      canSubmit: !!(submit && !submit.disabled),
    };
  })()`);
  if (got.threw) {
    fail(at, `usability probe did not run — ${got.why}`);
    return;
  }
  if (!got.form || !got.field) {
    fail(at, 'the swapped 429 left no form field to type into');
    return;
  }
  if (!got.enabled || got.typed !== 'still-usable@example.com') {
    fail(at, 'the swapped form does not accept input');
  }
  if (!got.canSubmit) {
    fail(at, 'the swapped form has no usable submit control');
  }
  if (!got.hxPost) {
    fail(at, 'the swapped form lost hx-post, so a later submit would leave the page');
  }
};

const exhaustHtmx = async (want) => {
  let sawRetry = false;
  for (let n = 1; n <= want.budget; n++) {
    const at = `${want.label} try ${n}`;
    await openAt(at, want.path);
    const ready = await evalPage(`({
      htmx: typeof htmx !== 'undefined',
      form: !!document.querySelector(${JSON.stringify(want.form)}),
    })`);
    if (ready.threw || !ready.htmx) {
      fail(want.label, 'htmx is not on the page, so this check cannot see a swap');
      return;
    }
    if (!ready.form) {
      fail(at, `${want.form} is absent before submit`);
      return;
    }

    const got = await evalPage(want.submit);
    if (got.threw) {
      fail(at, `submit did not run — ${got.why}`);
      return;
    }
    if (!got.ok) {
      fail(at, got.why || 'submit did not start');
      return;
    }
    if (got.event === 'timeout') {
      fail(at, `htmx never swapped — landed on ${got.href} (${got.bodyStart})`);
      return;
    }
    if (got.navigated) {
      fail(at, `the submit left the page for ${got.href} — htmx did not handle it`);
      return;
    }
    if (!got.hasForm && (String(got.slot || '').includes('429 ') || namesRetry(got.slot))) {
      fail(want.label, 'htmx swapped the plain 429 over the form, which is the defect');
      return;
    }
    if (got.hasForm && namesRetry(got.retry)) {
      sawRetry = true;
      await proveUsable(want.label, want.field, want.form);
      console.log(`${want.label.padEnd(16)} 429-html retry visible, form still usable`);
      break;
    }
  }
  if (!sawRetry) {
    fail(want.label, `never exhausted after ${want.budget} htmx submits — the limiter was not reached`);
  }
};

await exhaustHtmx({
  label: 'newsletter 429',
  path: '/about',
  form: 'form#newsletter-form',
  field: '#newsletter-email',
  budget: 8,
  submit: `(() => {
    const form = document.querySelector('form#newsletter-form');
    const input = document.querySelector('#newsletter-email');
    if (!form || !input) return { ok: false, why: 'footer form missing before submit' };
    input.value = 'layout-nl-' + Date.now() + '@example.com';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    return new Promise((resolve) => {
      const done = (ev) => resolve({
        ok: true,
        event: ev.type,
        href: location.pathname,
        navigated: location.pathname !== '/about',
        hasForm: !!document.querySelector('form#newsletter-form'),
        retry: (document.querySelector('#newsletter-notice') || {}).textContent || '',
        slot: (document.querySelector('.goen-footer__news') || {}).innerText || '',
        bodyStart: document.body.innerText.trim().slice(0, 80),
      });
      const t = setTimeout(() => done({ type: 'timeout' }), 8000);
      const wrap = (ev) => { clearTimeout(t); done(ev); };
      document.addEventListener('htmx:after:swap', wrap, { once: true });
      form.requestSubmit();
    });
  })()`,
});

await exhaustHtmx({
  label: 'contact 429',
  path: '/contact',
  form: 'form#contact-form',
  field: '#contact-email',
  budget: 8,
  submit: `(() => {
    const form = document.querySelector('form#contact-form');
    if (!form) return { ok: false, why: 'contact form missing before submit' };
    const set = (sel, value) => {
      const el = document.querySelector(sel);
      if (!el) return;
      el.value = value;
      el.dispatchEvent(new Event('input', { bubbles: true }));
    };
    set('#contact-name', '版面檢查');
    set('#contact-email', 'layout-contact-' + Date.now() + '@example.com');
    set('#contact-message', '想確認一下出貨時間,謝謝。');
    return new Promise((resolve) => {
      const done = (ev) => resolve({
        ok: true,
        event: ev.type,
        href: location.pathname,
        navigated: location.pathname !== '/contact',
        hasForm: !!document.querySelector('form#contact-form'),
        retry: (document.querySelector('.ui-alert--error .ui-alert__body') || {}).textContent || '',
        slot: (document.querySelector('.contact__layout') || {}).innerText || '',
        bodyStart: document.body.innerText.trim().slice(0, 80),
      });
      const t = setTimeout(() => done({ type: 'timeout' }), 8000);
      const wrap = (ev) => { clearTimeout(t); done(ev); };
      document.addEventListener('htmx:after:swap', wrap, { once: true });
      form.requestSubmit();
    });
  })()`,
});

const waitForHref = async (match, label) => {
  for (let i = 0; i < 50; i++) {
    const { result } = await send(ws, 'Runtime.evaluate', {
      expression: 'location.href', returnByValue: true,
    });
    const href = String(result.value || '');
    if (match(href)) {
      await new Promise((r) => setTimeout(r, 250));
      return href;
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  fail(label, 'navigation never reached the expected product URL after add-to-cart');
  return '';
};

const provePdpAdd = async (label, scriptingOff) => {
  await send(ws, 'Emulation.setScriptExecutionDisabled', { value: scriptingOff });
  await send(ws, 'Emulation.setDeviceMetricsOverride', {
    width: 375, height: 812, deviceScaleFactor: 1, mobile: true,
  });
  const slug = process.env.PDP_SLUG || 'nimbus-buds-pro';
  const start = `${ORIGIN}/p/${slug}`;
  await send(ws, 'Page.navigate', { url: start });
  await settled(ws, `${label} open`, start);

  const pick = await evalPage(`(() => {
    const form = document.querySelector('.goen-pdp__form');
    const add = form && form.querySelector('.goen-pdp__add');
    if (form && add && !add.disabled) return { ok: true, ready: true };
    const swatch = document.querySelector('.goen-swatch:not(.goen-swatch--on):not(.goen-swatch--out)');
    if (!swatch) return { ok: false, why: 'no choosable swatch on the pdp' };
    const href = swatch.getAttribute('href');
    if (!href) return { ok: false, why: 'swatch has no href' };
    return { ok: true, ready: false, href };
  })()`);
  if (pick.threw) {
    fail(label, `chooser probe did not run — ${pick.why}`);
    return;
  }
  if (!pick.ok) {
    fail(label, pick.why || 'could not prepare a variant on the pdp');
    return;
  }
  if (!pick.ready) {
    const chosen = ORIGIN + pick.href;
    await send(ws, 'Page.navigate', { url: chosen });
    await settled(ws, `${label} choose`, chosen);
  }

  // Press the button from where a person has to press it. Submitting from the
  // top of the page put the button 148px below the fold at 375, and then asking
  // whether the confirmation beside it was on screen measured nothing about the
  // confirmation: it measured whether anything had scrolled. Adding in place
  // scrolls nothing on purpose, so the button is brought into view first and
  // the assertions below keep their meaning under both mechanisms.
  const submit = await evalPage(`(() => {
    const form = document.querySelector('.goen-pdp__form');
    const add = form && form.querySelector('.goen-pdp__add');
    if (!form || !add || add.disabled) {
      return { ok: false, why: 'add-to-cart is not ready before submit' };
    }
    add.scrollIntoView({ block: 'center', behavior: 'instant' });
    // With script, press twice in the same tick: the second request queues
    // behind the first, and the first response swaps this form out of the page.
    // Exactly one POST may leave, or a double press adds the product twice.
    window.__pdpAddPosts = 0;
    if (!${scriptingOff}) {
      const originalFetch = window.fetch;
      window.fetch = (input, init) => {
        const method = String((init && init.method) || (input && input.method) || 'GET').toUpperCase();
        if (method === 'POST') window.__pdpAddPosts += 1;
        return originalFetch.call(window, input, init);
      };
      form.requestSubmit();
      form.requestSubmit();
    } else {
      form.requestSubmit();
    }
    return { ok: true };
  })()`);
  if (submit.threw || !submit.ok) {
    fail(label, submit.why || 'add-to-cart submit did not start');
    return;
  }

  // Without script the browser navigates: the address becomes this product with
  // added=added and #buybox aiming the landing. With script nothing navigates and
  // the address stays the product's own, with no outcome in it and no fragment:
  // ?added= says what one press did and is not a page to come back to.
  let landed;
  if (scriptingOff) {
    landed = await waitForHref(
      (href) => href.includes(`/p/${slug}`) && href.includes('added=added')
        && href.includes('?') && href.includes('#buybox'),
      label,
    );
  } else {
    let noticed = false;
    for (let i = 0; i < 50 && !noticed; i++) {
      const seen = await evalPage(`!!document.querySelector('.goen-pdp__added[role="status"]')`);
      noticed = seen === true;
      if (!noticed) await new Promise((r) => setTimeout(r, 100));
    }
    if (!noticed) {
      fail(label, 'the success notice never appeared after add-to-cart');
      return;
    }
    landed = await evalPage('location.href');
    if (typeof landed !== 'string' || !landed.includes(`/p/${slug}`)
      || landed.includes('added') || landed.includes('#')) {
      fail(label, `with script the address after add-to-cart is ${landed}, want the product's own address with no added and no fragment`);
      return;
    }
  }
  if (!landed) return;

  const got = await evalPage(`(() => {
    const add = document.querySelector('.goen-pdp__add');
    const notice = document.querySelector('.goen-pdp__added[role="status"]');
    const link = document.querySelector('.goen-pdp__addedlink');
    const addRect = add ? add.getBoundingClientRect() : null;
    const noticeRect = notice ? notice.getBoundingClientRect() : null;
    const viewport = window.innerHeight;
    return {
      href: location.href,
      addDisabled: !!(add && add.disabled),
      notice: notice ? notice.textContent.trim() : '',
      hasCartLink: !!(link && link.getAttribute('href') === '/cart'),
      noticeInView: !!(noticeRect && noticeRect.top >= 0 && noticeRect.bottom <= viewport + 1),
      noticeNearAdd: !!(addRect && noticeRect && noticeRect.top >= addRect.bottom - 2
        && noticeRect.top - addRect.bottom < 96),
    };
  })()`);
  if (got.threw) {
    fail(label, `post-add probe did not run — ${got.why}`);
    return;
  }
  if (!scriptingOff) {
    // Let a wrongly issued second request reach the wrapper before counting.
    await new Promise((r) => setTimeout(r, 500));
    const posts = await evalPage('window.__pdpAddPosts');
    if (posts.threw || posts !== 1) {
      fail(label, `two quick add-to-cart presses sent ${posts.threw ? posts.why : posts} POSTs, want exactly 1`);
    }
  }
  if (got.addDisabled) {
    fail(label, 'add-to-cart is disabled after a successful add');
  }
  if (!got.notice) {
    fail(label, 'no success notice rendered after add-to-cart');
  }
  if (!got.hasCartLink) {
    fail(label, 'the success notice has no /cart link');
  }
  if (!got.noticeNearAdd) {
    fail(label, 'the success notice is not adjacent to the add button at 375px');
  }
  if (!got.noticeInView) {
    fail(label, 'the success notice is not fully inside the 375px viewport');
  }

  const cartCount = () => `(() => {
    const badge = document.querySelector('.goen-header__cart .ui-badge--count');
    return badge ? parseInt(badge.textContent.trim(), 10) : 0;
  })()`;

  const before = await evalPage(`({
    notices: document.querySelectorAll('.goen-pdp__added').length,
    cartUnits: ${cartCount()},
    documentStarted: performance.timeOrigin,
  })`);
  if (before.threw) {
    fail(label, `pre-refresh probe did not run — ${before.why}`);
    return;
  }
  if (before.cartUnits < 1) {
    fail(label, 'the header cart badge did not reflect the add-to-cart write');
  }

  await send(ws, 'Page.reload', { ignoreCache: false });
  let refreshed = false;
  for (let i = 0; i < 50; i++) {
    const ready = await evalPage(`({
      complete: document.readyState === 'complete',
      documentStarted: performance.timeOrigin,
      href: location.href,
    })`);
    if (!ready.threw && ready.complete && ready.documentStarted !== before.documentStarted
      && ready.href === landed) {
      refreshed = true;
      break;
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  if (!refreshed) {
    fail(label, 'refresh did not complete in a new product document');
    return;
  }
  const after = await evalPage(`({
    href: location.href,
    notices: document.querySelectorAll('.goen-pdp__added').length,
    cartUnits: ${cartCount()},
    addDisabled: !!(document.querySelector('.goen-pdp__add') && document.querySelector('.goen-pdp__add').disabled),
  })`);
  if (after.threw) {
    fail(label, `refresh probe did not run — ${after.why}`);
    return;
  }
  if (after.addDisabled) {
    fail(label, 'refresh left add-to-cart disabled');
  }
  if (scriptingOff && after.notices < before.notices) {
    fail(label, 'refresh dropped the add-to-cart notice');
  }
  if (!scriptingOff && after.notices !== 0) {
    fail(label, 'refreshing the product address showed an add-to-cart notice that the address does not carry');
  }
  if (after.cartUnits !== before.cartUnits) {
    fail(label, `refresh changed the cart unit count from ${before.cartUnits} to ${after.cartUnits}`);
  }
  console.log(`${label.padEnd(24)} scripting=${scriptingOff ? 'off' : 'on'} ` +
    `noticeInView=${got.noticeInView} noticeNearAdd=${got.noticeNearAdd} cartLink=${got.hasCartLink}`);
};

await provePdpAdd('pdp add 375 off', true);
await provePdpAdd('pdp add 375 on', false);
await send(ws, 'Emulation.setScriptExecutionDisabled', { value: false });

// Choosing a colour whose photograph is tagged puts that photograph first. With
// script the gallery rides the buy column's swap; with none the navigation
// renders it. Both start from the page's default state and press the swatch
// through the input pipeline, so a swatch something else covers fails here.
// The scripted pass then presses the other colour from the column the swap
// brought in, because a control a swap delivers has to work as well as the one
// the page loaded with.
const COLOUR_SLUG = process.env.COLOUR_SLUG || '';
const COLOUR_VALUE = process.env.COLOUR_VALUE || '';
const COLOUR_KEY = process.env.COLOUR_KEY || '';

// A press while a swap's cross-fade runs lands on the view transition's overlay
// and not on the page, so with script the previous transition finishes first.
// Without script nothing swaps, and nothing here may wait on a page callback.
const pressMarked = async (afterTransition) => {
  if (afterTransition) {
    const idle = await evalPage(`(async () => {
      const fading = () => document.getAnimations().filter((a) =>
        String((a.effect && a.effect.pseudoElement) || '').startsWith('::view-transition'));
      if (document.activeViewTransition) await document.activeViewTransition.finished.catch(() => {});
      await Promise.all(fading().map((a) => a.finished.catch(() => {})));
      await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
      return true;
    })()`);
    if (idle !== true) return false;
  }
  const at = await evalPage(`(() => {
    const el = document.querySelector('[data-layout-press]');
    if (!el) return { ok: false };
    el.scrollIntoView({ block: 'center', behavior: 'instant' });
    const r = el.getBoundingClientRect();
    return { ok: true, x: r.left + r.width / 2, y: r.top + r.height / 2 };
  })()`);
  if (at.threw || !at.ok) return false;
  for (const type of ['mousePressed', 'mouseReleased']) {
    await send(ws, 'Input.dispatchMouseEvent', { type, x: at.x, y: at.y, button: 'left', clickCount: 1 });
  }
  return true;
};

const galleryState = `(() => {
  const galleries = document.querySelectorAll('#gallery');
  const main = document.querySelector('#gallery img');
  return {
    galleries: galleries.length,
    src: main ? main.getAttribute('src') : '',
    values: [...new URL(location.href).searchParams.values()],
    documentStarted: performance.timeOrigin,
  };
})()`;

const waitForGallery = async (want) => {
  for (let i = 0; i < 50; i++) {
    const got = await evalPage(galleryState);
    if (!got.threw && want(got)) return got;
    await new Promise((r) => setTimeout(r, 100));
  }
  return evalPage(galleryState);
};

const provePdpColourPhoto = async (label, scriptingOff) => {
  if (!COLOUR_SLUG || !COLOUR_VALUE || !COLOUR_KEY) {
    fail(label, 'COLOUR_SLUG, COLOUR_VALUE and COLOUR_KEY are unset — run it through make check-layout, which names a photograph the seed tags');
    return;
  }
  await send(ws, 'Emulation.setScriptExecutionDisabled', { value: scriptingOff });
  await send(ws, 'Emulation.setDeviceMetricsOverride', {
    width: 375, height: 812, deviceScaleFactor: 1, mobile: true,
  });
  const start = `${ORIGIN}/p/${COLOUR_SLUG}`;
  await send(ws, 'Page.navigate', { url: start });
  await settled(ws, `${label} open`, start);

  const before = await evalPage(galleryState);
  if (before.threw) {
    fail(label, `gallery probe did not run — ${before.why}`);
    return;
  }
  if (!before.src || before.src.includes(`/${COLOUR_KEY}`)) {
    fail(label, `the default page opens on ${before.src || 'no photograph'}, so choosing ${COLOUR_VALUE} can show no change`);
    return;
  }

  const marked = await evalPage(`(() => {
    const want = ${JSON.stringify(COLOUR_VALUE)};
    const swatch = [...document.querySelectorAll('#buybox a.goen-swatch')]
      .find((a) => [...new URL(a.href).searchParams.values()].includes(want));
    if (!swatch) return false;
    swatch.setAttribute('data-layout-press', '');
    return true;
  })()`);
  if (marked !== true || !(await pressMarked(!scriptingOff))) {
    fail(label, `no swatch on the default page chooses ${COLOUR_VALUE}`);
    return;
  }
  const chosen = await waitForGallery((g) => g.values.includes(COLOUR_VALUE) && g.src.includes(`/${COLOUR_KEY}`));
  if (chosen.threw || !chosen.src.includes(`/${COLOUR_KEY}`)) {
    fail(label, `choosing ${COLOUR_VALUE} left the gallery opening on ${chosen.src || 'nothing'}, want ${COLOUR_KEY}`);
    return;
  }
  if (!chosen.values.includes(COLOUR_VALUE)) {
    fail(label, `the address does not carry ${COLOUR_VALUE} after choosing it`);
  }
  if (chosen.galleries !== 1) {
    fail(label, `the page holds ${chosen.galleries} #gallery elements after choosing, want 1`);
  }
  // In place with script, a new document without: the two paths are what make
  // the swatch work either way, and each must be the one that ran.
  if ((chosen.documentStarted !== before.documentStarted) !== scriptingOff) {
    fail(label, scriptingOff
      ? 'choosing a colour with scripting off did not load a new page'
      : 'choosing a colour with script loaded a new page instead of swapping in place');
  }

  if (!scriptingOff) {
    const other = await evalPage(`(() => {
      const on = document.querySelector('#buybox a.goen-swatch--on');
      const box = on && on.closest('fieldset');
      const next = box && box.querySelector('a.goen-swatch:not(.goen-swatch--on)');
      if (!next) return false;
      next.setAttribute('data-layout-press', '');
      return true;
    })()`);
    if (other !== true || !(await pressMarked(true))) {
      fail(label, 'the swapped buy column offers no other colour to choose');
      return;
    }
    const back = await waitForGallery((g) => !g.values.includes(COLOUR_VALUE) && g.src === before.src);
    if (!back.threw && back.documentStarted !== before.documentStarted) {
      fail(label, 'the swatch the swap delivered loaded a new page instead of swapping in place');
    }
    if (back.threw || back.src !== before.src) {
      fail(label, `choosing the other colour from the swapped column opens on ${back.src || 'nothing'}, ` +
        `want ${before.src} (address carries ${JSON.stringify(back.values || [])})`);
    } else if (back.galleries !== 1) {
      fail(label, `the page holds ${back.galleries} #gallery elements after choosing back, want 1`);
    }
  }
  console.log(`${label.padEnd(24)} scripting=${scriptingOff ? 'off' : 'on'} opens=${before.src} chosen=${chosen.src}`);
};

await provePdpColourPhoto('pdp colour photo 375 off', true);
await provePdpColourPhoto('pdp colour photo 375 on', false);
await send(ws, 'Emulation.setScriptExecutionDisabled', { value: false });

// The product page's related products are two across below 1024, wherever the
// grid's gap changes. A card narrower than 40% of the row, or a second card on
// a line of its own, is the single column the track width falls to when it
// halves a gap the grid no longer has.
const RELATED_ROW_PROBE = `(() => {
  const grid = document.querySelector('.goen-pdp__related .goen-tiles__grid');
  if (!grid) return { ok: false, why: 'the product page has no related products to measure' };
  const cards = [...grid.children].map((c) => {
    const r = c.getBoundingClientRect();
    return { top: r.top, width: r.width };
  });
  if (cards.length < 2) return { ok: false, why: 'fewer than two related cards' };
  return { ok: true, row: grid.getBoundingClientRect().width, a: cards[0], b: cards[1] };
})()`;

for (const width of [375, 768, 1023]) {
  const label = `related ${width}`;
  await send(ws, 'Emulation.setDeviceMetricsOverride', { width, height: 1024, deviceScaleFactor: 1, mobile: false });
  const target = ORIGIN + '/p/' + (process.env.PRODUCT_SLUG || '');
  await send(ws, 'Page.navigate', { url: target });
  await settled(ws, label, target);
  const got = await evalPage(RELATED_ROW_PROBE);
  if (got.threw || !got.ok) {
    fail(label, `the probe did not run: ${got.why}`);
    continue;
  }
  if (![got.row, got.a.top, got.a.width, got.b.top, got.b.width].every(Number.isFinite)) {
    fail(label, 'the related cards were not measured');
    continue;
  }
  if (Math.abs(got.a.top - got.b.top) > 1) {
    fail(label, `the second related card starts ${Math.round(got.b.top - got.a.top)}px below the first, want one row of two`);
  }
  for (const [name, card] of [['first', got.a], ['second', got.b]]) {
    if (card.width <= got.row * 0.4) {
      fail(label, `the ${name} related card is ${Math.round(card.width)}px of a ${Math.round(got.row)}px row, want more than 40%`);
    }
  }
  console.log(`${label.padEnd(24)} cards=${Math.round(got.a.width)}+${Math.round(got.b.width)} of ${Math.round(got.row)}px`);
}

// axe-core, once per route.
//
// A separate pass rather than a call inside settled(), and that is deliberate:
// the journeys above spend a live rate limiter and compare timestamps across a
// reload, and a second or two of audit inserted between their requests would
// change what they measure. Running afterwards costs one extra navigation per
// route and changes nothing any other assertion sees.
const AXE_RUN = `axe.run(document, ${JSON.stringify(AXE_OPTIONS)}).then((r) => {
  const finding = (v) => ({
    id: v.id, tags: v.tags, impact: v.impact, help: v.help,
    target: v.nodes[0] && v.nodes[0].target ? String(v.nodes[0].target[0]) : '(no node)',
    count: v.nodes.length,
    targets: v.nodes.flatMap((node) => (node.target || []).map(String)),
  });
  return JSON.stringify({
    version: axe.version,
    rules: axe.getRules(${JSON.stringify(WCAG_TAGS)}).map((v) => ({
      id: v.ruleId, tags: v.tags,
    })),
    executed: [...new Set(['passes', 'violations', 'incomplete', 'inapplicable']
      .flatMap((kind) => r[kind].map((v) => v.id)))].sort(),
    violations: r.violations.map(finding),
    incomplete: r.incomplete.map(finding),
  });
})`;

// A moderate or minor finding is reported and does not gate. ::warning is what
// puts it on the pull request's Files view; outside Actions it is a plain line.
const annotate = (msg) => console.log(
  process.env.GITHUB_ACTIONS ? `::warning title=axe-core::${msg}` : `  warning: ${msg}`);

// Where the browser actually ends up, which is not always where it was sent.
//
// A signed-in visitor can be sent away from a page, which settled()'s
// href === url would report as a page that never loaded. about:blank
// first, so a document that is still the PREVIOUS page cannot be mistaken for
// this one, and then whatever the browser landed on.
//
// It reports rather than exits, unlike settled(), because this pass runs last:
// an exit here would throw away the failure list everything above built.
const settledFor = async (pass, route, url) => {
  await send(ws, 'Page.navigate', { url: 'about:blank' });
  for (let i = 0; i < 30; i++) {
    const { result } = await send(ws, 'Runtime.evaluate', {
      expression: 'location.href', returnByValue: true,
    });
    if (String(result.value) === 'about:blank') break;
    await new Promise((r) => setTimeout(r, 50));
  }

  await send(ws, 'Page.navigate', { url });
  for (let i = 0; i < 100; i++) {
    const { result } = await send(ws, 'Runtime.evaluate', {
      expression: 'document.readyState + " " + location.href', returnByValue: true,
    });
    const [state, href] = String(result.value).split(' ');
    if (state === 'complete' && href !== 'about:blank') {
      await new Promise((r) => setTimeout(r, 150));
      return href;
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  fail(`${pass} ${route}`, 'the page never finished loading');
  return '';
};

const SIGNED_OUT_ROUTES = new Set(['/signin', '/register', '/forgot']);
// Adjacent undersized targets distinguish the WCAG 2.2 selection from a 2.0 run.
const TARGET_SIZE_FIXTURE = `(() => {
  const group = document.createElement('div');
  group.id = 'axe-target-fixture';
  group.style.cssText = 'position:fixed;top:0;left:0;display:flex;gap:0;z-index:2147483647';
  for (const name of ['one', 'two']) {
    const button = document.createElement('button');
    button.id = 'axe-target-' + name;
    button.type = 'button';
    button.textContent = 'Fixture ' + name;
    button.style.cssText = 'width:10px;height:10px;min-width:0;min-height:0;padding:0;margin:0;border:0;box-sizing:border-box;overflow:hidden';
    group.append(button);
  }
  document.body.append(group);
  return true;
})()`;

const proveTargetSizeGates = async () => {
  await send(ws, 'Emulation.setDeviceMetricsOverride', {
    width: AXE_WIDTH.width, height: AXE_WIDTH.height, deviceScaleFactor: 1, mobile: false,
  });
  if (!(await settledFor('axe', 'target fixture', `${ORIGIN}/about`))) return;
  try {
    await send(ws, 'Runtime.evaluate', { expression: axeSource });
    await evalPage(TARGET_SIZE_FIXTURE);
    const rejectsFixture = (audit) => audit.violations.some((v) => v.id === 'target-size'
      && v.targets.some((target) => target.includes('axe-target-')) && gatesAccessibility(v));
    const small = JSON.parse(await evalPage(AXE_RUN));
    if (!rejectsFixture(small)) {
      fail('axe target fixture', 'the WCAG 2.2 gate did not reject adjacent 10px targets');
      return;
    }
    await evalPage(`(() => {
      const group = document.getElementById('axe-target-fixture');
      group.style.gap = '24px';
      for (const button of group.children) {
        button.style.width = '24px';
        button.style.height = '24px';
      }
    })()`);
    const fixed = JSON.parse(await evalPage(AXE_RUN));
    if (rejectsFixture(fixed)) {
      fail('axe target fixture', 'the restored 24px targets still fail the target-size gate');
      return;
    }
    console.log('axe WCAG 2.2 target fixture: undersized targets rejected; restored targets passed');
  } catch (err) {
    fail('axe target fixture', `the browser proof did not complete — ${err.message}`);
  } finally {
    await evalPage("document.getElementById('axe-target-fixture')?.remove()");
  }
};

const auditAccessibility = async () => {
  await send(ws, 'Emulation.setDeviceMetricsOverride', {
    width: AXE_WIDTH.width, height: AXE_WIDTH.height, deviceScaleFactor: 1, mobile: false,
  });

  // The auth pages answer a signed-in visitor with a redirect to /account, so
  // they are audited first with the session cookie taken off and it is put back
  // before the first page that needs it.
  const signedOutOnly = ([asked]) => SIGNED_OUT_ROUTES.has(asked);
  const visits = [...visited.entries()];
  const requested = [...visits.filter(signedOutOnly), ...visits.filter((v) => !signedOutOnly(v))];
  console.log(`\naxe-core ${WCAG_LEVEL} (${WCAG_TAGS.join(" + ")}), ${requested.length} routes at ${AXE_WIDTH.width}px; best-practice advisory`);

  const { cookies } = await send(ws, 'Network.getCookies', { urls: [ORIGIN] });
  const session = cookies.find((c) => c.name === 'goen_session');
  if (session) {
    await send(ws, 'Network.deleteCookies', { name: session.name, domain: session.domain, path: session.path });
  }
  let sessionRestored = !session;

  const observed = {};
  const unaudited = [];
  const audited = new Set();
  let debtMoved = false;
  let selectedRules = null;
  const executedRules = new Set();

  for (const [asked, url] of requested) {
    if (!sessionRestored && !SIGNED_OUT_ROUTES.has(asked)) {
      await send(ws, 'Network.setCookie', {
        name: session.name, value: session.value, domain: session.domain, path: session.path,
      });
      sessionRestored = true;
    }
    const landed = await settledFor('axe', asked, url);
    if (!landed) {
      unaudited.push(asked);
      continue;
    }
    // The page that was served, which is what the baseline has to name: a
    // redirected route audited under the name it was asked for would record
    // one page's debt against another's.
    const route = routeOf(landed);
    if (audited.has(route)) {
      console.log(`axe ${asked.padEnd(46).slice(0, 46)} -> ${route}, already audited`);
      continue;
    }
    audited.add(route);

    let audit;
    try {
      const injected = await send(ws, 'Runtime.evaluate', {
        expression: axeSource, includeCommandLineAPI: true,
      });
      if (injected.exceptionDetails) {
        unaudited.push(route);
        fail(`axe ${route}`, 'axe-core did not load — ' +
          (injected.exceptionDetails.exception?.description || 'no exception detail'));
        continue;
      }
      const evaluated = await send(ws, 'Runtime.evaluate', {
        expression: AXE_RUN, awaitPromise: true, returnByValue: true,
      }, 120000);
      if (evaluated.exceptionDetails || typeof evaluated.result?.value !== 'string') {
        unaudited.push(route);
        fail(`axe ${route}`, 'axe.run did not return a result — ' +
          (evaluated.exceptionDetails?.exception?.description
            || JSON.stringify(evaluated).slice(0, 300)));
        continue;
      }
      audit = JSON.parse(evaluated.result.value);
    } catch (err) {
      unaudited.push(route);
      fail(`axe ${route}`, `the audit did not complete — ${err.message}`);
      continue;
    }

    if (selectedRules === null) {
      selectedRules = audit.rules.filter((rule) => !wcagRuleExclusion(rule)).map((rule) => rule.id).sort();
      const excluded = audit.rules.filter((rule) => wcagRuleExclusion(rule)).map((rule) => ({
        id: rule.id, reason: wcagRuleExclusion(rule),
      }));
      console.log(`axe-core ${audit.version} excluded WCAG rules: ${JSON.stringify(excluded)}`);
    }
    for (const id of audit.executed) executedRules.add(id);
    for (const v of audit.incomplete) {
      annotate(`${route}: manual review needed for ${v.id} — ${v.help}; first: ${v.target}`);
    }
    const violations = audit.violations;
    const known = axeBaseline[route] || [];
    const gating = new Set();
    for (const v of violations) {
      const detail = `${v.id} (${v.impact}) — ${v.help}; first: ${v.target}` +
        (v.count > 1 ? ` (and ${v.count - 1} more on this page)` : '');
      if (!gatesAccessibility(v)) {
        annotate(`${route}: ${detail}`);
        continue;
      }
      gating.add(v.id);
      if (known.includes(v.id)) continue;
      debtMoved = true;
      fail(`axe ${route}`, detail);
    }
    observed[route] = [...gating].sort();

    // A baseline entry that stopped firing is removed by the change that fixed
    // it, not by the next person to read a stale file. Only routes this run
    // actually audited are judged, so a skipped back office cannot delete the
    // debt recorded for it.
    for (const id of known) {
      if (observed[route].includes(id)) continue;
      debtMoved = true;
      fail(`axe ${route}`, `the baseline lists ${id}, which no longer fires here — remove it`);
    }

    console.log(`axe ${route.padEnd(46).slice(0, 46)} ` +
      `violations=${violations.length} incomplete=${audit.incomplete.length} gating=${observed[route].length}`);
  }

  if (selectedRules !== null) {
    const executed = selectedRules.filter((id) => executedRules.has(id));
    const neverRan = selectedRules.filter((id) => !executedRules.has(id));
    console.log(`axe executed WCAG rules (${executed.length}): ${executed.join(', ')}`);
    console.log(`axe selected WCAG rules that never ran: ${neverRan.join(', ') || '(none)'}`);
  }

  if (!debtMoved) return;

  // The exact file that makes this run green, so accepting a finding is a
  // review of these lines rather than a second run to collect them.
  const merged = { ...axeBaseline };
  for (const [route, ids] of Object.entries(observed)) {
    if (ids.length) merged[route] = ids;
    else delete merged[route];
  }
  const ordered = {};
  for (const route of Object.keys(merged).sort()) ordered[route] = merged[route];
  console.log('::group::axe baseline candidate — scripts/axe-baseline.json');
  // A route whose audit crashed keeps whatever the baseline already said about
  // it and contributes nothing new, so a candidate collected over one is
  // incomplete. Say which, rather than let it be pasted as if it were whole.
  if (unaudited.length) {
    console.log(`INCOMPLETE — these routes were not audited: ${unaudited.join(', ')}`);
  }
  console.log(JSON.stringify({ ...axeBaselineFile, routes: ordered }, null, 2));
  console.log('::endgroup::');
};

// WCAG 1.4.4 (text at 200%) and 1.4.10 (reflow at 320px), on every route this
// run visited.
//
// Each route is loaded at 320 x 800 and measured, then its root font is set to
// 200% and it is measured again. Per width a route can fail two checks:
//   scroll  the page scrolls sideways. body.scrollWidth is compared with the 320
//           that was asked for and not with innerWidth, because phone emulation
//           widens innerWidth to fit the content; after scrollTo(10000, 0) a
//           scrollX other than 0 settles any disagreement.
//   text    one entry per owning element (text-320:a.goen-footer__link): a
//           run of text is cut by the clip of an ancestor that does not
//           scroll, or runs past the right edge of the viewport. This is what
//           neither width above sees: a position:fixed element wider than the
//           screen, and text cut by overflow:hidden.
//
// Inside a scroll container (overflow auto | scroll) text is reachable, so it
// is not judged. A run wholly outside a clip is a panel parked out of view (a
// carousel's other slides), so only a run the clip or the edge cuts through
// counts. text-overflow: ellipsis and -webkit-line-clamp cut on purpose and
// mark the cut.
const REFLOW_WIDTH = 320;
const REFLOW_PROBE = `(async () => {
  await document.fonts.ready;
  await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
  const width = ${REFLOW_WIDTH};
  const scrolls = (o) => o === 'auto' || o === 'scroll';
  const clips = (o) => o === 'hidden' || o === 'clip';
  const cuts = (lo, hi, boxLo, boxHi) => lo < boxHi && hi > boxLo && (lo < boxLo - 0.5 || hi > boxHi + 0.5);
  const text = [];
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  const range = document.createRange();
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    if (!node.nodeValue.trim()) continue;
    const owner = node.parentElement;
    if (!owner || owner.closest('script, style, noscript, template, option')) continue;
    if (getComputedStyle(owner).visibility !== 'visible') continue;
    range.selectNodeContents(node);
    const rects = [...range.getClientRects()].filter((r) => r.width > 0 && r.height > 0);
    if (!rects.length) continue;
    const left = Math.min(...rects.map((r) => r.left));
    const right = Math.max(...rects.map((r) => r.right));
    const top = Math.min(...rects.map((r) => r.top));
    const bottom = Math.max(...rects.map((r) => r.bottom));
    let xFree = false;
    let yFree = false;
    let cause = '';
    for (let a = owner; a && a !== document.body && a !== document.documentElement && !cause; a = a.parentElement) {
      const cs = getComputedStyle(a);
      const box = a.getBoundingClientRect();
      if (!xFree) {
        if (scrolls(cs.overflowX)) xFree = true;
        else if (clips(cs.overflowX)) {
          if (box.width <= 1 || cs.textOverflow === 'ellipsis') xFree = true;
          else if (cuts(left, right, box.left, box.right)) cause = 'cut by the clip of';
        }
      }
      if (!yFree && !cause) {
        if (scrolls(cs.overflowY)) yFree = true;
        else if (clips(cs.overflowY)) {
          if (box.height <= 1 || (cs.webkitLineClamp && cs.webkitLineClamp !== 'none')) yFree = true;
          else if (cuts(top, bottom, box.top, box.bottom)) cause = 'cut by the clip of';
        }
      }
      if (cs.position === 'fixed') break;
    }
    if (!cause && !xFree && cuts(left, right, 0, width)) cause = 'runs past the viewport in';
    if (cause) {
      // The attribute, not className: on an SVG element className is an SVGAnimatedString.
      const where = owner.tagName.toLowerCase() + '.' + (owner.getAttribute('class') || '').split(' ')[0];
      text.push({ owner: where, detail: cause + ' ' + JSON.stringify(node.nodeValue.trim().slice(0, 24)) });
    }
  }
  const scrollWidth = document.body.scrollWidth;
  window.scrollTo(10000, 0);
  const scrollX = window.scrollX;
  window.scrollTo(0, 0);
  return { scrollWidth, scrollX, text };
})()`;

const auditReflow = async () => {
  await send(ws, 'Emulation.setDeviceMetricsOverride', {
    width: REFLOW_WIDTH, height: 800, deviceScaleFactor: 1, mobile: true,
  });

  // The auth pages answer a signed-in visitor with a redirect to /account, so
  // they are measured first with the session cookie taken off, as the axe pass
  // does.
  const signedOutOnly = ([asked]) => SIGNED_OUT_ROUTES.has(asked);
  const visits = [...visited.entries()];
  const requested = [...visits.filter(signedOutOnly), ...visits.filter((v) => !signedOutOnly(v))];
  console.log(`\nreflow: ${requested.length} routes at ${REFLOW_WIDTH}px and at 200% text`);

  const { cookies } = await send(ws, 'Network.getCookies', { urls: [ORIGIN] });
  const session = cookies.find((c) => c.name === 'goen_session');
  if (session) {
    await send(ws, 'Network.deleteCookies', { name: session.name, domain: session.domain, path: session.path });
  }
  let sessionRestored = !session;

  const observed = {};
  const measured = new Set();
  const unmeasured = [];
  let debtMoved = false;

  const measure = async (zoom) => {
    const evaluated = await send(ws, 'Runtime.evaluate', {
      expression: REFLOW_PROBE, awaitPromise: true, returnByValue: true,
    }, 60000);
    if (evaluated.exceptionDetails || !evaluated.result || !evaluated.result.value) {
      throw new Error(evaluated.exceptionDetails?.exception?.description || JSON.stringify(evaluated).slice(0, 300));
    }
    const got = evaluated.result.value;
    const found = {};
    if (got.scrollWidth > REFLOW_WIDTH || got.scrollX !== 0) {
      found[`scroll-${zoom}`] = `scrollWidth ${got.scrollWidth} > ${REFLOW_WIDTH}, scrollX ${got.scrollX} after scrollTo`;
    }
    // One entry per owner, so a baseline that lists a route's footer link does
    // not accept a new cut elsewhere on it.
    for (const run of got.text) {
      const key = `text-${zoom}:${run.owner}`;
      found[key] = found[key] ? found[key] + `; ${run.detail}` : run.detail;
    }
    return found;
  };

  for (const [asked, url] of requested) {
    if (!sessionRestored && !SIGNED_OUT_ROUTES.has(asked)) {
      await send(ws, 'Network.setCookie', {
        name: session.name, value: session.value, domain: session.domain, path: session.path,
      });
      sessionRestored = true;
    }
    const landed = await settledFor('reflow', asked, url);
    if (!landed) {
      unmeasured.push(asked);
      continue;
    }
    const route = routeOf(landed);
    if (measured.has(route)) continue;
    measured.add(route);

    const found = {};
    try {
      Object.assign(found, await measure('320'));
      await send(ws, 'Runtime.evaluate', { expression: `document.documentElement.style.fontSize = '200%'` });
      Object.assign(found, await measure('200'));
    } catch (err) {
      unmeasured.push(route);
      fail(`reflow ${route}`, `the measurement did not complete — ${err.message}`);
      continue;
    }

    const known = reflowBaseline[route] || [];
    for (const [check, detail] of Object.entries(found)) {
      if (known.includes(check)) continue;
      debtMoved = true;
      fail(`reflow ${route}`, `${check}: ${detail}`);
    }
    // A listed check that stopped firing is removed by the change that fixed it,
    // so this file can only shrink.
    for (const check of known) {
      if (check in found) continue;
      debtMoved = true;
      fail(`reflow ${route}`, `the baseline lists ${check}, which no longer fires here — remove it`);
    }
    observed[route] = Object.keys(found).sort();
    console.log(`reflow ${route.padEnd(46).slice(0, 46)} failing=${observed[route].join(',') || '-'}`);
  }

  if (!debtMoved) return;

  const merged = { ...reflowBaseline };
  for (const [route, checks] of Object.entries(observed)) {
    if (checks.length) merged[route] = checks;
    else delete merged[route];
  }
  const ordered = {};
  for (const route of Object.keys(merged).sort()) ordered[route] = merged[route];
  console.log('::group::reflow baseline candidate — scripts/reflow-baseline.json');
  if (unmeasured.length) {
    console.log(`INCOMPLETE — these routes were not measured: ${unmeasured.join(', ')}`);
  }
  console.log(JSON.stringify({ ...reflowBaselineFile, routes: ordered }, null, 2));
  console.log('::endgroup::');
};

await proveTargetSizeGates();
await auditAccessibility();
await auditReflow();

ws.close();

if (failures.length) {
  console.error(`\nlayout check FAILED (${failures.length}):`);
  for (const f of failures) console.error(`  - ${f}`);
  process.exit(1);
}
console.log('\nlayout check PASS');
