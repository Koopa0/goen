import assert from 'node:assert/strict';

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
        reject(new Error(`${method} ${JSON.stringify(params)} exceeded 15 seconds`));
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
async function press(point) {
 await connection.send('Input.dispatchMouseEvent', { type: 'mouseMoved', ...point });
 await connection.send('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, ...point });
 await connection.send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...point });
}
async function visibleFocus() {
 return evaluate(`(() => { const e = document.activeElement, r = e.getBoundingClientRect(), s = getComputedStyle(e);
 const target = getComputedStyle(e, '::after');
 const hit = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
 return { text: e.textContent.trim(), tag: e.tagName, focusVisible: e.matches(':focus-visible'),
  uncovered: hit === e || e.contains(hit), width: r.width, height: r.height, right: r.right, viewport: innerWidth,
  outlineWidth: parseFloat(s.outlineWidth), outlineStyle: s.outlineStyle,
  targetWidth: parseFloat(target.width), targetHeight: parseFloat(target.height) }; })()`);
}
// The shelf's filters are a column beside the results where the page has the
// room, and a modal drawer where it does not; the language menu is the one
// popover the page keeps. A pointer on an option's words ticks it, the drawer
// opens from its button by the keyboard, keeps focus inside, and gives it back
// on Escape, and the chip a filter leaves is reachable and visibly focused.
try {
 for (const locale of ['zh-Hant', 'en']) for (const width of [1440, 320, 375]) for (const text of [100, 200]) for (const dark of [false, true]) {
  const label = `${locale}-${width}-${text}-${dark ? 'dark' : 'light'}`;
  const path = '/c/' + (process.env.DEPARTMENT_SLUG || 'audio');
  await navigate(path, width, text, locale, dark);
  assert.equal(await evaluate('document.documentElement.lang'), locale);
  const brand = await evaluate("document.querySelector('.goen-filters input[name=brand]')?.value");
  assert.ok(brand, label + ': a brand facet is required');
  await navigate(path + '?brand=' + encodeURIComponent(brand), width, text, locale, dark);
  await evaluate(`window.checkedOpener = document.querySelector('[data-filters-open]');
   window.checkedBox = document.querySelector('.goen-filters input[name=brand][value=${JSON.stringify(brand)}]'); true`);
  const drawer = await evaluate('checkedOpener.getClientRects().length > 0');
  if (drawer) {
   await evaluate('checkedOpener.focus()');
   await key('Enter');
   // The drawer slides in; the probe waits that out.
   await delay(600);
   const opened = await evaluate(`(() => { const d = document.getElementById('filters-drawer');
    return { open: d.open, modal: d.matches(':modal'), inside: d.contains(document.activeElement), box: d.contains(checkedBox), expanded: checkedOpener.getAttribute('aria-expanded') }; })()`);
   assert.deepEqual(opened, { open: true, modal: true, inside: true, box: true, expanded: 'true' }, label + ': Enter opens the drawer, modal, with focus and the filters in it');
  } else {
   assert.equal(await evaluate("getComputedStyle(document.querySelector('.goen-facets')).display !== 'none' && !document.getElementById('filters-drawer').open"), true, label + ': the filters stand beside the results');
  }
  // A pointer can only press what is on the screen: the option is scrolled to,
  // by the least that shows it, before its words are pressed.
  await evaluate("checkedBox.closest('label').querySelector('.goen-filters__label').scrollIntoView({ block: 'nearest' })");
  const checked = await evaluate('checkedBox.checked');
  const words = await evaluate(`(() => { const e = checkedBox.closest('label').querySelector('.goen-filters__label'); const range = document.createRange(); range.selectNodeContents(e); const r = range.getBoundingClientRect(); return {x:r.x + r.width / 2, y:r.y + r.height / 2}; })()`);
  await press(words);
  assert.equal(await evaluate('checkedBox.checked'), !checked, label + ': pressing an option\'s words ticks it');
  await press(words);
  assert.equal(await evaluate('checkedBox.checked'), checked, label + ': a second press restores the filter');
  if (drawer) {
   await key('Escape');
   await delay(600);
   const closed = await evaluate(`(() => ({ open: document.getElementById('filters-drawer').open, back: document.activeElement === checkedOpener,
    home: !!document.querySelector('.goen-facets [data-facets-panel]'), expanded: checkedOpener.getAttribute('aria-expanded') }))()`);
   assert.deepEqual(closed, { open: false, back: true, home: true, expanded: 'false' }, label + ': Escape closes the drawer and gives focus back to its button');
  } else {
   await evaluate('checkedBox.focus()');
  }
  for (let step = 0; step < 40; step++) {
   if (await evaluate("document.activeElement.matches('.goen-filters__chip-remove')")) break;
   await key('Tab');
  }
  assert.equal(await evaluate("document.activeElement.matches('.goen-filters__chip-remove')"), true, label + ': applied chip is keyboard reachable');
  const chip = await visibleFocus();
  assert.ok(chip.uncovered && chip.focusVisible && chip.outlineWidth > 0 && chip.outlineStyle !== 'none' && chip.targetWidth >= 24 && chip.targetHeight >= 24 && chip.right <= chip.viewport, label + ': focused chip must be visible: ' + JSON.stringify(chip));

  // The language menu lives in the site's drawer; from 1024 the bar's language
  // is one button and there is no menu to hold open.
  if (width === 375) {
   // The drawer slides in; the probe waits that out.
   await evaluate("document.getElementById('site-menu').showModal(); new Promise(done => setTimeout(done, 1000))");
   await evaluate("window.checkedLanguage = [...document.querySelectorAll('.goen-langmenu')].find(e => e.getBoundingClientRect().width > 0); checkedLanguage.querySelector('summary').focus()");
   await key('Enter'); await key('Tab');
   assert.equal(await evaluate('checkedLanguage.open && checkedLanguage.contains(document.activeElement)'), true, label + ': language menu stays open within');
   await evaluate("[...checkedLanguage.querySelectorAll('button')].at(-1).focus()"); await key('Tab');
   assert.equal(await evaluate('checkedLanguage.open'), false, label + ': language menu closes when focus leaves');
   await evaluate("document.getElementById('site-menu').close()");
  }
  console.log('PASS shelf keyboard and pointer ' + label + ': ' + JSON.stringify(chip));
 }
} finally { connection.close(); }
