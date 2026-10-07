// Probe only: full-page back office shots over CDP.
// Usage: node scripts/admin-shots.mjs <outdir>   (CDP_PORT, GOEN_URL, ADMIN_TOKEN)

import { mkdirSync, writeFileSync } from 'node:fs';

const CDP_PORT = Number(process.env.CDP_PORT || 9333);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const CAP = 4000;
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
  const m = await evaluate(`(() => {
    const r = (e) => { const b = e.getBoundingClientRect(); return { w: Math.round(b.width * 10) / 10, h: Math.round(b.height * 10) / 10 }; };
    const out = {};
    out.values = [...document.querySelectorAll('.goen-report__value')].map((e) => {
      const cs = getComputedStyle(e);
      const lh = parseFloat(cs.lineHeight) || parseFloat(cs.fontSize) * 1.2;
      return { text: e.textContent.trim(), ...r(e), lines: Math.round(e.getBoundingClientRect().height / lh), fontSize: cs.fontSize, overflowWrap: cs.overflowWrap, scrollW: e.scrollWidth, clientW: e.clientWidth, textW: (() => { const g = document.createRange(); g.selectNodeContents(e); return Math.round(g.getBoundingClientRect().width * 10) / 10; })(), tileW: Math.round(e.closest('.goen-report__figure').getBoundingClientRect().width * 10) / 10, viewport: innerWidth };
    });
    const t = document.querySelector('.goen-admin__stock');
    if (t) {
      const wrap = t.closest('.goen-admin__tablewrap');
      out.table = { ...r(t), minWidth: getComputedStyle(t).minWidth, wrapClient: wrap.clientWidth, wrapScroll: wrap.scrollWidth };
      const wb = wrap.getBoundingClientRect();
      const rel = (e) => { const b = e.getBoundingClientRect(); return { left: Math.round(b.left - wb.left), right: Math.round(b.right - wb.left) }; };
      out.frame = { w: Math.round(wb.width), viewport: innerWidth };
      out.heads = [...t.querySelectorAll('thead th')].map((e) => ({ t: e.textContent.trim(), ...r(e), ...rel(e) }));
      const adj = t.querySelector('tbody tr td:last-child button[type=submit]');
      if (adj) out.adjust = { ...r(adj), ...rel(adj), insideFrame: rel(adj).right <= Math.round(wb.width) };
      out.firstRows = [...t.querySelectorAll('tbody tr')].slice(0, 3).map((tr) => [...tr.children].slice(0, 2).map((td) => {
        const a = td.querySelector('a');
        return { cell: r(td), whiteSpace: getComputedStyle(td).whiteSpace, a: a ? { text: a.textContent.trim(), ...r(a), display: getComputedStyle(a).display } : null };
      }));
    }
    return out;
  })()`);
  console.log('MEASURE ' + file + ' ' + JSON.stringify(m));
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

async function tabMeasure(width) {
  await viewport(width);
  await navigate(ORIGIN + '/admin/stock');
  await sleep(800);
  const found = [];
  for (let i = 0; i < 80; i++) {
    await send('Input.dispatchKeyEvent', { type: 'rawKeyDown', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 });
    await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 });
    const hit = await evaluate(`(() => {
      const a = document.activeElement;
      const t = document.querySelector('.goen-admin__stock');
      if (!a || !t || !a.closest('tbody td:last-child') || !t.contains(a)) return null;
      const wb = t.closest('.goen-admin__tablewrap').getBoundingClientRect();
      const f = a.getBoundingClientRect();
      const pin = a.closest('tr').firstElementChild.getBoundingClientRect();
      return { tag: a.tagName, name: a.name || a.textContent.trim(), left: Math.round(f.left - wb.left), right: Math.round(f.right - wb.left), frameW: Math.round(wb.width), pinnedRight: Math.round(pin.right - wb.left), clearOfPinned: f.left >= pin.right - 1 };
    })()`);
    if (hit) { console.log('MEASURE tab-adjust-' + width + ' ' + JSON.stringify(hit)); break; }
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
await send('Network.setCookie', { name: 'goen_session', value: process.env.ADMIN_TOKEN, domain: '127.0.0.1', path: '/' });
await send('Network.setCookie', { name: 'goen_locale', value: 'zh-Hant', domain: '127.0.0.1', path: '/' });

await viewport(1440);

const pages = [
  ['reports-30', '/admin/reports?days=30'],
  ['reports-90', '/admin/reports?days=90'],
  ['stock', '/admin/stock'],
];
for (const width of [1440, 768, 375, 320]) {
  for (const [name, path] of pages) {
    if (name === 'stock' && (width === 768 || width === 320)) continue;
    if (!path) { failures.push(`admin-${name}-${width}: no link found`); continue; }
    try { await shot(path, `admin-${name}-${width}.png`, width); } catch (e) { failures.push(`admin-${name}-${width}: ${e.message}`); }
  }
}

await tabMeasure(375);
await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'light' }, { name: 'forced-colors', value: 'active' }] });
try { await shot('/admin/stock', 'admin-stock-forced-1440.png', 1440); } catch (e) { failures.push(`forced: ${e.message}`); }

ws.close();
if (failures.length) {
  console.log(`\n${failures.length} failure(s):\n  ${failures.join('\n  ')}`);
  process.exit(1);
}
console.log('all shots captured');
