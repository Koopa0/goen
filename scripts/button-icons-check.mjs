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
async function key(name, modifiers = 0) {
 const params = { key: name, code: name, text: name === 'Enter' ? '\r' : '', windowsVirtualKeyCode: name === 'Tab' ? 9 : name === 'Enter' ? 13 : 27, modifiers };
 await connection.send('Input.dispatchKeyEvent', { type: 'keyDown', ...params });
 await connection.send('Input.dispatchKeyEvent', { type: 'keyUp', ...params });
}
const { readFileSync } = await import('node:fs');
const styles = ['primary', 'secondary', 'outline', 'ghost', 'danger'];
const samples = [...styles.map(style => ({name: style, classes: 'goen-btn goen-btn--' + style})),
 {name: 'small', classes: 'goen-btn goen-btn--primary goen-btn--sm'},
 {name: 'large', classes: 'goen-btn goen-btn--primary goen-btn--lg'},
 {name: 'icon', classes: 'goen-btn goen-btn--primary goen-btn--icon'},
 {name: 'link', classes: 'goen-btn goen-btn--outline'}];
const svg = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M3 3h2l3 12h10l3-9H6M9 21h.01M18 21h.01"></path></svg>';
const failures = [];
try {
 for (const surface of ['app', 'admin']) for (const width of [320, 375, 1440]) for (const dark of [false, true]) {
  await connection.send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false });
  await connection.send('Emulation.setEmulatedMedia', { features: [{name: 'prefers-color-scheme', value: dark ? 'dark' : 'light'}] });
  await connection.send('Page.navigate', {url: 'about:blank'}); await delay(50);
  const {frameTree} = await connection.send('Page.getFrameTree');
  const css = readFileSync('assets/css/app/base.css', 'utf8') + readFileSync('assets/css/app/' + surface + '.css', 'utf8');
  const buttons = samples.map(({name, classes}) => {
   const tag = name === 'link' ? 'a' : 'button';
   const attr = name === 'link' ? 'href="#end"' : 'type="button"';
   return `<section><p>${name}</p><${tag} ${attr} class="${classes}" data-sample="${name}" aria-label="Save">${svg}${name === 'icon' ? '' : '<span>Save</span>'}</${tag}></section>`;
  }).join('');
  await connection.send('Page.setDocumentContent', {frameId: frameTree.frame.id, html: `<!doctype html><html lang="en"><meta name="viewport" content="width=device-width"><style>${css}body{margin:0}main{padding:16px}section{margin-bottom:16px}p{margin:0 0 4px}</style><body><main>${buttons}<span id="end"></span></main></body></html>`});
  let normal;
  for (const text of [100, 200]) {
   await evaluate(`document.documentElement.style.fontSize = '${text * 16 / 100}px'; document.body.tabIndex = -1; document.body.focus(); window.scrollTo(0, 0)`);
   assert.equal(await evaluate('parseFloat(getComputedStyle(document.documentElement).fontSize)'), text * 16 / 100, 'actual text size');
   assert.equal(await evaluate("matchMedia('(prefers-color-scheme: dark)').matches"), dark, 'actual colour preference');
   assert.equal(await evaluate('innerWidth'), width, 'actual viewport width');
   const values = await evaluate(`([...document.querySelectorAll('[data-sample]')].map(e => {
    const icon=e.querySelector('svg').getBoundingClientRect(), r=e.getBoundingClientRect();
    return {name:e.dataset.sample, iconWidth:icon.width, iconHeight:icon.height, fontSize:parseFloat(getComputedStyle(e).fontSize), targetHeight:r.height, targetWidth:r.width, right:r.right, viewport:innerWidth};
   }))`);
   const label = `${surface}-${width}-${text}-${dark ? 'dark' : 'light'}`;
   if (text === 100) normal = values;
   else for (const [i, value] of values.entries()) {
    if (Math.abs(value.iconWidth - normal[i].iconWidth * 2) > 0.1 || Math.abs(value.iconHeight - normal[i].iconHeight * 2) > 0.1) failures.push(label + ': icon does not grow with doubled text: ' + JSON.stringify({before:normal[i], after:value}));
   }
   for (const value of values) assert.ok(value.targetHeight >= 44 && value.targetWidth >= 44 && value.right <= value.viewport, label + ': target must fit and remain at least 44px: ' + JSON.stringify(value));
   for (const sample of samples) {
    await key('Tab');
    const active = await evaluate("({name:document.activeElement.dataset.sample,focus:document.activeElement.matches(':focus-visible'),outline:getComputedStyle(document.activeElement).outlineStyle})");
    assert.equal(active.name, sample.name, label + ': native keyboard order');
    assert.ok(active.focus && active.outline !== 'none', label + ': visible keyboard focus');
   }
   if (process.env.EVIDENCE_DIR) {
    mkdirSync(process.env.EVIDENCE_DIR, {recursive:true});
    const {cssContentSize} = await connection.send('Page.getLayoutMetrics');
    const {data} = await connection.send('Page.captureScreenshot', {format:'png',captureBeyondViewport:true,clip:{x:0,y:0,width,height:Math.ceil(cssContentSize.height),scale:1}});
    writeFileSync(process.env.EVIDENCE_DIR + '/' + label + '.png', Buffer.from(data,'base64'));
   }
   console.log('button icon measurements ' + label + ': ' + JSON.stringify(values));
  }
 }
 assert.deepEqual(failures, [], 'all shared button icons must scale with the reader\'s text');
 console.log('PASS button icon scaling in both surfaces at all widths and colour preferences');
} finally {connection.close();}
