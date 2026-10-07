// Probe only: storefront control shots and measurements over CDP.
// Usage: node scripts/controls-shots.mjs <outdir>

import { appendFileSync, mkdirSync, writeFileSync } from 'node:fs';

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

// Device emulation, so 375 is a phone viewport and not a window of that size.
const metrics = (width, height) =>
  send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: width < 600 });

const cookie = (name, value) => send('Network.setCookie', { name, value, domain: '127.0.0.1', path: '/' });

const media = (forced) => send('Emulation.setEmulatedMedia', {
  features: [
    { name: 'prefers-color-scheme', value: 'light' },
    { name: 'forced-colors', value: forced ? 'active' : 'none' },
  ],
});

function note(line) {
  console.log(line);
  appendFileSync(`${outDir}/measures.txt`, line + '\n');
}

// Box, weight, ground and borders of the first element matching each selector.
async function measure(label, selectors) {
  for (const sel of selectors) {
    const got = await evaluate(`(() => {
      const e = document.querySelector(${JSON.stringify(sel)});
      if (!e) return null;
      const r = e.getBoundingClientRect();
      const s = getComputedStyle(e);
      return {
        x: +r.x.toFixed(1), y: +r.y.toFixed(1), w: +r.width.toFixed(1), h: +r.height.toFixed(1),
        weight: s.fontWeight, size: s.fontSize, color: s.color, bg: s.backgroundColor,
        border: [s.borderTopWidth, s.borderRightWidth, s.borderBottomWidth, s.borderLeftWidth].join(' '),
        outline: s.outlineStyle + ' ' + s.outlineWidth, radius: s.borderRadius,
      };
    })()`);
    note(`MEASURE ${label} ${sel} ${JSON.stringify(got)}`);
  }
}

async function overflow(label) {
  const got = await evaluate(`({
    scrollW: document.documentElement.scrollWidth,
    clientW: document.documentElement.clientWidth,
    rootFont: getComputedStyle(document.documentElement).fontSize,
  })`);
  note(`MEASURE ${label} overflow ${JSON.stringify(got)}`);
  if (got.scrollW > got.clientW) failures.push(`${label}: scrollW ${got.scrollW} > viewport ${got.clientW}`);
}

async function settle(width) {
  await evaluate('document.fonts.ready.then(() => 1)', true);
  const height = Math.min(CAP, Math.max(await evaluate('Math.ceil(document.documentElement.scrollHeight)'), 400));
  await metrics(width, height);
  for (let i = 0; i < 40; i++) {
    const loading = await evaluate('[...document.images].filter((i) => !i.complete).length');
    if (!loading) break;
    await sleep(250);
  }
  await sleep(500);
  return height;
}

async function save(file, clip) {
  const { data } = await send('Page.captureScreenshot', clip ? { format: 'png', clip: { ...clip, scale: 1 } } : { format: 'png' });
  writeFileSync(`${outDir}/${file}`, Buffer.from(data, 'base64'));
}

// A full-page shot. `prepare` runs after load, before the shot (open a menu,
// grow the text, focus a field).
async function shot(name, path, width, { text200 = false, forced = false, prepare, measures = [], clip, clipSel, tag = '' } = {}) {
  const file = `${name}${tag}-${width}${text200 ? '-text200' : ''}${forced ? '-forced' : ''}.png`;
  try {
    await media(forced);
    await metrics(width, 900);
    await navigate(ORIGIN + path);
    if (text200) await evaluate(`document.documentElement.style.fontSize = '200%'`);
    if (prepare) await prepare();
    const height = await settle(width);
    const facts = await evaluate(`({
      path: location.pathname + location.search,
      h1: (document.querySelector('h1') || {}).textContent || '',
      lang: document.documentElement.lang,
    })`);
    await measure(file, measures);
    if (text200 || width === 375) await overflow(file);
    let region = clip && { x: 0, y: 0, width, height: Math.min(clip, height) };
    if (clipSel) {
      const r = await evaluate(`(() => { const e = document.querySelector(${JSON.stringify(clipSel)}); if (!e) return null; const b = e.getBoundingClientRect(); return { x: Math.max(0, b.x - 24), y: Math.max(0, b.y + scrollY - 24), width: Math.min(innerWidth, b.width + 48), height: b.height + 48 }; })()`);
      if (r) region = r; else failures.push(`${file}: ${clipSel} not found`);
    }
    await save(file, region);
    console.log(`${file} ${facts.path} h1=${JSON.stringify(facts.h1.trim())} lang=${facts.lang} shotHeight=${height}`);
    if (/404|找不到/.test(facts.h1)) failures.push(`${file}: landed on a not-found page`);
  } catch (e) {
    failures.push(`${file}: ${e.message}`);
  } finally {
    await media(false);
  }
}

