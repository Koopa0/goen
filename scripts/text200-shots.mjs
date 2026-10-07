// Probe only: storefront shots at 200% text and normal size, with MEASURE lines,
// and a Tab walk that measures the focused control against the fixed bars.
// Usage: node scripts/text200-shots.mjs <outdir>   (reads CART_TOKEN)

import { mkdirSync, writeFileSync } from 'node:fs';

const CDP_PORT = Number(process.env.CDP_PORT || 9333);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const CAP = 6000;
const outDir = process.argv[2];
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
const setText = (big) => evaluate(`document.documentElement.style.fontSize = ${big ? "'200%'" : "''"}; 1`);

const FACTS = `(() => {
  const box = (sel) => {
    const e = document.querySelector(sel);
    if (!e) return null;
    const r = e.getBoundingClientRect();
    return [Math.round(r.x), Math.round(r.y), Math.round(r.width), Math.round(r.height)].join(',');
  };
  return JSON.stringify({
    scrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
    innerWidth: window.innerWidth,
    rootFont: getComputedStyle(document.documentElement).fontSize,
    buybar: document.documentElement.dataset.buybar || '',
    stepper: box('.goen-stepper'),
    stepperValue: box('.goen-stepper__value'),
    lineName: box('.goen-line__name'),
    lineMoney: box('.goen-line--cart .goen-line__money'),
    thumb: box('.goen-line--cart .goen-line__thumb'),
    cartBar: box('.goen-cart__summary'),
    buyBarBox: box('.goen-buybar'),
    buyBtn: box('.goen-buybar__btn'),
    buyBarOverflow: (() => { const e = document.querySelector('.goen-buybar'); return e ? e.scrollWidth - e.clientWidth : null; })(),
    footerBottom: box('.goen-footer__legal') || box('.goen-footer'),
    lastFooterLink: (() => { const l = [...document.querySelectorAll('.goen-footer a')].pop(); if (!l) return null; const r = l.getBoundingClientRect(); return Math.round(r.bottom); })(),
    couponInput: box('.goen-checkout__couponrow .goen-input'),
    summaryTotal: box('.goen-summary__row--total'),
  });
})()`;

async function measure(name) {
  const facts = JSON.parse(await evaluate(FACTS));
  const flag = facts.scrollWidth > facts.innerWidth ? ' OVERFLOW' : '';
  console.log(`MEASURE ${name} ${Object.entries(facts).map(([k, v]) => `${k}=${v}`).join(' ')}${flag}`);
  return facts;
}

async function save(file, fmt = 'png') {
  const { data } = await send('Page.captureScreenshot', { format: fmt, ...(fmt === 'jpeg' ? { quality: 55 } : {}) });
  writeFileSync(`${outDir}/${file}`, Buffer.from(data, 'base64'));
}

// Full-page shot, optional 200% text.
async function shot(path, name, width, big) {
  await metrics(width, 900);
  await navigate(ORIGIN + path);
  await setText(big);
  await evaluate('document.fonts.ready.then(() => 1)', true);
  await sleep(300);
  const height = Math.min(CAP, Math.max(await evaluate('Math.ceil(document.documentElement.scrollHeight)'), 400));
  await metrics(width, height);
  for (let i = 0; i < 40; i++) {
    if (!(await evaluate('[...document.images].filter((i) => !i.complete).length'))) break;
    await sleep(250);
  }
  await sleep(500);
  const file = `${name}.png`;
  await measure(name);
  await save(file);
  console.log(`${file} ${path}`);
}

// Viewport-sized shot: the fixed bars are where they are for a reader.
async function viewport(path, name, width, height, big, scrollTo) {
  await metrics(width, height);
  await navigate(ORIGIN + path);
  await setText(big);
  await evaluate('document.fonts.ready.then(() => 1)', true);
  await sleep(300);
  if (scrollTo) {
    await evaluate(scrollTo);
    await sleep(900);
  }
  await measure(name);
  await save(`${name}.png`);
}

async function key(k, code, vk, shift = false) {
  const base = { key: k, code, windowsVirtualKeyCode: vk, modifiers: shift ? 8 : 0 };
  await send('Input.dispatchKeyEvent', { type: 'rawKeyDown', ...base });
  await send('Input.dispatchKeyEvent', { type: 'keyUp', ...base });
}

