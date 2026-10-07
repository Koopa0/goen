// Probe only: the home page's department directory over CDP, with the numbers
// the design cares about printed as MEASURE lines.
// Usage: node scripts/home-tiles-shots.mjs <outdir> <label>
// Reads GOEN_URL, CDP_PORT and GOEN_DATABASE_URL; the departments are hidden by
// setting their products to draft, in the probe database only.

import { execFileSync } from 'node:child_process';
import { mkdirSync, writeFileSync } from 'node:fs';

const CDP_PORT = Number(process.env.CDP_PORT || 9333);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const DB = process.env.GOEN_DATABASE_URL;
const [outDir, label] = process.argv.slice(2);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const sql = (text) => execFileSync('psql', [DB, '-X', '-q', '-v', 'ON_ERROR_STOP=1', '-c', text], { stdio: ['ignore', 'pipe', 'inherit'] });

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
const cookie = (name, value) => send('Network.setCookie', { name, value, domain: '127.0.0.1', path: '/' });

// Departments the probe adds so the directory can be seen at 7 and 10, with a
// long name in each language and a name that starts with a digit. None has a
// photograph, so each is its tone and first character.
const EXTRA = [
  ['probe-camping', '戶外露營登山旅行運動休閒生活用品專賣館', 'Outdoor camping, hiking and travel equipment', 'sage', 90],
  ['probe-printing', '3D 列印', '3D printing', 'mist', 91],
  ['probe-pets', '寵物用品', 'Pet supplies', 'blush', 92],
  ['probe-toys', '兒童玩具', 'Toys', null, 93],
];

function setup() {
  sql(`CREATE TABLE IF NOT EXISTS probe_hidden (product_id uuid PRIMARY KEY)`);
  for (const [slug, name, nameEn, tone, position] of EXTRA) {
    const t = tone ? `'${tone}'` : 'NULL';
    sql(`WITH c AS (
           INSERT INTO categories (slug, name, name_en, tone, position)
           VALUES ('${slug}', '${name}', '${nameEn}', ${t}, ${position})
           ON CONFLICT DO NOTHING RETURNING id
         ), p AS (
           INSERT INTO products (category_id, slug, name, status, published_at)
           SELECT id, '${slug}-item', '${slug}', 'active', now() FROM c RETURNING id
         )
         INSERT INTO product_variants (product_id, sku, price_cents, stock_quantity, safety_stock, position)
         SELECT id, upper('${slug}-sku'), 1000, 5, 0, 0 FROM p`);
  }
}

// Lists the first n departments in the order the home page does, or, for
// 'solo', only the extra department without a photograph.
function list(which) {
  sql(`UPDATE products SET status = 'active' WHERE id IN (SELECT product_id FROM probe_hidden)`);
  sql(`DELETE FROM probe_hidden`);
  const keep = which === 'solo' ? `r.slug = 'probe-toys'` : `r.n <= ${Number(which)}`;
  sql(`WITH RECURSIVE roots AS (
         SELECT id, slug, row_number() OVER (ORDER BY position, name, id) AS n FROM categories WHERE parent_id IS NULL
       ), tree AS (
         SELECT id, id AS root FROM roots
         UNION ALL
         SELECT k.id, t.root FROM categories k JOIN tree t ON k.parent_id = t.id
       ), hide AS (
         UPDATE products p SET status = 'draft'
         FROM tree t JOIN roots r ON r.id = t.root
         WHERE p.category_id = t.id AND p.status = 'active' AND NOT (${keep})
         RETURNING p.id
       )
       INSERT INTO probe_hidden SELECT id FROM hide`);
}

