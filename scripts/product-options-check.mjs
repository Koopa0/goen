import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { readFile, mkdtemp, rm } from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { contrastRatio, measureControlBoundary } from './control-boundary.mjs';

const fixtures = JSON.parse(await readFile(process.argv[2], 'utf8'));
const server = createServer(async (request, response) => {
  const url = new URL(request.url, 'http://localhost');
  if (url.pathname === '/base.css' || url.pathname === '/app.css') {
    response.setHeader('Content-Type', 'text/css');
    response.end(await readFile(new URL('../assets/css/app' + url.pathname, import.meta.url)));
  } else if (url.pathname === '/p/two-colours') {
    const choice = url.searchParams.get('colour') || 'blue';
    assert.ok(fixtures[choice], `unknown choice ${choice}`);
    response.setHeader('Content-Type', 'text/html');
    response.end(fixtures[choice]);
  } else {
    response.statusCode = 404;
    response.end();
  }
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
const profile = await mkdtemp(join(tmpdir(), 'goen-product-options-'));
let chromeLog = '';
const chrome = spawn(process.env.GOEN_CHROME, [
  '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
  '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank',
], { stdio: ['ignore', 'ignore', 'pipe'] });
chrome.stderr.on('data', data => { chromeLog += data; });
let socket;
let browserTarget;
const pending = new Map();
let sequence = 0;

async function until(probe, description) {
  const deadline = performance.now() + 10000;
  while (performance.now() < deadline) {
    const result = await probe();
    if (result) return result;
    await delay(20);
  }
  throw new Error(`timed out: ${description}\n${chromeLog.slice(-1500)}`);
}

function send(method, params = {}, sessionId) {
  const id = ++sequence;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`${method} timed out`)); }, 10000);
    pending.set(id, { resolve, reject, timer });
    socket.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
  });
}

try {
  const port = await until(async () => {
    try { return (await readFile(join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0]; }
    catch { return false; }
  }, 'Chrome debugging port');
  const version = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json();
  socket = new WebSocket(version.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, { once: true });
    socket.addEventListener('error', reject, { once: true });
  });
  socket.addEventListener('message', ({ data }) => {
    const message = JSON.parse(data);
    const command = pending.get(message.id);
    if (!command) return;
    pending.delete(message.id);
    clearTimeout(command.timer);
    if (message.error) command.reject(new Error(JSON.stringify(message.error)));
    else command.resolve(message.result);
  });
  browserTarget = (await send('Target.createTarget', { url: 'about:blank' })).targetId;
  const { sessionId } = await send('Target.attachToTarget', { targetId: browserTarget, flatten: true });
  await send('Page.enable', {}, sessionId);
  async function evaluate(expression) {
    const result = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true }, sessionId);
    assert.equal(result.exceptionDetails, undefined, JSON.stringify(result.exceptionDetails));
    return result.result.value;
  }
  for (const choice of Object.keys(fixtures)) {
    for (const width of [320, 375, 1440]) {
      for (const fontSize of ['100%', '200%']) {
      await send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false }, sessionId);
      await send('Page.navigate', { url: `${origin}/p/two-colours?colour=${choice}` }, sessionId);
      await until(() => evaluate(`document.readyState === 'complete' && !!document.querySelector('#buybox')`), 'product ready');
      await evaluate(`document.documentElement.style.fontSize = ${JSON.stringify(fontSize)}`);
      const state = await evaluate(`(() => {
        const contrast = ${contrastRatio.toString()};
        const rgba = value => value.match(/[\\d.]+/g).map(Number);
        const circle = document.querySelector('.goen-swatch__dot circle');
        const stroke = rgba(getComputedStyle(circle).stroke);
        const ground = rgba(getComputedStyle(document.body).backgroundColor);
        const ring = stroke.slice(0, 3).map((c, i) => c * (stroke[3] ?? 1) + ground[i] * (1 - (stroke[3] ?? 1)));
        const selected = document.querySelector('.goen-swatch--on.goen-swatch--out:not(.goen-swatch--dot)');
        const style = getComputedStyle(selected);
        const boundary = (${measureControlBoundary.toString()})(['.goen-swatch--on.goen-swatch--out:not(.goen-swatch--dot)'], contrast)[0];
        return { ring: contrast(ring, ground), strokeWidth: parseFloat(getComputedStyle(circle).strokeWidth),
          text: contrast(rgba(style.color), boundary.fill), edge: Math.max(boundary.borderContrast, boundary.outlineContrast),
          light: boundary.fill.every(c => c > 200),
          label: document.querySelector('.goen-swatch--dot').textContent.trim(),
          overflow: document.documentElement.scrollWidth > innerWidth };
      })()`);
      assert.ok(state.ring >= 3 && state.strokeWidth >= 1, `${choice}@${width}: swatch ring ${state.ring}:1, want >=3:1`);
      assert.ok(state.text >= 4.5, `${choice}@${width}: selected unavailable text ${state.text}:1`);
      assert.ok(state.light && state.edge >= 3, `${choice}@${width}: selected unavailable option needs a light fill and a dark edge`);
      assert.equal(state.overflow, false, `${choice}@${width}: product options overflow`);
      // A colour option says its name on the box at rest, not only under the
      // pointer or focus: the shopper should not have to guess it.
      const named = await evaluate(`(() => {
        document.activeElement?.blur();
        return [...document.querySelectorAll('.goen-swatch--dot')].map(box => {
          const label = box.querySelector('.goen-swatch__label');
          const rect = label?.getBoundingClientRect();
          const inside = rect && box.getBoundingClientRect();
          return { text: label?.textContent.trim() ?? '',
            visible: !!rect && getComputedStyle(label).clipPath === 'none' && rect.width > 1 && rect.height > 1 &&
              rect.left >= inside.left && rect.right <= inside.right };
        });
      })()`);
      assert.ok(named.length > 0, `${choice}@${width}/${fontSize}: no colour option`);
      for (const n of named) assert.equal(n.visible, true, `${choice}@${width}/${fontSize}: colour name ${JSON.stringify(n.text)} not shown on its box at rest`);
      assert.equal(await evaluate(`document.documentElement.scrollWidth > innerWidth`), false, `${choice}@${width}/${fontSize}: colour names overflow`);
      console.log(`PASS ${choice}@${width}/${fontSize}: ring=${state.ring.toFixed(2)}, text=${state.text.toFixed(2)}, edge=${state.edge.toFixed(2)}`);
      }
    }
  }
} finally {
  if (socket?.readyState === WebSocket.OPEN) {
    if (browserTarget) await send('Target.closeTarget', { targetId: browserTarget }).catch(() => {});
    socket.close();
  }
  for (const command of pending.values()) { clearTimeout(command.timer); command.reject(new Error('browser closed')); }
  chrome.kill();
  await new Promise(resolve => { if (chrome.exitCode !== null) resolve(); else chrome.once('exit', resolve); });
  server.closeAllConnections();
  await new Promise(resolve => server.close(resolve));
  await rm(profile, { recursive: true, force: true, maxRetries: 10, retryDelay: 50 });
}
