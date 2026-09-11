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

const CDP_PORT = Number(process.env.CDP_PORT || 9222);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700/').replace(/\/$/, '');

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
  { label: 'compare 375', width: 375, height: 812, path: '/compare?p=PRODUCT_SLUG', marker: '.goen-compare' },
  { label: 'compare 1440', width: 1440, height: 900, path: '/compare?p=PRODUCT_SLUG', marker: '.goen-compare' },
];

const CART = [
  { label: 'cart 375', width: 375, height: 812, path: '/cart' },
  { label: 'cart 1440', width: 1440, height: 900, path: '/cart' },
  { label: 'checkout 375', width: 375, height: 812, path: '/checkout' },
  { label: 'checkout 1440', width: 1440, height: 900, path: '/checkout' },
  // The checkout with 超商取貨 chosen. It is a different form — a store picker
  // rather than a street address — so a layout row for the default method
  // measures only half the page. PICKUP_SHIP is the version id the Makefile
  // reads from the database, and the marker insists the store field is there.
  { label: 'pickup 375', width: 375, height: 812, path: '/checkout?ship=PICKUP_SHIP', marker: '#pickup_store_code' },
  { label: 'pickup 1440', width: 1440, height: 900, path: '/checkout?ship=PICKUP_SHIP', marker: '#pickup_store_code' },
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

// The expired redemption-form notice. GET ?badform=1 is the 303 landing of a
// stripped or stale operation_id. Without these rows the sweep never measures
// the sentence that replaced ?short=1, and a single-locale row would leave the
// other voice unmeasured. CUST_TOKEN is the customer the Makefile signed in.
const ACCOUNT = [
  { label: 'points badform 375', width: 375, height: 812, locale: 'zh-Hant',
    notice: '這份兌換表單已過期,請重新送出。' },
  { label: 'points badform 1440', width: 1440, height: 900, locale: 'zh-Hant',
    notice: '這份兌換表單已過期,請重新送出。' },
  { label: 'points badform en 375', width: 375, height: 812, locale: 'en',
    notice: 'That redemption form expired. Submit it again.' },
  { label: 'points badform en 1440', width: 1440, height: 900, locale: 'en',
    notice: 'That redemption form expired. Submit it again.' },
];

const MIN_TAP = 44; // the smallest comfortable touch target, in CSS px

// The narrowest a product card may be once the viewport is wide enough for the
// filter rail to sit beside the grid. Two-up on a 375px phone gives 166px and
// that is correct; the defect is a card that is no wider at 1024 than it is on
// a phone, which is a column count that stopped fitting once the rail took
// 272px out of the row. Only checked where rail === 'beside'.
const MIN_CARD = 200;

let nextId = 1;
const pending = new Map();

function send(ws, method, params = {}) {
  const id = nextId++;
  ws.send(JSON.stringify({ id, method, params }));
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    setTimeout(() => {
      if (pending.delete(id)) reject(new Error(`${method} timed out`));
    }, 30000);
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
      return;
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  console.error(`\n${label}: the page never finished loading`);
  process.exit(2);
};

const failures = [];
const fail = (where, msg) => failures.push(`${where}: ${msg}`);

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
  const rail = document.querySelector('.goen-filters').getBoundingClientRect();
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
  const taps = [...document.querySelectorAll('.goen-filters__option, .goen-filters__apply, .goen-filters .ui-input')]
    .map((e) => e.getBoundingClientRect().height);
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
  // Every control a finger has to hit. The radio INPUT is intentionally small —
  // its label is the target — so the label is measured where one wraps it.
  // A control that is aria-hidden AND out of the tab order is a target for
  // nobody: no pointer user can see it and no keyboard user can reach it. The
  // checkout's default submit button is one — it exists so Enter places the
  // order rather than pressing a 更新 button above it. Both attributes are
  // required, because either one alone is a defect rather than an intention.
  const targets = [...document.querySelectorAll('button, .ui-btn, input[type=number], label.goen-checkout__ship, a.goen-checkout__ship')]
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

const ACCOUNT_PROBE = `(() => {
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
  // Buttons and the points field only: crumb links are not the surface this
  // row exists to measure, and they sit under the 44px floor on every account
  // page.
  const taps = [...document.querySelectorAll('.goen-account .ui-btn, .goen-account input[type=number]')]
    .map((e) => e.getBoundingClientRect().height).filter((h) => h > 0);
  return {
    viewportWidth: de.clientWidth,
    scrollWidth: document.body.scrollWidth,
    overflowing: [...document.querySelectorAll('body *')]
      .filter((e) => { const r = e.getBoundingClientRect(); return r.width > 0 && r.right > de.clientWidth + 0.5 && !clipped(e); })
      .slice(0, 4).map((e) => e.tagName.toLowerCase() + '.' + String(e.className || '').split(' ')[0]),
    minTap: taps.length ? +Math.min(...taps).toFixed(1) : 0,
    controls: taps.length,
    notice: notice ? notice.textContent.trim() : '',
    ${ACCESSIBILITY}
  };
})()`;

if (process.env.CUST_TOKEN) {
  await send(ws, 'Network.enable');
  await send(ws, 'Network.setCookie', {
    name: 'goen_session', value: process.env.CUST_TOKEN, domain: '127.0.0.1', path: '/',
  });

  for (const want of ACCOUNT) {
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
      expression: ACCOUNT_PROBE, returnByValue: true,
    });
    if (evaluated.exceptionDetails || !evaluated.result || evaluated.result.value === undefined) {
      fail(want.label, 'the probe did not run — ' +
        (evaluated.exceptionDetails?.exception?.description || JSON.stringify(evaluated).slice(0, 400)));
      continue;
    }
    const got = evaluated.result.value;
    const at = want.label;

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
    if (got.controls > 0 && got.minTap < MIN_TAP) {
      fail(at, `smallest control is ${got.minTap}px, want >= ${MIN_TAP}`);
    }
    console.log(`${at.padEnd(24)} scrollW=${got.scrollWidth}/${got.viewportWidth} ` +
      `lang=${got.lang} tap=${got.minTap || '-'} notice=${JSON.stringify(got.notice)}`);
  }
} else {
  console.log('points badform   skipped (no CUST_TOKEN)');
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
  //   - the fixture wrote no session row (the Makefile used to swallow that);
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

ws.close();

if (failures.length) {
  console.error(`\nlayout check FAILED (${failures.length}):`);
  for (const f of failures) console.error(`  - ${f}`);
  process.exit(1);
}
console.log('\nlayout check PASS');
