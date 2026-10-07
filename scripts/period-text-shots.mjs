// Probe only: the four places that say a period in words, shot and measured over CDP.
// Usage: node scripts/period-text-shots.mjs zh-Hant|en <outdir>
//   reads PLACED_TOKEN, PLACED_ORDER, CUST_TOKEN and RETURN_FORM_ORDER (scripts/check-layout.sql).

import { mkdirSync, writeFileSync } from 'node:fs';

const CDP_PORT = Number(process.env.CDP_PORT || 9333);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const CAP = 6000;
const [locale, outDir] = process.argv.slice(2);
const lang = locale === 'en' ? 'en' : 'zh';
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
  ...(forced ? [{ name: 'forced-colors', value: 'active' }] : []),
] });

// The elements that make up each place, in page order.
const PARTS = {
  department: `[document.querySelector('.goen-deptnotice')]`,
  phones: `[document.querySelector('.goen-deptnotice')]`,
  product: `(() => { const s = document.querySelector('.goen-pdp__source'); const p = document.querySelector('.goen-pdp__pricebox');
    const next = s && s.nextElementSibling && s.nextElementSibling.matches('.ui-period') ? s.nextElementSibling : null; return [p, s, next]; })()`,
  pay: `[document.querySelector('.goen-pay .goen-pagehead')]`,
  order: `[[...document.querySelectorAll('.goen-line--order')].find((l) => l.querySelector('.ui-statline--s')) || null]`,
};

// The place's box, its text, its lines, any track inside it, and what a
// department page cares about: the first product's top.
const measure = (place) => `(() => {
  const r = (e) => { const b = e.getBoundingClientRect(); return [+b.left.toFixed(1), +b.top.toFixed(1), +b.width.toFixed(1), +b.height.toFixed(1)]; };
  const parts = (${PARTS[place]}).filter(Boolean);
  const lines = (e) => { const s = getComputedStyle(e); const lh = parseFloat(s.lineHeight) || parseFloat(s.fontSize) * 1.5; return Math.round(e.getBoundingClientRect().height / lh); };
  const text = (sel) => [...document.querySelectorAll(sel)].map((e) => ({ box: r(e), lines: lines(e), text: e.textContent.replace(/\\s+/g, ' ').trim().slice(0, 160),
    colour: getComputedStyle(e).color, bg: getComputedStyle(e).backgroundColor, weight: getComputedStyle(e).fontWeight, size: getComputedStyle(e).fontSize }));
  const tile = document.querySelector('.goen-tiles__grid > li');
  return {
    scrollWidth: document.documentElement.scrollWidth, viewport: document.documentElement.clientWidth,
    rootFont: getComputedStyle(document.documentElement).fontSize,
    forced: matchMedia('(forced-colors: active)').matches,
    firstTileTop: tile ? +tile.getBoundingClientRect().top.toFixed(1) : null,
    parts: parts.map((e) => (e.className || e.tagName) + ' ' + r(e).join(',')),
    tracks: parts.reduce((n, e) => n + (e.matches('.ui-period') ? 1 : e.querySelectorAll('.ui-period').length), 0),
    texts: text('.goen-deptnotice__name, .goen-deptnotice__left, .goen-pdp__source, .goen-pdp__left, .goen-pay__window, .goen-pay__deadline, .goen-pay__hold, .goen-pay .goen-pagehead > .ui-statline, .goen-line--order .ui-statline--s'),
  };
})()`;

const closeup = (place) => `(() => {
  const parts = (${PARTS[place]}).filter(Boolean);
  if (!parts.length) return { width: 0, height: 0 };
  const rs = parts.map((e) => e.getBoundingClientRect());
  const x = Math.max(0, Math.min(...rs.map((b) => b.left)) - 24);
  const y = Math.max(0, Math.min(...rs.map((b) => b.top)) - 16);
  const right = Math.min(document.documentElement.clientWidth, Math.max(...rs.map((b) => b.right)) + 24);
  const bottom = Math.max(...rs.map((b) => b.bottom)) + 16;
  return { x: x + scrollX, y: y + scrollY, width: right - x, height: Math.min(bottom - y, 900) };
})()`;

