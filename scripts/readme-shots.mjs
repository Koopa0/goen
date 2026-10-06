// Probe only: captures the README screenshots over CDP against a running goen.
//
// Usage: node scripts/readme-shots.mjs <storefront|backoffice> <outdir>
// Needs Chrome on CDP_PORT and goen on GOEN_URL; backoffice needs ADMIN_TOKEN.

import { mkdirSync, writeFileSync } from 'node:fs';
import { crc32, deflateSync, inflateSync } from 'node:zlib';

const CDP_PORT = Number(process.env.CDP_PORT || 9333);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const WIDTH = 1440;
const HEIGHT = 1245;
const [kind, outDir] = process.argv.slice(2);

const LOCALES = [
  { cookie: 'en', file: 'en', title: 'Tea and coffee week' },
  { cookie: 'zh-Hant', file: 'zh-TW', title: '茶與咖啡週' },
];

const problems = [];
const problem = (where, msg) => {
  problems.push(`${where}: ${msg}`);
  console.log(`PROBLEM ${where}: ${msg}`);
};

// ---- CDP ----
let nextId = 1;
const pending = new Map();
let ws;
function send(method, params = {}, timeoutMs = 60000) {
  const id = nextId++;
  ws.send(JSON.stringify({ id, method, params }));
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      if (pending.delete(id)) reject(new Error(`${method} timed out`));
    }, timeoutMs);
    pending.set(id, { resolve, reject, timer });
  });
}
async function pageSocket() {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    try {
      const list = await (await fetch(`http://127.0.0.1:${CDP_PORT}/json/list`)).json();
      const page = list.find((t) => t.type === 'page');
      if (page) return page.webSocketDebuggerUrl;
    } catch {
      /* not listening yet */
    }
    await sleep(200);
  }
  throw new Error(`no Chrome page target on port ${CDP_PORT}`);
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function evaluate(expression, awaitPromise = false) {
  const { result, exceptionDetails } = await send('Runtime.evaluate', {
    expression, awaitPromise, returnByValue: true,
  });
  if (exceptionDetails) throw new Error(`evaluate failed: ${JSON.stringify(exceptionDetails).slice(0, 400)}`);
  return result.value;
}
async function navigate(url) {
  await send('Page.navigate', { url: 'about:blank' });
  for (let i = 0; i < 100 && (await evaluate('location.href')) !== 'about:blank'; i++) await sleep(100);
  await send('Page.navigate', { url });
  for (let i = 0; i < 300; i++) {
    const [state, href] = String(await evaluate('document.readyState + " " + location.href')).split(' ');
    if (state === 'complete' && href !== 'about:blank') return href;
    await sleep(100);
  }
  throw new Error(`${url} never finished loading`);
}

// Every <img> whose box meets the viewport, fetched and decoded.
const IMAGES = `(() => {
  const imgs = [...document.images].filter((i) => {
    const r = i.getBoundingClientRect();
    return r.width > 0 && r.height > 0 && r.bottom > 0 && r.top < innerHeight && r.right > 0 && r.left < innerWidth
      && getComputedStyle(i).visibility !== 'hidden';
  });
  return Promise.all(imgs.map((i) => i.decode().then(() => '', () => i.currentSrc || i.src))).then((failed) => ({
    count: imgs.length,
    pending: imgs.filter((i) => !i.complete || i.naturalWidth === 0).map((i) => i.currentSrc || i.src),
    failed: failed.filter(Boolean),
  }));
})()`;

