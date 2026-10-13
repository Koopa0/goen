import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { readFileSync, mkdtempSync, rmSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { resolve, join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';

const fixtures = JSON.parse(readFileSync(0, 'utf8'));
const server = createServer((req, res) => {
  if (req.url.startsWith('/static/')) {
    const file = resolve('assets', req.url.slice(8).split('?')[0]);
    if (!file.startsWith(resolve('assets') + '/')) { res.writeHead(404).end(); return; }
    try {
      res.setHeader('Content-Type', file.endsWith('.css') ? 'text/css' : 'application/octet-stream');
      res.end(readFileSync(file));
    } catch { res.writeHead(404).end(); }
    return;
  }
  res.setHeader('Content-Type', 'text/html; charset=utf-8');
  res.end(fixtures[req.url.slice(1)]);
});
server.listen(0, '127.0.0.1');
await once(server, 'listening');
const origin = `http://127.0.0.1:${server.address().port}`;
const profile = mkdtempSync(join(tmpdir(), 'points-reflow-'));
const chrome = spawn(process.env.CHROME, ['--headless=new', '--no-sandbox', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'], { stdio: ['ignore', 'ignore', 'pipe'] });
let stderr = '';
chrome.stderr.on('data', chunk => { stderr += chunk; });
let ws;
try {
  let endpoint;
  for (let i = 0; i < 600; i++) {
    endpoint = stderr.match(/DevTools listening on (ws:\/\/[^\s]+)/)?.[1];
    if (endpoint) break;
    if (chrome.exitCode !== null) throw new Error(stderr);
    await delay(50);
  }
  assert.ok(endpoint, 'Chrome did not expose DevTools');
  const host = new URL(endpoint).host;
  const targets = await (await fetch(`http://${host}/json/list`)).json();
  ws = new WebSocket(targets.find(t => t.type === 'page').webSocketDebuggerUrl);
  await once(ws, 'open');
  let sequence = 0;
  const pending = new Map();
  ws.addEventListener('message', event => {
    const message = JSON.parse(event.data);
    if (!message.id) return;
    const waiter = pending.get(message.id);
    pending.delete(message.id);
    message.error ? waiter.reject(new Error(JSON.stringify(message.error))) : waiter.resolve(message.result);
  });
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++sequence;
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params }));
  });
  const evaluate = async expression => {
    const reply = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
    assert.ok(!reply.exceptionDetails, JSON.stringify(reply.exceptionDetails));
    return reply.result.value;
  };
  await send('Page.enable');
  for (const [name] of Object.entries(fixtures)) {
    for (const width of [320, 375, 1440]) {
      await send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false });
      await send('Page.navigate', { url: `${origin}/${name}` });
      for (let i = 0; i < 200; i++) {
        if (await evaluate(`document.readyState === 'complete' && !!document.querySelector('.goen-points__submit')`)) break;
        await delay(25);
      }
      await evaluate(`document.fonts.ready`);
      await evaluate(`document.documentElement.style.fontSize = '200%'`);
      const box = await evaluate(`(() => {
        const button = document.querySelector('.goen-points__submit');
        const form = button.closest('form');
        const b = button.getBoundingClientRect(), f = form.getBoundingClientRect();
        const range = document.createRange(); range.selectNodeContents(button);
        return { button: { left: b.left, right: b.right, width: b.width }, form: { left: f.left, right: f.right, width: f.width }, text: [...range.getClientRects()].map(r => ({ left: r.left, right: r.right })), whiteSpace: getComputedStyle(button).whiteSpace, viewport: innerWidth, document: document.documentElement.scrollWidth };
      })()`);
      console.log(`${name}@${width}@text200 ${JSON.stringify(box)}`);
      assert.ok(box.button.right <= box.form.right + 0.5 && box.button.right <= width + 0.5, `${name}@${width}: redemption button exceeds its form or viewport`);
      assert.ok(box.text.every(r => r.left >= box.button.left - 0.5 && r.right <= box.button.right + 0.5), `${name}@${width}: redemption label is clipped`);
    }
  }
} finally {
  ws?.close();
  chrome.kill('SIGTERM');
  if (chrome.exitCode === null && chrome.signalCode === null) {
    const exited = once(chrome, 'exit');
    const killer = setTimeout(() => chrome.kill('SIGKILL'), 5000);
    await exited;
    clearTimeout(killer);
  }
  server.close();
  // Chrome's helper processes can outlive the browser and keep writing the
  // profile; a leftover temp directory must not fail a check that already passed.
  try {
    rmSync(profile, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
  } catch (err) {
    console.warn(`points-reflow: left ${profile}: ${err.message}`);
  }
}
