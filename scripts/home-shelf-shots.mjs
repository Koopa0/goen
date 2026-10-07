// Probe only: the home page's department band over CDP, with the numbers the
// design cares about printed as MEASURE lines.
// Usage: node scripts/home-shelf-shots.mjs <outdir> <label>
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

// One department with long names, eight long sub-categories and five products,
// shown alone by list('long') so it is the band.
const LONG = {
  slug: 'probe-long',
  name: '戶外露營登山旅行運動休閒生活用品專賣館',
  nameEn: 'Outdoor camping, hiking, travel and sports equipment superstore',
  subs: [
    ['probe-long-a', '登山背包與行李箱', 'Hiking backpacks and suitcases'],
    ['probe-long-b', '露營帳篷與睡袋', 'Camping tents and sleeping bags'],
    ['probe-long-c', '戶外炊具與餐具', 'Outdoor cookware and tableware'],
    ['probe-long-d', '運動服飾與配件', 'Sportswear and accessories'],
    ['probe-long-e', '防水與保暖衣物', 'Waterproof and insulated clothing'],
    ['probe-long-f', '急救與安全用品', 'First aid and safety supplies'],
    ['probe-long-g', '照明與電力', 'Lighting and portable power'],
    ['probe-long-h', '地圖導航與錶', 'Maps, navigation and watches'],
  ],
};

function setupLong() {
  sql(`WITH c AS (
         INSERT INTO categories (slug, name, name_en, tone, position)
         VALUES ('${LONG.slug}', '${LONG.name}', '${LONG.nameEn}', 'sage', 95)
         ON CONFLICT DO NOTHING RETURNING id
       ), k AS (
         INSERT INTO categories (parent_id, slug, name, name_en, position)
         SELECT c.id, v.slug, v.name, v.name_en, v.pos
         FROM c, (VALUES ${LONG.subs.map(([a, b, e], i) => `('${a}', '${b}', '${e}', ${i})`).join(',')}) AS v(slug, name, name_en, pos)
         RETURNING id
       ), p AS (
         INSERT INTO products (category_id, slug, name, status, published_at)
         SELECT c.id, '${LONG.slug}-item-' || n, 'Long department product ' || n, 'active', now()
         FROM c, generate_series(1, 5) AS n RETURNING id
       )
       INSERT INTO product_variants (product_id, sku, price_cents, stock_quantity, safety_stock, position)
       SELECT id, 'PROBE-LONG-' || row_number() OVER (), 1000, 5, 0, 0 FROM p`);
}

