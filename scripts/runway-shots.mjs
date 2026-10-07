// Probe only: back office dashboard and report shots over CDP, with measurements.
// Usage: node scripts/runway-shots.mjs <outdir>   (reads ADMIN_TOKEN)

import { mkdirSync, writeFileSync } from 'node:fs';

const CDP_PORT = Number(process.env.CDP_PORT || 9333);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const CAP = 6000;
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
const media = (forced) =>
  send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'light' }, { name: 'forced-colors', value: forced ? 'active' : 'none' }] });
const measure = (label, value) => console.log(`MEASURE ${label} ${JSON.stringify(value)}`);
const cookie = (name, value) => send('Network.setCookie', { name, value, domain: '127.0.0.1', path: '/' });

async function capture(file) {
  const height = Math.min(CAP, Math.max(await evaluate('Math.ceil(document.documentElement.scrollHeight)'), 400));
  const width = await evaluate('window.innerWidth');
  await metrics(width, height);
  await sleep(500);
  const { data } = await send('Page.captureScreenshot', { format: 'png' });
  writeFileSync(`${outDir}/${file}`, Buffer.from(data, 'base64'));
  console.log(`${file} shotHeight=${height}`);
}

async function open(path, width, textPercent) {
  await metrics(width, 900);
  await navigate(ORIGIN + path);
  if (textPercent) await evaluate(`document.documentElement.style.fontSize = '${textPercent}%'`);
  await evaluate('document.fonts.ready.then(() => 1)', true);
  await sleep(500);
  const facts = await evaluate(`({ path: location.pathname, admin: !!document.querySelector('.goen-admin') })`);
  if (!facts.admin || facts.path !== path) failures.push(`${path}: landed on ${facts.path}`);
}

// Every runway row of a list: the name and the days left it reads, if it reads any.
const rowFacts = `(list) => [...list.querySelectorAll('.goen-report__row')].map((r) => {
  const m = r.textContent.match(/(?:約|About)\\s*(\\d+)|(\\d+)\\s*天以上|More than\\s*(\\d+)/);
  const bar = r.querySelector('.goen-chartrangebar');
  return {
    name: (r.querySelector('a') || {}).textContent || null,
    days: m ? Number(m[1] || m[2] || m[3]) : null,
    soldOut: /已售完|sold out/i.test(r.textContent),
    bar: !!bar,
    text: r.textContent.replace(/\\s+/g, ' ').trim().slice(0, 120),
  };
})`;

async function measurePage(tag, path) {
  measure(`${tag} overflow html [scrollWidth, clientWidth]`, await evaluate('[document.documentElement.scrollWidth, document.documentElement.clientWidth]'));
  measure(`${tag} lists [rows, scrollWidth, clientWidth]`, await evaluate(`[...document.querySelectorAll('.goen-report__rows')].map((l) => [l.children.length, l.scrollWidth, l.clientWidth])`));
  measure(`${tag} chart labels cut`, await evaluate(`[...document.querySelectorAll('.goen-chartbar__label')].filter((e) => e.scrollWidth > e.clientWidth).length`));
  measure(`${tag} first row boxes [x, y, w, h]`, await evaluate(`(() => {
    const r = document.querySelector('.goen-report__row');
    if (!r) return null;
    const box = (e) => { if (!e) return null; const b = e.getBoundingClientRect(); return [Math.round(b.left), Math.round(b.top), Math.round(b.width), Math.round(b.height)]; };
    return { row: box(r), name: box(r.querySelector('a, .goen-report__name')), figure: box(r.querySelector('.goen-report__figure')), bar: box(r.querySelector('.goen-chartrangebar, .goen-chartbar, .goen-report__bar')) };
  })()`));
  if (path === '/admin') {
    const runway = await evaluate(`(() => {
      const section = document.querySelector('#runway-heading')?.closest('section');
      if (!section) return null;
      const list = section.querySelector('.goen-report__rows');
      return { rows: list ? (${rowFacts})(list) : [], sentence: list ? null : (section.querySelector('.goen-admin__hint') || {}).textContent };
    })()`);
    measure(`${tag} dashboard runway`, runway && { count: runway.rows.length, daysLeft: runway.rows.map((r) => r.days), names: runway.rows.map((r) => r.name), sentence: runway.sentence });
  } else {
    const stock = await evaluate(`(() => {
      const list = document.querySelector('#stock ~ .goen-report__rows');
      return list ? (${rowFacts})(list) : [];
    })()`);
    measure(`${tag} report stock`, { count: stock.length, soldOut: stock.filter((r) => r.soldOut).length, withBar: stock.filter((r) => r.bar).length, daysLeft: stock.map((r) => r.days), first: stock.slice(0, 3).map((r) => r.text) });
    const sellers = await evaluate(`[...document.querySelectorAll('.goen-report__rows')].map((l) => l.className + ' ' + l.children.length)`);
    measure(`${tag} report list classes and rows`, sellers);
  }
}

async function states(path, base) {
  for (const [width, text, suffix] of [[1440, 0, '1440'], [375, 0, '375'], [375, 200, '375-text200']]) {
    try {
      await open(path, width, text);
      await measurePage(`${base} ${suffix}`, path);
      await capture(`${base}-${suffix}.png`);
    } catch (e) { failures.push(`${base}-${suffix}: ${e.message}`); }
  }
  try {
    await media(true);
    await open(path, 1440, 0);
    await capture(`${base}-1440-forced.png`);
  } catch (e) { failures.push(`${base}-1440-forced: ${e.message}`); }
  await media(false);
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
await media(false);
await send('Emulation.setScrollbarsHidden', { hidden: true });
await send('Network.enable');
await send('Network.clearBrowserCookies');
await cookie('goen_locale', 'zh-Hant');
await cookie('goen_session', process.env.ADMIN_TOKEN);

await states('/admin', 'admin-overview');
await states('/admin/reports', 'admin-report');

ws.close();
if (failures.length) {
  console.log(`\n${failures.length} failure(s):\n  ${failures.join('\n  ')}`);
  process.exit(1);
}
console.log('all shots captured');
