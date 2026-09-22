// Layout conformance for the storefront, measured over the Chrome DevTools
// Protocol.
//
// Why this exists as a driven browser rather than a screenshot: Chrome on macOS
// refuses to open a window narrower than ~500px, so `--window-size=375` renders
// at 500 and crops. Overflow it shows is fake and overflow it hides is missed.
// Emulation.setDeviceMetricsOverride sets the layout viewport for real, and
// every assertion below reads a box off the live layout.
//
// It found its first defect before it was committed: at 375 the page scrolled
// horizontally to 410px, because .ui-price is a nowrap row whose sale pair
// exceeds the column and a bare 1fr grid track will not shrink below its
// content. That is the class of bug this guards.
//
// Usage: make check-layout   (needs Chrome and a server on GOEN_URL)

import { readFileSync } from 'node:fs';

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
const AXE_SOURCE = process.env.AXE_SOURCE || '.layout-chrome/axe.min.js';
const AXE_BASELINE = process.env.AXE_BASELINE || 'scripts/axe-baseline.json';

// The impacts that fail the run. moderate and minor are printed as annotations:
// they are real and they are not "this page is unusable for somebody", and a
// gate that fails on all four would be turned off within a week.
const AXE_GATES = new Set(['serious', 'critical']);

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

// What the two artboards fold into. Column counts are read off the rendered
// boxes — how many children share the top row — not off the CSS, so a rule that
// stops applying is caught rather than a rule that stops existing.
const EXPECTED = [
  { label: '375 (artboard)', width: 375, height: 812, cats: 3, tiles: 2, hero: 'stacked' },
  { label: '768 (md)', width: 768, height: 1024, cats: 3, tiles: 3, hero: 'stacked' },
  { label: '1024 (lg)', width: 1024, height: 900, cats: 6, tiles: 4, hero: 'side-by-side' },
  { label: '1440 (artboard)', width: 1440, height: 900, cats: 6, tiles: 4, hero: 'side-by-side' },
];