function setup() {
  setupLong();
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
  const keep = which === 'none' ? `false` : which === 'tech' ? `r.slug = 'tech'` : which === 'solo' ? `r.slug = 'probe-toys'` : which === 'long' ? `r.slug = 'probe-long'` : `r.n <= ${Number(which)}`;
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
  const de = document.documentElement;
  const out = { scrollWidth: de.scrollWidth, clientWidth: de.clientWidth, pageHeight: de.scrollHeight, textScale: getComputedStyle(de).fontSize };
  const r = (e) => { const b = e.getBoundingClientRect(); return { x: Math.round(b.x), y: Math.round(b.y + scrollY), w: +b.width.toFixed(1), h: +b.height.toFixed(1) }; };
  const lh = (e) => { const s = getComputedStyle(e); return parseFloat(s.lineHeight) || parseFloat(s.fontSize) * 1.4; };
  const lines = (e) => e ? Math.round(e.getBoundingClientRect().height / lh(e)) : 0;
  const sectionOf = (sel) => { const h = document.querySelector(sel); return h && h.closest('section'); };

  const band = sectionOf('#band-heading');
  const row = sectionOf('#row-heading');
  const cats = sectionOf('#cats-heading');
  const rules = document.querySelector('.goen-rules');
  out.bandDrawn = !!band;
  out.rowTop = row ? r(row.querySelector('ul')).y : null;
  out.firstProductRowTop = out.rowTop;
  out.order = [['row', row], ['cats', cats], ['band', band], ['rules', rules]].filter(([, e]) => e).map(([n, e]) => n + '@' + r(e).y).join(' < ');
  if (band) {
    const items = [...band.querySelectorAll('.goen-band__tiles > li')];
    const top = items.length ? Math.min(...items.map((l) => l.getBoundingClientRect().top)) : 0;
    const name = band.querySelector('.goen-band__name');
    const fact = band.querySelector('.goen-band__fact');
    const bandTop = r(band).y;
    Object.assign(out, {
      bandHeight: r(band).h,
      bandBox: r(band),
      bandTiles: items.length,
      bandColumns: items.filter((l) => Math.abs(l.getBoundingClientRect().top - top) < 2).length,
      bandTileWidth: items.length ? +items[0].getBoundingClientRect().width.toFixed(1) : 0,
      bandPhotos: band.querySelectorAll('img.goen-band__photo, .goen-band__media').length,
      bandName: name ? { size: getComputedStyle(name).fontSize, weight: getComputedStyle(name).fontWeight, lines: lines(name) } : null,
      bandFactLines: lines(fact),
      bandScrolls: band.querySelector('.goen-band__tiles').scrollWidth > band.querySelector('.goen-band__tiles').clientWidth + 1,
      bandRight: Math.round(Math.max(...[...band.querySelectorAll('*')].filter((e) => !e.closest('.goen-band__tiles')).map((e) => e.getBoundingClientRect().right))),
      bandOverflowing: [...band.querySelectorAll('*')].filter((e) => !e.closest('.goen-band__tiles') && e.getBoundingClientRect().right > de.clientWidth + 0.5).slice(0, 4).map((e) => e.tagName.toLowerCase() + '.' + String(e.className || '').split(' ')[0]),
      bandTone: getComputedStyle(band).backgroundColor,
      bandTextColour: name ? getComputedStyle(name).color : null,
      bandFactColour: fact ? getComputedStyle(fact).color : null,
      bandMoreHeight: band.querySelector('.goen-home__more') ? r(band.querySelector('.goen-home__more')).h : null,
      productRowTopFromBandTop: items.length ? Math.round(top + scrollY - bandTop) : null,
      bandImagesNotLoaded: [...band.querySelectorAll('img')].filter((i) => !i.complete || i.naturalWidth === 0).length,
    });
  }
  // The same photograph twice on the page: a department tile's file stem found
  // on any other <img>, and the vertical distance to the nearest one.
  const stem = (u) => (u || '').split('?')[0].split('/').pop().replace(/-\\d+(w)?(?=\\.[a-z0-9]+$)/i, '').replace(/\\.[a-z0-9]+$/i, '');
  const imgs = [...document.querySelectorAll('main img')];
  const repeats = [];
  for (const t of document.querySelectorAll('.goen-cat__photo')) {
    const ty = t.getBoundingClientRect().top + scrollY;
    for (const o of imgs) {
      if (o === t || stem(o.currentSrc || o.getAttribute('src')) !== stem(t.currentSrc || t.getAttribute('src'))) continue;
      repeats.push({ stem: stem(t.getAttribute('src')), in: (o.closest('section, .goen-hero__slide, article') || o).className.toString().split(' ')[0], dy: Math.round(Math.abs(o.getBoundingClientRect().top + scrollY - ty)) });
    }
  }
  out.photoRepeats = repeats.length ? repeats : 'none';
  out.photoRepeatMinDistance = repeats.length ? Math.min(...repeats.map((x) => x.dy)) : null;
  const catsEl = cats && cats.querySelector('ul');
  if (catsEl) { out.cats = { y: r(cats).y, h: r(cats).h, tiles: catsEl.children.length }; }
  out.pageOverflow = [...document.querySelectorAll('body *')].filter((e) => e.getBoundingClientRect().right > de.clientWidth + 0.5 && !e.closest('.goen-band__tiles, .goen-hero__track, .goen-header, nav')).slice(0, 4).map((e) => e.tagName.toLowerCase() + '.' + String(e.className || '').split(' ')[0]);
  return out;
})()`;

async function settle() {
  await evaluate('document.fonts.ready.then(() => 1)', true);
  await evaluate(`(async () => { const f = () => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))); for (let y = 0; y < document.documentElement.scrollHeight && y < 40000; y += innerHeight) { scrollTo(0, y); await f(); } scrollTo(0, 0); await f(); })()`, true);
  for (let i = 0; i < 60 && (await evaluate('[...document.images].filter((i) => !i.complete).length')); i++) await sleep(250);
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

// state: { name, width, locale, text (percent), forced, full }
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
  const grow = async (height) => { await metrics(s.width, Math.ceil(Math.min(7000, Math.max(height, 400)))); await sleep(250); };
  if (s.full) {
    await grow(m.pageHeight);
    await save(`${id}-page`);
    await metrics(s.width, 900);
  }
  if (m.bandDrawn) {
    const pad = 24;
    await grow(m.bandBox.y + m.bandBox.h + pad * 2);
    const box = (await evaluate(measure)).bandBox;
    await save(`${id}-band`, { x: 0, y: Math.max(0, Math.floor(box.y - pad)), width: s.width, height: Math.ceil(box.h + pad * 2) });
    if (s.focus) {
      await evaluate(`document.querySelector('#band-heading').closest('section').querySelectorAll('.goen-band__tiles a')[1].focus({ focusVisible: true })`);
      await send('Input.dispatchKeyEvent', { type: 'rawKeyDown', key: 'Shift', code: 'ShiftLeft', windowsVirtualKeyCode: 16 });
      await sleep(400);
      const box2 = (await evaluate(measure)).bandBox;
      console.log(`MEASURE ${label} ${id} focus ${await evaluate(`(() => { const t = document.activeElement; const c = getComputedStyle(t); return JSON.stringify({ focusVisible: t.matches(':focus-visible'), outline: c.outlineStyle + ' ' + c.outlineWidth + ' ' + c.outlineColor, inBand: !!t.closest('.goen-band--shelf') }); })()`)}`);
      await save(`${id}-band-focus`, { x: 0, y: Math.max(0, Math.floor(box2.y - pad)), width: s.width, height: Math.ceil(box2.h + pad * 2) });
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

const WIDTHS = [1440, 1100, 1024, 768, 375, 320];
const failures = [];
const run = async (s) => { try { await capture(s); } catch (e) { failures.push(`${s.name} ${s.locale} ${s.width}: ${e.message}`); } };

// The seeded shop: six departments, in both languages, at every width, and at
// 200% text and in forced colours.
list(6);
await media([]);
for (const locale of ['zh-Hant', 'en']) {
  for (const width of WIDTHS) await run({ name: 'six', width, locale, full: width === 1440 || width === 375 || (locale === 'zh-Hant' && width === 768), focus: width === 1440 || width === 375 });
  for (const width of [375, 320]) await run({ name: 'six', width, locale, text: 200 });
  await media([{ name: 'forced-colors', value: 'active' }]);
  await run({ name: 'six', width: 1440, locale, forced: true, full: true });
  await media([]);
}

// Counts of departments, Chinese; the band is whichever department the day
// selects, so these show the page order and the directory around it.
for (const count of [1, 2, 10, 'solo']) {
  list(count);
  for (const width of WIDTHS) await run({ name: `n${count}`, width, locale: 'zh-Hant', full: (count === 10 && (width === 1440 || width === 375)) || (count === 'solo' && (width === 1440 || width === 375)) });
  if (count === 10) for (const width of [375, 320]) await run({ name: `n${count}`, width, locale: 'zh-Hant', text: 200 });
}

// The day the band is 3C: only that department is listed, so five-digit prices
// meet the narrowest cards. Keyboard focus on the second shelf card.
list('tech');
for (const locale of ['zh-Hant', 'en']) {
  for (const width of WIDTHS) await run({ name: 'tech', width, locale, full: width === 1440 || width === 375, focus: true });
  for (const width of [375, 320]) await run({ name: 'tech', width, locale, text: 200 });
  await media([{ name: 'forced-colors', value: 'active' }]);
  await run({ name: 'tech', width: 1440, locale, forced: true });
  await media([]);
}

// No department listed at all.
list('none');
for (const width of [1440, 375, 320]) await run({ name: 'zero', width, locale: 'zh-Hant', full: true });

// A band department with long names and eight long sub-categories, in both
// languages, at every width, at 200% text and in forced colours.
list('long');
for (const locale of ['zh-Hant', 'en']) {
  for (const width of WIDTHS) await run({ name: 'long', width, locale, full: width === 1440 || width === 375 });
  for (const width of [375, 320]) await run({ name: 'long', width, locale, text: 200 });
  await media([{ name: 'forced-colors', value: 'active' }]);
  await run({ name: 'long', width: 1440, locale, forced: true });
  await media([]);
}
list(6);

ws.close();
if (failures.length) {
  console.log(`\n${failures.length} failure(s):\n  ${failures.join('\n  ')}`);
  process.exit(1);
}
console.log('all shots captured');
