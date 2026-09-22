// Focused served-asset checks. The full layout/accessibility gate runs first.
import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
const origin = process.env.GOEN_URL;
const cdp = `http://127.0.0.1:${process.env.CDP_PORT || 9223}`;
if (!origin || !process.env.CART_TOKEN) throw new Error('feedback proof needs its own server and cart fixture');
const tabResponse = await fetch(`${cdp}/json/new?about:blank`, { method: 'PUT' });
if (!tabResponse.ok) throw new Error('Chrome did not create a page');
const tab = await tabResponse.json();
const ws = new WebSocket(tab.webSocketDebuggerUrl);
await new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject; });
let nextID = 0;
const requests = new Map();
const events = new Map();
ws.onmessage = ({ data }) => {
  const message = JSON.parse(data);
  if (message.id && requests.has(message.id)) {
    const pending = requests.get(message.id);
    requests.delete(message.id);
    clearTimeout(pending.timer);
    if (message.error) pending.reject(new Error(JSON.stringify(message.error)));
    else pending.resolve(message.result);
  } else if (events.has(message.method)) {
    const waiting = events.get(message.method);
    events.delete(message.method);
    clearTimeout(waiting.timer);
    waiting.resolve(message.params);
  }
};
function send(method, params = {}) {
  const id = ++nextID;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { requests.delete(id); reject(new Error(`CDP timeout: ${method}`)); }, 25000);
    requests.set(id, { resolve, reject, timer });
    ws.send(JSON.stringify({ id, method, params }));
  });
}
function event(method) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { events.delete(method); reject(new Error(`CDP event timeout: ${method}`)); }, 20000);
    events.set(method, { resolve, reject, timer });
  });
}
async function evaluate(expression) {
  const value = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (value.exceptionDetails) throw new Error(value.exceptionDetails.exception?.description || 'browser evaluation failed');
  return value.result.value;
}
async function navigate(path) {
  const loaded = event('Page.loadEventFired');
  await send('Page.navigate', { url: origin + path });
  await loaded;
  const ready = await evaluate(`document.readyState === 'complete' && !!document.querySelector('main')`);
  if (!ready) throw new Error('the served page did not load');
}
let failures = 0;
function check(name, ok, detail) {
  console.log(JSON.stringify({ name, status: ok ? 'pass' : 'fail', detail }));
  if (!ok) failures++;
}
try {
  await send('Page.enable');
  await send('Network.enable');
  await send('Network.setCacheDisabled', { cacheDisabled: true });
  await send('Network.setCookie', { name: 'goen_cart', value: process.env.CART_TOKEN, url: origin });
  await navigate('/checkout');
  const assets = await evaluate(`({ js: [...document.scripts].map(s => s.src).find(url => new URL(url).pathname.endsWith('/js/goen.js')), css: [...document.querySelectorAll('link[rel="stylesheet"]')].map(link => link.href).find(url => new URL(url).pathname.endsWith('/css/app/app.css')) })`);
  for (const [kind, path] of [['js', 'assets/js/goen.js'], ['css', 'assets/css/app/app.css']]) {
    if (!assets[kind]) throw new Error('the served page omitted its ' + kind + ' asset');
    const response = await fetch(assets[kind]);
    const content = await response.text();
    if (!response.ok || content !== readFileSync(path, 'utf8')) throw new Error('served ' + kind + ' does not match this phase source');
    check('served_' + kind, true, { url: assets[kind], sha256: createHash('sha256').update(content).digest('hex') });
  }
  for (const width of [375, 1440]) {
    await send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: width === 375 });
    await navigate('/checkout');
    const pending = await evaluate(`(async () => {
      const choice = document.querySelector('input[name="shipping"]:not(:checked)');
      const form = choice?.form;
      const source = choice?.closest('[hx-post]');
      if (!form || !source) throw new Error('checkout has no real shipping update source');
      const busyBefore = form.getAttribute('aria-busy');
      const buttons = [...form.querySelectorAll('button[type="submit"]')];
      const disabledBefore = buttons.map(button => button.getAttribute('aria-disabled'));
      const nativeBefore = buttons.map(button => button.disabled);
      const originalFetch = window.fetch;
      let release, started, finish;
      const held = new Promise(resolve => { release = resolve; });
      const issued = new Promise(resolve => { started = resolve; });
      const done = new Promise(resolve => { finish = resolve; });
      window.fetch = async (...args) => { started(); await held; return originalFetch(...args); };
      source.addEventListener('htmx:finally:request', () => finish(), { once: true });
      try {
        choice.click();
        await Promise.race([issued, new Promise((_, reject) => setTimeout(() => reject(new Error('shipping request never started')), 5000))]);
        const busy = form.getAttribute('aria-busy') === 'true' && buttons.some(button => button.getAttribute('aria-disabled') === 'true');
        const guarded = !form.dispatchEvent(new SubmitEvent('submit', { bubbles: true, cancelable: true }));
        const usable = buttons.every((button, i) => button.disabled === nativeBefore[i]);
        release();
        await Promise.race([done, new Promise((_, reject) => setTimeout(() => reject(new Error('shipping request never finished')), 15000))]);
        const restored = !form.hasAttribute('data-request-pending') && form.getAttribute('aria-busy') === busyBefore && buttons.every((button, i) => button.getAttribute('aria-disabled') === disabledBefore[i]);
        return { busy, guarded, usable, restored, currentForm: !!document.getElementById('checkout-form'), overflow: document.body.scrollWidth > innerWidth };
      } finally { release(); window.fetch = originalFetch; }
    })()`);
    check(`pending_${width}`, pending.busy && pending.guarded && pending.usable, pending);
    check(`cleanup_${width}`, pending.restored && pending.currentForm && !pending.overflow, pending);

    for (const motion of ['no-preference', 'reduce']) {
      await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: motion }] });
      await navigate('/');
      const menu = await evaluate(`(async () => {
        const menu = document.querySelector('[data-menu]');
        if (!menu || !CSS.supports('selector(details::details-content)') || !document.startViewTransition) throw new Error('Chrome cannot measure the required native motion surfaces');
        menu.querySelector('summary').click();
        const duration = getComputedStyle(menu, '::details-content').transitionDuration;
        const transition = document.startViewTransition(() => { document.body.dataset.feedbackProof = 'changed'; });
        await transition.ready;
        const animation = getComputedStyle(document.documentElement, '::view-transition-new(root)').animationName;
        transition.skipTransition();
        await transition.finished;
        return { open: menu.open, duration, animation };
      })()`);
      const reduced = motion === 'reduce';
      check(`menu_${reduced ? 'reduce' : 'normal'}_${width}`, menu.open && (reduced ? menu.duration === '0s' : parseFloat(menu.duration) > 0), menu);
      check(`transition_${reduced ? 'reduce' : 'normal'}_${width}`, reduced ? menu.animation === 'none' : menu.animation !== 'none', menu);
      await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
      await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
      check(`focus_${reduced ? 'reduce' : 'normal'}_${width}`, await evaluate(`(() => { const menu = document.querySelector('[data-menu]'); return !menu.open && document.activeElement === menu.querySelector('summary'); })()`));
    }

    await send('Emulation.setScriptExecutionDisabled', { value: true });
    await navigate('/checkout');
    const note = `feedback-${width}`;
    await evaluate(`(() => {
      const field = document.querySelector('[name="note"]');
      const button = document.querySelector('button[name="update"][value="shipping"]');
      if (!field || !button) throw new Error('no plain checkout update form');
      field.value = ${JSON.stringify(note)};
    })()`);
    const loaded = event('Page.loadEventFired');
    await evaluate(`document.querySelector('button[name="update"][value="shipping"]').click()`);
    await loaded;
    check(`noscript_${width}`, await evaluate(`document.querySelector('[name="note"]')?.value === ${JSON.stringify(note)} && !!document.getElementById('checkout-form')`));
    await send('Emulation.setScriptExecutionDisabled', { value: false });
  }
  check('probe_completed', true);
} finally {
  ws.close();
}
process.exitCode = failures ? 1 : 0;
