// Probe only: every place a period renders, shot and measured over CDP.
// Usage: node scripts/period-shots.mjs periods|ink <outdir>
//   periods reads PLACED_TOKEN, PLACED_ORDER, CUST_TOKEN and RETURN_FORM_ORDER
//   (scripts/check-layout.sql); ink shoots the campaign the workflow set to ink.

import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';

const CDP_PORT = Number(process.env.CDP_PORT || 9333);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const CAP = 6000;
const [mode, outDir] = process.argv.slice(2);
const failures = [];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// The layout gate's own period probe, read from this checkout, so "before" is
// held to the rule "after" must pass.
const gate = readFileSync(new URL('./check-layout.mjs', import.meta.url), 'utf8');
const g0 = gate.indexOf('const PERIOD_PROBE = `');
const g1 = gate.indexOf('})()`;', g0);
const GATE_PROBE = eval(gate.slice(g0 + 'const PERIOD_PROBE = '.length, g1 + '})()`'.length));

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

// Each period's box, cells, mark gap and drawn labels, and the space between it
// and the elements above and below it.
const MEASURE = `(() => {
  const r = (e) => { const b = e.getBoundingClientRect(); return [+b.left.toFixed(1), +b.top.toFixed(1), +b.width.toFixed(1), +b.height.toFixed(1)]; };
  const name = (e) => (e.className && typeof e.className === 'string' ? e.className.split(' ')[0] : e.tagName.toLowerCase());
  const periods = [...document.querySelectorAll('.ui-period')].map((p, n) => {
    p.closest('.goen-hero__slide')?.scrollIntoView({ inline: 'start', block: 'nearest', behavior: 'instant' });
    const box = p.getBoundingClientRect();
    const cells = [...p.children];
    const widths = cells.map((c) => c.getBoundingClientRect().width);
    const mark = cells.findIndex((c) => c.hasAttribute('data-mark'));
    const prev = p.previousElementSibling, next = p.nextElementSibling;
    const s = cells.length ? getComputedStyle(cells[0]) : null;
    return {
      n, in: name(p.parentElement), box: r(p), cells: cells.length,
      filled: cells.filter((c) => c.hasAttribute('data-cell')).length,
      cellWidth: widths.length ? [+Math.min(...widths).toFixed(2), +Math.max(...widths).toFixed(2)] : null,
      line: s ? s.borderBottomWidth + ' ' + s.borderBottomStyle + ' ' + s.borderBottomColor : null,
      markGap: mark >= 0 && cells[mark + 1] ? +(cells[mark + 1].getBoundingClientRect().left - cells[mark].getBoundingClientRect().right).toFixed(1) : null,
      labels: [...p.querySelectorAll('b, small')].filter((e) => e.getClientRects().length > 0).map((e) => e.textContent + '@' + r(e).join(',')),
      above: prev ? name(prev) + ' ' + r(prev).join(',') + ' gap ' + (box.top - prev.getBoundingClientRect().bottom).toFixed(1) : null,
      below: next ? name(next) + ' gap ' + (next.getBoundingClientRect().top - box.bottom).toFixed(1) : null,
    };
  });
  const tile = document.querySelector('.goen-tiles__grid > li');
  return {
    scrollWidth: document.documentElement.scrollWidth, viewport: document.documentElement.clientWidth,
    rootFont: getComputedStyle(document.documentElement).fontSize,
    forced: matchMedia('(forced-colors: active)').matches,
    home: document.querySelector('.goen-rowcard') ? 'rowcard' : document.querySelector('.goen-home__facts') ? 'facts' : '-',
    firstTileTop: tile ? +tile.getBoundingClientRect().top.toFixed(1) : null,
    periods,
  };
})()`;

// The rectangle around period n worth a close look: its own block where that is
// a small one, otherwise the period with the elements on either side. A hero
// slide is scrolled into its carousel first.
const closeup = (n) => `(() => {
  const p = document.querySelectorAll('.ui-period')[${n}];
  p.closest('.goen-hero__slide')?.scrollIntoView({ inline: 'start', block: 'nearest', behavior: 'instant' });
  const near = p.closest('.goen-deptnotice, .goen-rowcard, .goen-line--order');
  const parts = near ? [near] : [p.previousElementSibling, p, p.nextElementSibling].filter(Boolean);
  const rs = parts.map((e) => e.getBoundingClientRect());
  const x = Math.max(0, Math.min(...rs.map((b) => b.left)) - 24);
  const y = Math.max(0, Math.min(...rs.map((b) => b.top)) - 16);
  const right = Math.min(document.documentElement.clientWidth, Math.max(...rs.map((b) => b.right)) + 24);
  const bottom = Math.max(...rs.map((b) => b.bottom)) + 16;
  return { x: x + scrollX, y: y + scrollY, width: right - x, height: Math.min(bottom - y, 700) };
})()`;

