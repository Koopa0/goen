// Probe only: full-page shots over CDP and a count of the rules drawn on each page.
// Usage: node scripts/lines-shots.mjs storefront|admin <outdir> <label>
//   storefront reads CART_TOKEN, PLACED_TOKEN, PLACED_ORDER, CUST_TOKEN, RETURN_FORM_ORDER,
//   PRODUCT_SLUG and COMPARE_SLUG_B (scripts/check-layout.sql); admin reads ADMIN_TOKEN.

import { mkdirSync, writeFileSync } from 'node:fs';

const CDP_PORT = Number(process.env.CDP_PORT || 9333);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const CAP = 9000;
const [mode, outDir, label] = process.argv.slice(2);
const failures = [];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

let nextId = 1;
const pending = new Map();
let ws;
function send(method, params = {}) {
  const id = nextId++;
  ws.send(JSON.stringify({ id, method, params }));
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`${method} timed out`)); }, 60000);
    pending.set(id, { resolve, reject, timer });
  });
}
async function evaluate(expression, awaitPromise = false) {
  const { result, exceptionDetails } = await send('Runtime.evaluate', { expression, awaitPromise, returnByValue: true });
  if (exceptionDetails) throw new Error(`evaluate failed: ${JSON.stringify(exceptionDetails).slice(0, 300)}`);
  return result.value;
}
async function navigate(url) {
  await send('Page.navigate', { url: 'about:blank' });
  for (let i = 0; i < 100 && (await evaluate('location.href')) !== 'about:blank'; i++) await sleep(100);
  await send('Page.navigate', { url });
  for (let i = 0; i < 300; i++) {
    const state = String(await evaluate('document.readyState + " " + location.href'));
    if (state.startsWith('complete') && !state.endsWith('about:blank')) return;
    await sleep(100);
  }
  throw new Error(`${url} never finished loading`);
}
async function pageSocket() {
  for (let i = 0; i < 150; i++) {
    try {
      const list = await (await fetch(`http://127.0.0.1:${CDP_PORT}/json/list`)).json();
      const page = list.find((t) => t.type === 'page');
      if (page) return page.webSocketDebuggerUrl;
    } catch { /* not up yet */ }
    await sleep(200);
  }
  throw new Error('no Chrome page');
}

const metrics = (width, height) =>
  send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: width < 600 });
const media = (forced) => send('Emulation.setEmulatedMedia', { features: [
  { name: 'prefers-color-scheme', value: 'light' },
  { name: 'forced-colors', value: forced ? 'active' : 'none' },
] });

// Every rendered element whose top or bottom border draws (width >= 1px, a style that
// paints, a colour with alpha), plus every <hr>. Split into the page's main content and
// the chrome around it, and controls (fields, steppers, choice cards) apart from rules.
const MEASURE = `(() => {
  const clear = (c) => !c || c === 'transparent' || /rgba\\([^)]*,\\s*0\\)$/.test(c);
  const control = 'input, select, textarea, button, .goen-stepper, .goen-checkout__ship, .goen-swatch, .goen-input, .ui-input, .ui-select, .ui-textarea, .goen-checkout__select';
  const out = { main: 0, mainControls: 0, chrome: 0, chromeControls: 0, hr: 0, rules: {} };
  for (const el of document.querySelectorAll('body *')) {
    if (!el.checkVisibility({ visibilityProperty: true, opacityProperty: true })) continue;
    const r = el.getBoundingClientRect();
    if (r.width < 1 || r.height < 0) continue;
    const cs = getComputedStyle(el);
    const sides = ['top', 'bottom'].filter((s) => parseFloat(cs['border-' + s + '-width']) >= 1
      && !['none', 'hidden'].includes(cs['border-' + s + '-style']) && !clear(cs['border-' + s + '-color']));
    const hr = el.tagName === 'HR';
    if (!sides.length && !hr) continue;
    if (hr) out.hr++;
    const isControl = el.matches(control) || !!el.closest('.goen-stepper');
    const inMain = !!el.closest('main');
    if (inMain) { out.main++; if (isControl) out.mainControls++; } else { out.chrome++; if (isControl) out.chromeControls++; }
    if (isControl) continue;
    const frame = sides.length === 2 && parseFloat(cs.borderLeftWidth) >= 1 && !clear(cs.borderLeftColor) ? 'frame' : sides.join('+') || 'hr';
    const cls = [...el.classList].slice(0, 2).join('.');
    const key = (inMain ? 'main ' : 'chrome ') + el.tagName.toLowerCase() + (cls ? '.' + cls : '') + ' ' + frame;
    out.rules[key] = (out.rules[key] || 0) + 1;
  }
  return out;
})()`;