// Cart and checkout. They need a cart to exist, so the Makefile adds one
// through the site's own POST rather than reaching into the database — if
// add-to-cart breaks, this check fails too, which is correct.
// Every page that renders a document, at a phone width and at the artboard.
//
// Ten of the site's fifty-three page routes had a row here; the rest had never
// been measured in a browser at all, which is the only way an overflow or a
// 30px tap target is found. The back-office ones matter most: their tables are
// deliberately wider than a phone and scroll inside their own box, and nothing
// but this says whether the PAGE stayed put.
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
// wrapper measures chrome and calls the table covered (CLAUDE.md #24).
// COMPARE_SLUG_B is a second active seed product. table: true is what says
// this row measured columns, the sticky first cell, and (at 375) overflow
// inside the scroll box — not merely that a marker existed.
//
// The one-product empty state is a different page. It cannot stand in for
// the table.
const COMPARE = [
  { label: 'compare 375', width: 375, height: 812, path: '/compare?p=PRODUCT_SLUG&p=COMPARE_SLUG_B', marker: '.goen-compare__table', table: true },
  { label: 'compare 1440', width: 1440, height: 900, path: '/compare?p=PRODUCT_SLUG&p=COMPARE_SLUG_B', marker: '.goen-compare__table', table: true },
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
  // method measures only half the page. PICKUP_SHIP is the version id the
  // Makefile reads from the database, and the marker insists the chain
  // chooser is there.
  { label: 'pickup 375', width: 375, height: 812, path: '/checkout?ship=PICKUP_SHIP', marker: 'input[name=pickup_brand]' },
  { label: 'pickup 1440', width: 1440, height: 900, path: '/checkout?ship=PICKUP_SHIP', marker: 'input[name=pickup_brand]' },
  // The payment page. PLACED_ORDER is the NUMBER of the order the Makefile just
  // placed; PLACED_TOKEN, set as a cookie above, is the browser's proof that it
  // placed it. Two facts, two variables — without either the page is the 404 a
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

// The listing page. Its filter rail sits beside the grid from lg and above it
// below, which is the one thing its fold decides — asserted by comparing the
// rail's top against the results', the same way the hero's split is read.
const LISTING = [
  { label: 'listing 375', width: 375, height: 812, rail: 'stacked' },
  { label: 'listing 768', width: 768, height: 1024, rail: 'stacked' },
  { label: 'listing 1024', width: 1024, height: 900, rail: 'beside' },
  { label: 'listing 1440', width: 1440, height: 900, rail: 'beside' },
];

// English category names in the desktop header compete with the search field.
// A row that never sets goen_locale and never asks the field's width cannot
// see a crush. Home and listing at 1024/1440 are the surfaces that show the bar.
const HEADER_EN = [
  { label: 'header en 1024', width: 1024, height: 900, path: '/' },
  { label: 'header en 1440', width: 1440, height: 900, path: '/' },
  { label: 'listing header en 1024', width: 1024, height: 900, path: '/c/phones' },
  { label: 'listing header en 1440', width: 1440, height: 900, path: '/c/phones' },
];

// The back office. Needs a staff session, which the Makefile provides through
// ADMIN_TOKEN; without one these are skipped rather than silently measuring a
// sign-in page.
const ADMIN = [
  { label: 'admin 375', width: 375, height: 812, path: '/admin' },
  { label: 'admin 1440', width: 1440, height: 900, path: '/admin' },
  { label: 'admin stock 375', width: 375, height: 812, path: '/admin/stock' },
  { label: 'admin orders 375', width: 375, height: 812, path: '/admin/orders' },
  // The rest of the back office. Four of its fifteen pages were measured; the
  // others are the ones with the widest tables.
  { label: 'admin products 375', width: 375, height: 812, path: '/admin/products', marker: '.goen-admin' },
  { label: 'admin products 1440', width: 1440, height: 900, path: '/admin/products', marker: '.goen-admin' },
  // The product EDIT page, which had never been rendered in a browser at any
  // width — the busiest form in the back office, and now the one carrying the
  // 規格表 table. The marker is that table: PRODUCT_SLUG comes from the seed and
  // has three specs, so a row that measured the page without it would be
  // measuring the empty state of the thing it was added for.
  { label: 'admin product 375', width: 375, height: 812, path: '/admin/products/PRODUCT_SLUG', marker: '.ui-table' },
  { label: 'admin product 1440', width: 1440, height: 900, path: '/admin/products/PRODUCT_SLUG', marker: '.ui-table' },
  { label: 'admin reports 375', width: 375, height: 812, path: '/admin/reports', marker: '.goen-admin' },
  { label: 'admin reports 1440', width: 1440, height: 900, path: '/admin/reports', marker: '.goen-admin' },
  { label: 'admin audit 375', width: 375, height: 812, path: '/admin/audit', marker: '.goen-admin' },
  { label: 'admin audit 1440', width: 1440, height: 900, path: '/admin/audit', marker: '.goen-admin' },
  { label: 'admin coupons 375', width: 375, height: 812, path: '/admin/coupons', marker: '.goen-admin' },
  { label: 'admin coupons 1440', width: 1440, height: 900, path: '/admin/coupons', marker: '.goen-admin' },
  { label: 'admin credit 375', width: 375, height: 812, path: '/admin/credit', marker: '.goen-admin' },
  { label: 'admin credit 1440', width: 1440, height: 900, path: '/admin/credit', marker: '.goen-admin' },
  // .ui-table and not .goen-health: the status list is always present, so a
  // marker on it measures the HEALTHY page — chrome and nothing else — while
  // the alarm tables an operator has to act on go unrendered. The Makefile
  // seeds an unreconciled payment and a stranded 折讓 claim for exactly this.
  { label: 'admin health 375', width: 375, height: 812, path: '/admin/health', marker: '.ui-table' },
  { label: 'admin health 1440', width: 1440, height: 900, path: '/admin/health', marker: '.ui-table' },
  // ONE order in full, which is where every invoice control lives: issue, void
  // and the 折讓 form. The list had rows and the detail page had none, so no
  // browser had ever rendered a form on the page that files a tax document.
  // INVOICE_ORDER is the return-fixture order the Makefile refunds and then
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
  // bare path would measure a search box and call the page covered — the promo
  // strip's lesson (CLAUDE.md #24). CUSTOMER_ID is the customer the Makefile
  // seeded and gave the placed order to, so the detail page has its stats and its
  // order table on screen rather than the empty state.
  // The FAQ page. Its marker is the LIST rather than .goen-admin, because the seed
  // populates faq_entries and the page's two halves are a form and that list — a row
  // that passed on the chrome alone would measure the form and call the page covered.
  // One variant's stock ledger. Markered on the TABLE: a variant whose ledger is
  // empty renders a hint instead, and the seed now posts its stock through
  // record_inventory_movement precisely so this page has something to measure.
  { label: 'admin movements 375', width: 375, height: 812, path: '/admin/stock/PXL-9P-1-1', marker: '.ui-table' },
  { label: 'admin movements 1440', width: 1440, height: 900, path: '/admin/stock/PXL-9P-1-1', marker: '.ui-table' },
  { label: 'admin faq 375', width: 375, height: 812, path: '/admin/faq', marker: '.goen-admin__coupons' },
  { label: 'admin faq 1440', width: 1440, height: 900, path: '/admin/faq', marker: '.goen-admin__coupons' },
  { label: 'admin customers 375', width: 375, height: 812, path: '/admin/customers?q=layout', marker: '.ui-table' },
  { label: 'admin customers 1440', width: 1440, height: 900, path: '/admin/customers?q=layout', marker: '.ui-table' },
  { label: 'admin customer 375', width: 375, height: 812, path: '/admin/customers/CUSTOMER_ID', marker: '.goen-admin__stats' },
  { label: 'admin customer 1440', width: 1440, height: 900, path: '/admin/customers/CUSTOMER_ID', marker: '.goen-admin__stats' },
  { label: 'admin messages 375', width: 375, height: 812, path: '/admin/messages', marker: '.goen-admin__returns' },
  { label: 'admin messages 1440', width: 1440, height: 900, path: '/admin/messages', marker: '.goen-admin__returns' },
  { label: 'admin newsletter 375', width: 375, height: 812, path: '/admin/newsletter', marker: '.goen-admin' },
  { label: 'admin newsletter 1440', width: 1440, height: 900, path: '/admin/newsletter', marker: '.goen-admin' },
  { label: 'admin questions 1440', width: 1440, height: 900, path: '/admin/questions', marker: '.goen-admin__questions' },
  // /admin/warranty lists NOTHING until somebody searches — the /admin/customers
  // rule, because these rows carry a customer's name beside what they own. So the
  // row searches for the serial the Makefile's fixture registered, and the marker
  // is the table that exists only when the search found it. A row against the bare
  // path would measure a search box and report a checked page (CLAUDE.md #26).
  { label: 'admin warranty 375', width: 375, height: 812, path: '/admin/warranty?q=LAYOUT_SERIAL', marker: '.goen-admin__warranties' },
  { label: 'admin warranty 1440', width: 1440, height: 900, path: '/admin/warranty?q=LAYOUT_SERIAL', marker: '.goen-admin__warranties' },
  { label: 'admin returns 375', width: 375, height: 812, path: '/admin/returns', marker: '.goen-admin__returns' },
  { label: 'admin returns 1440', width: 1440, height: 900, path: '/admin/returns', marker: '.goen-admin__returns' },
  { label: 'admin taxonomy 375', width: 375, height: 812, path: '/admin/taxonomy', marker: '.goen-admin' },
  { label: 'admin taxonomy 1440', width: 1440, height: 900, path: '/admin/taxonomy', marker: '.goen-admin' },
  { label: 'admin campaigns 375', width: 375, height: 812, path: '/admin/campaigns', marker: '.goen-admin' },
  { label: 'admin campaigns 1440', width: 1440, height: 900, path: '/admin/campaigns', marker: '.goen-admin' },
  { label: 'admin shipping 375', width: 375, height: 812, path: '/admin/shipping', marker: '.goen-admin' },
  { label: 'admin shipping 1440', width: 1440, height: 900, path: '/admin/shipping', marker: '.goen-admin' },
  { label: 'admin tiers 375', width: 375, height: 812, path: '/admin/tiers', marker: '.goen-admin' },
  { label: 'admin tiers 1440', width: 1440, height: 900, path: '/admin/tiers', marker: '.goen-admin' },
  { label: 'admin staff 375', width: 375, height: 812, path: '/admin/staff', marker: '.goen-admin' },
  { label: 'admin staff 1440', width: 1440, height: 900, path: '/admin/staff', marker: '.goen-admin' },
];

// Signed-in customer surfaces. CUST_TOKEN is the session the Makefile mints;
// RETURN_FORM_ORDER is a delivered order with no return filed yet;
// INVOICE_ORDER is the refunded order the return fixture decided.
const ACCOUNT_BADFORM = [
  { label: 'points badform 375', width: 375, height: 812, locale: 'zh-Hant',
    notice: '這份兌換表單已過期,請重新送出。' },
  { label: 'points badform 1440', width: 1440, height: 900, locale: 'zh-Hant',
    notice: '這份兌換表單已過期,請重新送出。' },
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
  // Canonical order detail (#295). INVOICE_ORDER is delivered, refunded and
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
  // is the #320 topology and must not pass (#321 fixes the product).
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
// filter rail to sit beside the grid. Two-up on a 375px phone gives 166px and
// that is correct; the defect is a card that is no wider at 1024 than it is on
// a phone, which is a column count that stopped fitting once the rail took
// 272px out of the row. Only checked where rail === 'beside'.
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
    pending.set(id, { resolve, reject });
    setTimeout(() => {
      if (pending.delete(id)) reject(new Error(`${method} timed out`));
    }, timeoutMs);
  });
}