const measure = `(() => {
  const h = document.querySelector('#cats-heading');
  const sec = h && h.closest('section');
  const de = document.documentElement;
  const out = { drawn: !!sec, scrollWidth: de.scrollWidth, clientWidth: de.clientWidth };
  if (!sec) return out;
  const r = (e) => { const b = e.getBoundingClientRect(); return { x: Math.round(b.x), y: Math.round(b.y + scrollY), w: +b.width.toFixed(1), h: +b.height.toFixed(1) }; };
  const ul = sec.querySelector('ul');
  const lis = [...sec.querySelectorAll('ul > li')];
  const top = lis.length ? Math.min(...lis.map((l) => l.getBoundingClientRect().top)) : 0;
  const tile = sec.querySelector('.goen-cat');
  const well = sec.querySelector('.goen-cat__well');
  const photo = sec.querySelector('.goen-cat__photo');
  const names = [...sec.querySelectorAll('.goen-cat__name')];
  const lh = (e) => { const s = getComputedStyle(e); return parseFloat(s.lineHeight) || parseFloat(s.fontSize) * 1.4; };
  Object.assign(out, {
    section: r(sec),
    grid: ul ? r(ul) : null,
    tiles: lis.length,
    columns: lis.filter((l) => Math.abs(l.getBoundingClientRect().top - top) < 2).length,
    rows: new Set(lis.map((l) => Math.round(l.getBoundingClientRect().top))).size,
    tile: tile ? r(tile) : null,
    well: well ? r(well) : null,
    photo: photo ? r(photo) : null,
    nameSize: names[0] ? getComputedStyle(names[0]).fontSize : null,
    nameWeight: names[0] ? getComputedStyle(names[0]).fontWeight : null,
    nameLines: names.map((n) => Math.round(n.getBoundingClientRect().height / lh(n))),
    marks: sec.querySelectorAll('.goen-cat__mark').length,
    notLoaded: [...sec.querySelectorAll('img')].filter((i) => !i.complete || i.naturalWidth === 0).length,
    textScale: getComputedStyle(de).fontSize,
    sectionRight: Math.round(Math.max(...[...sec.querySelectorAll('*')].map((e) => e.getBoundingClientRect().right))),
    pageOverflow: [...document.querySelectorAll('body *')].filter((e) => e.getBoundingClientRect().right > de.clientWidth + 0.5 && !sec.contains(e)).slice(0, 4).map((e) => e.tagName.toLowerCase() + '.' + String(e.className || '').split(' ')[0]),
  });
  return out;
})()`;

async function settle() {
  await evaluate('document.fonts.ready.then(() => 1)', true);
  for (let i = 0; i < 40; i++) {
    if (!(await evaluate('[...document.images].filter((i) => !i.complete && i.loading !== "lazy").length'))) break;
    await sleep(250);
  }
  await sleep(300);
}

async function save(file, clip) {
  const { data } = await send('Page.captureScreenshot', clip ? { format: 'png', clip: { ...clip, scale: 1 }, captureBeyondViewport: true } : { format: 'png' });
  writeFileSync(`${outDir}/${file}.png`, Buffer.from(data, 'base64'));
}