const FOCUS = `(() => {
  const a = document.activeElement;
  if (!a || a === document.body || a === document.documentElement) return null;
  const r = a.getBoundingClientRect();
  const vh = window.innerHeight;
  const bars = [['cartBar', '.goen-cart__summary'], ['buyBar', '.goen-buybar']].map(([n, s]) => {
    const e = document.querySelector(s);
    if (!e) return null;
    const cs = getComputedStyle(e);
    const b = e.getBoundingClientRect();
    const shown = cs.visibility !== 'hidden' && cs.display !== 'none' && b.height > 0 && b.top < vh && b.bottom > 0 && !e.contains(a);
    return { n, shown, top: Math.round(b.top), bottom: Math.round(b.bottom), height: Math.round(b.height) };
  }).filter(Boolean);
  const obscured = bars.filter((b) => b.shown && r.bottom > b.top && r.top < b.bottom).map((b) => b.n);
  const label = a.tagName.toLowerCase() + (a.id ? '#' + a.id : '') + (a.className && typeof a.className === 'string' ? '.' + a.className.split(' ')[0] : '');
  return JSON.stringify({
    label,
    top: Math.round(r.top), bottom: Math.round(r.bottom), vh,
    scrollY: Math.round(window.scrollY),
    bars: bars.filter((b) => b.shown).map((b) => b.n + ':' + b.top + '-' + b.bottom).join('|'),
    obscured: obscured.join('|'),
    offscreen: r.bottom <= 0 || r.top >= vh,
    buybar: document.documentElement.dataset.buybar || '',
  });
})()`;

