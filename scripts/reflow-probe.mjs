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

async function shot(path, file, width, opts = {}) {
  await viewport(width);
  await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'light' }, { name: 'forced-colors', value: opts.forced ? 'active' : 'none' }] });
  await navigate(ORIGIN + path);
  if (opts.text200) await evaluate("document.documentElement.style.fontSize='200%'");
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
  if (opts.measure) console.log('MEASURE ' + file + ' ' + JSON.stringify(await evaluate(opts.measure)));
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

const mA = `(() => {
  const left = (e) => { if (!e) return null; const r = e.getBoundingClientRect(); return Math.round((r.left + parseFloat(getComputedStyle(e).paddingLeft)) * 10) / 10; };
  return {
    footCopyrightContentLeft: left(document.querySelector('.goen-adminfoot__copyright')),
    barContentLeft: left(document.querySelector('.goen-adminbar__inner')),
    mainContentLeft: left(document.querySelector('.goen-admin')),
    scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth,
  };
})()`;
const mShip = `(() => {
  const base = ${mA};
  const groups = [...document.querySelectorAll('.goen-admin__fields')].map((g) => {
    const tops = [...g.querySelectorAll(':scope > .goen-admin__field')].map((f) => { const c = f.querySelector('input,select,textarea'); return c ? Math.round(c.getBoundingClientRect().top) : null; });
    return { cols: getComputedStyle(g).gridTemplateColumns.split(' ').length, controlTops: tops };
  });
  const amount = [...document.querySelectorAll('.goen-admin__fields input[name=amount]')].map((i) => Math.round(i.getBoundingClientRect().width));
  const area = [...document.querySelectorAll('.goen-admin__prefixes textarea')].map((t) => ({ rows: t.rows, height: Math.round(t.getBoundingClientRect().height) }));
  const lists = [...document.querySelectorAll('.goen-admin__prefixes ul')].map((u) => { const cs = getComputedStyle(u); return { listStyle: cs.listStyleType, columnWidth: cs.columnWidth, height: Math.round(u.getBoundingClientRect().height) }; });
  return { base, groups, amountWidths: amount, zoneTextareas: area, districtLists: lists };
})()`;
const M = (extra) => `(() => ({ base: ${mA}, extra: ${extra} }))()`;
const mStaff = `(() => ({ emailOccurrences: [...document.querySelectorAll('tbody tr')].map((r) => (r.textContent.match(/[\\w.+-]+@[\\w.-]+/g) || []).join(' | ')) }))()`;
const mTier = `(() => ({ heads: [...document.querySelectorAll('thead th')].map((h) => h.textContent.trim() + ':' + getComputedStyle(h).textAlign), cells: [...((document.querySelector('tbody tr') || { children: [] }).children)].map((c) => getComputedStyle(c).textAlign) }))()`;
const mPick = `(() => ({ empty: (document.querySelector('.goen-admin__emptytitle') || {}).textContent || null, heads: [...document.querySelectorAll('thead th')].map((h) => h.textContent.trim() + ':' + getComputedStyle(h).textAlign) }))()`;
const mHome = `(() => ({ options: [...document.querySelectorAll('.goen-admin__langopt')].map((l) => { const cs = getComputedStyle(l); return { text: l.textContent.trim(), checked: !!l.querySelector('input:checked'), outline: cs.outlineStyle + ' ' + cs.outlineWidth + ' ' + cs.outlineColor }; }) }))()`;
const mStock = `(() => { const n = [...document.querySelectorAll('.goen-admin__num')]; return { numFields: n.length, widths: [...new Set(n.map((i) => Math.round(i.getBoundingClientRect().width)))], clipped: n.filter((i) => i.scrollWidth > i.clientWidth).length, tableWrapScroll: [...document.querySelectorAll('.goen-admin__tablewrap')].map((w) => w.scrollWidth + '/' + w.clientWidth) }; })()`;
const mSku = `(() => ({ hints: [...document.querySelectorAll('.goen-admin__hint')].map((h) => h.textContent.trim()), empty: (document.querySelector('.goen-admin__emptytitle') || {}).textContent || null }))()`;

await viewport(320);
await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'light' }, { name: 'forced-colors', value: 'none' }] });
await navigate(ORIGIN + '/admin/campaigns/layout-campaign');
await evaluate("document.documentElement.style.fontSize='200%'");
await sleep(800);
const out = await evaluate(`(() => {
  const w = document.documentElement.clientWidth;
  const fields = [...document.querySelectorAll('.goen-admin__field')].map((f) => {
    const c = f.querySelector('input,select,textarea');
    return {
      id: c ? c.id || c.name : null,
      control: c ? c.tagName.toLowerCase() + (c.type ? ':' + c.type : '') : null,
      fieldClientWidth: f.clientWidth,
      parentIsFields: f.parentElement.classList.contains('goen-admin__fields'),
      gridTemplateColumns: getComputedStyle(f).gridTemplateColumns,
      controlRight: c ? Math.round(c.getBoundingClientRect().right) : null,
    };
  });
  const wide = [...document.querySelectorAll('main *')].filter((e) => e.getBoundingClientRect().right > w + 1).slice(0, 25).map((e) => e.tagName.toLowerCase() + '.' + String(e.className).slice(0, 50) + '#' + e.id + ' right=' + Math.round(e.getBoundingClientRect().right));
  return { clientWidth: w, scrollWidth: document.documentElement.scrollWidth, fields, wide };
})()`);
console.log('MEASURE campaign-320-text200 ' + JSON.stringify(out));
const { data } = await send('Page.captureScreenshot', { format: 'png' });
writeFileSync(`${outDir}/admin-campaign-editor-320-text200.png`, Buffer.from(data, 'base64'));
ws.close();
