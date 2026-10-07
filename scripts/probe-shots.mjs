// Probe only: full-page shots over CDP.
// Usage: node scripts/probe-shots.mjs emails|storefront|admin|twofa <outdir> [emailsdir]
//   storefront reads CART_TOKEN, PRODUCT_SLUG, COMPARE_SLUG_B, PICKUP_SHIP;
//   admin reads ADMIN_TOKEN; twofa reads TWOFA_TOKEN_A, TWOFA_TOKEN_B and GOEN_DATABASE_URL.

import { mkdirSync, writeFileSync, readdirSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { createHmac } from 'node:crypto';
import { resolve } from 'node:path';

const CDP_PORT = Number(process.env.CDP_PORT || 9333);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const CAP = 6000;
const [mode, outDir, extraDir] = process.argv.slice(2);
const failures = [];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

let nextId = 1;
const pending = new Map();
let ws;
function send(method, params = {}) {
  const id = nextId++;
  ws.send(JSON.stringify({ id, method, params }));
  return new Promise((res, reject) => {
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`${method} timed out`)); }, 60000);
    pending.set(id, { resolve: res, reject, timer });
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
  await settle(url);
}
async function settle(what) {
  for (let i = 0; i < 300; i++) {
    const state = String(await evaluate('document.readyState + " " + location.href'));
    if (state.startsWith('complete') && !state.endsWith('about:blank')) return;
    await sleep(100);
  }
  throw new Error(`${what} never finished loading`);
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

// Wait for web fonts and images, grow the viewport to the page, and capture.
async function capturePage(file, width, after = async () => {}) {
  await evaluate('document.fonts.ready.then(() => 1)', true);
  await after();
  const height = Math.min(CAP, Math.max(await evaluate('Math.ceil(document.documentElement.scrollHeight)'), 400));
  await metrics(width, height);
  for (let i = 0; i < 40; i++) {
    const loading = await evaluate('[...document.images].filter((i) => !i.complete).length');
    if (!loading) break;
    await sleep(250);
  }
  await sleep(500);
  const facts = await evaluate(`({
    path: location.pathname + location.search,
    h1: (document.querySelector('h1') || {}).textContent || '',
    lang: document.documentElement.lang,
    scrollW: document.documentElement.scrollWidth,
    admin: !!document.querySelector('.goen-admin'),
  })`);
  const { data } = await send('Page.captureScreenshot', { format: 'png' });
  writeFileSync(`${outDir}/${file}`, Buffer.from(data, 'base64'));
  console.log(`${file} ${facts.path} h1=${JSON.stringify(facts.h1.trim())} lang=${facts.lang} scrollW=${facts.scrollW} shotHeight=${height}`);
  return facts;
}

async function shot(path, file, width, opts = {}) {
  await metrics(width, 900);
  await navigate(ORIGIN + path);
  const facts = await capturePage(file, width, opts.after);
  if (/404|找不到/.test(facts.h1)) failures.push(`${file}: landed on a not-found page`);
  if (opts.admin && (!facts.admin || !facts.path.startsWith(path.split('?')[0]))) failures.push(`${file}: landed on ${facts.path}`);
  return facts;
}

async function firstLinks(listPath, prefix, exclude = [], limit = 1) {
  await navigate(ORIGIN + listPath);
  return evaluate(`(() => {
    const ex = ${JSON.stringify(exclude)};
    return [...new Set([...document.querySelectorAll('main a[href^="${prefix}"]')]
      .map((e) => e.getAttribute('href'))
      .filter((h) => h !== '${prefix}' && !h.startsWith('${prefix}?') && !ex.some((x) => h.startsWith(x))))].slice(0, ${limit});
  })()`);
}

async function open(page, tag) {
  mkdirSync(outDir, { recursive: true });
  ws = new WebSocket(await pageSocket());
  await new Promise((res, reject) => { ws.onopen = res; ws.onerror = reject; });
  ws.onmessage = (ev) => {
    const msg = JSON.parse(ev.data);
    if (msg.id && pending.has(msg.id)) {
      const { resolve: res, reject, timer } = pending.get(msg.id);
      pending.delete(msg.id);
      clearTimeout(timer);
      msg.error ? reject(new Error(JSON.stringify(msg.error))) : res(msg.result);
    }
  };
  await send('Page.enable');
  await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'light' }] });
  await send('Emulation.setScrollbarsHidden', { hidden: true });
  await send('Network.enable');
  await send('Network.clearBrowserCookies');
  await cookie('goen_locale', 'zh-Hant');
}

