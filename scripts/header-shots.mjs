// Probe only: header crops and measurements over CDP.
// Usage: node scripts/header-shots.mjs <outdir>   (reads ADMIN_TOKEN, GOEN_URL, CDP_PORT)

import { mkdirSync, writeFileSync } from 'node:fs';

const CDP_PORT = Number(process.env.CDP_PORT || 9333);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
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

// Device emulation, not --window-size: only this makes a 320 or 375 CSS-px layout viewport.
const metrics = (width, height) =>
  send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: width < 600 });
const cookie = (name, value) => send('Network.setCookie', { name, value, domain: '127.0.0.1', path: '/' });
const forcedColours = (on) =>
  send('Emulation.setEmulatedMedia', {
    features: [
      { name: 'prefers-color-scheme', value: 'light' },
      { name: 'forced-colors', value: on ? 'active' : 'none' },
    ],
  });

// The cart count is written into the page, so every count is the same markup the server
// renders; only the number differs.
const setCount = (n) => evaluate(`(() => {
  const link = document.querySelector('#cart-link');
  let badge = link.querySelector('.ui-badge--count');
  if (${n} === 0) { if (badge) badge.remove(); return 0; }
  if (!badge) {
    badge = document.createElement('span');
    badge.className = 'ui-badge--count';
    badge.setAttribute('aria-hidden', 'true');
    link.appendChild(badge);
  }
  badge.textContent = String(${n});
  return ${n};
})()`);

const rect = (sel) => `(() => { const e = document.querySelector(${JSON.stringify(sel)}); if (!e) return null; const r = e.getBoundingClientRect(); return { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width * 10) / 10, h: Math.round(r.height * 10) / 10 }; })()`;

async function headerClip() {
  const r = await evaluate(`(() => { const e = document.querySelector('.goen-header'); const b = e.getBoundingClientRect(); return { x: 0, y: 0, width: Math.max(innerWidth, document.documentElement.scrollWidth), height: Math.ceil(b.bottom) + 8 }; })()`);
  return { ...r, scale: 1 };
}

async function crop(file, clip) {
  const { data } = await send('Page.captureScreenshot', { format: 'png', clip, captureBeyondViewport: true });
  writeFileSync(`${outDir}/${file}`, Buffer.from(data, 'base64'));
}

async function measure(tag) {
  const m = await evaluate(`(() => {
    const px = (sel) => { const e = document.querySelector(sel); if (!e) return null; const r = e.getBoundingClientRect(); return r.width.toFixed(1) + 'x' + r.height.toFixed(1); };
    const svg = (sel) => {
      const e = document.querySelector(sel);
      if (!e) return null;
      const r = e.getBoundingClientRect(); const s = getComputedStyle(e);
      return r.width.toFixed(1) + 'x' + r.height.toFixed(1) + ' stroke=' + s.strokeWidth + ' color=' + s.color;
    };
    const bar = document.querySelector('.goen-header__bar');
    const first = bar.firstElementChild && bar.firstElementChild.getBoundingClientRect();
    const acts = document.querySelector('.goen-header__actions').getBoundingClientRect();
    const search = document.querySelector('.goen-header__search').getBoundingClientRect();
    const count = document.querySelector('.goen-header__cart .ui-badge--count');
    const cs = count && getComputedStyle(count);
    const admin = document.querySelector('.goen-header__action--wide[href="/admin"]');
    const as = admin && getComputedStyle(admin);
    return JSON.stringify({
      innerWidth, docScrollWidth: document.documentElement.scrollWidth,
      barScrollWidth: bar.scrollWidth, barClientWidth: bar.clientWidth,
      actionsWidth: +acts.width.toFixed(1), searchWidth: +search.width.toFixed(1),
      barUsedRight: +acts.right.toFixed(1),
      menuIcon: svg('.goen-header__menu > summary svg'),
      menuTarget: px('.goen-header__menu > summary'),
      langIcon: svg('.goen-header__actions .goen-langmenu svg'),
      langTarget: px('.goen-header__actions .goen-langmenu > summary'),
      userIcon: svg('.goen-header__actions a[href="/account"] svg'),
      userTarget: px('.goen-header__actions a[href="/account"]'),
      heartIcon: svg('.goen-header__action--wide[href="/account/wishlist"] svg'),
      heartTarget: px('.goen-header__action--wide[href="/account/wishlist"]'),
      adminTarget: px('.goen-header__action--wide[href="/admin"]'),
      adminText: admin ? admin.textContent.trim() + ' ' + as.fontSize + ' ' + as.color : null,
      cartIcon: svg('.goen-header__cart svg'),
      cartTarget: px('#cart-link'),
      count: count ? count.textContent + ' ' + cs.fontSize + ' w' + cs.fontWeight + ' ' + cs.color + ' bg=' + cs.backgroundColor + ' pos=' + cs.position : null,
    });
  })()`);
  console.log(`MEASURE ${tag} ${m}`);
  const o = JSON.parse(m);
  if (o.docScrollWidth > o.innerWidth) failures.push(`${tag}: page scrolls sideways (${o.docScrollWidth} > ${o.innerWidth})`);
}

