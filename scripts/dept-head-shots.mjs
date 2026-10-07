// Probe only: shots and measurements of every department head over CDP.
// Usage: node scripts/dept-head-shots.mjs <outdir>

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

const metrics = (width, height) =>
  send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: width < 600 });
const cookie = (name, value) => send('Network.setCookie', { name, value, domain: '127.0.0.1', path: '/' });

const MEASURE = `(() => {
  const rect = (el) => {
    if (!el) return null;
    const r = el.getBoundingClientRect();
    return { x: Math.round(r.x), y: Math.round(r.y + scrollY), w: Math.round(r.width), h: Math.round(r.height) };
  };
  const band = document.querySelector('.goen-listing__head .goen-band');
  const img = document.querySelector('.goen-listing__head .goen-band__media img');
  const tile = document.querySelector('.goen-listing__results .goen-tiles__grid > li');
  const notice = document.querySelector('.goen-listing__head .goen-deptnotice');
  const body = document.querySelector('.goen-listing__head .goen-band__body');
  const h1 = document.querySelector('.goen-listing__head h1');
  const vw = document.documentElement.clientWidth;
  const imgRect = rect(img);
  const lines = (el) => {
    if (!el) return 0;
    const lh = parseFloat(getComputedStyle(el).lineHeight) || el.getBoundingClientRect().height;
    return Math.round(el.getBoundingClientRect().height / lh);
  };
  return {
    path: location.pathname,
    vw,
    vh: innerHeight,
    scrollW: document.documentElement.scrollWidth,
    band: rect(band),
    photo: imgRect,
    photoShare: imgRect ? Math.round((imgRect.w / vw) * 1000) / 10 : null,
    photoFetchPriority: img ? img.getAttribute('fetchpriority') : null,
    photoSrcset: img ? !!img.getAttribute('srcset') : null,
    bodyBox: rect(body),
    noticeBox: rect(notice),
    noticeLines: notice ? lines(notice.querySelector('a')) : 0,
    noticeText: notice ? notice.textContent.replace(/\\s+/g, ' ').trim() : null,
    h1Text: h1 ? h1.textContent.trim() : null,
    h1Size: h1 ? getComputedStyle(h1).fontSize : null,
    h1Lines: lines(h1),
    chips: document.querySelectorAll('.goen-listing__head .goen-pagehead__chip').length,
    facts: document.querySelectorAll('.goen-listing__head .ui-statline > div').length,
    firstTile: rect(tile),
    firstTileTop: tile ? Math.round(tile.getBoundingClientRect().top + scrollY) : null,
    paddingTop: body ? getComputedStyle(body).paddingTop : null,
    tone: document.querySelector('.goen-listing__head .goen-pagehead')?.getAttribute('data-tone') || null,
  };
})()`;

async function load(path, width, height, opts = {}) {
  await metrics(width, height);
  await navigate(ORIGIN + path);
  await evaluate('document.fonts.ready.then(() => 1)', true);
  if (opts.text200) await evaluate(`document.documentElement.style.fontSize = '200%'; 1`);
  for (let i = 0; i < 40; i++) {
    const loading = await evaluate('[...document.images].filter((i) => !i.complete && i.loading !== "lazy").length');
    if (!loading) break;
    await sleep(250);
  }
  await sleep(400);
}

async function shoot(file, width, cap = 1800) {
  const height = Math.min(cap, Math.max(await evaluate('Math.ceil(document.documentElement.scrollHeight)'), 400));
  await metrics(width, height);
  await sleep(300);
  const { data } = await send('Page.captureScreenshot', { format: 'png' });
  writeFileSync(`${outDir}/${file}`, Buffer.from(data, 'base64'));
}

