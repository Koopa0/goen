import assert from 'node:assert/strict';
import { mkdirSync, writeFileSync } from 'node:fs';

class Connection {
  pending = new Map();
  listeners = new Map();
  sequence = 0;

  constructor(socket) {
    this.socket = socket;
    socket.addEventListener('message', ({ data }) => {
      const message = JSON.parse(data);
      if (message.id) {
        const command = this.pending.get(message.id);
        if (!command) return;
        clearTimeout(command.timer);
        this.pending.delete(message.id);
        if (message.error) command.reject(new Error(JSON.stringify(message.error)));
        else command.resolve(message.result);
      } else {
        for (const listener of this.listeners.get(message.method) || []) listener(message);
      }
    });
    socket.addEventListener('close', () => this.cancel());
  }

  static async open(url) {
    const socket = new WebSocket(url);
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => { socket.close(); reject(new Error('CDP socket did not open')); }, 15000);
      socket.addEventListener('open', () => { clearTimeout(timer); resolve(); }, { once: true });
      socket.addEventListener('error', () => { clearTimeout(timer); socket.close(); reject(new Error('CDP socket failed')); }, { once: true });
    });
    return new Connection(socket);
  }

  send(method, params = {}, sessionId) {
    const id = ++this.sequence;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`${method} exceeded 15 seconds`));
      }, 15000);
      this.pending.set(id, { resolve, reject, timer });
      try {
        this.socket.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
      } catch (error) {
        clearTimeout(timer);
        this.pending.delete(id);
        reject(error);
      }
    });
  }

  on(method, listener) {
    if (!this.listeners.has(method)) this.listeners.set(method, new Set());
    this.listeners.get(method).add(listener);
    return () => this.listeners.get(method).delete(listener);
  }

  cancel() {
    for (const command of this.pending.values()) {
      clearTimeout(command.timer);
      command.reject(new Error('CDP connection closed'));
    }
    this.pending.clear();
  }

  close() { this.cancel(); this.socket.close(); }
}

const origin = (process.env.GOEN_URL || 'http://127.0.0.1:9700').replace(/\/$/, '');
const port = Number(process.env.CDP_PORT || 9222);
const pages = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
const page = pages.find((target) => target.type === 'page');
assert.ok(page, 'a live Chrome page is required');
const connection = await Connection.open(page.webSocketDebuggerUrl);
await connection.send('Page.enable');
await connection.send('Network.enable');
const evaluate = async (expression) => {
 const { result, exceptionDetails } = await connection.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
 if (exceptionDetails) throw new Error(JSON.stringify(exceptionDetails));
 return result.value;
};
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function navigate(path, width, text, locale, dark) {
 await connection.send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false });
 await connection.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: dark ? 'dark' : 'light' }] });
 await connection.send('Network.clearBrowserCookies');
 await connection.send('Network.setCookie', { name: 'goen_locale', value: locale, url: origin, path: '/' });
 const loaded = new Promise((resolve, reject) => {
  const timer = setTimeout(() => { off(); reject(new Error('page load exceeded 15 seconds')); }, 15000);
  const off = connection.on('Page.loadEventFired', () => { clearTimeout(timer); off(); resolve(); });
 });
 await connection.send('Page.navigate', { url: origin + path });
 await loaded;
 let ready = false;
 for (let attempt = 0; attempt < 150; attempt++) {
  ready = await evaluate(`location.pathname === ${JSON.stringify(path.split('?')[0])} && document.readyState === 'complete'`);
  if (ready) break;
  await delay(100);
 }
 assert.ok(ready, 'requested route must load: ' + path);
 await evaluate(`document.fonts.ready.then(() => { document.documentElement.style.fontSize = '${text * 16 / 100}px'; })`);
 await delay(100);
 assert.equal(await evaluate('parseFloat(getComputedStyle(document.documentElement).fontSize)'), text * 16 / 100, 'actual root text size');
 assert.equal(await evaluate("matchMedia('(prefers-color-scheme: dark)').matches"), dark, 'actual colour preference');
 assert.equal(await evaluate('innerWidth'), width, 'actual viewport width');
}
async function key(name, modifiers = 0) {
 const params = { key: name, code: name, text: name === 'Enter' ? '\r' : '', windowsVirtualKeyCode: name === 'Tab' ? 9 : name === 'Enter' ? 13 : 27, modifiers };
 await connection.send('Input.dispatchKeyEvent', { type: 'keyDown', ...params });
 await connection.send('Input.dispatchKeyEvent', { type: 'keyUp', ...params });
}
async function capture(name) {
 if (!process.env.EVIDENCE_DIR) return;
 mkdirSync(process.env.EVIDENCE_DIR, { recursive: true });
 const { data } = await connection.send('Page.captureScreenshot', { format: 'png' });
 writeFileSync(process.env.EVIDENCE_DIR + '/' + name + '.png', Buffer.from(data, 'base64'));
}

// Real Tab events must reveal every link that the compact phone trail clips.
try {
 for (const locale of ['zh-Hant', 'en']) for (const width of [320, 375, 1440]) for (const text of [100, 200]) for (const dark of [false, true]) {
  const label = `${locale}-${width}-${text}-${dark ? 'dark' : 'light'}`;
  await navigate('/p/' + (process.env.PRODUCT_SLUG || 'pixelight-9-pro'), width, text, locale, dark);
  assert.equal(await evaluate('document.documentElement.lang'), locale, label + ': actual page language');
  const count = await evaluate("document.querySelectorAll('.ui-crumbs__link').length");
  assert.ok(count >= 2, label + ': a multi-link product trail is required');
  let seen = 0;
  for (let step = 0; step < 100 && seen < count; step++) {
   await key('Tab');
   const state = await evaluate(`(() => {
    const e = document.activeElement;
    if (!e.matches('.ui-crumbs__link')) return null;
    const r = e.getBoundingClientRect(), s = getComputedStyle(e);
    const hit = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
    return { text: e.textContent.trim(), width: r.width, height: r.height, left: r.left, right: r.right,
     viewport: innerWidth, clip: s.clipPath, focusVisible: e.matches(':focus-visible'), outlineWidth: parseFloat(s.outlineWidth),
     outlineStyle: s.outlineStyle, uncovered: hit === e || e.contains(hit) };
   })()`);
   if (!state) continue;
   await capture(label + '-crumb-' + seen);
   assert.ok(state.width > 1 && state.height > 1 && state.clip === 'none', label + ': focused breadcrumb is clipped: ' + JSON.stringify(state));
   assert.ok(state.focusVisible && state.outlineWidth > 0 && state.outlineStyle !== 'none', label + ': focus indicator is missing: ' + JSON.stringify(state));
   assert.ok(state.uncovered && state.left >= 0 && state.right <= state.viewport, label + ': focused breadcrumb is obscured or outside viewport: ' + JSON.stringify(state));
   console.log('breadcrumb state ' + label + ': ' + JSON.stringify(state));
   seen++;
  }
  assert.equal(seen, count, label + ': every breadcrumb must remain keyboard reachable');
  console.log('PASS breadcrumb focus ' + label + ': ' + seen + ' visible links');
 }
} finally { connection.close(); }
