// Screenshots of a running goen, taken with Chrome over the DevTools protocol.
//
//   node scripts/screenshots.mjs <outdir>
//
// SHOT_PAGES lists the entries, one per line or separated by commas:
//
//   path@width[@lang][@text200][@forced][@member][@noscript]
//
//   /p/{PRODUCT_SLUG}@375@en@text200
//
// lang is zh or en (default zh). text200 doubles the root font size, the way a
// reader who zooms text to 200% sees the page (WCAG 1.4.4). forced emulates
// forced-colors. member makes the visitor the signed-in customer (CUST_TOKEN)
// whatever the path, and the capture must end on the requested path. noscript
// loads the page with JavaScript off, as a visitor without scripting gets it;
// the runner's own measurements still run. {NAME} is replaced by the
// environment variable NAME, so an entry can point at the slugs and order
// numbers scripts/check-layout.sql writes to its env file (names ending _SLUG
// or _ORDER, PICKUP_SHIP, CUSTOMER_ID, LAYOUT_SERIAL; never a token).
// A comma inside a path needs the one-entry-per-line form.
//
// Who is looking follows from the path: /admin is staff (ADMIN_TOKEN), /account
// the signed-in customer (CUST_TOKEN), /cart and /checkout the cart's owner
// (CART_TOKEN), /orders the placer of an unpaid order (PLACED_TOKEN). Any other
// path is a guest. A page asked for as a user whose token is not set fails.
// /account/orders/{NUMBER} keeps the customer session when it redirects to
// /orders/{the same NUMBER}.
//
// SHOT_VIEW is full (the whole page, at most CAP pixels tall) or viewport (the
// first screen). Device emulation sets the width, so media and container
// queries and touch input see the device, which --window-size does not do.
// CDP_PORT and GOEN_URL name Chrome and the shop. The PNGs and manifest.json
// are written to <outdir>. An entry fails, and the exit status is 1, when it
// cannot be shot, answers 400 or above, or needs a session and ends outside its
// visitor path or the same order's canonical path (a lapsed session lands on
// /signin).
// The manifest's reflow observations include body width, actual window scroll,
// viewport and root font size. They do not decide whether a capture passes.

import { mkdirSync, writeFileSync } from 'node:fs';
import { screenshotRouteMatches } from './screenshot-route.mjs';
import { screenshotReflow } from './screenshot-reflow.mjs';
import { parseEntry, screenshotVisitor } from './screenshot-entry.mjs';

const CDP_PORT = Number(process.env.CDP_PORT || 9222);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const VIEW = process.env.SHOT_VIEW || 'full';
const CAP = 12000;
const PHONE_HEIGHT = 812;
const DESKTOP_HEIGHT = 900;
const outDir = process.argv[2];
if (!outDir || !['full', 'viewport'].includes(VIEW)) {
  console.error('usage: SHOT_PAGES=… [SHOT_VIEW=full|viewport] node scripts/screenshots.mjs <outdir>');
  process.exit(2);
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function slug(index, entry) {
  const name = entry.path.replace(/^\//, '').replace(/[^A-Za-z0-9]+/g, '-').replace(/-$/, '') || 'home';
  const parts = [String(index + 1).padStart(2, '0'), name.slice(0, 60), entry.width, entry.lang === 'en' ? 'en' : 'zh'];
  if (entry.text200) parts.push('text200');
  if (entry.forced) parts.push('forced');
  if (entry.member) parts.push('member');
  if (entry.noscript) parts.push('noscript');
  return parts.join('-') + '.png';
}

let nextId = 1;
const pending = new Map();
let ws;

function send(method, params = {}) {
  const id = nextId++;
  ws.send(JSON.stringify({ id, method, params }));
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      pending.delete(id);
      reject(new Error(`${method} timed out`));
    }, 60000);
    pending.set(id, { resolve, reject, timer });
  });
}

async function evaluate(expression, awaitPromise = false) {
  const { result, exceptionDetails } = await send('Runtime.evaluate', { expression, awaitPromise, returnByValue: true });
  if (exceptionDetails) throw new Error(`evaluate failed: ${JSON.stringify(exceptionDetails).slice(0, 300)}`);
  return result.value;
}

async function connect() {
  for (let i = 0; i < 150; i++) {
    try {
      const targets = await (await fetch(`http://127.0.0.1:${CDP_PORT}/json/list`)).json();
      const page = targets.find((t) => t.type === 'page');
      if (page) {
        ws = new WebSocket(page.webSocketDebuggerUrl);
        await new Promise((resolve, reject) => {
          ws.onopen = resolve;
          ws.onerror = reject;
        });
        ws.onmessage = (event) => {
          const msg = JSON.parse(event.data);
          const call = pending.get(msg.id);
          if (!call) return;
          pending.delete(msg.id);
          clearTimeout(call.timer);
          if (msg.error) call.reject(new Error(JSON.stringify(msg.error)));
          else call.resolve(msg.result);
        };
        return;
      }
    } catch {
      // Chrome is still starting.
    }
    await sleep(200);
  }
  throw new Error(`no Chrome page on port ${CDP_PORT}`);
}

