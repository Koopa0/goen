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

async function filterShell() {
 const closed = await evaluate("Boolean(document.querySelector('.goen-filters__shell:not([open])'))");
 if (closed) { await evaluate("document.querySelector('.goen-filters__shell > summary').focus()"); await key('Enter'); }
}
async function visibleFocus() {
 return evaluate(`(() => { const e = document.activeElement, r = e.getBoundingClientRect(), s = getComputedStyle(e);
 const hit = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
 return { text: e.textContent.trim(), tag: e.tagName, focusVisible: e.matches(':focus-visible'),
  uncovered: hit === e || e.contains(hit), width: r.width, height: r.height, right: r.right, viewport: innerWidth,
  outlineWidth: parseFloat(s.outlineWidth), outlineStyle: s.outlineStyle }; })()`);
}
try {
 for (const locale of ['zh-Hant', 'en']) for (const width of [320, 375, 1440]) for (const text of [100, 200]) for (const dark of [false, true]) {
  const label = `${locale}-${width}-${text}-${dark ? 'dark' : 'light'}`;
  const path = '/c/' + (process.env.DEPARTMENT_SLUG || 'audio');
  await navigate(path, width, text, locale, dark);
  assert.equal(await evaluate('document.documentElement.lang'), locale);
  const brand = await evaluate("document.querySelector('.goen-filters__group input[name=brand]')?.value");
  assert.ok(brand, label + ': a brand facet is required');
  await navigate(path + '?brand=' + encodeURIComponent(brand), width, text, locale, dark);
  await filterShell();
  await evaluate("window.checkedPopover = document.querySelector('.goen-filters__group:has(input[name=brand])'); checkedPopover.querySelector('summary').focus()");
  await key('Enter');
  assert.equal(await evaluate('checkedPopover.open'), true, label + ': Enter opens the group');
  await key('Tab');
  assert.equal(await evaluate('checkedPopover.open && checkedPopover.contains(document.activeElement)'), true, label + ': focus within the group keeps it open');
  await evaluate("[...checkedPopover.querySelectorAll('input')].at(-1).focus()");
  await key('Tab');
  const exit = await evaluate('({open:checkedPopover.open,inside:checkedPopover.contains(document.activeElement)})');
  await capture(label + '-exit');
  assert.equal(exit.inside, false, label + ': Tab leaves the group');
  assert.equal(exit.open, false, label + ': panel must close on keyboard focus leaving: ' + JSON.stringify(exit));
  for (let step = 0; step < 30; step++) {
   if (await evaluate("document.activeElement.matches('.goen-filters__chip-remove')")) break;
   await key('Tab');
  }
  assert.equal(await evaluate("document.activeElement.matches('.goen-filters__chip-remove')"), true, label + ': applied chip is keyboard reachable');
  const chip = await visibleFocus();
  await capture(label + '-chip');
  assert.ok(chip.uncovered && chip.focusVisible && chip.width >= 24 && chip.height >= 24 && chip.right <= chip.viewport, label + ': focused chip must be visible: ' + JSON.stringify(chip));

  await evaluate("checkedPopover.querySelector('summary').focus()"); await key('Enter'); await key('Tab'); await key('Escape');
  assert.equal(await evaluate("!checkedPopover.open && document.activeElement === checkedPopover.querySelector('summary')"), true, label + ': Escape returns focus to summary');
  await key('Enter'); await key('Tab'); await evaluate('document.activeElement.blur()');
  assert.equal(await evaluate('checkedPopover.open'), false, label + ': null relatedTarget closes the group');
  await evaluate("checkedPopover.querySelector('summary').focus()"); await key('Enter'); await key('Tab', 8);
  assert.equal(await evaluate('checkedPopover.open'), false, label + ': reverse Tab out closes the group');
  await evaluate("checkedPopover.querySelector('summary').focus()"); await key('Enter');
  await evaluate("document.querySelector('h1').click()");
  assert.equal(await evaluate('checkedPopover.open'), false, label + ': outside click still closes the group');

  if (width === 1440) {
   await evaluate("window.checkedLanguage = [...document.querySelectorAll('.goen-langmenu')].find(e => e.getBoundingClientRect().width > 0); checkedLanguage.querySelector('summary').focus()");
   await key('Enter'); await key('Tab');
   assert.equal(await evaluate('checkedLanguage.open && checkedLanguage.contains(document.activeElement)'), true, label + ': language menu stays open within');
   await evaluate("[...checkedLanguage.querySelectorAll('button')].at(-1).focus()"); await key('Tab');
   assert.equal(await evaluate('checkedLanguage.open'), false, label + ': language menu closes when focus leaves');
  }
  console.log('PASS popover keyboard ' + label + ': ' + JSON.stringify(chip));
 }
} finally { connection.close(); }