async function tabWalk(path, name, width, height, big, maxTabs) {
  await metrics(width, height);
  await navigate(ORIGIN + path);
  await setText(big);
  await evaluate('document.fonts.ready.then(() => 1)', true);
  await sleep(500);
  let bad = 0;
  for (let i = 1; i <= maxTabs; i++) {
    await key('Tab', 'Tab', 9);
    await sleep(350);
    const raw = await evaluate(FOCUS);
    if (!raw) { console.log(`MEASURE ${name} tab=${i} focus=none`); continue; }
    const f = JSON.parse(raw);
    const inMain = f.top >= 0;
    console.log(`MEASURE ${name} tab=${i} el=${f.label} top=${f.top} bottom=${f.bottom} vh=${f.vh} scrollY=${f.scrollY} bars=${f.bars || '-'} buybar=${f.buybar} obscured=${f.obscured || 'no'}${f.offscreen ? ' OFFSCREEN' : ''}`);
    if (f.obscured) bad++;
    if (inMain) await save(`${name}-tab${String(i).padStart(2, '0')}.jpg`, 'jpeg');
  }
  console.log(`MEASURE ${name} tabsObscured=${bad}`);
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
await send('Emulation.setFocusEmulationEnabled', { enabled: true });
await send('Network.enable');
await send('Network.clearBrowserCookies');
await cookie('goen_locale', 'zh-Hant');
if (process.env.CART_TOKEN) await cookie('goen_cart', process.env.CART_TOKEN);

// The first product whose buy bar is not empty (one that can be bought).
async function buyableProduct() {
  await navigate(ORIGIN + '/c/home-living');
  const links = await evaluate(`[...new Set([...document.querySelectorAll('main a[href^="/p/"]')].map((a) => a.getAttribute('href')))]`);
  for (const href of links.slice(0, 12)) {
    await navigate(ORIGIN + href);
    const ok = await evaluate(`!!document.querySelector('#buybar .goen-buybar__btn') && !!document.querySelector('#add-to-cart:not([disabled])')`);
    if (ok) { console.log('buyable product', href); return href; }
  }
  console.log('no buyable product found, using the watch');
  return '/p/meridian-watch-c1';
}
const PRODUCT = await buyableProduct();
const PAST_CTA = `(() => { const b = document.getElementById('add-to-cart'); window.scrollTo(0, b ? b.getBoundingClientRect().bottom + scrollY + 40 : 700); return 1; })()`;

async function setLocale(locale) {
  await navigate(ORIGIN + '/');
  await evaluate(`fetch('/locale', { method: 'POST', credentials: 'same-origin', headers: { 'content-type': 'application/x-www-form-urlencoded' }, body: 'locale=${locale}&return=/' }).then(() => 1)`, true);
}
const SOLD = process.env.SOLD_SLUG ? `/p/${process.env.SOLD_SLUG}` : null;
const NORM = process.env.NORM_SLUG ? `/p/${process.env.NORM_SLUG}` : PRODUCT;
const PAST_RESTOCK = `(() => { const b = document.getElementById('restock'); window.scrollTo(0, b ? b.getBoundingClientRect().bottom + scrollY + 40 : 700); return 1; })()`;
const TO_BOTTOM = `(() => { window.scrollTo(0, document.documentElement.scrollHeight); return 1; })()`;
const ADD = `(() => { document.getElementById('add-to-cart').click(); return 1; })()`;

// A product page after pressing add: the bar then carries the outcome and 查看購物車.
async function justAdded(name, width, height, big) {
  await metrics(width, height);
  await navigate(ORIGIN + NORM);
  await setText(big);
  await evaluate(ADD);
  await sleep(2500);
  await setText(big);
  await evaluate(PAST_CTA);
  await sleep(900);
  await measure(name);
  await save(`${name}.png`);
}

const withSession = async (fn) => {
  await cookie('goen_session', process.env.CUST_TOKEN);
  try { await fn(); } finally { await send('Network.deleteCookies', { name: 'goen_session', domain: '127.0.0.1', path: '/' }); }
};

const mixedCart = async (fn) => {
  if (!process.env.MIXED_CART_TOKEN) throw new Error('no mixed cart');
  await cookie('goen_cart', process.env.MIXED_CART_TOKEN);
  try { await fn(); } finally { await cookie('goen_cart', process.env.CART_TOKEN || ''); }
};

const base = [
  () => shot(PRODUCT, 'text200-product-375', 375, true),
  () => shot('/cart', 'text200-cart-375', 375, true),
  () => shot('/checkout', 'text200-checkout-375', 375, true),
  () => shot(PRODUCT, 'normal-product-375', 375, false),
  () => shot('/cart', 'normal-cart-375', 375, false),
  () => shot('/cart', 'normal-cart-320', 320, false),
  () => shot('/checkout', 'normal-checkout-375', 375, false),
  () => shot(PRODUCT, 'normal-product-1440', 1440, false),
  () => shot('/cart', 'normal-cart-1440', 1440, false),
  () => shot('/checkout', 'normal-checkout-1440', 1440, false),
  () => viewport('/cart', 'view200-cart-375', 375, 667, true),
  () => viewport(PRODUCT, 'view200-product-375', 375, 667, true, PAST_CTA),
  () => viewport(PRODUCT, 'viewnormal-product-375', 375, 667, false, PAST_CTA),
  // The page bottom with the bar showing.
  () => viewport(PRODUCT, 'bottom200-product-375', 375, 667, true, TO_BOTTOM),
  () => viewport(PRODUCT, 'bottomnormal-product-375', 375, 667, false, TO_BOTTOM),
  () => tabWalk('/cart', 'tab200-cart-375', 375, 667, true, 25),
  () => tabWalk('/cart', 'tabnormal-cart-375', 375, 667, false, 25),
  () => tabWalk(PRODUCT, 'tab200-product-375', 375, 667, true, 60),
  () => tabWalk(PRODUCT, 'tabnormal-product-375', 375, 667, false, 60),
];

const states = (lang) => [
  // Sold out: the restock bar.
  ...(SOLD ? [
    () => viewport(SOLD, `${lang}-soldout-normal-375`, 375, 667, false, PAST_RESTOCK),
    () => viewport(SOLD, `${lang}-soldout-200-375`, 375, 667, true, PAST_RESTOCK),
    () => viewport(SOLD, `${lang}-soldout-200-375-bottom`, 375, 667, true, TO_BOTTOM),
    () => tabWalk(SOLD, `${lang}-tab200-soldout-375`, 375, 667, true, 40),
  ] : []),
  // A cart with a sold-out line, a low-stock line and a third.
  () => mixedCart(() => shot('/cart', `${lang}-mixedcart-normal-375`, 375, false)),
  () => mixedCart(() => shot('/cart', `${lang}-mixedcart-normal-320`, 320, false)),
  () => mixedCart(() => shot('/cart', `${lang}-mixedcart-200-375`, 375, true)),
  () => mixedCart(() => viewport('/cart', `${lang}-mixedcart-view200-375`, 375, 667, true)),
  () => mixedCart(() => tabWalk('/cart', `${lang}-tab200-mixedcart-375`, 375, 667, true, 40)),
  () => mixedCart(() => shot('/cart', `${lang}-mixedcart-normal-1440`, 1440, false)),
  () => shot(`/orders/${process.env.PLACED_ORDER}/pay`, `${lang}-pay-normal-375`, 375, false),
  () => shot(`/orders/${process.env.PLACED_ORDER}/pay`, `${lang}-pay-200-375`, 375, true),
  () => withSession(() => shot(`/orders/${process.env.RETURN_FORM_ORDER}`, `${lang}-order-normal-375`, 375, false)),
  () => withSession(() => shot(`/orders/${process.env.RETURN_FORM_ORDER}`, `${lang}-order-200-375`, 375, true)),
  () => shot('/checkout', `${lang}-checkout-200-375`, 375, true),
  () => shot(PRODUCT, `${lang}-product-200-375`, 375, true),
  () => shot(PRODUCT, `${lang}-product-normal-375`, 375, false),
  () => justAdded(`${lang}-justadded-normal-375`, 375, 667, false),
  () => justAdded(`${lang}-justadded-200-375`, 375, 667, true),
];

const steps = [
  ...base,
  ...states('zh'),
  () => setLocale('en'),
  ...states('en'),
  () => setLocale('zh-Hant'),
];
for (const [i, step] of steps.entries()) {
  try { await step(); } catch (e) { failures.push(`step ${i}: ${e.message}`); console.log(`FAIL step ${i}: ${e.message}`); }
}

ws.close();
if (failures.length) {
  console.log(`\n${failures.length} failure(s):\n  ${failures.join('\n  ')}`);
  process.exit(1);
}
console.log('all shots captured');