// The pay page at 320 and 375 with the root at 200%, as the reflow gate sets it: the box of
// each part of a line and of the total, and the outermost elements that pass the viewport.
const DIAG = `(() => {
  const info = (el) => {
    const r = el.getBoundingClientRect();
    const cs = getComputedStyle(el);
    return { el: el.tagName.toLowerCase() + '.' + [...el.classList].join('.'), left: Math.round(r.left), right: Math.round(r.right),
      width: Math.round(r.width), scrollW: el.scrollWidth, display: cs.display, flex: cs.flex, wrap: cs.flexWrap,
      minWidth: cs.minWidth, whiteSpace: cs.whiteSpace, fontSize: cs.fontSize, cols: cs.gridTemplateColumns,
      text: (el.textContent || '').trim().replace(/\\s+/g, ' ').slice(0, 24) };
  };
  const sels = ['.goen-pay', '.goen-pay__lines', '.goen-pay__lines > li', '.goen-pay__lines .goen-line__body',
    '.goen-pay__lines .goen-line__meta', '.goen-pay__lines .goen-line__money', '.goen-pay__lines .goen-line__total',
    '.goen-pay__summary', '.goen-pay__summary .goen-summary', '.goen-summary__row--total',
    '.goen-summary__row--total > dt', '.goen-summary__row--total > dd'];
  const rows = sels.flatMap((q) => [...document.querySelectorAll(q)].slice(0, 2).map((el) => ({ q, ...info(el) })));
  const over = (el) => el && el.getBoundingClientRect().right > innerWidth + 0.5;
  const past = [...document.querySelectorAll('body *')].filter((el) => over(el) && !over(el.parentElement)).slice(0, 10).map(info);
  return { innerWidth, scrollWidth: document.documentElement.scrollWidth, rootFont: getComputedStyle(document.documentElement).fontSize, rows, past };
})()`;

async function pay200() {
  for (const width of [320, 375]) {
    await media(false);
    await send('Emulation.setDeviceMetricsOverride', { width, height: 800, deviceScaleFactor: 1, mobile: true });
    await navigate(ORIGIN + `/orders/${process.env.PLACED_ORDER}/pay`);
    await evaluate('document.fonts.ready.then(() => 1)', true);
    await evaluate(`document.documentElement.style.fontSize = '200%'`);
    await evaluate('new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(() => setTimeout(r, 300))))', true);
    const d = await evaluate(DIAG);
    console.log(`DIAG ${label} pay-${width}-text200 innerWidth=${d.innerWidth} scrollWidth=${d.scrollWidth} root=${d.rootFont}`);
    for (const r of d.rows) console.log(`DIAG ${label} pay-${width}-text200 box ${JSON.stringify(r)}`);
    for (const r of d.past) console.log(`DIAG ${label} pay-${width}-text200 past ${JSON.stringify(r)}`);
    const height = Math.min(CAP, Math.max(await evaluate('Math.ceil(document.documentElement.scrollHeight)'), 400));
    await send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: true });
    await sleep(400);
    const { data } = await send('Page.captureScreenshot', { format: 'png' });
    writeFileSync(`${outDir}/pay-${width}-text200.png`, Buffer.from(data, 'base64'));
  }
}

