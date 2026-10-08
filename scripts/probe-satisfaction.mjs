// Probe only: whole-product screenshots over CDP.
// Usage: node scripts/probe-satisfaction.mjs storefront|admin|flows|pay <outdir>
// Reads the values scripts/check-layout.sql and the workflow's extra fixtures write into the environment.

import { mkdirSync, writeFileSync, appendFileSync } from 'node:fs';

const CDP_PORT = Number(process.env.CDP_PORT || 9333);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const CAP = 12000;
const [mode, outDir] = process.argv.slice(2);
const failures = [];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const env = process.env;

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
async function settle(what) {
  for (let i = 0; i < 300; i++) {
    const state = String(await evaluate('document.readyState + " " + location.href'));
    if (state.startsWith('complete') && !state.endsWith('about:blank')) return;
    await sleep(100);
  }
  throw new Error(`${what} never finished loading`);
}
async function navigate(url) {
  await send('Page.navigate', { url: 'about:blank' });
  for (let i = 0; i < 100 && (await evaluate('location.href')) !== 'about:blank'; i++) await sleep(100);
  await send('Page.navigate', { url });
  await settle(url);
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

// Who is looking: a guest, a guest with the cart, the placer of the unpaid order, a signed-in customer, staff.
async function as(who, lang) {
  await send('Network.clearBrowserCookies');
  await cookie('goen_locale', lang);
  if (who === 'cart') await cookie('goen_cart', env.CART_TOKEN);
  if (who === 'placed') await cookie('goen_placed', env.PLACED_TOKEN);
  if (who === 'customer') await cookie('goen_session', env.CUST_TOKEN);
  if (who === 'admin') await cookie('goen_session', env.ADMIN_TOKEN);
}

const manifest = [];
// Wait for fonts and images, grow the viewport to the page, and capture.
async function capturePage(file, meta, after = async () => {}) {
  await evaluate('document.fonts.ready.then(() => 1)', true);
  await after();
  const height = Math.min(CAP, Math.max(await evaluate('Math.ceil(document.documentElement.scrollHeight)'), 400));
  await metrics(meta.width, height);
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
  const line = { file, ...meta, path: facts.path, h1: facts.h1.trim(), htmlLang: facts.lang, scrollW: facts.scrollW, shotHeight: height, capped: height === CAP };
  manifest.push(line);
  appendFileSync(`${outDir}/manifest-${mode}.jsonl`, JSON.stringify(line) + '\n');
  console.log(`${file} ${facts.path} h1=${JSON.stringify(line.h1)} lang=${facts.lang} scrollW=${facts.scrollW} h=${height}`);
  return facts;
}

// shot: one page, as `who`, at each width and language asked for.
// spec: { name, path, who, widths, langs, big (also at 200% text on a phone), admin, notFound }
async function shot(spec) {
  const { name, path, who = 'guest', widths = [1440, 375], langs = ['zh-Hant'], big = false } = spec;
  if (!path) { failures.push(`${name}: no path (nothing in the fixtures to point it at)`); return; }
  const runs = [];
  for (const lang of langs) for (const width of widths) {
    if (lang !== 'zh-Hant' && width !== 1440) continue;
    runs.push({ lang, width, text: '100%' });
  }
  if (big) runs.push({ lang: 'zh-Hant', width: 375, text: '200%' });
  for (const run of runs) {
    const file = `${name}-${run.width}-${run.lang === 'zh-Hant' ? 'zh' : 'en'}${run.text === '200%' ? '-text200' : ''}.png`;
    try {
      await as(who, run.lang);
      await metrics(run.width, 900);
      await navigate(ORIGIN + path);
      const after = async () => {
        if (run.text === '200%') {
          await evaluate(`document.documentElement.style.fontSize = '200%'`);
          await sleep(500);
        }
        if (spec.after) await spec.after();
      };
      const facts = await capturePage(file, { page: name, state: spec.state || '', width: run.width, lang: run.lang, text: run.text, who, requested: path }, after);
      const missing = /404|找不到|not found/i.test(facts.h1);
      if (missing && !spec.notFound) failures.push(`${file}: landed on a not-found page (${facts.path})`);
      if (spec.admin && !facts.admin) failures.push(`${file}: not the back office (${facts.path})`);
    } catch (e) { failures.push(`${file}: ${e.message}`); }
  }
}

const hrefs = (selector) => evaluate(`[...new Set([...document.querySelectorAll(${JSON.stringify(selector)})].map((e) => e.getAttribute('href')))]`);

async function open() {
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
}

const menuOpen = async () => {
  await evaluate(`(async () => {
    const m = document.querySelector('.goen-header__menu') || document.querySelector('header details');
    if (m) m.open = true;
    await new Promise((d) => setTimeout(d, 1000));
  })()`, true);
};

// Makes the campaign slide that has no photograph the current one, and says whether it is in view.
async function textSlide() {
  const seen = await evaluate(`(async () => {
    const slide = document.querySelector('.goen-hero__slide--text');
    if (!slide) return 'no slide without a photograph in the page';
    const dots = [...document.querySelectorAll('.goen-hero__tabs a')];
    const slides = [...slide.parentElement.children];
    const dot = dots[slides.indexOf(slide)];
    if (dot) dot.click();
    slide.scrollIntoView({ inline: 'start', block: 'nearest' });
    await new Promise((d) => setTimeout(d, 1200));
    const r = slide.getBoundingClientRect();
    return r.left >= -2 && r.left < innerWidth / 2 ? 'in view' : 'NOT in view, left=' + Math.round(r.left);
  })()`, true);
  console.log('slide without a photograph:', seen);
  if (seen !== 'in view') failures.push('home-slide-without-photo: ' + seen);
}

async function storefront() {
  // The department that carries a campaign, and one of its sub-categories.
  await as('guest', 'zh-Hant');
  await metrics(1440, 900);
  await navigate(ORIGIN + '/');
  const depts = (await hrefs('a[href^="/c/"]')).filter((h) => !h.includes('?'));
  console.log('departments', JSON.stringify(depts));
  let campaignDept = '';
  let subCategory = '';
  for (const d of depts) {
    await navigate(ORIGIN + d);
    const campaign = await evaluate(`!!document.querySelector('main a[href^="/s/"], main [class*="campaign"]')`);
    if (campaign && !campaignDept) campaignDept = d;
    if (!subCategory) {
      const inside = (await hrefs('main a[href^="/c/"]')).filter((h) => h !== d && !h.includes('?'));
      if (inside.length) subCategory = inside[0];
    }
  }
  if (!campaignDept) { campaignDept = depts[0]; console.log('no department showed a campaign; using', campaignDept); }
  if (!subCategory) subCategory = depts.find((d) => d !== campaignDept);
  console.log('campaign department', campaignDept, 'sub-category', subCategory);

  const compare = [env.PRODUCT_SLUG, env.COMPARE_SLUG_B, env.COMPARE_SLUG_C].filter(Boolean).map((s) => 'p=' + encodeURIComponent(s)).join('&');
  const pages = [
    { name: 'home', path: '/', langs: ['zh-Hant', 'en'], big: true },
    { name: 'department-campaign', path: campaignDept, langs: ['zh-Hant', 'en'], big: true },
    { name: 'category', path: subCategory, big: true },
    { name: 'product-variants', path: `/p/${env.MULTI_VARIANT_SLUG}`, langs: ['zh-Hant', 'en'], big: true },
    { name: 'product-campaign', path: `/p/${env.CAMPAIGN_PRODUCT}`, state: 'on campaign', big: true },
    { name: 'product-related', path: `/p/${env.MULTI_VARIANT_SLUG}`, widths: [768, 1024], state: 'related products' },
    { name: 'home-slide-without-photo', path: '/', widths: [768, 375], state: 'campaign slide without a photograph made current', after: textSlide },
    { name: 'search', path: `/search?q=${encodeURIComponent(env.SEARCH_TERM || '')}` },
    { name: 'deals', path: '/deals' },
    { name: 'campaign', path: '/s/layout-campaign' },
    { name: 'compare-three', path: `/compare?${compare}` },
    { name: 'cart-two-items', path: '/cart', who: 'cart', langs: ['zh-Hant', 'en'] },
    { name: 'checkout-guest-home', path: `/checkout?ship=${env.HOME_SHIP}`, who: 'cart', langs: ['zh-Hant', 'en'] },
    { name: 'order-pending', path: `/orders/${env.PLACED_ORDER}`, who: 'placed', state: 'pending, guest' },
    { name: 'order-paid', path: `/account/orders/${env.PICKING_ORDER}`, who: 'customer', state: 'paid, packing' },
    { name: 'order-delivered', path: `/account/orders/${env.RETURN_FORM_ORDER}`, who: 'customer', state: 'delivered' },
    { name: 'account', path: '/account', who: 'customer', state: 'signed in' },
    { name: 'wishlist', path: '/account/wishlist', who: 'customer', state: 'with items' },
    { name: 'signin', path: '/signin' },
    { name: 'register', path: '/register' },
    { name: 'not-found', path: '/no-such-page-here', notFound: true },
  ];
  for (const p of pages) await shot(p);

  // The pickup chooser: the page that issues the nonce, then the page as the carrier's return sends the shopper back.
  await as('cart', 'zh-Hant');
  await metrics(1440, 900);
  const ship = `/checkout?ship=${env.PICKUP_SHIP}`;
  const store = '&pickup_chain=seven_eleven&pickup_store_code=131386&pickup_store_name=' + encodeURIComponent('信義威秀門市') +
    '&pickup_store_addr=' + encodeURIComponent('台北市信義區松壽路20號') + '&pickup_n=';
  const nonceNow = async () => {
    const jar = (await send('Network.getCookies', { urls: [ORIGIN] })).cookies.find((c) => c.name === 'goen_pickup');
    return jar ? Buffer.from(jar.value, 'base64url').toString().split('|')[0] : '';
  };
  for (const width of [1440, 375]) {
    const file = `checkout-pickup-chosen-${width}-zh.png`;
    try {
      await as('cart', 'zh-Hant');
      await metrics(width, 900);
      await navigate(ORIGIN + ship);
      const nonce = await nonceNow();
      if (!nonce) throw new Error('the checkout page issued no pickup nonce');
      await navigate(ORIGIN + ship + store + nonce);
      const facts = await capturePage(file, { page: 'checkout-pickup-chosen', state: 'pickup store chosen', width, lang: 'zh-Hant', text: '100%', who: 'cart', requested: ship });
      const shown = await evaluate(`document.body.textContent.includes('信義威秀門市')`);
      if (!shown) failures.push(`${file}: the page does not show the chosen store (${facts.path})`);
      // The same return with a nonce the shop never issued.
      const refused = `refused-${width}-zh.png`;
      await navigate(ORIGIN + ship);
      await navigate(ORIGIN + ship + store + 'a'.repeat(nonce.length));
      await capturePage(`checkout-pickup-${refused}`, { page: 'checkout-pickup-refused', state: 'pickup store refused (wrong nonce)', width, lang: 'zh-Hant', text: '100%', who: 'cart', requested: ship });
    } catch (e) { failures.push(`${file}: ${e.message}`); }
  }
}

async function pay() {
  const state = env.PAY_STATE;
  await shot({ name: `pay-payments-${state}`, path: `/orders/${env.PLACED_ORDER}/pay`, who: 'placed', state: `payments ${state}`, widths: [1440, 375] });
}

async function stockMeasure() {
  const m = await evaluate(`(() => {
    const table = document.querySelector('.goen-admin__stock');
    const frame = table.closest('.goen-admin__tablewrap') || table.parentElement;
    const pinned = table.querySelector('tbody tr td:last-child');
    const pos = getComputedStyle(pinned).position;
    const left = pinned.getBoundingClientRect().left;
    const btns = [...table.querySelectorAll('tbody td:nth-child(4) .ui-btn, tbody td:nth-child(5) .ui-btn')];
    const right = Math.max(...btns.map((b) => b.getBoundingClientRect().right));
    return { frameClient: frame.clientWidth, frameScroll: frame.scrollWidth, table: Math.round(table.getBoundingClientRect().width),
      pinnedPosition: pos, pinnedLeft: Math.round(left), lastButtonRight: Math.round(right), buttons: btns.length,
      clear: Math.round(left - right) };
  })()`);
  console.log('MEASURE ' + innerWidthNote + JSON.stringify(m));
}
let innerWidthNote = '';

async function admin() {
  for (const width of [1440, 1024, 375]) {
    innerWidthNote = `w=${width} `;
    await shot({ name: 'admin-stock', path: '/admin/stock', who: 'admin', admin: true, widths: [width], after: stockMeasure });
  }
}

const key = (type, k, code, vk) => send('Input.dispatchKeyEvent', { type, key: k, code, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk });

async function flows() {
  // Tab through the header of the home page, one shot of the header per stop.
  for (const width of [1440, 375]) {
    await as('guest', 'zh-Hant');
    await metrics(width, 900);
    await navigate(ORIGIN + '/');
    await sleep(500);
    let entered = false;
    for (let step = 1; step <= 40; step++) {
      await key('rawKeyDown', 'Tab', 'Tab', 9);
      await key('keyUp', 'Tab', 'Tab', 9);
      await sleep(250);
      const at = await evaluate(`(() => {
        const a = document.activeElement;
        const header = document.querySelector('header');
        const r = (header || document.body).getBoundingClientRect();
        return {
          inHeader: !!(header && a && header.contains(a)),
          tag: a ? a.tagName.toLowerCase() : '',
          cls: a ? String(a.className).slice(0, 60) : '',
          label: a ? (a.getAttribute('aria-label') || a.textContent || '').trim().slice(0, 40) : '',
          bottom: Math.ceil(r.bottom),
        };
      })()`);
      if (!at.inHeader) {
        if (entered) { console.log(`focus left the header at stop ${step}`); break; }
        console.log(`stop ${step} is before the header: <${at.tag}> ${at.label}`);
      } else entered = true;
      const height = Math.min(Math.max(at.bottom + 40, 120), 900);
      const inside = at.inHeader ? '' : ' (before the header)';
      const { data } = await send('Page.captureScreenshot', { format: 'png', clip: { x: 0, y: 0, width, height, scale: 1 } });
      const file = `flow-header-focus-${width}-${String(step).padStart(2, '0')}.png`;
      writeFileSync(`${outDir}/${file}`, Buffer.from(data, 'base64'));
      const line = { file, page: 'header focus walk', state: `stop ${step}${inside}: <${at.tag}> ${at.label}`, width, lang: 'zh-Hant', text: '100%', who: 'guest', requested: '/', h1: '', path: '/' };
      appendFileSync(`${outDir}/manifest-${mode}.jsonl`, JSON.stringify(line) + '\n');
      console.log(file, at.tag, JSON.stringify(at.label), at.cls);
    }
  }
  // The phone menu open, on a department.
  await as('guest', 'zh-Hant');
  await metrics(375, 812);
  await navigate(ORIGIN + '/');
  const depts = (await hrefs('a[href^="/c/"]')).filter((h) => !h.includes('?'));
  const dept = depts[0];
  await shot({ name: 'flow-phone-menu-open', path: dept, widths: [375], state: `menu open on ${dept}`, after: menuOpen });
}

await open();
if (mode === 'storefront') await storefront();
else if (mode === 'admin') await admin();
else if (mode === 'flows') await flows();
else if (mode === 'pay') await pay();
else throw new Error(`unknown mode ${mode}`);

ws.close();
if (failures.length) {
  console.log(`\n${failures.length} failure(s):\n  ${failures.join('\n  ')}`);
  process.exit(1);
}
console.log('all shots captured');