// state: { name, width, locale, count, text (percent), forced, full, focus, hover }
async function capture(s) {
  await cookie('goen_locale', s.locale);
  await metrics(s.width, 900);
  await navigate(ORIGIN + '/');
  if (s.text) await evaluate(`document.documentElement.style.fontSize = '${s.text}%'`);
  await settle();
  const m = await evaluate(measure);
  const id = `${s.name}-${s.locale}-${s.width}${s.text ? `-text${s.text}` : ''}${s.forced ? '-forced' : ''}`;
  console.log(`MEASURE ${label} ${id} ${JSON.stringify(m)}`);
  if (m.scrollWidth > m.clientWidth) console.log(`OVERFLOW ${label} ${id} scrollWidth ${m.scrollWidth} > ${m.clientWidth}`);
  const grow = async (height) => { await metrics(s.width, Math.ceil(Math.min(6000, Math.max(height, 400)))); await sleep(200); };
  if (s.full) {
    await grow(await evaluate('Math.ceil(document.documentElement.scrollHeight)'));
    await save(`${id}-page`);
    await metrics(s.width, 900);
  }
  if (m.drawn) {
    const pad = 24;
    const y = Math.max(0, Math.floor(m.section.y - pad));
    await grow(m.section.y + m.section.h + pad * 2);
    await save(id, { x: 0, y, width: s.width, height: Math.ceil(m.section.h + pad * 2) });
    if (s.focus) {
      await evaluate(`(() => { const e = document.querySelector('#cats-heading').closest('section').querySelectorAll('.goen-cat')[1]; e.focus({ focusVisible: true }); })()`);
      await send('Input.dispatchKeyEvent', { type: 'rawKeyDown', key: 'Shift', code: 'ShiftLeft', windowsVirtualKeyCode: 16 });
      const ring = await evaluate(`(() => { const t = document.activeElement; const w = t.querySelector('.goen-cat__well') || t.querySelector('.goen-cat__photo') || t; const a = getComputedStyle(t), b = getComputedStyle(w); return JSON.stringify({ focusVisible: t.matches(':focus-visible'), tileOutline: a.outlineStyle, wellOutline: b.outlineStyle + ' ' + b.outlineWidth + ' ' + b.outlineOffset + ' ' + b.outlineColor }); })()`);
      console.log(`MEASURE ${label} ${id} focus ${ring}`);
      await sleep(200);
      await save(`${id}-focus`, { x: 0, y, width: s.width, height: Math.ceil(m.section.h + pad * 2) });
    }
    if (s.hover) {
      const box = await evaluate(`(() => { const b = document.querySelector('#cats-heading').closest('section').querySelector('.goen-cat').getBoundingClientRect(); return { x: b.x + b.width / 2, y: b.y + b.height / 3 }; })()`);
      await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: box.x, y: box.y });
      await sleep(300);
      await save(`${id}-hover`, { x: 0, y, width: s.width, height: Math.ceil(m.section.h + pad * 2) });
      await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 0, y: 0 });
    }
  }
}

mkdirSync(outDir, { recursive: true });
setup();
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
await send('Network.enable');
await send('Network.clearBrowserCookies');
await send('Emulation.setScrollbarsHidden', { hidden: true });
const media = (features) => send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'light' }, { name: 'prefers-reduced-motion', value: 'reduce' }, ...features] });
await media([]);

const WIDTHS = [1440, 1024, 768, 375, 320];
const failures = [];
const run = async (s) => { try { await capture(s); } catch (e) { failures.push(`${s.name} ${s.locale} ${s.width}: ${e.message}`); } };

// The seeded shop: six departments, in both languages, at every width.
list(6);
await media([]);
for (const locale of ['zh-Hant', 'en']) {
  for (const width of WIDTHS) {
    await run({ name: 'six', width, locale, full: locale === 'zh-Hant' && (width === 1440 || width === 375), focus: locale === 'zh-Hant' && (width === 1440 || width === 375), hover: locale === 'zh-Hant' && width === 1440 });
  }
  for (const width of [375, 320]) await run({ name: 'six', width, locale, text: 200 });
}
await media([{ name: 'forced-colors', value: 'active' }]);
await run({ name: 'six', width: 1440, locale: 'zh-Hant', forced: true });
await media([]);

// Other counts, Chinese, at every width; ten also in English and at 200% text.
for (const count of [1, 2, 3, 4, 5, 7, 10, 'solo']) {
  list(count);
  for (const width of WIDTHS) await run({ name: `n${count}`, width, locale: 'zh-Hant' });
  if (count === 10 || count === 7) {
    for (const width of [1440, 375, 320]) await run({ name: `n${count}`, width, locale: 'en' });
    for (const width of [375, 320]) await run({ name: `n${count}`, width, locale: 'zh-Hant', text: 200 });
    await run({ name: `n${count}`, width: 1024, locale: 'zh-Hant', text: 200 });
    await media([{ name: 'forced-colors', value: 'active' }]);
    await run({ name: `n${count}`, width: 1440, locale: 'zh-Hant', forced: true });
    await media([]);
  }
}
list(6);

ws.close();
if (failures.length) {
  console.log(`\n${failures.length} failure(s):\n  ${failures.join('\n  ')}`);
  process.exit(1);
}
console.log('all shots captured');
