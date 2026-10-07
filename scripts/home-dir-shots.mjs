// Probe only: the home page at several widths and states, with MEASURE lines.
// Usage: node scripts/home-dir-shots.mjs <outdir>

import { mkdirSync, writeFileSync } from 'node:fs';

const CDP_PORT = Number(process.env.CDP_PORT || 9333);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const CAP = 7000;
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

// What the finding cares about, read from the page as it is.
const MEASURE = `(() => {
  const px = (e, p) => parseFloat(getComputedStyle(e)[p]);
  const box = (e) => { const r = e.getBoundingClientRect(); return Math.round(r.width) + 'x' + Math.round(r.height * 10) / 10; };
  const dir = document.querySelector('.goen-cats__grid');
  const name = dir && dir.querySelector('.goen-cat__name');
  const row = name && name.closest('.goen-cat');
  const out = {
    layout: dir ? dir.className.replace('goen-cats__grid ', '') : 'none',
    rows: dir ? dir.querySelectorAll('.goen-cat').length : 0,
    nameSize: name ? px(name, 'fontSize') : null,
    nameWeight: name ? getComputedStyle(name).fontWeight : null,
    nameLines: name ? Math.round(name.getBoundingClientRect().height / px(name, 'lineHeight')) : null,
    rowBox: row ? box(row) : null,
    rowHeights: dir ? [...dir.querySelectorAll('.goen-cat')].map((e) => Math.round(e.getBoundingClientRect().height)).join(',') : null,
    dirBox: dir ? box(dir) : null,
    headings: [...document.querySelectorAll('.goen-home__heading')].map((h) => h.textContent.trim().slice(0, 24) + ' ' + px(h, 'fontSize') + '/' + getComputedStyle(h).fontWeight + '/' + getComputedStyle(h).lineHeight),
    meta: (document.querySelector('.goen-home__meta') || {}).textContent || null,
    scrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
  };
  return JSON.stringify(out);
})()`;

const lines = [];
function measure(label, json) {
  const line = `MEASURE ${label} ${json}`;
  console.log(line);
  lines.push(line);
}

async function load(width, locale, rootPercent) {
  await metrics(width, 900);
  await cookie('goen_locale', locale);
  await navigate(ORIGIN + '/');
  if (rootPercent) await evaluate(`document.documentElement.style.fontSize = '${rootPercent}%'`);
  await evaluate('document.fonts.ready.then(() => 1)', true);
  await sleep(300);
}

async function shot(file, width) {
  const height = Math.min(CAP, Math.max(await evaluate('Math.ceil(document.documentElement.scrollHeight)'), 400));
  await metrics(width, height);
  for (let i = 0; i < 40; i++) {
    const loading = await evaluate('[...document.images].filter((i) => !i.complete).length');
    if (!loading) break;
    await sleep(250);
  }
  await sleep(400);
  const { data } = await send('Page.captureScreenshot', { format: 'png' });
  writeFileSync(`${outDir}/${file}.png`, Buffer.from(data, 'base64'));
}

// The directory cut down to the section that matters, shot at its own height.
async function shotDirectory(file, width) {
  const clip = await evaluate(`(() => {
    const h = document.querySelector('#cats-heading');
    if (!h) return null;
    const s = h.closest('section').getBoundingClientRect();
    return { x: 0, y: s.top + scrollY, width: innerWidth, height: Math.ceil(s.height) + 24, scale: 1 };
  })()`);
  if (!clip) return;
  await metrics(width, Math.min(CAP, Math.ceil(clip.y + clip.height + 40)));
  await sleep(300);
  const { data } = await send('Page.captureScreenshot', { format: 'png', clip, captureBeyondViewport: true });
  writeFileSync(`${outDir}/${file}.png`, Buffer.from(data, 'base64'));
}