const focusField = (sel) => async () => {
  await evaluate(`document.querySelector(${JSON.stringify(sel)})?.focus({ focusVisible: true })`);
  await send('Input.dispatchKeyEvent', { type: 'rawKeyDown', key: 'Shift', code: 'ShiftLeft', windowsVirtualKeyCode: 16 });
};

const HOME = [
  '.goen-hero__tabs a:not([aria-current])',
  '.goen-hero__tabs a[aria-current]',
  '#site-search',
  '#newsletter-email',
  '#newsletter-submit',
];
const DEPARTMENT = [
  '.goen-listing__head .goen-pagehead__chip',
  '.goen-listing__head .goen-pagehead__chip[aria-current]',
  '.goen-listing__filters',
  '.goen-filters__shell-summary',
  '.goen-filters__summary',
  '.goen-filters__selected',
  '#sort',
  '.goen-listing__count',
  '#site-search',
];
const SEARCH = ['.goen-search-sort', '.goen-search-sort .goen-listing__count', '#sort', '#site-search'];

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
await cookie('goen_locale', 'zh-Hant');

await metrics(1440, 900);
await navigate(ORIGIN + '/c/home-living');
const subcategory = await evaluate(`document.querySelector('.goen-pagehead__chips a')?.getAttribute('href') || null`);
console.log('subcategory', subcategory);
const search = '/search?q=%E8%8C%B6';

// Open the first filter group, or the phone's sheet, so the toolbar shows its panel.
const openFilters = async () => {
  await evaluate(`(() => {
    const shell = document.querySelector('.goen-filters__shell');
    const sum = document.querySelector('.goen-filters__shell-summary');
    if (sum && getComputedStyle(sum).display !== 'none' && shell) shell.open = true;
    const group = document.querySelector('.goen-filters__group');
    if (group) group.open = true;
  })()`);
};

for (const width of [1440, 375]) {
  // The pages, at rest.
  await shot('home', '/', width, { measures: HOME });
  await shot('department', '/c/home-living', width, { measures: DEPARTMENT });
  if (subcategory) await shot('subcategory', subcategory, width, { measures: DEPARTMENT });
  await shot('search', search, width, { measures: SEARCH });
  // The toolbar with a group open.
  await shot('department-filters-open', '/c/home-living', width, { prepare: openFilters, measures: DEPARTMENT });
  // The header search: unfocused and focused, the header strip only.
  await shot('header-search', '/', width, { clip: 260, measures: ['#site-search', '.goen-header__search'] });
  await shot('header-search-focus', '/', width, { clip: 260, prepare: focusField('#site-search'), measures: ['#site-search'] });
}

