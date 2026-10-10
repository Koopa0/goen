import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { join } from 'node:path';

const [root, fixtures, debuggingPort] = process.argv.slice(2);
const server = createServer((request, response) => {
  const url = new URL(request.url, 'http://localhost');
  const path = url.pathname.startsWith('/static/')
    ? join(root, 'assets', url.pathname.slice(8))
    : join(fixtures, `${url.searchParams.get('lang')}.html`);
  try {
    const types = { css: 'text/css', js: 'text/javascript', html: 'text/html', woff2: 'font/woff2', svg: 'image/svg+xml' };
    response.setHeader('Content-Type', types[path.split('.').pop()] || 'application/octet-stream');
    response.end(readFileSync(path));
  } catch {
    response.statusCode = 404;
    response.end();
  }
});
await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const debugging = `http://127.0.0.1:${debuggingPort}`;
let socket;
let target;
try {
  target = await (await fetch(`${debugging}/json/new?about:blank`, { method: 'PUT' })).json();
  socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let id = 0;
  const pending = new Map();
  socket.onmessage = ({ data }) => {
    const message = JSON.parse(data);
    const request = pending.get(message.id);
    if (!request) return;
    pending.delete(message.id);
    clearTimeout(request.timer);
    if (message.error) request.reject(new Error(JSON.stringify(message.error)));
    else request.resolve(message.result);
  };
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    const current = ++id;
    const timer = setTimeout(() => { pending.delete(current); reject(new Error(`${method} timed out`)); }, 20000);
    pending.set(current, { resolve, reject, timer });
    socket.send(JSON.stringify({ id: current, method, params }));
  });
  const evaluate = async (expression) => {
    const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
    return result.result.value;
  };
  await send('Page.enable');
  for (const locale of ['en', 'zh-Hant']) {
    for (const width of [320, 375, 1440]) {
      for (const fontSize of [16, 32]) {
        await send('Emulation.setDeviceMetricsOverride', { width, height: 812, deviceScaleFactor: 1, mobile: width < 768 });
        await send('Page.navigate', { url: `${origin}/?lang=${locale}` });
        for (let attempt = 0; await evaluate('document.readyState') !== 'complete'; attempt++) {
          if (attempt === 100) throw new Error('Campaign page did not finish loading');
          await sleep(30);
        }
        await evaluate(`document.fonts.ready.then(() => {
          document.documentElement.style.fontSize = '${fontSize}px';
          return new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
        })`);
        const geometry = await evaluate(`(() => {
          window.scrollTo(10000, 0);
          const rect = document.querySelector('.goen-adminbar__actions').getBoundingClientRect();
          return { locale: document.documentElement.lang, viewport: document.documentElement.clientWidth,
            bodyWidth: document.body.scrollWidth, innerWidth, scrollX,
            fontSize: getComputedStyle(document.documentElement).fontSize,
            actionsRight: rect.right, actionsWidth: rect.width,
            buttons: [...document.querySelectorAll('.goen-adminbar__actions button, .goen-adminbar__actions summary')]
              .filter(element => !element.closest('details:not([open]) .goen-langmenu__panel'))
              .map(element => { const box = element.getBoundingClientRect();
                return { left: box.left, right: box.right, height: box.height }; }) };
        })()`);
        const label = `${locale} ${width}px ${fontSize / 16 * 100}% text`;
        console.log(`${label}: ${JSON.stringify(geometry)}`);
        assert.equal(geometry.locale, locale, label);
        assert.equal(geometry.fontSize, `${fontSize}px`, label);
        assert.ok(geometry.bodyWidth <= geometry.viewport, `${label}: campaign body widens`);
        assert.equal(geometry.innerWidth, width, `${label}: campaign expands the mobile viewport`);
        assert.equal(geometry.scrollX, 0, `${label}: campaign scrolls horizontally`);
        assert.ok(geometry.actionsRight <= width + 0.5, `${label}: admin bar actions escape the viewport`);
        for (const button of geometry.buttons) {
          assert.ok(button.left >= 0 && button.right <= width + 0.5, `${label}: staff control escapes the viewport`);
          assert.ok(button.height >= 44, `${label}: staff control loses its target height`);
        }
      }
    }
  }
} finally {
  socket?.close();
  if (target) await fetch(`${debugging}/json/close/${target.id}`).catch(() => {});
  server.closeAllConnections();
  server.close();
}