async function shot(name, path, { width, text200 = false, forced = false, closeups = false }) {
  const suffix = `${width}${text200 ? '-text200' : ''}${forced ? '-forced' : ''}`;
  await media(forced);
  await metrics(width, width === 375 ? 812 : 900);
  await navigate(ORIGIN + path);
  if (text200) await evaluate(`document.documentElement.style.fontSize = '200%'`);
  await evaluate('document.fonts.ready.then(() => 1)', true);
  const firstTileTop = await evaluate(`(() => { const t = document.querySelector('.goen-tiles__grid > li'); return t ? +t.getBoundingClientRect().top.toFixed(1) : null; })()`);
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
  writeFileSync(`${outDir}/${name}-${suffix}.png`, Buffer.from(data, 'base64'));
  if (/404|找不到/.test(h1)) failures.push(`${name}-${suffix}: landed on a not-found page`);
  const measured = await evaluate(MEASURE);
  console.log(`MEASURE ${name} ${suffix} ${path} firstTileTop@${width === 375 ? 812 : 900}=${firstTileTop} ` + JSON.stringify(measured));
  if (closeups) {
    for (let n = 0; n < measured.periods.length; n++) {
      const clip = await evaluate(closeup(n));
      if (!(clip.width > 0 && clip.height > 0)) continue;
      await sleep(150);
      const { data: close } = await send('Page.captureScreenshot', { format: 'png', clip: { ...clip, scale: 2 }, captureBeyondViewport: true });
      writeFileSync(`${outDir}/${name}-${suffix}-period${n}.png`, Buffer.from(close, 'base64'));
    }
  }
  await media(false);
}

// At 320px, as the layout gate measures: the boxes, the labels, and what the
// gate's own period probe says of the page.
async function at320(name, path) {
  for (const text200 of [false, true]) {
    const suffix = `320${text200 ? '-text200' : ''}`;
    await media(false);
    await metrics(320, 800);
    await navigate(ORIGIN + path);
    if (text200) await evaluate(`document.documentElement.style.fontSize = '200%'`);
    await evaluate('document.fonts.ready.then(() => 1)', true);
    await sleep(300);
    console.log(`MEASURE ${name} ${suffix} ${path} ` + JSON.stringify(await evaluate(MEASURE)));
    const got = await evaluate(GATE_PROBE);
    console.log(`GATE ${name} ${suffix} periods=${got.periods} problems=${got.problems.length}` +
      (got.problems.length ? ' ' + JSON.stringify(got.problems.slice(0, 6)) : ' ok'));
    if (!text200) {
      const height = Math.min(CAP, Math.max(await evaluate('Math.ceil(document.documentElement.scrollHeight)'), 400));
      await metrics(320, height);
      await sleep(400);
      const { data } = await send('Page.captureScreenshot', { format: 'png' });
      writeFileSync(`${outDir}/${name}-${suffix}.png`, Buffer.from(data, 'base64'));
    }
  }
  await media(true);
  await metrics(320, 800);
  await navigate(ORIGIN + path);
  await sleep(300);
  const got = await evaluate(GATE_PROBE);
  console.log(`GATE ${name} 320-forced periods=${got.periods} forced=${got.forced} problems=${got.problems.length}` +
    (got.problems.length ? ' ' + JSON.stringify(got.problems.slice(0, 6)) : ' ok'));
  await media(false);
}

async function capture(pages) {
  for (const [name, path] of pages) {
    if (!path) { failures.push(`${name}: no link found`); continue; }
    try {
      await shot(name, path, { width: 1440, closeups: true });
      await shot(name, path, { width: 375, closeups: true });
      await shot(name, path, { width: 375, text200: true });
      await shot(name, path, { width: 1440, forced: true, closeups: true });
      await at320(name, path);
    } catch (e) {
      failures.push(`${name}: ${e.message}`);
    }
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
await cookie('goen_locale', 'zh-Hant');

if (mode === 'periods') {
  if (process.env.PLACED_TOKEN) await cookie('goen_placed', process.env.PLACED_TOKEN);
  await metrics(1440, 900);
  const department = await firstPageWhere('/', '/c/', `!!document.querySelector('.goen-deptnotice .ui-period')`);
  const product = await firstPageWhere('/s/autumn-picks', '/p/', `!!document.querySelector('.goen-pdp__buy > .ui-period')`);
  console.log('department', department, 'product', product);
  await capture([
    ['home', '/'],
    ['campaign', '/s/layout-campaign'],
    ['campaign-long', '/s/autumn-picks'],
    ['department', department],
    ['product', product],
    ['deals', '/deals'],
    ['pay', `/orders/${process.env.PLACED_ORDER}/pay`],
  ]);
  // Signed in as the customer whose delivered order is still inside its window.
  await cookie('goen_session', process.env.CUST_TOKEN);
  await capture([['order', `/orders/${process.env.RETURN_FORM_ORDER}`]]);
} else if (mode === 'ink') {
  for (const [name, path] of [['ink-campaign', '/s/autumn-picks'], ['ink-home', '/']]) {
    try {
      await shot(name, path, { width: 1440, closeups: true });
      await shot(name, path, { width: 375, closeups: true });
    } catch (e) {
      failures.push(`${name}: ${e.message}`);
    }
  }
} else {
  throw new Error(`unknown mode ${mode}`);
}

ws.close();
if (failures.length) {
  console.log(`\n${failures.length} failure(s):\n  ${failures.join('\n  ')}`);
  process.exit(1);
}
console.log('all shots captured');