async function shot(place, path, { width, text200 = false, forced = false }) {
  const suffix = `${lang}-${width}${text200 ? '-text200' : ''}${forced ? '-forced' : ''}`;
  await media(forced);
  const viewport = width === 375 ? 812 : 900;
  await metrics(width, viewport);
  await navigate(ORIGIN + path);
  if (text200) await evaluate(`document.documentElement.style.fontSize = '200%'`);
  await evaluate('document.fonts.ready.then(() => 1)', true);
  await sleep(300);
  const first = await evaluate(measure(place));
  const height = Math.min(CAP, Math.max(await evaluate('Math.ceil(document.documentElement.scrollHeight)'), 400));
  await metrics(width, height);
  for (let i = 0; i < 40; i++) {
    const loading = await evaluate('[...document.images].filter((i) => !i.complete).length');
    if (!loading) break;
    await sleep(250);
  }
  await sleep(500);
  const h1 = await evaluate(`(document.querySelector('h1') || {}).textContent || ''`);
  const { data } = await send('Page.captureScreenshot', { format: 'png' });
  writeFileSync(`${outDir}/${place}-${suffix}.png`, Buffer.from(data, 'base64'));
  if (/404|找不到/.test(h1)) failures.push(`${place}-${suffix}: landed on a not-found page`);
  console.log(`MEASURE ${place} ${suffix} ${path} firstTileTop@${viewport}=${first.firstTileTop} ` + JSON.stringify(first));
  const clip = await evaluate(closeup(place));
  if (clip.width > 0 && clip.height > 0) {
    await sleep(150);
    const { data: close } = await send('Page.captureScreenshot', { format: 'png', clip: { ...clip, scale: 2 }, captureBeyondViewport: true });
    writeFileSync(`${outDir}/${place}-${suffix}-close.png`, Buffer.from(close, 'base64'));
  } else {
    failures.push(`${place}-${suffix}: the place is not on the page`);
  }
  await media(false);
}

async function capture(place, path) {
  if (!path) { failures.push(`${place}: no page found`); return; }
  try {
    await shot(place, path, { width: 1440 });
    await shot(place, path, { width: 375 });
    await shot(place, path, { width: 375, text200: true });
    await shot(place, path, { width: 1440, forced: true });
  } catch (e) {
    failures.push(`${place}: ${e.message}`);
  }
}

// The first link under prefix, from listPath, whose own page satisfies the test.
async function firstPageWhere(listPath, prefix, test, limit = 30) {
  await navigate(ORIGIN + listPath);
  const hrefs = await evaluate(`[...new Set([...document.querySelectorAll('a[href^="${prefix}"]')].map((e) => e.getAttribute('href')).filter((h) => h !== '${prefix}' && !h.startsWith('${prefix}?')))]`);
  for (const href of hrefs.slice(0, limit)) {
    await navigate(ORIGIN + href);
    if (await evaluate(test)) return href;
  }
  return null;
}

const cookie = (name, value) => send('Network.setCookie', { name, value, domain: '127.0.0.1', path: '/' });

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
await media(false);
await send('Emulation.setScrollbarsHidden', { hidden: true });
await send('Network.enable');
await send('Network.clearBrowserCookies');
await cookie('goen_locale', locale);
if (process.env.PLACED_TOKEN) await cookie('goen_placed', process.env.PLACED_TOKEN);
await metrics(1440, 900);

// ONLY=pay shoots the pay page alone, for a run with a test payment key, where a payment can start.
if (process.env.ONLY === 'pay') {
  await capture('pay', `/orders/${process.env.PLACED_ORDER}/pay`);
} else {
  const department = await firstPageWhere('/', '/c/', `!!document.querySelector('.goen-deptnotice')`);
  const product = await firstPageWhere('/s/autumn-picks', '/p/', `!!document.querySelector('.goen-pdp__source')`);
  console.log('department', department, 'product', product);
  await capture('department', department);
  // The layout gate holds this page's first product inside a phone's first screen.
  if (department !== '/c/phones') await capture('phones', '/c/phones');
  await capture('product', product);
  await capture('pay', `/orders/${process.env.PLACED_ORDER}/pay`);
  // Signed in as the customer whose delivered order carries a registered warranty.
  await cookie('goen_session', process.env.CUST_TOKEN);
  await capture('order', `/orders/${process.env.RETURN_FORM_ORDER}`);
}

ws.close();
if (failures.length) {
  console.log(`\n${failures.length} failure(s):\n  ${failures.join('\n  ')}`);
  process.exit(1);
}
console.log('all shots captured');