// A page at 320 and 375 with the root at 200%, as the reflow gate sets it: the page's
// scrollWidth against the viewport, and the elements that pass it.
async function text200(path, name) {
  for (const width of [320, 375]) {
    await media(false);
    await send('Emulation.setDeviceMetricsOverride', { width, height: 800, deviceScaleFactor: 1, mobile: true });
    await navigate(ORIGIN + path);
    await evaluate('document.fonts.ready.then(() => 1)', true);
    await evaluate(`document.documentElement.style.fontSize = '200%'`);
    await evaluate('new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(() => setTimeout(r, 300))))', true);
    const d = await evaluate(DIAG.replace(/const sels = \[[^\]]*\];/, 'const sels = [];'));
    const h1 = await evaluate(`(document.querySelector('h1') || {}).textContent || ''`);
    console.log(`MEASURE ${label} text200 ${name}-${width} h1=${JSON.stringify(h1.trim())} innerWidth=${d.innerWidth} scrollWidth=${d.scrollWidth} root=${d.rootFont} over=${d.scrollWidth > d.innerWidth}`);
    for (const r of d.past) console.log(`MEASURE ${label} text200 ${name}-${width} past ${JSON.stringify(r)}`);
    if (/404|找不到/.test(h1)) failures.push(`${name}-${width}-text200: landed on a not-found page`);
    const height = Math.min(CAP, Math.max(await evaluate('Math.ceil(document.documentElement.scrollHeight)'), 400));
    await send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: true });
    await sleep(400);
    const { data } = await send('Page.captureScreenshot', { format: 'png' });
    writeFileSync(`${outDir}/${name}-${width}-text200.png`, Buffer.from(data, 'base64'));
  }
}

// The viewport grows to the page before the images are awaited, so a lazy image
// below the fold loads instead of being waited on until the timeout.
async function shot(path, name, width, { forced = false, notFound = false } = {}) {
  const file = `${name}-${width}${forced ? '-forced' : ''}.png`;
  await media(forced);
  await metrics(width, 900);
  await navigate(ORIGIN + path);
  await evaluate('document.fonts.ready.then(() => 1)', true);
  const height = Math.min(CAP, Math.max(await evaluate('Math.ceil(document.documentElement.scrollHeight)'), 400));
  await metrics(width, height);
  for (let i = 0; i < 40; i++) {
    const loading = await evaluate('[...document.images].filter((i) => !i.complete).length');
    if (!loading) break;
    await sleep(250);
  }
  await sleep(500);
  const facts = await evaluate(`({
    path: location.pathname + location.search,
    admin: !!document.querySelector('.goen-admin'),
    h1: (document.querySelector('h1') || {}).textContent || '',
    forced: matchMedia('(forced-colors: active)').matches,
    scrollW: document.documentElement.scrollWidth,
  })`);
  const m = await evaluate(MEASURE);
  const { data } = await send('Page.captureScreenshot', { format: 'png' });
  writeFileSync(`${outDir}/${file}`, Buffer.from(data, 'base64'));
  console.log(`${file} ${facts.path} h1=${JSON.stringify(facts.h1.trim())} forced=${facts.forced} scrollW=${facts.scrollW} shotHeight=${height}`);
  const rules = Object.entries(m.rules).sort((a, b) => a[0].localeCompare(b[0])).map(([k, n]) => `${k} x${n}`);
  const what = forced ? 'boundaries-that-draw' : 'rules';
  console.log(`MEASURE ${label} ${what} ${name}-${width} main=${m.main} mainControls=${m.mainControls} mainRules=${m.main - m.mainControls} chrome=${m.chrome} chromeControls=${m.chromeControls} hr=${m.hr}`);
  for (const r of rules) console.log(`MEASURE ${label} ${what} ${name}-${width}   ${r}`);
  if (!notFound && /404|找不到/.test(facts.h1)) failures.push(`${file}: landed on a not-found page`);
  if (mode === 'admin' && (!facts.admin || !facts.path.startsWith(path.split('?')[0]))) {
    failures.push(`${file}: landed on ${facts.path}`);
  }
}

async function firstLink(listPath, prefix, exclude = []) {
  await media(false);
  await navigate(ORIGIN + listPath);
  return evaluate(`(() => {
    const ex = ${JSON.stringify(exclude)};
    const a = [...document.querySelectorAll('main a[href^="${prefix}"]')]
      .map((e) => e.getAttribute('href'))
      .find((h) => h !== '${prefix}' && !h.startsWith('${prefix}?') && !ex.some((x) => h.startsWith(x)));
    return a || null;
  })()`);
}

const cookie = (name, value) => send('Network.setCookie', { name, value, domain: '127.0.0.1', path: '/' });

async function capture(pages, widths = [1440, 375], opts = {}) {
  for (const width of widths) {
    for (const [name, path, extra] of pages) {
      if (!path) { failures.push(`${name}-${width}: no link found`); continue; }
      try { await shot(path, name, width, { ...opts, ...extra }); } catch (e) { failures.push(`${name}-${width}: ${e.message}`); }
    }
  }
}