async function pageSocket() {
  for (let i = 0; i < 50; i++) {
    try {
      const list = await (await fetch(`http://127.0.0.1:${CDP_PORT}/json/list`)).json();
      const page = list.find((t) => t.type === 'page');
      if (page) return page.webSocketDebuggerUrl;
    } catch {
      /* Chrome is not listening yet */
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`no Chrome page target on port ${CDP_PORT}`);
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

  const body = document.querySelector('.goen-hero__body').getBoundingClientRect();
  const media = document.querySelector('.goen-hero__media').getBoundingClientRect();

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
// Every navigation here used to be followed by a fixed 1200ms sleep, which is a race
// the check loses on a cold server: the probe then runs against about:blank or a
// half-built document, reads null off a querySelector, and throws — reported as a
// TypeError in the checker rather than as the page not being ready.
//
// Polls readyState instead, with a ceiling. A page that never completes is a real
// failure and says so.
// Every route this run actually visited, in the order it first saw them, as
// route -> the URL that reached it. The axe pass at the end reads this rather
// than a second list of paths: a list would drift from the tables above, and
// the point of the audit is that it covers what the gate covers.
const visited = new Map();

// A route key has to mean the same thing next week. The fixtures mint a
// customer id, an order number carrying today's date and a warranty serial from
// the shell's pid on every run, so a key taken verbatim would name a page that
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
// CI reported two product images as missing at the 375 artboard — the run's
// first navigation — that loaded correctly at 768, 1024 and 1440 seconds later.
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
// as an image the server does not serve. The run that sent this here says so
// exactly: the two tiles named in its failure never appear in the server's log
// during the fifteen seconds, and are requested the instant the viewport widens
// for the next artboard.
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
    const { resolve, reject } = pending.get(msg.id);
    pending.delete(msg.id);
    msg.error ? reject(new Error(JSON.stringify(msg.error))) : resolve(msg.result);
  }
};