const drawer = async () => {
  await evaluate(`(async () => { const m = document.querySelector('.goen-header__menu'); if (m) m.open = true; await new Promise((d) => setTimeout(d, 1000)); })()`, true);
};

async function emails() {
  const files = readdirSync(extraDir).filter((f) => f.endsWith('.html')).sort();
  for (const f of files) {
    const stem = f.replace(/\.html$/, '');
    for (const width of [600, 375]) {
      await metrics(width, 900);
      await navigate('file://' + resolve(extraDir, f));
      await capturePage(`email-${stem}-${width}.png`, width);
    }
  }
}

async function storefront() {
  await cookie('goen_cart', process.env.CART_TOKEN);
  const p = process.env.PRODUCT_SLUG;
  const widths = [768, 1024];
  for (const width of widths) {
    for (const [name, path] of [['home', '/'], ['department', '/c/home-living'], ['product', `/p/${p}`], ['cart', '/cart'], ['checkout', '/checkout']]) {
      try { await shot(path, `storefront-${name}-${width}.png`, width); } catch (e) { failures.push(`${name}-${width}: ${e.message}`); }
    }
  }
  for (const width of [375, 768, 1440]) {
    try { await shot(`/compare?p=${p}`, `storefront-compare-one-${width}.png`, width); } catch (e) { failures.push(`compare-one-${width}: ${e.message}`); }
  }
  // The open menu on a department page, whose own entry is the current one.
  for (const [dept, path] of [['home-living', '/c/home-living'], ['phones', '/c/phones']]) {
    for (const width of [375, 768]) {
      try { await shot(path, `storefront-drawer-${dept}-${width}.png`, width, { after: drawer }); } catch (e) { failures.push(`drawer-${dept}-${width}: ${e.message}`); }
    }
  }
  // The pickup chooser: first the page that issues the nonce, then the same page
  // as the carrier's return sends the shopper back to it.
  const ship = `/checkout?ship=${process.env.PICKUP_SHIP}`;
  await metrics(1440, 900);
  await navigate(ORIGIN + ship);
  const nonce = await evaluate(`document.querySelector('input[name=pickup_n]')?.value || ''`);
  console.log('pickup nonce length', nonce.length);
  if (!nonce) failures.push('pickup: the checkout page issued no nonce');
  const store = '&pickup_chain=seven_eleven&pickup_store_code=131386&pickup_store_name=' + encodeURIComponent('信義威秀門市') +
    '&pickup_store_addr=' + encodeURIComponent('台北市信義區松壽路20號') + '&pickup_n=';
  const wrong = 'a'.repeat(nonce.length || 20);
  for (const width of [375, 768, 1440]) {
    try {
      await shot(ship, `storefront-pickup-start-${width}.png`, width);
      await shot(ship + store + nonce, `storefront-pickup-chosen-${width}.png`, width);
      await shot(ship + store + wrong, `storefront-pickup-refused-${width}.png`, width);
    } catch (e) { failures.push(`pickup-${width}: ${e.message}`); }
  }
}

async function admin() {
  await cookie('goen_session', process.env.ADMIN_TOKEN);
  await metrics(1440, 900);
  const returns = await firstLinks('/admin/returns', '/admin/returns/', [], 4);
  console.log('return links', JSON.stringify(returns));
  const pages = [
    ['picking', '/admin/orders/picking'],
    ['picking-slips', '/admin/orders/picking/slips'],
    ['returns', '/admin/returns'],
    ...returns.map((h, i) => [`return-${i + 1}`, h]),
    ['health', '/admin/health'],
    ['twofactor-off', '/admin/verify'],
  ];
  for (const width of [1440, 375]) {
    for (const [name, path] of pages) {
      try { await shot(path, `admin-${name}-${width}.png`, width, { admin: true }); } catch (e) { failures.push(`admin-${name}-${width}: ${e.message}`); }
    }
  }
  // Print: the slips as the printer gets them, at A4 width.
  await send('Emulation.setEmulatedMedia', { media: 'print', features: [{ name: 'prefers-color-scheme', value: 'light' }] });
  for (const [name, path] of [['picking-slips', '/admin/orders/picking/slips'], ['picking', '/admin/orders/picking']]) {
    try {
      await metrics(794, 1123);
      await navigate(ORIGIN + path);
      await capturePage(`admin-${name}-print.png`, 794);
      const { data } = await send('Page.printToPDF', { preferCSSPageSize: true, printBackground: true });
      writeFileSync(`${outDir}/admin-${name}-print.pdf`, Buffer.from(data, 'base64'));
    } catch (e) { failures.push(`print ${name}: ${e.message}`); }
  }
  await send('Emulation.setEmulatedMedia', { media: '', features: [{ name: 'prefers-color-scheme', value: 'light' }] });
}