async function settle(label) {
  const fonts = await evaluate('document.fonts.ready.then(() => document.fonts.status)', true);
  let images;
  for (let i = 0; i < 120; i++) {
    images = await evaluate(IMAGES, true);
    if (images.pending.length === 0 && images.failed.length === 0) {
      await evaluate('new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))', true);
      const again = await evaluate(IMAGES, true);
      if (again.count === images.count && again.pending.length === 0 && again.failed.length === 0) break;
    }
    await sleep(500);
  }
  await evaluate('document.fonts.ready', true);
  await sleep(500);
  const facts = await evaluate(`({
    fontsStatus: document.fonts.status,
    // Each rendered run of text, checked at its own weight and style, so a
    // face that never loaded (a fallback glyph, a missing subset) is named.
    uncovered: (() => {
      const runs = new Map();
      const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
      for (let n = walk.nextNode(); n; n = walk.nextNode()) {
        const el = n.parentElement;
        if (!el || !n.textContent.trim() || !el.getClientRects().length) continue;
        const cs = getComputedStyle(el);
        const key = cs.fontStyle + ' ' + cs.fontWeight + ' 16px ' + cs.fontFamily;
        runs.set(key, (runs.get(key) || '') + n.textContent);
      }
      return [...runs].filter(([font, text]) => !document.fonts.check(font, text)).map(([font]) => font);
    })(),
    faces: [...document.fonts].filter((f) => f.status === 'loaded').map((f) => f.family + ' ' + f.weight)
      .filter((v, i, a) => a.indexOf(v) === i),
    scrollbar: innerWidth - document.documentElement.clientWidth,
    viewport: innerWidth + 'x' + innerHeight + '@' + devicePixelRatio,
    scrollY,
    dark: matchMedia('(prefers-color-scheme: dark)').matches,
    hovered: [...document.querySelectorAll(':hover')].map((e) => e.tagName + (e.className && typeof e.className === 'string' ? '.' + e.className.split(' ')[0] : '')),
    focused: document.activeElement ? document.activeElement.tagName : '',
    lang: document.documentElement.lang,
    promo: !!document.querySelector('.goen-promo'),
    layoutText: /layout/i.test(document.body.innerText),
  })`);
  console.log(`${label} fonts=${fonts}/${facts.fontsStatus} uncovered=${JSON.stringify(facts.uncovered)} faces=${JSON.stringify(facts.faces)}`);
  console.log(`${label} images=${images.count} pending=${JSON.stringify(images.pending)} failed=${JSON.stringify(images.failed)}`);
  console.log(`${label} viewport=${facts.viewport} scrollbar=${facts.scrollbar} scrollY=${facts.scrollY} dark=${facts.dark} hovered=${JSON.stringify(facts.hovered)} focused=${facts.focused} lang=${facts.lang}`);
  if (images.pending.length || images.failed.length) problem(label, 'images not loaded: ' + JSON.stringify(images));
  if (facts.fontsStatus !== 'loaded') problem(label, 'fonts still loading');
  if (facts.uncovered.length) problem(label, 'text with no loaded face: ' + JSON.stringify(facts.uncovered));
  if (facts.scrollbar !== 0) problem(label, `a ${facts.scrollbar}px scrollbar takes layout width`);
  if (facts.viewport !== `${WIDTH}x${HEIGHT}@1`) problem(label, 'viewport is ' + facts.viewport);
  if (facts.scrollY !== 0) problem(label, 'scrolled to ' + facts.scrollY);
  if (facts.dark) problem(label, 'dark scheme');
  if (facts.promo) problem(label, 'promotional strip present');
  if (facts.layoutText) problem(label, 'page text mentions a layout fixture');
  if (facts.hovered.some((h) => /hero/.test(h))) problem(label, 'pointer over the carousel');
  return facts;
}