await send(ws, 'Page.enable');

// The cart pages need the browser to carry the cart cookie the Makefile just
// obtained. Without it /cart is empty and its check would measure nothing.
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
// The cookie carries a TOKEN and the URL carries the NUMBER, and they are two
// variables for that reason: they used to be one, from the days when the number
// was the proof, and reading the token into the URL is what made the pay rows
// name an order that does not exist.
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

// Whether the filter shell exposes its form and a control. On desktop a closed
// <details> keeps ::details-content at content-visibility:hidden until the
// stylesheet opens it; display:flex on the form alone is not enough.
const FILTER_SHELL_PROBE = `(() => {
  const shell = document.querySelector('.goen-filters__shell');
  const form = document.querySelector('.goen-filters');
  const input = document.querySelector('.goen-filters .ui-input, .goen-filters .ui-select');
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
  const resultsRect = results.getBoundingClientRect();
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
    resultsBesideFilter: resultsRect.left > rail.right - 2,
  };
})()`;

const assertDesktopResultsLayout = (at, got) => {
  if (got.threw || !got.ok) {
    fail(at, got.why || 'listing layout probe failed');
    return;
  }
  if (got.rail !== 'beside') {
    fail(at, `results are not beside the filter rail — ${JSON.stringify(got)}`);
  }
  if (!got.resultsBesideFilter) {
    fail(at, `results sit in the narrow filter column — ${JSON.stringify(got)}`);
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
    // The filter rail takes 272px out of the row, so a column count copied from
    // the home page produced 156px cards here — narrower than the same card on
    // a 375px phone. Column count alone would not have caught that; the width
    // is what the visitor sees.
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
  if (want.rail === 'beside') {
    const filters = await evalPage(FILTER_SHELL_PROBE);
    if (filters.threw) fail(at, `filter visibility probe failed — ${filters.why}`);
    assertDesktopFiltersVisible(at, filters);
  }
  if (got.minTap < MIN_TAP) fail(at, `smallest filter control is ${got.minTap}px, want >= ${MIN_TAP}`);
  if (want.rail === 'beside' && got.cardWidth > 0 && got.cardWidth < MIN_CARD) {
    fail(at, `product card is ${got.cardWidth}px wide, want >= ${MIN_CARD} — ` +
      `too many columns for the space the filter rail leaves`);
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
  { label: 'listing audio zh 1440', width: 1440, height: 900, path: '/c/audio', locale: 'zh-Hant', rail: 'beside' },
  { label: 'listing audio en 1440', width: 1440, height: 900, path: '/c/audio', locale: 'en', rail: 'beside' },
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
  if (want.rail === 'beside') {
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
    const apply = document.querySelector('.goen-filters__apply');
    const applyRect = apply ? apply.getBoundingClientRect() : null;
    return {
      ok: true,
      shellOpen: shell.open,
      applyVisible: !!(applyRect && applyRect.height > 0 && applyRect.bottom > 0),
    };
  })()`);
  if (expanded.threw || !expanded.ok) {
    fail(label, expanded.why || 'expanded probe failed');
    return;
  }
  if (!expanded.shellOpen) fail(label, 'filter shell did not open after the summary was activated');
  if (!expanded.applyVisible) fail(label, 'apply control is not visible after expanding the shell');

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
  // still fits once the filter rail has taken its width out of the row, which is
  // a question only the desktop layout can answer — its own declaration says
  // "Only checked where rail === 'beside'", and the two call sites that honour
  // that are assertDesktopResultsLayout and the `want.rail === 'beside'` guard
  // on the LISTING rows. This journey runs at 375, where the rail is stacked and
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
  await send(ws, 'Network.setCookie', {
    name: 'goen_locale', value: 'en', domain: '127.0.0.1', path: '/',
  });
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
    .replace('COMPARE_SLUG_B', process.env.COMPARE_SLUG_B || '')
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
  // where it measured a nav bar. CLAUDE.md #24, in the section that learned it.
  if (__MARKER__ && !document.querySelector(__MARKER__)) return { noMarker: true };
  // Nav links, buttons and inputs. Table cells are not targets.
  const taps = [...document.querySelectorAll('.goen-admin .ui-navitem, .goen-admin button, .goen-admin input, .goen-admin .ui-filter')]
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
// an empty outer item and parks the form as a direct ul child (#320).
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
} else {
  console.log('account pages    skipped (no CUST_TOKEN)');
}

if (process.env.ADMIN_TOKEN) {
  await send(ws, 'Network.enable');
  await send(ws, 'Network.setCookie', {
    name: 'goen_session', value: process.env.ADMIN_TOKEN, domain: '127.0.0.1', path: '/',
  });

  // Authentication failure must stop this gate; it cannot remove the admin
  // rows and turn an unmeasured surface into a smaller successful sweep.
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
      throw new Error(`admin fixture did not reach the back office (${at.href}); ` +
        'the disposable TOTP fixture must complete the real verification POST before this sweep');
    }
  }

  if (process.env.GOEN_TOTP_KEY) {
    const measure = async (label, marker, enrolling) => {
      await imagesFetched(label);
      const got = await evalPage(`(() => {
        const input = document.querySelector(${JSON.stringify(marker)});
        const secret = document.querySelector('.goen-twofa__secret code');
        const qr = document.querySelector('.goen-twofa__qr');
        const controls = [...document.querySelectorAll('.goen-twofa button, .goen-twofa input')]
          .map(e => e.getBoundingClientRect().height).filter(h => h > 0);
        return { marker: !!input, secret: !!secret, qr: !!qr && qr.naturalWidth > 0,
          viewportWidth: document.documentElement.clientWidth,
          scrollWidth: document.body.scrollWidth,
          minTap: controls.length ? Math.min(...controls) : 0,
          ${ACCESSIBILITY} };
      })()`);
      if (got.threw || !got.marker) { fail(label, 'required OTP input is absent'); return; }
      checkAccessibility(label, got);
      if (got.scrollWidth > got.viewportWidth) fail(label, 'two-factor page scrolls horizontally');
      if (got.minTap < MIN_TAP) fail(label, `OTP controls are ${got.minTap}px, want >= ${MIN_TAP}`);
      if (enrolling && (!got.secret || !got.qr)) fail(label, 'enrolment secret or local QR image is absent');
      await send(ws, 'Runtime.evaluate', { expression: axeSource });
      const axe = await evalPage(`axe.run(document, {
        runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }, resultTypes: ['violations']
      }).then(r => r.violations.filter(v => ['serious', 'critical'].includes(v.impact)).map(v => v.id))`);
      if (axe.threw || !Array.isArray(axe)) fail(label, 'two-factor axe audit did not complete');
      else for (const rule of axe) fail(label, `axe ${rule}`);
      console.log(`${label}: OTP input measured, scrollW=${got.scrollWidth}/${got.viewportWidth}`);
    };
    for (const width of [375, 1440]) {
      await send(ws, 'Emulation.setDeviceMetricsOverride', {
        width, height: width === 375 ? 812 : 900, deviceScaleFactor: 1, mobile: width < 768,
      });
      await send(ws, 'Page.navigate', { url: ORIGIN + '/admin/verify' });
      await settled(ws, `TOTP challenge ${width}`, ORIGIN + '/admin/verify');
      await measure(`TOTP challenge ${width}`, 'form[action="/admin/verify"] input[name="code"]', false);
    }
    const enrolToken = readFileSync('.layout-chrome/enrol-token', 'utf8').trim();
    await send(ws, 'Network.setCookie', { name: 'goen_session', value: enrolToken, domain: '127.0.0.1', path: '/' });
    for (const width of [375, 1440]) {
      await send(ws, 'Emulation.setDeviceMetricsOverride', {
        width, height: width === 375 ? 812 : 900, deviceScaleFactor: 1, mobile: width < 768,
      });
      await send(ws, 'Page.navigate', { url: ORIGIN + '/admin/verify' });
      await settled(ws, `TOTP enrol entry ${width}`, ORIGIN + '/admin/verify');
      const submitted = await evalPage(`(() => {
        const form = document.querySelector('form[action="/admin/verify/enrol"]');
        if (!form) return false;
        form.requestSubmit(); return true;
      })()`);
      if (submitted !== true) throw new Error('enrolment fixture did not render its start form');
      await settled(ws, `TOTP enrol ${width}`, ORIGIN + '/admin/verify/enrol');
      await measure(`TOTP enrol ${width}`, 'form[action="/admin/verify/confirm"] input[name="code"]', true);
      // The secret is only in this POST response, so audit it here instead of
      // asking the final GET-only sweep to fabricate the same document.
      visited.delete('/admin/verify/enrol');
    }
    await send(ws, 'Network.setCookie', { name: 'goen_session', value: process.env.ADMIN_TOKEN, domain: '127.0.0.1', path: '/' });
  } else if (process.env.GITHUB_ACTIONS) {
    throw new Error('CI layout gate requires its disposable TOTP key');
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
    if (got.controls === 0) {
      fail(at, 'no controls found — the probe measured nothing');
    } else if (got.minTap < MIN_TAP) {
      fail(at, `smallest control is ${got.minTap}px, want >= ${MIN_TAP}`);
    }
    console.log(`${at.padEnd(16)} scrollW=${got.scrollWidth}/${got.viewportWidth} ` +
      `controls=${got.controls} tap=${got.minTap}`);
  }
} else {
  console.log('admin           skipped (no ADMIN_TOKEN)');
}

// The footer newsletter and the contact panel both target themselves with
// outerHTML. htmx 4 swaps every status except 204/304, so a plain-text 429
// replaces the interactive surface with raw `429 …` and the visitor cannot
// retry. A handler test can only see the fragment; these rows spend the live
// limiter through the actual submit control and read the swapped DOM.
const RETRY_ZH = '請求過於頻繁,請稍後再試。';
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
        retry: (document.querySelector('#newsletter-error') || {}).textContent || '',
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
    form.requestSubmit();
    return { ok: true };
  })()`);
  if (submit.threw || !submit.ok) {
    fail(label, submit.why || 'add-to-cart submit did not start');
    return;
  }

  // The address says the same thing either way — this product, added=added,
  // the selection that was bought — but the fragment belongs to the navigation
  // and not to the outcome. With no script the browser navigates and #buybox is
  // what aims the landing; with script nothing navigates, nothing needs aiming,
  // and a fragment in the pushed URL would claim a jump that did not happen.
  const landed = await waitForHref(
    (href) => href.includes(`/p/${slug}`) && href.includes('added=added')
      && href.includes('?') && href.includes('#buybox') === scriptingOff,
    label,
  );
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
  if (after.notices < before.notices) {
    fail(label, 'refresh dropped the add-to-cart notice');
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

// axe-core, once per route.
//
// A separate pass rather than a call inside settled(), and that is deliberate:
// the journeys above spend a live rate limiter and compare timestamps across a
// reload, and a second or two of audit inserted between their requests would
// change what they measure. Running afterwards costs one extra navigation per
// route and changes nothing any other assertion sees.
//
// What it asks for is WCAG 2.0/2.1 A and AA. Not the best-practice rules: those
// are opinions about landmarks and heading order, and a build gate holding an
// opinion is how a gate gets disabled.
const AXE_RUN = `axe.run(document, {
  runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] },
  resultTypes: ['violations'],
}).then((r) => JSON.stringify(r.violations.map((v) => ({
  id: v.id,
  impact: v.impact,
  help: v.help,
  target: v.nodes[0] && v.nodes[0].target ? String(v.nodes[0].target[0]) : '(no node)',
  count: v.nodes.length,
}))))`;

// A moderate or minor finding is reported and does not gate. ::warning is what
// puts it on the pull request's Files view; outside Actions it is a plain line.
const annotate = (msg) => console.log(
  process.env.GITHUB_ACTIONS ? `::warning title=axe-core::${msg}` : `  warning: ${msg}`);

// Where the browser actually ends up, which is not always where it was sent.
//
// By the time the audit runs the session carries a signed-in cookie, and
// /forgot answers 303 to /account for a visitor who already is: settled()'s
// href === url would report that as a page that never loaded. about:blank
// first, so a document that is still the PREVIOUS page cannot be mistaken for
// this one, and then whatever the browser landed on.
//
// It reports rather than exits, unlike settled(), because this pass runs last:
// an exit here would throw away the failure list everything above built.
const axeSettled = async (route, url) => {
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
  fail(`axe ${route}`, 'the page never finished loading for the audit');
  return '';
};

const auditAccessibility = async () => {
  await send(ws, 'Emulation.setDeviceMetricsOverride', {
    width: AXE_WIDTH.width, height: AXE_WIDTH.height, deviceScaleFactor: 1, mobile: false,
  });

  const requested = [...visited.entries()];
  console.log(`\naxe-core wcag2a + wcag2aa, ${requested.length} routes at ${AXE_WIDTH.width}px`);

  const observed = {};
  const unaudited = [];
  const audited = new Set();
  let debtMoved = false;

  for (const [asked, url] of requested) {
    const landed = await axeSettled(asked, url);
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

    let violations;
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
      violations = JSON.parse(evaluated.result.value);
    } catch (err) {
      unaudited.push(route);
      fail(`axe ${route}`, `the audit did not complete — ${err.message}`);
      continue;
    }

    const known = axeBaseline[route] || [];
    const gating = new Set();
    for (const v of violations) {
      const detail = `${v.id} (${v.impact}) — ${v.help}; first: ${v.target}` +
        (v.count > 1 ? ` (and ${v.count - 1} more on this page)` : '');
      if (!AXE_GATES.has(v.impact)) {
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
      `violations=${violations.length} gating=${observed[route].length}`);
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

await auditAccessibility();

ws.close();

if (failures.length) {
  console.error(`\nlayout check FAILED (${failures.length}):`);
  for (const f of failures) console.error(`  - ${f}`);
  process.exit(1);
}
console.log('\nlayout check PASS');
