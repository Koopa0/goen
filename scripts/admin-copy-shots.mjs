// Probe only: full-page back office shots over CDP.
// Usage: node scripts/admin-shots.mjs <outdir>   (CDP_PORT, GOEN_URL, ADMIN_TOKEN)

import { mkdirSync, writeFileSync } from 'node:fs';

const CDP_PORT = Number(process.env.CDP_PORT || 9333);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const CAP = 16000;
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

async function viewport(width) {
  await send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: width < 600 });
}

async function shot(path, file, width) {
  await viewport(width);
  await navigate(ORIGIN + path);
  await evaluate('document.fonts.ready.then(() => 1)', true);
  for (let i = 0; i < 40; i++) {
    const loading = await evaluate('[...document.images].filter((i) => !i.complete).length');
    if (!loading) break;
    await sleep(250);
  }
  await sleep(800);
  const facts = await evaluate(`({
    path: location.pathname,
    admin: !!document.querySelector('.goen-admin'),
    h1: (document.querySelector('h1') || {}).textContent || '',
    rows: document.querySelectorAll('table tbody tr').length,
    height: Math.ceil(document.documentElement.scrollHeight),
    lang: document.documentElement.lang,
  })`);
  const height = Math.min(CAP, Math.max(facts.height, 400));
  await send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: width < 600 });
  await sleep(400);
  const { data } = await send('Page.captureScreenshot', { format: 'png' });
  writeFileSync(`${outDir}/${file}`, Buffer.from(data, 'base64'));
  console.log(`${file} ${path} h1=${JSON.stringify(facts.h1.trim())} rows=${facts.rows} admin=${facts.admin} lang=${facts.lang} pageHeight=${facts.height} shotHeight=${height}`);
  if (!facts.admin || (!facts.path.startsWith(path.split('?')[0]))) {
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
await send('Network.setCookie', { name: 'goen_session', value: process.env.ADMIN_TOKEN, domain: '127.0.0.1', path: '/' });
await send('Network.setCookie', { name: 'goen_locale', value: 'zh-Hant', domain: '127.0.0.1', path: '/' });

async function measure(name) {
  const m = await evaluate(`({
    scrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
    placeholders: [...document.querySelectorAll('main input[placeholder], main textarea[placeholder]')].map((e) => e.getAttribute('placeholder')),
    hints: [...document.querySelectorAll('main .goen-admin__hint')].map((e) => e.textContent.replace(/\\s+/g, ' ').trim()).filter((t) => t.startsWith('For example') || t.startsWith('例如')),
    headings: [...document.querySelectorAll('main h2')].map((e) => e.textContent.replace(/\\s+/g, ' ').trim()).slice(0, 4),
  })`);
  console.log(`MEASURE ${name} ${JSON.stringify(m)}`);
}
await send('Network.setCookie', { name: 'goen_locale', value: 'en', domain: '127.0.0.1', path: '/' });
await shot('/admin/shipping', 'en-admin-shipping-375.png', 375);
await measure('shipping-en-375');
await viewport(375);
await navigate(ORIGIN + '/admin/shipping');
await evaluate("document.documentElement.style.fontSize = '200%'");
await sleep(500);
await measure('shipping-en-375-200pct');
const full = await evaluate('Math.ceil(document.documentElement.scrollHeight)');
await send('Emulation.setDeviceMetricsOverride', { width: 375, height: Math.min(CAP, full), deviceScaleFactor: 1, mobile: true });
await sleep(400);
{
  const { data } = await send('Page.captureScreenshot', { format: 'png' });
  writeFileSync(`${outDir}/en-admin-shipping-375-200pct.png`, Buffer.from(data, 'base64'));
}

ws.close();
if (failures.length) {
  console.log(`\n${failures.length} failure(s):\n  ${failures.join('\n  ')}`);
  process.exit(1);
}
console.log('all shots captured');