const b32 = (s) => {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  for (const ch of s) bits += alphabet.indexOf(ch).toString(2).padStart(5, '0');
  const bytes = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(parseInt(bits.slice(i, i + 8), 2));
  return Buffer.from(bytes);
};
const totp = (secret) => {
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)));
  const h = createHmac('sha1', secret).update(counter).digest();
  const o = h[19] & 15;
  const n = ((h[o] & 127) << 24) | (h[o + 1] << 16) | (h[o + 2] << 8) | h[o + 3];
  return String(n % 1000000).padStart(6, '0');
};
const mailedCode = () => execFileSync('psql', [process.env.GOEN_DATABASE_URL, '-X', '-A', '-t', '-c',
  `SELECT payload->>'code' FROM outbox_messages WHERE topic = 'staff.enrolment' ORDER BY created_at DESC LIMIT 1`]).toString().trim();
const submit = async (selector, fields) => {
  await evaluate(`(() => { const f = document.querySelector('${selector}'); ${Object.entries(fields).map(([k, v]) => `f.elements['${k}'].value = '${v}';`).join(' ')} f.requestSubmit(); })()`);
  await sleep(1500);
  await settle(selector);
};

async function twofa() {
  for (const width of [1440, 375]) {
    await cookie('goen_session', process.env.TWOFA_TOKEN_A);
    await shot('/admin', `twofactor-setup-start-${width}.png`, width);
    await metrics(width, 900);
    await navigate(ORIGIN + '/admin/verify');
    await evaluate(`document.querySelector('form[action="/admin/verify/enrol"]').requestSubmit()`);
    await sleep(1500);
    await settle('enrol');
    await capturePage(`twofactor-setup-enrolling-${width}.png`, width);
    if (width === 1440) continue;
    // A wrong pair of codes, then the right pair.
    await submit('form[action="/admin/verify/confirm"]', { code: '000000', mailed_code: '00000000' });
    await capturePage(`twofactor-setup-wrong-codes-${width}.png`, width);
    await navigate(ORIGIN + '/admin/verify');
    await evaluate(`document.querySelector('form[action="/admin/verify/enrol"]').requestSubmit()`);
    await sleep(1500);
    await settle('enrol again');
    const secret = await evaluate(`[...document.querySelectorAll('.goen-twofa__secret code')].map((c) => c.textContent).join('')`);
    await submit('form[action="/admin/verify/confirm"]', { code: totp(b32(secret)), mailed_code: mailedCode() });
    await capturePage(`twofactor-setup-done-${width}.png`, width);
  }
  // A second session of the enrolled account: signed in with a password, not yet verified.
  for (const width of [1440, 375]) {
    await cookie('goen_session', process.env.TWOFA_TOKEN_B);
    await shot('/admin', `twofactor-verify-${width}.png`, width);
    await metrics(width, 900);
    await submit('form[action="/admin/verify"]', { code: '000000' });
    await capturePage(`twofactor-verify-wrong-${width}.png`, width);
  }
}

await open();
if (mode === 'emails') await emails();
else if (mode === 'storefront') await storefront();
else if (mode === 'admin') await admin();
else if (mode === 'twofa') await twofa();
else throw new Error(`unknown mode ${mode}`);

ws.close();
if (failures.length) {
  console.log(`\n${failures.length} failure(s):\n  ${failures.join('\n  ')}`);
  process.exit(1);
}
console.log('all shots captured');