// Edge states made by changing the rendered list in place: the stylesheet is
// what is under test, not the server's choice of layout.
const LONG_ZH = '居家生活與收納整理選物專區特別長的館名測試';
const LONG_EN = 'Home Living Storage and Organisation Essentials Department';
function variant(kind) {
  return `(() => {
    const dir = document.querySelector('.goen-cats__grid');
    if (!dir) return 'no directory';
    const lis = [...dir.children];
    const lang = document.documentElement.lang;
    const setName = (li, text) => { li.querySelector('.goen-cat__name').textContent = text; };
    if ('${kind}' === 'long') {
      setName(lis[0], lang.startsWith('en') ? '${LONG_EN}' : '${LONG_ZH}');
      const subs = lis[0].querySelector('.goen-cat__subs');
      if (subs) subs.textContent = lang.startsWith('en') ? 'Kitchen\\u00a0&\\u00a0dining\\u00a0· Storage\\u00a0&\\u00a0shelving\\u00a0· Lighting' : '廚房餐具\\u00a0· 收納整理\\u00a0· 燈具照明';
      return 'long names';
    }
    if ('${kind}' === 'nophoto') {
      const first = lis[0].querySelector('.goen-cat__photo');
      if (first) {
        const mark = document.createElement('span');
        mark.className = 'goen-cat__mark';
        mark.setAttribute('aria-hidden', 'true');
        mark.textContent = lis[0].querySelector('.goen-cat__name').textContent.slice(0, 1);
        first.replaceWith(mark);
      }
      return 'first row without a photograph';
    }
    if ('${kind}' === 'one') {
      lis.slice(1).forEach((li) => li.remove());
      return 'one row';
    }
    if ('${kind}' === 'ten') {
      dir.className = 'goen-cats__grid goen-cats__grid--columns';
      while (dir.children.length < 10) dir.appendChild(lis[dir.children.length % lis.length].cloneNode(true));
      return 'ten rows, columns layout';
    }
    if ('${kind}' === 'noitems') {
      lis[0].querySelector('.goen-cat__count').textContent = '0\\u00a0' + (lang.startsWith('en') ? 'items' : '件');
      return 'department with no items';
    }
    return 'unknown';
  })()`;
}

async function main() {
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

  for (const locale of ['zh-Hant', 'en']) {
    const tag = locale === 'en' ? 'en' : 'zh';
    for (const width of [1440, 1024, 768, 375, 320]) {
      try {
        await load(width, locale);
        measure(`${tag} ${width}`, await evaluate(MEASURE));
        await shot(`home-${tag}-${width}`, width);
        await load(width, locale);
        await shotDirectory(`dir-${tag}-${width}`, width);
        for (const kind of ['long', 'nophoto', 'noitems', 'one', 'ten']) {
          if (kind === 'ten' && width < 375) continue;
          await load(width, locale);
          const what = await evaluate(variant(kind));
          await sleep(200);
          measure(`${tag} ${width} ${kind}`, await evaluate(MEASURE));
          if (width === 1440 || width === 375 || width === 320) await shotDirectory(`dir-${tag}-${width}-${kind}`, width);
          void what;
        }
      } catch (e) { failures.push(`${tag} ${width}: ${e.message}`); }
    }
    try {
      await load(375, locale, 200);
      measure(`${tag} 375 text200`, await evaluate(MEASURE));
      await shot(`home-${tag}-375-text200`, 375);
      await load(375, locale, 200);
      await shotDirectory(`dir-${tag}-375-text200`, 375);
      await load(320, locale, 200);
      measure(`${tag} 320 text200`, await evaluate(MEASURE));
      await shotDirectory(`dir-${tag}-320-text200`, 320);
    } catch (e) { failures.push(`${tag} text200: ${e.message}`); }
    try {
      await send('Emulation.setEmulatedMedia', { features: [{ name: 'forced-colors', value: 'active' }, { name: 'prefers-color-scheme', value: 'light' }] });
      await load(1440, locale);
      measure(`${tag} 1440 forced`, await evaluate(MEASURE));
      await shot(`home-${tag}-1440-forced`, 1440);
      await load(1440, locale);
      await shotDirectory(`dir-${tag}-1440-forced`, 1440);
    } catch (e) { failures.push(`${tag} forced: ${e.message}`); }
    await send('Emulation.setEmulatedMedia', { features: [{ name: 'forced-colors', value: 'none' }, { name: 'prefers-color-scheme', value: 'light' }] });
  }

  writeFileSync(`${outDir}/measure.txt`, lines.join('\n') + '\n');
  ws.close();
  if (failures.length) {
    console.log(`\n${failures.length} failure(s):\n  ${failures.join('\n  ')}`);
    process.exit(1);
  }
  console.log('all shots captured');
}
main().catch((e) => { console.error(e); process.exit(1); });
