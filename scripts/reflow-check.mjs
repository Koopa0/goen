// Real-page reflow assertions using the same text probe as check-layout.
// Entries use screenshots.mjs syntax; --staff adds the fixture staff session.
import { mkdirSync, writeFileSync } from 'node:fs';
import { parseEntry, screenshotVisitor } from './screenshot-entry.mjs';
import { screenshotRouteMatches } from './screenshot-route.mjs';
import { reflowProbe } from './reflow-probe.mjs';
import { screenshotReflow } from './screenshot-reflow.mjs';

const CDP_PORT = Number(process.env.CDP_PORT || 9222);
const ORIGIN = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const staff = process.argv.includes('--staff');
const entries = process.argv.slice(2).filter((arg) => arg !== '--staff');
if (!entries.length) throw new Error('usage: node reflow-check.mjs [--staff] path@width[@en][@text200] ...');
const evidence = process.env.REFLOW_EVIDENCE_DIR;
if (evidence) mkdirSync(evidence, { recursive: true });

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

const manifest = [];
let failed = false;
await connect();
try {
  await send('Page.enable');
  await send('Network.enable');
  await send('Emulation.setScrollbarsHidden', { hidden: true });
  for (const [index, text] of entries.entries()) {
    const entry = parseEntry(text, process.env);
    await send('Network.clearBrowserCookies');
    await send('Network.setCookie', { name: 'goen_locale', value: entry.lang, url: ORIGIN });
    const visitor = staff ? { cookie: 'goen_session', token: 'ADMIN_TOKEN', prefix: '/' } : screenshotVisitor(entry);
    if (visitor) {
      if (!process.env[visitor.token]) throw new Error(`${visitor.token} is required`);
      await send('Network.setCookie', { name: visitor.cookie, value: process.env[visitor.token], url: ORIGIN });
    }
    // Keep a fixed-height layout viewport, including while taking evidence.
    await send('Emulation.setDeviceMetricsOverride', { width: entry.width, height: 800, deviceScaleFactor: 1, mobile: entry.width < 600 });
    await navigate(ORIGIN + entry.path);
    if (entry.text200) await evaluate("document.documentElement.style.fontSize = '200%'");
    const facts = await evaluate(`({path: location.pathname + location.search, status: performance.getEntriesByType('navigation')[0]?.responseStatus, lang: document.documentElement.lang, rootFontSize: getComputedStyle(document.documentElement).fontSize})`);
    if (facts.status !== 200 || facts.lang !== entry.lang || !(facts.path === entry.path || (visitor && screenshotRouteMatches(entry.path, facts.path, visitor.prefix)))) throw new Error(`${text}: unexpected document ${JSON.stringify(facts)}`);
    if (staff && !await evaluate(`Boolean(document.querySelector('.goen-header a[href="/admin"]'))`)) throw new Error(`${text}: staff session was not accepted`);
    const got = await evaluate(reflowProbe(entry.width), true);
    const movement = await evaluate(`(${screenshotReflow.toString()})()`, true);
    const record = { entry: text, staff, ...facts, ...got, movement };
    if (got.scrollWidth > entry.width || movement.scrollX !== 0 || got.text.length || !movement.settled || !movement.restored) failed = true;
    console.log(JSON.stringify(record));
    if (evidence) {
      const { data } = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true, clip: { x: 0, y: 0, width: entry.width, height: await evaluate('document.documentElement.scrollHeight'), scale: 1 } });
      record.file = `${index + 1}.png`;
      writeFileSync(`${evidence}/${record.file}`, Buffer.from(data, 'base64'));
    }
    manifest.push(record);
  }
} finally {
  ws.close();
  if (evidence) writeFileSync(`${evidence}/manifest.json`, JSON.stringify(manifest, null, 2) + '\n');
}
process.exitCode = failed ? 1 : 0;