async function state({ file, width, locale, staff, count = 1, forced = false, big = false, path = '/' }) {
  await cookie('goen_locale', locale);
  if (staff) await cookie('goen_session', process.env.ADMIN_TOKEN);
  else await send('Network.deleteCookies', { name: 'goen_session', domain: '127.0.0.1', path: '/' });
  await forcedColours(forced);
  await metrics(width, 900);
  await navigate(ORIGIN + path);
  await evaluate('document.fonts.ready.then(() => 1)', true);
  await setCount(count);
  if (big) await evaluate(`document.documentElement.style.fontSize = '200%'`);
  await sleep(250);
  const tag = `${file} ${width}px ${locale} ${staff ? 'staff' : 'shopper'} count=${count}${forced ? ' forced' : ''}${big ? ' 200%' : ''}`;
  await measure(tag);
  await crop(`${file}.png`, await headerClip());
}

async function focusWalk(width, locale, staff) {
  await cookie('goen_locale', locale);
  if (staff) await cookie('goen_session', process.env.ADMIN_TOKEN);
  else await send('Network.deleteCookies', { name: 'goen_session', domain: '127.0.0.1', path: '/' });
  await forcedColours(false);
  await metrics(width, 900);
  await navigate(ORIGIN + '/');
  await setCount(12);
  await evaluate('document.activeElement && document.activeElement.blur(); window.scrollTo(0, 0)');
  for (let i = 0; i < 14; i++) {
    for (const type of ['rawKeyDown', 'keyUp']) {
      await send('Input.dispatchKeyEvent', { type, key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 });
    }
    const f = await evaluate(`(() => {
      const e = document.activeElement; if (!e || !e.closest('.goen-header__bar')) return null;
      const s = getComputedStyle(e); const r = e.getBoundingClientRect();
      return JSON.stringify({ n: e.tagName + '.' + String(e.className).split(' ')[0], name: e.getAttribute('aria-label') || e.textContent.trim().slice(0, 20),
        ring: e.matches(':focus-visible') + ' ' + s.outlineStyle + ' ' + s.outlineWidth + ' ' + s.outlineColor,
        box: Math.round(r.x) + ',' + Math.round(r.y) + ' ' + r.width.toFixed(1) + 'x' + r.height.toFixed(1) });
    })()`);
    if (!f) continue;
    const o = JSON.parse(f);
    console.log(`MEASURE focus ${width}px ${locale} ${staff ? 'staff' : 'shopper'} #${i} ${f}`);
    if (!o.ring.startsWith('true solid')) failures.push(`focus ${width} ${o.n}: ${o.ring}`);
    const safe = o.n.replace(/[^a-z0-9]+/gi, '-');
    await crop(`focus-${width}-${locale}-${staff ? 'staff' : 'shopper'}-${i}-${safe}.png`, await headerClip());
    if (o.n.includes('goen-header__cart') || o.n.includes('cart')) break;
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
await send('Emulation.setScrollbarsHidden', { hidden: true });
await send('Network.enable');
await send('Network.clearBrowserCookies');

const widths = [320, 375, 640, 768, 1023, 1024, 1280, 1440];
const run = async (job) => { try { await state(job); } catch (e) { failures.push(`${job.file}: ${e.message}`); } };

for (const locale of ['zh-Hant', 'en']) {
  const l = locale === 'en' ? 'en' : 'zh';
  for (const staff of [false, true]) {
    const r = staff ? 'staff' : 'shopper';
    for (const width of widths) await run({ file: `header-${width}-${l}-${r}`, width, locale, staff });
    for (const width of [320, 1280]) {
      for (const count of [0, 12, 100, 12345, 123456]) await run({ file: `header-${width}-${l}-${r}-count${count}`, width, locale, staff, count });
    }
    for (const width of [375, 1280]) await run({ file: `header-${width}-${l}-${r}-forced`, width, locale, staff, count: 12, forced: true });
    for (const width of widths) await run({ file: `header-${width}-${l}-${r}-200pct`, width, locale, staff, count: 12, big: true });
  }
}
await forcedColours(false);

// Keyboard focus on each control, at the phone and the desk, for a shopper and for staff.
for (const [width, staff] of [[375, false], [1280, false], [1280, true]]) {
  for (const locale of ['zh-Hant', 'en']) {
    try { await focusWalk(width, locale, staff); } catch (e) { failures.push(`focus ${width}: ${e.message}`); }
  }
}

// The phone menu with a current department, to see the underline beside its count.
for (const locale of ['zh-Hant', 'en']) {
  try {
    await cookie('goen_locale', locale);
    await metrics(375, 800);
    await navigate(ORIGIN + '/c/home-living');
    await evaluate(`document.querySelector('.goen-header__menu > summary').click()`);
    await sleep(300);
    await crop(`drawer-375-${locale === 'en' ? 'en' : 'zh'}.png`, { x: 0, y: 0, width: 375, height: 520, scale: 1 });
    console.log('MEASURE drawer ' + locale + ' ' + await evaluate(`JSON.stringify([...document.querySelectorAll('.goen-header__drawer .ui-navitem[aria-current="page"]')].map((a) => ({ link: a.getBoundingClientRect().width.toFixed(1), name: (a.querySelector('.goen-header__navname') || a).getBoundingClientRect().width.toFixed(1), count: (a.querySelector('small') || {textContent: ''}).textContent })))`));
  } catch (e) { failures.push(`drawer ${locale}: ${e.message}`); }
}

// The heart on the product page and the wishlist, unsaved then saved, signed in as a customer.
for (const width of [1440, 375]) {
  try {
    await cookie('goen_locale', 'zh-Hant');
    await cookie('goen_session', process.env.CUST_TOKEN);
    await metrics(width, 900);
    await navigate(ORIGIN + '/p/meridian-watch-c1');
    const wish = `document.querySelector('form[action="/account/wishlist"]')`;
    for (const phase of ['unsaved', 'saved']) {
      if (phase === 'saved') {
        await evaluate(`${wish}.requestSubmit()`);
        await sleep(1500);
        await navigate(ORIGIN + '/p/meridian-watch-c1');
      }
      await evaluate(`${wish}.scrollIntoView({ block: 'center' })`);
      await sleep(300);
      const box = await evaluate(`(() => { const r = ${wish}.getBoundingClientRect(); return { x: Math.max(0, r.x - 20), y: scrollY + r.y - 20, width: Math.min(innerWidth, r.width + 40), height: r.height + 40 }; })()`);
      console.log(`MEASURE heart-${width}-${phase} ${JSON.stringify(box)} pressed=${await evaluate(`${wish}.querySelector('button').getAttribute('aria-pressed')`)}`);
      await crop(`pdp-heart-${width}-${phase}.png`, { ...box, scale: 1 });
    }
    await metrics(width, 900);
    await navigate(ORIGIN + '/account/wishlist');
    await sleep(400);
    await crop(`wishlist-${width}.png`, { x: 0, y: 0, width, height: 700, scale: 1 });
    await send('Network.deleteCookies', { name: 'goen_session', domain: '127.0.0.1', path: '/' });
  } catch (e) { failures.push(`heart ${width}: ${e.message}`); }
}

// The buy button and the buy bar with the bag.
for (const [width, name] of [[1440, 'pdp-1440'], [375, 'pdp-375']]) {
  try {
    await cookie('goen_locale', 'zh-Hant');
    await metrics(width, 900);
    await navigate(ORIGIN + '/p/meridian-watch-c1');
    await evaluate(`document.querySelector('#add-to-cart')?.scrollIntoView({ block: 'center' })`);
    await sleep(400);
    const box = await evaluate(rect('#add-to-cart'));
    console.log(`MEASURE ${name} buy button ${JSON.stringify(box)} icon ${await evaluate(rect('#add-to-cart svg'))}`);
    await crop(`${name}-buybox.png`, { x: 0, y: Math.max(0, await evaluate('scrollY') + box.y - 120), width, height: 260, scale: 1 });
    const bar = await evaluate(rect('.goen-buybar__btn'));
    if (bar && width === 375) {
      console.log(`MEASURE ${name} buybar button ${JSON.stringify(bar)} icon ${await evaluate(rect('.goen-buybar__btn svg'))}`);
      const vh = await evaluate('innerHeight');
      await crop(`${name}-buybar.png`, { x: 0, y: await evaluate('scrollY') + vh - 120, width, height: 120, scale: 1 });
    }
  } catch (e) { failures.push(`${name}: ${e.message}`); }
}

ws.close();
if (failures.length) {
  console.log(`\n${failures.length} failure(s):\n  ${failures.join('\n  ')}`);
  process.exit(1);
}
console.log('all shots captured');
