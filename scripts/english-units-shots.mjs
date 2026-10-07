// Probe only: fact-line shots over CDP, zh and English, 1440 and 375.
// Usage: node scripts/english-units-shots.mjs <outdir>
//   storefront reads CART_TOKEN, PLACED_TOKEN, PLACED_ORDER, CUST_TOKEN and
//   RETURN_FORM_ORDER (scripts/check-layout.sql); admin reads ADMIN_TOKEN.

import { mkdirSync, writeFileSync } from 'node:fs';

const CDP_PORT = Number(process.env.CDP_PORT || 9333);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const CAP = 6000;
const mode = 'storefront';
const [outDir] = process.argv.slice(2);
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

// The first link under prefix, from listPath, whose own page satisfies the test.
async function firstPageWhere(listPath, prefix, test, limit = 20) {
  await navigate(ORIGIN + listPath);
  const hrefs = await evaluate(`[...new Set([...document.querySelectorAll('main a[href^="${prefix}"]')].map((e) => e.getAttribute('href')).filter((h) => h !== '${prefix}' && !h.startsWith('${prefix}?')))]`);
  for (const href of hrefs.slice(0, limit)) {
    await navigate(ORIGIN + href);
    if (await evaluate(test)) return href;
  }
  return null;
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
await send('Network.setCookie', { name: 'goen_locale', value: 'zh-Hant', domain: '127.0.0.1', path: '/' });
const pages = [['home', '/'], ['department', '/c/home-living'], ['department-tech', '/c/tech'], ['deals', '/deals']];
for (const locale of ['zh-Hant', 'en']) {
  await cookie('goen_locale', locale);
  for (const width of [1440, 375]) {
    for (const [name, path] of pages) {
      try {
        await shot(path, `${name}-${locale}-${width}.png`, width);
        const facts = await evaluate(`[...document.querySelectorAll('dl.ui-statline')].map((d) => d.innerText.replace(/\\s+/g, ' ').trim())`);
        console.log(`MEASURE ${name} ${locale} ${width} scrollWidth=${await evaluate('document.documentElement.scrollWidth')} facts=${JSON.stringify(facts)}`);
      } catch (e) { failures.push(`${name}-${locale}-${width}: ${e.message}`); }
    }
  }
}
if (failures.length) { console.error(failures.join('\n')); process.exit(1); }
process.exit(0);
