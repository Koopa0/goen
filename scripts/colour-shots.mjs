// Probe only: full-page shots over CDP, storefront or back office.
// Usage: node scripts/colour-shots.mjs storefront|admin <outdir>
//   storefront reads CART_TOKEN, PLACED_TOKEN, PLACED_ORDER, CUST_TOKEN and
//   RETURN_FORM_ORDER (scripts/check-layout.sql); admin reads ADMIN_TOKEN.

import { mkdirSync, writeFileSync } from 'node:fs';

const CDP_PORT = Number(process.env.CDP_PORT || 9333);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const CAP = 6000;
const [mode, outDir] = process.argv.slice(2);
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

// The viewport grows to the page before the images are awaited, so a lazy image
// below the fold loads instead of being waited on until the timeout.
async function shot(path, file, width) {
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
    lang: document.documentElement.lang,
  })`);
  const { data } = await send('Page.captureScreenshot', { format: 'png' });
  writeFileSync(`${outDir}/${file}`, Buffer.from(data, 'base64'));
  console.log(`${file} ${facts.path} h1=${JSON.stringify(facts.h1.trim())} lang=${facts.lang} shotHeight=${height}`);
  if (/404|找不到/.test(facts.h1)) failures.push(`${file}: landed on a not-found page`);
  if (mode === 'admin' && (!facts.admin || !facts.path.startsWith(path.split('?')[0]))) {
    failures.push(`${file}: landed on ${facts.path}`);
  }
}

async function firstLink(listPath, prefix, exclude = []) {
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

async function capture(pages) {
  for (const width of [1440, 375]) {
    for (const [name, path] of pages) {
      if (!path) { failures.push(`${name}-${width}: no link found`); continue; }
      try { await shot(path, `${name}-${width}.png`, width); } catch (e) { failures.push(`${name}-${width}: ${e.message}`); }
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
await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'light' }] });
await send('Emulation.setScrollbarsHidden', { hidden: true });
await send('Network.enable');
await send('Network.clearBrowserCookies');
await cookie('goen_locale', 'zh-Hant');

if (mode === 'storefront') {
  if (process.env.CART_TOKEN) await cookie('goen_cart', process.env.CART_TOKEN);
  if (process.env.PLACED_TOKEN) await cookie('goen_placed', process.env.PLACED_TOKEN);
  await metrics(1440, 900);
  const dealProduct = await firstLink('/deals', '/p/');
  console.log('deal product', dealProduct);
  // Signed out: the shop as a guest with a cart and an unpaid order.
  await capture([
    ['home', '/'],
    ['department', '/c/home-living'],
    ['product', '/p/meridian-watch-c1'],
    ['product-deal', dealProduct],
    ['search', '/search?q=%E8%8C%B6'],
    ['deals', '/deals'],
    ['cart', '/cart'],
    ['checkout', '/checkout'],
    ['pay', `/orders/${process.env.PLACED_ORDER}/pay`],
    ['signin', '/signin'],
  ]);
  // Signed in as the customer whose delivered order is still inside its window.
  await cookie('goen_session', process.env.CUST_TOKEN);
  await capture([['order', `/orders/${process.env.RETURN_FORM_ORDER}`]]);
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
    ['admin-campaigns', '/admin/campaigns'],
    ['admin-products', '/admin/products'],
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