mkdirSync(outDir, { recursive: true });
ws = new WebSocket(await pageSocket());
await new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject; });
ws.onmessage = (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.id && pending.has(msg.id)) {
    const { resolve, reject, timer } = pending.get(msg.id);
    pending.delete(msg.id);
    clearTimeout(timer);
    msg.error ? reject(new Error(JSON.stringify(msg.error))) : resolve(msg.result);
  }
};
await send('Page.enable');
await send('Emulation.setScrollbarsHidden', { hidden: true });
await send('Network.enable');
await send('Network.clearBrowserCookies');
await cookie('goen_locale', 'zh-Hant');

if (mode === 'storefront') {
  if (process.env.CART_TOKEN) await cookie('goen_cart', process.env.CART_TOKEN);
  if (process.env.PLACED_TOKEN) await cookie('goen_placed', process.env.PLACED_TOKEN);
  await metrics(1440, 900);
  const compare = `/compare?p=${encodeURIComponent(process.env.PRODUCT_SLUG || '')}&p=${encodeURIComponent(process.env.COMPARE_SLUG_B || '')}`;
  // Signed out: the shop as a guest with a cart and an unpaid order.
  await capture([
    ['home', '/'],
    ['department', '/c/home-living'],
    ['product', '/p/meridian-watch-c1'],
    ['search', '/search?q=%E8%8C%B6'],
    ['compare', compare],
    ['shipping', '/shipping'],
    ['notfound', '/this-page-is-not-here', { notFound: true }],
    ['cart', '/cart'],
    ['checkout', '/checkout'],
    ['pay', `/orders/${process.env.PLACED_ORDER}/pay`],
  ]);
  await capture([['cart', '/cart'], ['checkout', '/checkout']], [1440], { forced: true });
  // Signed in as the customer whose delivered order is still inside its window.
  await cookie('goen_session', process.env.CUST_TOKEN);
  await capture([
    ['order', `/orders/${process.env.RETURN_FORM_ORDER}`],
    ['account', '/account'],
  ]);
} else if (mode === 'pay200') {
  if (process.env.CART_TOKEN) await cookie('goen_cart', process.env.CART_TOKEN);
  if (process.env.PLACED_TOKEN) await cookie('goen_placed', process.env.PLACED_TOKEN);
  await pay200();
} else if (mode === 'review') {
  // The designer's re-shoot list: a cart of three lines and a sold-out one, a two-parcel order,
  // cart, checkout and pay at 200% text, forced colours on pay, order and account, and the
  // back-office overview at 375. CART2_TOKEN and TWO_PARCEL_ORDER come from scripts/lines-probe-seed.sql.
  await cookie('goen_cart', process.env.CART2_TOKEN);
  await cookie('goen_placed', process.env.PLACED_TOKEN);
  await metrics(1440, 900);
  await capture([['cart-four', '/cart'], ['checkout-four', '/checkout']], [1440, 375]);
  await text200('/cart', 'cart-four');
  await text200('/checkout', 'checkout-four');
  await text200(`/orders/${process.env.PLACED_ORDER}/pay`, 'pay');
  await capture([['pay', `/orders/${process.env.PLACED_ORDER}/pay`]], [1440], { forced: true });
  await cookie('goen_session', process.env.CUST_TOKEN);
  await capture([['order-two-parcels', `/orders/${process.env.TWO_PARCEL_ORDER}`]], [1440, 375]);
  await capture([['order-two-parcels', `/orders/${process.env.TWO_PARCEL_ORDER}`], ['account', '/account']], [1440], { forced: true });
  await cookie('goen_session', process.env.ADMIN_TOKEN);
  await capture([['admin-overview', '/admin']], [375]);
} else if (mode === 'admin') {
  await cookie('goen_session', process.env.ADMIN_TOKEN);
  await metrics(1440, 900);
  const order = await firstLink('/admin/orders', '/admin/orders/', ['/admin/orders/picking']);
  console.log('order', order);
  await capture([
    ['admin-overview', '/admin'],
    ['admin-orders', '/admin/orders'],
    ['admin-order', order],
    ['admin-report', '/admin/reports'],
    ['admin-newsletter', '/admin/newsletter'],
  ]);
} else {
  throw new Error(`unknown mode ${mode}`);
}

ws.close();
if (failures.length) {
  console.log(`\n${failures.length} failure(s):\n  ${failures.join('\n  ')}`);
  process.exit(1);
}
console.log('all shots captured');