// Additions: filters on, footer field, focus, phone menu, more widths.
await navigate(ORIGIN + '/c/phones');
const pairs = await evaluate(`[...document.querySelectorAll('.goen-filters input[type=checkbox]')].map((i) => encodeURIComponent(i.name) + '=' + encodeURIComponent(i.value))`);
console.log('filter checkboxes', pairs.length);
const picks = { 1: pairs.slice(0, 1), 2: pairs.slice(0, 2), many: pairs.slice(0, 12) };
for (const [label, list] of Object.entries(picks)) {
  if (!list.length) { failures.push(`no filter options for ${label}`); continue; }
  const path = '/c/phones?' + list.join('&');
  for (const width of [1440, 375]) await shot('department-on-' + label, path, width, { measures: DEPARTMENT, prepare: openFilters });
  await shot('department-on-' + label, path, 375, { text200: true, measures: DEPARTMENT, prepare: openFilters });
  await shot('department-on-' + label, path, 1440, { forced: true, measures: DEPARTMENT });
}
const longAddress = 'a-rather-long-typed-address-for-the-footer@example-company-name.example.com';
const typeAddress = async () => evaluate(`(() => { const i = document.querySelector('#newsletter-email'); i.value = ${JSON.stringify(longAddress)}; return true; })()`);
for (const [locale, tag] of [['zh-Hant', ''], ['en', '-en']]) {
  await cookie('goen_locale', locale);
  await shot('footer-field' + tag, '/', 375, { text200: true, prepare: typeAddress, clipSel: '.goen-footer__news', measures: ['#newsletter-email', '#newsletter-submit'] });
  await shot('footer-field-long' + tag, '/', 1440, { prepare: typeAddress, clipSel: '.goen-footer__news', measures: ['#newsletter-email', '#newsletter-submit'] });
  await shot('footer-field-long' + tag, '/', 320, { prepare: typeAddress, clipSel: '.goen-footer__news', measures: ['#newsletter-email', '#newsletter-submit'] });
  await shot('footer-subscribe-focus' + tag, '/', 1440, { prepare: focusField('#newsletter-submit'), clipSel: '.goen-footer__news' });
}
await cookie('goen_locale', 'zh-Hant');
await shot('sort-focus', '/c/home-living', 1440, { prepare: focusField('#sort'), clipSel: '.goen-listing__filters' });
await shot('sort-focus', '/c/home-living', 375, { prepare: async () => { await openFilters(); await focusField('#sort')(); }, clipSel: '.goen-listing__filters' });
const openMenu = async () => { await evaluate(`(() => { const m = document.querySelector('[data-menu]'); if (m) m.open = true; })()`); };
await shot('phone-menu', '/c/home-living', 375, { prepare: openMenu, clip: 900 });
await shot('phone-menu', '/c/home-living', 375, { forced: true, prepare: openMenu, clip: 900 });
for (const width of [320, 768, 1024]) {
  await shot('home', '/', width, { measures: HOME });
  await shot('department', '/c/home-living', width, { measures: DEPARTMENT });
  await shot('search', search, width, { measures: SEARCH });
  await shot('header-search-focus', '/', width, { clip: 260, prepare: focusField('#site-search'), measures: ['#site-search', '.goen-header__search'] });
}

// The phone at 200% text: nothing may scroll sideways.
await shot('home', '/', 375, { text200: true, measures: HOME });
await shot('department', '/c/home-living', 375, { text200: true, measures: DEPARTMENT });
await shot('department-filters-open', '/c/home-living', 375, { text200: true, prepare: openFilters, measures: DEPARTMENT });
await shot('search', search, 375, { text200: true, measures: SEARCH });

// Forced colours at 1440.
await shot('home', '/', 1440, { forced: true });
await shot('department', '/c/home-living', 1440, { forced: true });
if (subcategory) await shot('subcategory', subcategory, 1440, { forced: true });
await shot('search', search, 1440, { forced: true });
await shot('header-search-focus', '/', 1440, { forced: true, clip: 260, prepare: focusField('#site-search') });

// English, for the header and the newsletter.
await cookie('goen_locale', 'en');
for (const width of [1440, 375]) {
  await shot('en-home', '/', width, { measures: HOME });
  await shot('en-header-search-focus', '/', width, { clip: 260, prepare: focusField('#site-search'), measures: ['#site-search'] });
}
await cookie('goen_locale', 'zh-Hant');

ws.close();
if (failures.length) {
  console.log(`\n${failures.length} failure(s):\n  ${failures.join('\n  ')}`);
  process.exit(1);
}
console.log('all shots captured');
