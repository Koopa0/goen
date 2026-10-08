import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { readFile, mkdtemp, rm } from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';

const fixtures = JSON.parse(await readFile(process.argv[2], 'utf8'));
const htmx = await readFile(new URL('../assets/js/vendor/htmx.min.js', import.meta.url));
const goen = await readFile(new URL('../assets/js/goen.js', import.meta.url));
let requests = [];
let submitted;
const server = createServer(async (request, response) => {
  const url = new URL(request.url, 'http://localhost');
  if (url.pathname === '/htmx.js') {
    response.setHeader('Content-Type', 'text/javascript');
    response.end(htmx);
  } else if (url.pathname === '/goen.js') {
    response.setHeader('Content-Type', 'text/javascript');
    response.end(goen);
  } else if (url.pathname === '/p/two-colours') {
    const choice = url.searchParams.get('colour') || 'blue';
    assert.ok(fixtures[choice], `unknown choice ${choice}`);
    response.setHeader('Content-Type', 'text/html');
    if (request.headers['hx-request']) requests.push({ choice, response });
    else response.end(fixtures[choice]);
  } else if (url.pathname === '/cart/items' && request.method === 'POST') {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    submitted = new URLSearchParams(Buffer.concat(chunks).toString()).get('variant');
    response.setHeader('Content-Type', 'text/html');
    response.end(fixtures[submitted]);
  } else {
    response.statusCode = 404;
    response.end();
  }
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
const profile = await mkdtemp(join(tmpdir(), 'goen-product-choice-'));
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
  for (const latest of ['blue', 'black']) {
    const older = latest === 'blue' ? 'black' : 'blue';
    for (const firstResponse of ['older', 'latest', 'third']) {
      const expected = firstResponse === 'third' ? 'red' : latest;
      requests = [];
      submitted = undefined;
      await send('Page.navigate', { url: `${origin}/p/two-colours` }, sessionId);
      await until(() => evaluate(`document.readyState === 'complete' && !!document.querySelector('#pdp-add') && typeof htmx !== 'undefined'`), 'product ready');
      await evaluate(`window.choicePending = 0; document.addEventListener('htmx:before:request', () => window.choicePending++); document.addEventListener('htmx:finally:request', () => window.choicePending--); document.querySelector('[href="/p/two-colours?colour=${older}"]').click()`);
      await until(() => requests.length === 1, 'older request started');
      await evaluate(`document.querySelector('[href="/p/two-colours?colour=${latest}"]').click()`);
      await until(() => requests.length === 2, 'latest request started');
      if (firstResponse === 'third') {
        await evaluate(`document.querySelector('[href="/p/two-colours?colour=red"]').click()`);
        await until(() => requests.length === 3, 'third request started');
        requests[2].response.end(fixtures.red);
        await delay(100);
      }
      assert.deepEqual(requests.slice(0, 2).map(request => request.choice), [older, latest]);
      const order = firstResponse === 'older' ? [0, 1] : [1, 0];
      requests[order[0]].response.end(fixtures[requests[order[0]].choice]);
      await delay(100);
      requests[order[1]].response.end(fixtures[requests[order[1]].choice]);
      await until(() => evaluate('window.choicePending === 0'), 'both requests settled');
      const state = await evaluate(`({
        variant: document.querySelector('#pdp-add [name="variant"]').value,
        selected: document.querySelector('#buybox [aria-current="true"]').textContent.trim(),
        price: document.querySelector('#buybox .goen-pdp__price').textContent.trim(),
        stickyPrice: document.querySelector('#buybar .goen-buybar__price').textContent.trim(),
        image: document.querySelector('#gallery img').getAttribute('src'),
        colour: new URL(location.href).searchParams.get('colour'),
        boxes: document.querySelectorAll('#buybox').length
      })`);
      assert.equal(state.variant, expected, `latest purchase variant (${latest}, ${firstResponse} response first)`);
      assert.equal(state.selected.toLowerCase(), expected, 'selected option agrees');
      assert.equal(state.price, state.stickyPrice, 'sticky price agrees with purchase price');
      assert.equal(state.image, `/${expected}.webp`, 'gallery agrees with purchase');
      assert.equal(state.colour, expected, 'history agrees with purchase');
      assert.equal(state.boxes, 1, 'one live purchase form');
      await evaluate(`document.querySelector('#buybar button[form="pdp-add"]').click()`);
      await until(() => submitted !== undefined, 'sticky purchase POST');
      assert.equal(submitted, expected, 'sticky button submits the displayed variant');
      await until(() => evaluate('window.choicePending === 0'), 'purchase settled');
      console.log(`PASS latest=${latest}, ${firstResponse} response first, submitted=${submitted}`);
    }
  }
  // Native links still reach the selected form when enhancement is unavailable.
  await send('Emulation.setScriptExecutionDisabled', { value: true }, sessionId);
  await send('Page.navigate', { url: `${origin}/p/two-colours?colour=black` }, sessionId);
  await until(() => evaluate(`document.readyState === 'complete' && document.querySelector('#pdp-add [name="variant"]')?.value === 'black'`), 'native selected product');
  console.log('PASS native option URL renders the selected purchase form');
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
  await rm(profile, { recursive: true, force: true });
}
