// Focused served-asset checks. The full layout/accessibility gate runs first.
import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
const origin = process.env.GOEN_URL;
const cdp = `http://127.0.0.1:${process.env.CDP_PORT || 9223}`;
if (!origin || !process.env.CART_TOKEN) throw new Error('constraint proof needs its own server and cart fixture');
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
    for (const scripting of [true, false]) {
      await send('Emulation.setScriptExecutionDisabled', { value: !scripting });
      await navigate('/checkout');
      const mode = `${scripting ? 'script' : 'native'}_${width}`;
      const initial = await evaluate(`(() => {
        const field = document.getElementById('postal_code');
        const message = document.getElementById('postal_code-error');
        if (!field || !message) throw new Error('served checkout omitted postal controls');
        field.focus();
        field.select();
        return { hidden: getComputedStyle(message).display === 'none', aria: field.getAttribute('aria-invalid'), description: field.getAttribute('aria-describedby'), numeric: field.inputMode === 'numeric' };
      })()`);
      check(`initial_${mode}`, initial.hidden && initial.aria !== 'true' && initial.description === 'postal_code-error' && initial.numeric, initial);
      await send('Input.insertText', { text: 'abc' });
      await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 });
      await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 });
      const invalid = await evaluate(`(() => {
        const field = document.getElementById('postal_code');
        return { value: field.value, invalid: !field.validity.valid, aria: field.getAttribute('aria-invalid'), visible: getComputedStyle(document.getElementById('postal_code-error')).display !== 'none' };
      })()`);
      check(`pattern_${mode}`, invalid.value === 'abc' && invalid.invalid, invalid);
      check(`visible_${mode}`, invalid.visible, invalid);
      if (scripting) check(`aria_${width}`, invalid.aria === 'true', invalid);
      await evaluate(`(() => { const field = document.getElementById('postal_code'); field.focus(); field.select(); })()`);
      await send('Input.insertText', { text: '001' });
      const corrected = await evaluate(`(() => {
        const field = document.getElementById('postal_code');
        return { value: field.value, valid: field.validity.valid, aria: field.getAttribute('aria-invalid'), hidden: getComputedStyle(document.getElementById('postal_code-error')).display === 'none' };
      })()`);
      check(`recovery_${mode}`, corrected.value === '001' && corrected.valid && corrected.aria !== 'true' && corrected.hidden, corrected);
      await evaluate(`document.getElementById('postal_code').select()`);
      await send('Input.insertText', { text: '0012345' });
      check(`postal_bound_${mode}`, await evaluate(`document.getElementById('postal_code').value === '001234'`));
      for (const id of ['city', 'district']) {
        await evaluate(`(() => { const field = document.getElementById(${JSON.stringify(id)}); if (!field) throw new Error('region field is absent'); field.focus(); field.select(); })()`);
        await send('Input.insertText', { text: String.fromCodePoint(0x20000).repeat(20) });
        const accepted = await evaluate(`(() => { const field = document.getElementById(${JSON.stringify(id)}); return field.validity.valid && [...field.value].length === 20; })()`);
        await send('Input.insertText', { text: String.fromCodePoint(0x20000) });
        const refused = await evaluate(`(() => { const field = document.getElementById(${JSON.stringify(id)}); return !field.validity.valid && [...field.value].length === 21; })()`);
        check(`unicode_${id}_${mode}`, accepted && refused, { accepted, refused });
      }
    }
  }
  check('probe_completed', true);
} finally {
  ws.close();
}
process.exitCode = failures ? 1 : 0;
