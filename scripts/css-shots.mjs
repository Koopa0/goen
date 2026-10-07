// Probe only: storefront shots over CDP.
// Usage: node scripts/css-shots.mjs <outdir>   (CDP_PORT, GOEN_URL, CART_TOKEN, PLACED_TOKEN, PLACED_ORDER)

import { mkdirSync, writeFileSync } from 'node:fs';

const CDP_PORT = Number(process.env.CDP_PORT || 9333);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const CAP = 6000;
const outDir = process.argv[2];
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

async function shot(path, file, width) {
  await send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: width < 600 });
  await navigate(ORIGIN + path);
  await evaluate('document.fonts.ready.then(() => 1)', true);
  for (let i = 0; i < 40; i++) {
    const loading = await evaluate('[...document.images].filter((i) => !i.complete).length');
    if (!loading) break;
    await sleep(250);
  }
  await sleep(800);
  const facts = await evaluate(`({
    path: location.pathname + location.search,
    h1: (document.querySelector('h1') || {}).textContent || '',
    height: Math.ceil(document.documentElement.scrollHeight),
    lang: document.documentElement.lang,
    scrollbar: innerWidth - document.documentElement.clientWidth,
  })`);
  const height = Math.min(CAP, Math.max(facts.height, 400));
  await send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: width < 600 });
  await sleep(400);
  const { data } = await send('Page.captureScreenshot', { format: 'png' });
  writeFileSync(`${outDir}/${file}`, Buffer.from(data, 'base64'));
  console.log(`${file} ${facts.path} h1=${JSON.stringify(facts.h1.trim())} lang=${facts.lang} pageHeight=${facts.height} shotHeight=${height}`);
  if (/404|找不到/.test(facts.h1)) failures.push(`${file}: landed on a not-found page`);
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
const cookie = (name, value) => send('Network.setCookie', { name, value, domain: '127.0.0.1', path: '/' });
await cookie('goen_locale', 'zh-Hant');
if (process.env.CART_TOKEN) await cookie('goen_cart', process.env.CART_TOKEN);
if (process.env.PLACED_TOKEN) await cookie('goen_placed', process.env.PLACED_TOKEN);

const pages = [
  ['home', '/'],
  ['dept-home-living', '/c/home-living'],
  ['dept-beauty', '/c/beauty'],
  ['listing-tea-coffee', '/c/tea-coffee'],
  ['search', '/search?q=%E8%8C%B6'],
  ['pdp-watch', '/p/meridian-watch-c1'],
  ['cart', '/cart'],
  ['pay', `/orders/${process.env.PLACED_ORDER}/pay`],
];
for (const width of [1440, 375]) {
  for (const [name, path] of pages) {
    try { await shot(path, `${name}-${width}.png`, width); } catch (e) { failures.push(`${name}-${width}: ${e.message}`); }
  }
}

ws.close();
if (failures.length) {
  console.log(`\n${failures.length} failure(s):\n  ${failures.join('\n  ')}`);
  process.exit(1);
}
console.log('all shots captured');
