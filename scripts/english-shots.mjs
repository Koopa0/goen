// Probe only: English compare and product pages over CDP.
// Usage: node scripts/english-shots.mjs <outdir>

import { mkdirSync, writeFileSync } from 'node:fs';

const CDP_PORT = Number(process.env.CDP_PORT || 9333);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const CAP = 6000;
const [outDir] = process.argv.slice(2);
const mode = 'storefront';
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
await cookie('goen_locale', 'en');
await metrics(1440, 900);

// Product pages, read in English: the first whose description is Chinese prose.
const candidates = [];
for (const list of ['/', '/deals', '/c/home-living', '/c/tech']) {
  await navigate(ORIGIN + list);
  const hrefs = await evaluate(`[...new Set([...document.querySelectorAll('main a[href^="/p/"]')].map((e) => e.getAttribute('href')))]`);
  for (const h of hrefs) if (!candidates.includes(h)) candidates.push(h);
}
console.log('candidates', candidates.length);
const proseFact = `(() => {
  const p = document.querySelector('.goen-pdp__prose');
  if (!p) return null;
  return { han: /[\u4e00-\u9fff]/.test(p.textContent), lang: p.getAttribute('lang'), text: p.textContent.trim().slice(0, 40) };
})()`;
let untranslated = null;
let translated = null;
for (const href of candidates.slice(0, 40)) {
  await navigate(ORIGIN + href);
  const f = await evaluate(proseFact);
  if (!f) continue;
  if (f.han && !untranslated) untranslated = href;
  if (!f.han && !translated) translated = href;
  if (untranslated && translated) break;
}
console.log('untranslated product', untranslated, 'translated product', translated);

const slugs = candidates.slice(0, 2).map((h) => h.replace('/p/', '').split('?')[0]);
await capture([
  ['compare-empty', '/compare'],
  ['compare-filled', `/compare?p=${slugs[0]}&p=${slugs[1]}`],
  ['product-no-english-description', untranslated],
  ['product-english-description', translated],
]);

for (const width of [1440, 375]) {
  await metrics(width, 900);
  await navigate(ORIGIN + '/compare');
  console.log(`MEASURE compare-empty-${width} hint=${JSON.stringify(await evaluate(`(document.querySelector('.goen-empty__text') || {}).textContent`))} scrollW=${await evaluate('document.documentElement.scrollWidth')} viewport=${width}`);
  if (untranslated) {
    await navigate(ORIGIN + untranslated);
    console.log(`MEASURE product-${width} prose=${JSON.stringify(await evaluate(proseFact))} scrollW=${await evaluate('document.documentElement.scrollWidth')} viewport=${width}`);
  }
}
await navigate(ORIGIN + '/c/tech');
console.log('MEASURE listing compare note=' + JSON.stringify(await evaluate(`(document.querySelector('.goen-compare-pick__note') || {}).textContent || null`)));

ws.close();
if (!untranslated) failures.push('no product with a Chinese-only description was found');
if (failures.length) {
  console.log(`\n${failures.length} failure(s):\n  ${failures.join('\n  ')}`);
  process.exit(1);
}
console.log('all shots captured');