// Leaving through about:blank makes every entry a fresh load, even when two
// entries share a path.
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

async function shoot(entry, file) {
  const device = (height) =>
    send('Emulation.setDeviceMetricsOverride', {
      width: entry.width,
      height,
      deviceScaleFactor: 1,
      mobile: entry.width < 600,
    });
  const height = entry.width < 600 ? PHONE_HEIGHT : DESKTOP_HEIGHT;

  await send('Network.clearBrowserCookies');
  await send('Network.setCookie', { name: 'goen_locale', value: entry.lang, domain: '127.0.0.1', path: '/' });
  const visitor = screenshotVisitor(entry);
  if (visitor) {
    if (!process.env[visitor.token]) throw new Error(`${entry.path} needs ${visitor.token}, which this data set does not create`);
    await send('Network.setCookie', { name: visitor.cookie, value: process.env[visitor.token], domain: '127.0.0.1', path: '/' });
  }
  await send('Emulation.setEmulatedMedia', {
    features: [
      { name: 'prefers-color-scheme', value: 'light' },
      ...(entry.forced ? [{ name: 'forced-colors', value: 'active' }] : []),
    ],
  });

  await send('Emulation.setScriptExecutionDisabled', { value: entry.noscript });
  await device(height);
  await navigate(ORIGIN + entry.path);
  if (entry.text200) {
    await evaluate(`document.documentElement.style.fontSize = '200%'`);
    await sleep(300);
  }
  await evaluate('document.fonts.ready.then(() => 1)', true);

  let shotHeight = height;
  if (VIEW === 'full') {
    shotHeight = Math.min(CAP, Math.max(await evaluate('Math.ceil(document.documentElement.scrollHeight)'), height));
    await device(shotHeight);
  }
  for (let i = 0; i < 40 && (await evaluate('[...document.images].some((i) => !i.complete)')); i++) await sleep(250);
  await sleep(300);

  const facts = await evaluate(`({
    finalPath: location.pathname + location.search,
    status: (performance.getEntriesByType('navigation')[0] || {}).responseStatus || 0,
    h1: ((document.querySelector('h1') || {}).textContent || '').trim(),
    htmlLang: document.documentElement.lang,
    scrollWidth: document.documentElement.scrollWidth,
  })`);
  const { data } = await send('Page.captureScreenshot', { format: 'png' });
  writeFileSync(`${outDir}/${file}`, Buffer.from(data, 'base64'));
  let reflow;
  try {
    reflow = await evaluate(`(${screenshotReflow.toString()})()`, true);
  } catch (e) {
    reflow = { error: e.message };
  }
  const result = { ...facts, reflow, height: shotHeight, capped: shotHeight === CAP };
  if (facts.status >= 400) result.error = `answered ${facts.status}`;
  else if (visitor && !screenshotRouteMatches(entry.path, facts.finalPath, visitor.prefix)) result.error = `needs ${visitor.token} but ended on ${facts.finalPath}`;
  return result;
}

const list = process.env.SHOT_PAGES || '';
const entries = list
  .split(list.includes('\n') ? '\n' : ',')
  .map((s) => s.trim())
  .filter(Boolean);
if (entries.length === 0) {
  console.error('SHOT_PAGES is empty');
  process.exit(2);
}

mkdirSync(outDir, { recursive: true });
await connect();
await send('Page.enable');
await send('Network.enable');
await send('Emulation.setScrollbarsHidden', { hidden: true });

const manifest = [];
let failed = 0;
for (const [index, text] of entries.entries()) {
  const record = { entry: text };
  try {
    const entry = parseEntry(text, process.env);
    record.file = slug(index, entry);
    Object.assign(record, { requested: entry.path, width: entry.width, lang: entry.lang, text200: entry.text200, forced: entry.forced, member: entry.member, noscript: entry.noscript, view: VIEW });
    Object.assign(record, await shoot(entry, record.file));
    if (record.error) {
      failed++;
      console.error(`${record.file}: ${record.error}`);
    }
    console.log(`${record.file} ${record.status} ${record.finalPath} h1=${JSON.stringify(record.h1)} scrollWidth=${record.scrollWidth} height=${record.height}`);
  } catch (e) {
    record.error = e.message;
    failed++;
    console.error(`${text}: ${e.message}`);
  }
  manifest.push(record);
}
writeFileSync(`${outDir}/manifest.json`, JSON.stringify(manifest, null, 2) + '\n');
ws.close();
console.log(`${manifest.length - failed} of ${manifest.length} shot`);
process.exit(failed ? 1 : 0);