// ---- PNG: Chrome writes RGBA; the README images are 8-bit RGB. ----
const SIGNATURE = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
function readPNG(buf) {
  if (!buf.subarray(0, 8).equals(SIGNATURE)) throw new Error('not a PNG');
  const chunks = [];
  for (let off = 8; off < buf.length;) {
    const len = buf.readUInt32BE(off);
    chunks.push({ type: buf.toString('latin1', off + 4, off + 8), data: buf.subarray(off + 8, off + 8 + len) });
    off += 12 + len;
  }
  const h = chunks[0].data;
  return {
    chunks, width: h.readUInt32BE(0), height: h.readUInt32BE(4), depth: h[8], colourType: h[9], interlace: h[12],
  };
}
const paeth = (a, b, c) => {
  const p = a + b - c;
  const pa = Math.abs(p - a), pb = Math.abs(p - b), pc = Math.abs(p - c);
  return pa <= pb && pa <= pc ? a : pb <= pc ? b : c;
};
function pixels(png) {
  const bpp = { 2: 3, 6: 4 }[png.colourType];
  if (png.depth !== 8 || !bpp || png.interlace !== 0) {
    throw new Error(`unexpected PNG: depth ${png.depth} colour type ${png.colourType} interlace ${png.interlace}`);
  }
  const raw = inflateSync(Buffer.concat(png.chunks.filter((c) => c.type === 'IDAT').map((c) => c.data)));
  const stride = png.width * bpp;
  const out = Buffer.alloc(stride * png.height);
  for (let y = 0; y < png.height; y++) {
    const f = raw[y * (stride + 1)];
    const src = y * (stride + 1) + 1;
    const dst = y * stride;
    for (let x = 0; x < stride; x++) {
      const a = x >= bpp ? out[dst + x - bpp] : 0;
      const b = y ? out[dst - stride + x] : 0;
      const c = x >= bpp && y ? out[dst - stride + x - bpp] : 0;
      const v = raw[src + x];
      out[dst + x] = (f === 0 ? v : f === 1 ? v + a : f === 2 ? v + b : f === 3 ? v + ((a + b) >> 1) : f === 4 ? v + paeth(a, b, c) : NaN) & 255;
      if (f > 4) throw new Error('bad filter ' + f);
    }
  }
  return { bpp, data: out };
}
function chunk(type, data) {
  const head = Buffer.alloc(4);
  head.writeUInt32BE(data.length);
  const body = Buffer.concat([Buffer.from(type, 'latin1'), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body) >>> 0);
  return Buffer.concat([head, body, crc]);
}
function toRGB(buf) {
  const png = readPNG(buf);
  const { bpp, data } = pixels(png);
  if (bpp === 3) return { out: buf, from: 'RGB' };
  const { width, height } = png;
  const rgb = Buffer.alloc(width * height * 3);
  for (let i = 0, j = 0; i < data.length; i += 4, j += 3) {
    if (data[i + 3] !== 255) throw new Error(`pixel ${i / 4} is not opaque (alpha ${data[i + 3]})`);
    rgb[j] = data[i]; rgb[j + 1] = data[i + 1]; rgb[j + 2] = data[i + 2];
  }
  const stride = width * 3;
  const filtered = Buffer.alloc((stride + 1) * height);
  const trial = Buffer.alloc(stride);
  const best = Buffer.alloc(stride);
  for (let y = 0; y < height; y++) {
    const row = y * stride;
    let bestFilter = 0, bestSum = Infinity;
    for (let f = 0; f < 5; f++) {
      let sum = 0;
      for (let x = 0; x < stride; x++) {
        const a = x >= 3 ? rgb[row + x - 3] : 0;
        const b = y ? rgb[row - stride + x] : 0;
        const c = x >= 3 && y ? rgb[row - stride + x - 3] : 0;
        const v = rgb[row + x];
        const p = (f === 0 ? v : f === 1 ? v - a : f === 2 ? v - b : f === 3 ? v - ((a + b) >> 1) : v - paeth(a, b, c)) & 255;
        trial[x] = p;
        sum += p < 128 ? p : 256 - p;
      }
      if (sum < bestSum) { bestSum = sum; bestFilter = f; trial.copy(best); }
    }
    filtered[y * (stride + 1)] = bestFilter;
    best.copy(filtered, y * (stride + 1) + 1);
  }
  const ihdr = Buffer.from(png.chunks[0].data);
  ihdr[9] = 2;
  const firstIDAT = png.chunks.findIndex((c) => c.type === 'IDAT');
  const before = png.chunks.slice(1, firstIDAT).filter((c) => c.type !== 'IDAT');
  const after = png.chunks.slice(firstIDAT).filter((c) => c.type !== 'IDAT' && c.type !== 'IEND');
  const out = Buffer.concat([
    SIGNATURE, chunk('IHDR', ihdr), ...before.map((c) => chunk(c.type, c.data)),
    chunk('IDAT', deflateSync(filtered, { level: 9 })), ...after.map((c) => chunk(c.type, c.data)),
    chunk('IEND', Buffer.alloc(0)),
  ]);
  const check = pixels(readPNG(out));
  if (!check.data.equals(rgb)) throw new Error('the RGB re-encode does not decode to the same pixels');
  return { out, from: `RGBA (chunks ${png.chunks.map((c) => c.type).join(',')})` };
}

async function capture(label, file) {
  const { data } = await send('Page.captureScreenshot', { format: 'png' });
  const { out, from } = toRGB(Buffer.from(data, 'base64'));
  const png = readPNG(out);
  console.log(`${label} wrote ${file}: ${png.width} x ${png.height}, depth ${png.depth}, colour type ${png.colourType}, from ${from}, ${out.length} bytes`);
  if (png.width !== WIDTH || png.height !== HEIGHT || png.depth !== 8 || png.colourType !== 2) {
    problem(label, 'the PNG is not 1440 x 1245 8-bit RGB');
  }
  writeFileSync(`${outDir}/${file}`, out);
}

