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