async function measure(label, path, width, height, opts = {}) {
  try {
    await load(path, width, height, opts);
    const m = await evaluate(MEASURE);
    console.log(`MEASURE ${label} ${JSON.stringify(m)}`);
    if (m.scrollW > m.vw) failures.push(`${label}: scrolls sideways (${m.scrollW} > ${m.vw})`);
    if (opts.file) await shoot(opts.file, width);
    return m;
  } catch (e) {
    failures.push(`${label}: ${e.message}`);
    return null;
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
await send('Network.clearBrowserCookies');
await cookie('goen_locale', 'zh-Hant');

// The top-level departments are the header's links.
await metrics(1440, 900);
await navigate(ORIGIN + '/');
const departments = await evaluate(`[...new Set([...document.querySelectorAll('header a[href^="/c/"], nav a[href^="/c/"]')]
  .map((a) => a.getAttribute('href')).filter((h) => /^\\/c\\/[a-z0-9-]+$/.test(h)))]`);
console.log('departments', JSON.stringify(departments));
if (departments.length === 0) failures.push('no department links found');

const seen = {};
for (const path of departments) {
  const slug = path.replace('/c/', '');
  for (const width of [1440, 375]) {
    const m = await measure(`${slug} ${width}`, path, width, width === 1440 ? 900 : 812, { file: `${slug}-${width}.png` });
    if (width === 1440 && m) seen[slug] = m;
  }
  for (const width of [320, 768, 1024]) {
    await measure(`${slug} ${width}`, path, width, width === 320 ? 640 : 900);
  }
}

// One with a campaign, one without a photograph (or else without a campaign).
const slugs = Object.keys(seen);
const withNotice = slugs.find((s) => seen[s].noticeBox) || slugs[0];
const bare = slugs.find((s) => !seen[s].photo) || slugs.find((s) => !seen[s].noticeBox && s !== withNotice) || slugs[1];
console.log(`MEASURE picks withNotice=${withNotice} noPhotoOrNoNotice=${bare} photoless=${slugs.filter((s) => !seen[s].photo).join(',') || 'none'}`);
for (const slug of [withNotice, bare]) {
  const path = `/c/${slug}`;
  await measure(`${slug} 320 shot`, path, 320, 640, { file: `${slug}-320.png` });
  await measure(`${slug} 375 text200`, path, 375, 812, { file: `${slug}-375-text200.png`, text200: true });
  await measure(`${slug} 1440 text200`, path, 1440, 900, { file: `${slug}-1440-text200.png`, text200: true });
  for (const width of [1440, 375]) {
    await send('Emulation.setEmulatedMedia', { features: [{ name: 'forced-colors', value: 'active' }] });
    await measure(`${slug} ${width} forced`, path, width, width === 1440 ? 900 : 812, { file: `${slug}-${width}-forced.png` });
    await send('Emulation.setEmulatedMedia', { features: [{ name: 'forced-colors', value: 'none' }] });
  }
}

// English, for the long names and the notice's words.
await cookie('goen_locale', 'en');
for (const slug of new Set([withNotice, bare])) {
  for (const width of [1440, 375]) {
    await measure(`${slug} ${width} en`, `/c/${slug}`, width, width === 1440 ? 900 : 812, { file: `${slug}-${width}-en.png` });
  }
}
await cookie('goen_locale', 'zh-Hant');

// A department's sub-categories: the gate's /c/phones, a sole sub-category and a deeper one.
for (const path of ['/c/phones', '/c/tea-coffee', '/c/accessories', '/c/chargers']) {
  const slug = path.replace('/c/', '');
  await measure(`sub ${slug} 375`, path, 375, 812, { file: `sub-${slug}-375.png` });
  await measure(`sub ${slug} 1440`, path, 1440, 900, { file: `sub-${slug}-1440.png` });
}

// The widths between a phone and a desktop, for the departments whose heads differ most.
for (const slug of ['tech', 'beauty', 'food-drink']) {
  for (const width of [600, 744, 768, 1024]) {
    await measure(`mid ${slug} ${width}`, `/c/${slug}`, width, 900, { file: `mid-${slug}-${width}.png` });
  }
}

// A long English name at phone widths with 200% text, and the desktop wrap as a regression check.
await cookie('goen_locale', 'en');
for (const slug of ['books-stationery', 'accessories']) {
  for (const width of [375, 320]) {
    await measure(`en ${slug} ${width} text200`, `/c/${slug}`, width, 812, { file: `en-${slug}-${width}-text200.png`, text200: true });
  }
  await measure(`en ${slug} 1440 regress`, `/c/${slug}`, 1440, 900, { file: `en-${slug}-1440-regress.png` });
}
await cookie('goen_locale', 'zh-Hant');

ws.close();
if (failures.length) {
  console.log(`\n${failures.length} failure(s):\n  ${failures.join('\n  ')}`);
  process.exit(1);
}
console.log('all shots captured');