// ---- the shots ----
async function storefront(loc) {
  const label = `storefront ${loc.cookie}`;
  const href = await navigate(ORIGIN + '/');
  const facts = await settle(label);
  if (facts.lang !== loc.cookie) problem(label, `<html lang> is ${facts.lang}`);
  if (new URL(href).pathname !== '/') problem(label, 'landed on ' + href);
  const hero = await evaluate(`(() => {
    const track = document.querySelector('.goen-hero__track');
    const first = track && track.children[0];
    const box = (el) => { if (!el) return null; const r = el.getBoundingClientRect(); return [Math.round(r.left), Math.round(r.top), Math.round(r.width), Math.round(r.height)]; };
    const cats = document.querySelector('#cats-heading');
    return {
      slides: track ? track.children.length : 0,
      scrollLeft: track ? track.scrollLeft : -1,
      firstBox: box(first),
      title: first ? (first.querySelector('.goen-hero__title') || {}).textContent : '',
      stats: first ? [...first.querySelectorAll('.ui-statline > *')].map((e) => e.innerText.replace(/\\s+/g, ' ').trim()) : [],
      period: first ? first.querySelectorAll('.ui-period *').length : 0,
      periodText: first ? ((first.querySelector('.ui-period') || {}).innerText || '').replace(/\\s+/g, ' ').trim().slice(0, 200) : '',
      periodBox: box(first && first.querySelector('.ui-period')),
      current: [...document.querySelectorAll('.goen-hero__tabs a')].findIndex((a) => a.getAttribute('aria-current') === 'true'),
      tabs: [...document.querySelectorAll('.goen-hero__tabs a')].map((a) => a.textContent.trim()),
      directory: cats ? cats.textContent.trim() : '',
      directoryBox: box(cats),
    };
  })()`);
  console.log(`${label} hero=${JSON.stringify(hero)}`);
  if (hero.title.trim() !== loc.title) problem(label, `first slide is ${JSON.stringify(hero.title)}, want ${loc.title}`);
  if (hero.scrollLeft !== 0) problem(label, 'carousel is not on its first slide');
  if (!hero.period) problem(label, 'first slide has no day grid');
  if (!hero.stats.length) problem(label, 'first slide has no fact line');
  if (!hero.directoryBox || hero.directoryBox[1] >= HEIGHT) problem(label, 'the directory does not start in the viewport');
  await capture(label, `storefront.${loc.file}.png`);
}

async function backoffice(loc, path, file) {
  const label = `backoffice ${path} ${loc.cookie}`;
  const href = await navigate(ORIGIN + path);
  const facts = await settle(label);
  if (facts.lang !== loc.cookie) problem(label, `<html lang> is ${facts.lang}`);
  const admin = await evaluate(`({
    path: location.pathname,
    admin: !!document.querySelector('.goen-admin'),
    h1: (document.querySelector('h1') || {}).textContent || '',
    hints: [...document.querySelectorAll('.goen-admin__hint')].map((e) => e.textContent.trim()),
    unavailable: [...document.querySelectorAll('.goen-admin__hint[role=status]')].map((e) => e.textContent.trim()),
    tasks: [...document.querySelectorAll('.goen-admin__task')].map((e) => e.innerText.replace(/\\s+/g, ' ').trim()),
    stats: [...document.querySelectorAll('.ui-statline > *')].map((e) => e.innerText.replace(/\\s+/g, ' ').trim()),
    rows: document.querySelectorAll('table tbody tr').length,
    firstRow: ((document.querySelector('table tbody tr') || {}).innerText || '').replace(/\\s+/g, ' ').trim(),
    headings: [...document.querySelectorAll('h2')].map((h) => h.textContent.trim() + '@' + Math.round(h.getBoundingClientRect().top)),
  })`);
  console.log(`${label} admin=${JSON.stringify(admin)}`);
  if (admin.path !== path || !admin.admin) problem(label, 'not the back office: landed on ' + href);
  if (admin.unavailable.length) problem(label, 'unavailable notices: ' + JSON.stringify(admin.unavailable));
  if (!admin.rows) problem(label, 'no order rows');
  if (path === '/admin' && !admin.stats.length) problem(label, 'no 7-day stat line');
  await capture(label, file);
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
await send('Network.enable');
await send('Emulation.setDeviceMetricsOverride', { width: WIDTH, height: HEIGHT, deviceScaleFactor: 1, mobile: false });
await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'light' }] });
await send('Emulation.setScrollbarsHidden', { hidden: true });
if (kind === 'backoffice') {
  if (!process.env.ADMIN_TOKEN) throw new Error('ADMIN_TOKEN is not set');
  await send('Network.setCookie', { name: 'goen_session', value: process.env.ADMIN_TOKEN, domain: '127.0.0.1', path: '/' });
}
for (const loc of LOCALES) {
  await send('Network.setCookie', { name: 'goen_locale', value: loc.cookie, domain: '127.0.0.1', path: '/' });
  if (kind === 'storefront') {
    await storefront(loc);
  } else {
    await backoffice(loc, '/admin', `backoffice.${loc.file}.png`);
    await backoffice(loc, '/admin/orders', `backoffice-orders.${loc.file}.png`);
  }
}
ws.close();
if (problems.length) {
  console.log(`\n${problems.length} problem(s):\n  ${problems.join('\n  ')}`);
  process.exit(1);
}
console.log(`${kind}: every shot passed its checks`);
