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
let purchases = [];
const server = createServer(async (request, response) => {
  const url = new URL(request.url, 'http://localhost');
  if (url.pathname === '/htmx.js') {
    response.setHeader('Content-Type', 'text/javascript');
    response.end(htmx);
  } else if (url.pathname === '/goen.js') {
    response.setHeader('Content-Type', 'text/javascript');
    response.end(goen);
  } else if (url.pathname === '/p/two-colours') {
    const choice = (url.searchParams.get('locale') || 'en') + '-' + (url.searchParams.get('colour') || 'blue');
    assert.ok(fixtures[choice], `unknown choice ${choice}`);
    response.setHeader('Content-Type', 'text/html');
    response.setHeader('Set-Cookie', `goen_locale=${url.searchParams.get('locale') || 'en'}; Path=/`);
    if (url.searchParams.get('added') === 'added') response.end(fixtures[choice + '-added']);
    else if (request.headers['hx-request']) requests.push({ choice, response });
    else response.end(fixtures[choice]);
  } else if (url.pathname === '/cart/items' && request.method === 'POST') {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const form = new URLSearchParams(Buffer.concat(chunks).toString());
    const variant = form.get('variant');
    const locale = /(?:^|; )goen_locale=([^;]*)/u.exec(request.headers.cookie || '')?.[1] || 'en';
    response.setHeader('Content-Type', 'text/html');
    purchases.push({variant, locale, quantity: form.get('quantity'), choice: locale + '-' + variant + '-added', response});
  } else {
    response.statusCode = 404;
    response.end();
  }
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
const profile = await mkdtemp(join(tmpdir(), 'goen-product-purchase-'));
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

// Keep the production POST/303/GET shape while holding the POST response.
function release(held) {
  if (held.variant) {
    held.response.statusCode = 303;
    held.response.setHeader('Location', `/p/two-colours?colour=${held.variant}&locale=${held.locale}&added=added#buybox`);
    held.response.end();
  } else held.response.end(fixtures[held.choice]);
}

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
  const problems = [];
  for (const locale of ['en', 'zh-Hant']) {
    for (const initial of ['blue', 'black']) {
      const latest = initial === 'blue' ? 'black' : 'blue';
      for (const firstResponse of ['purchase', 'choice']) {
        for (const cancelRead of [false, true]) {
          requests = [];
          purchases = [];
          await send('Page.navigate', {url: `${origin}/p/two-colours?locale=${locale}&colour=${initial}`}, sessionId);
          await until(() => evaluate(`document.readyState === 'complete' && !!document.querySelector('#pdp-add') && typeof htmx !== 'undefined'`), 'product ready');
          await evaluate(`window.purchaseProbe = { requests: [], finished: new Set(), errors: [] };
            document.addEventListener('htmx:before:request', event => { if (!event.defaultPrevented) purchaseProbe.requests.push(event.detail.ctx); });
            document.addEventListener('htmx:finally:request', event => purchaseProbe.finished.add(event.detail.ctx));
            document.addEventListener('htmx:error', event => purchaseProbe.errors.push(String(event.detail.error)));
            document.querySelector('#pdp-add button[type="submit"]').click();
            document.querySelector('#pdp-add button[type="submit"]').click()`);
          await until(() => purchases.length === 1, 'held purchase POST');
          await evaluate(`document.querySelector('[href="/p/two-colours?colour=${latest}&locale=${locale}"]').click()`);
          await until(() => requests.length === 1, 'choice GET after purchase started');
          if (cancelRead) {
            await evaluate('purchaseProbe.requests[1].request.abort()');
            await until(() => evaluate('purchaseProbe.finished.has(purchaseProbe.requests[1])'), 'cancelled read settled');
            assert.equal(await evaluate('purchaseProbe.requests[0].request.signal.aborted'), false, 'cancelling a read must not cancel the purchase');
            await evaluate(`document.querySelector('[href="/p/two-colours?colour=${latest}&locale=${locale}"]').click()`);
            await until(() => requests.length === 2, 'replacement choice GET');
          }
          assert.equal(purchases[0].variant, initial, 'POST keeps the submitted variant');
          assert.equal(purchases[0].quantity, '1', 'POST keeps the submitted quantity');
          const responses = firstResponse === 'purchase' ? [purchases[0], requests.at(-1)] : [requests.at(-1), purchases[0]];
          release(responses[0]);
          await until(() => evaluate(`purchaseProbe.finished.has(purchaseProbe.requests[${firstResponse === 'purchase' ? '0' : 'purchaseProbe.requests.length - 1'}])`), 'first response settled');
          release(responses[1]);
          await until(() => evaluate('purchaseProbe.finished.has(purchaseProbe.requests[0]) && purchaseProbe.finished.has(purchaseProbe.requests.at(-1))'), 'both responses settled');
          const state = await evaluate(`({
            variant: document.querySelector('#pdp-add [name="variant"]').value,
            selected: document.querySelector('#buybox [aria-current="true"]').textContent.trim().toLowerCase(),
            price: document.querySelector('#buybox .goen-pdp__price').textContent.trim(),
            stickyPrice: document.querySelector('#buybar .goen-buybar__price')?.textContent.trim(),
            image: document.querySelector('#gallery img').getAttribute('src'),
            colour: new URL(location.href).searchParams.get('colour'),
            notices: document.querySelectorAll('#buybox #added').length,
            notice: document.querySelector('#buybox #added')?.textContent.trim(),
            cartCount: document.querySelector('#cart-count').textContent.trim(),
            boxes: document.querySelectorAll('#buybox').length,
            busy: document.querySelector('#pdp-add').hasAttribute('data-request-pending'),
            disabled: document.querySelector('#add-to-cart').getAttribute('aria-disabled'),
            requests: purchaseProbe.requests.length, errors: purchaseProbe.errors
          })`);
          const at = `${locale}: purchase=${initial}, choice=${latest}, ${firstResponse} response first, read cancelled=${cancelRead}`;
          try {
            assert.equal(state.variant, latest, 'final purchase variant follows the last choice');
            assert.equal(state.selected, latest, 'selected option follows the last choice');
            assert.equal(state.price, state.stickyPrice, 'sticky price agrees with the displayed choice');
            assert.equal(state.image, `/${latest}.webp`, 'gallery agrees with the displayed choice');
            assert.equal(state.colour, latest, 'history agrees with the displayed choice');
            assert.equal(state.notices, 1, 'one live purchase confirmation');
            assert.ok(state.notice?.includes(locale === 'en' ? 'Added to your cart.' : '已加入購物車。'), 'accepted purchase remains confirmed');
            assert.equal(state.cartCount, '1', 'accepted purchase updates the cart once');
            assert.equal(state.boxes, 1, 'one live buy box');
            assert.equal(state.busy, false, 'the live purchase form is no longer pending');
            assert.equal(state.disabled, null, 'the live purchase button is available');
            assert.equal(state.requests, cancelRead ? 3 : 2, 'one POST and the requested reads');
            assert.equal(state.errors.length, cancelRead ? 1 : 0, 'only the cancelled read may fail');
            assert.ok(state.errors.every(error => error.includes('AbortError')), 'only an explicit read cancellation is reported');
            assert.equal(purchases.length, 1, 'choice does not repeat or cancel the purchase');
            console.log(`PASS ${at}`);
          } catch (error) {
            problems.push(`${at}: ${error.message}; state=${JSON.stringify(state)}`);
          }
        }
      }
    }
  }
  assert.deepEqual(problems, [], problems.join('\n'));
  // Native links still reach the selected form when enhancement is unavailable.
  await send('Emulation.setScriptExecutionDisabled', { value: true }, sessionId);
  await send('Page.navigate', { url: `${origin}/p/two-colours?colour=black&locale=en` }, sessionId);
  await until(() => evaluate(`document.readyState === 'complete' && document.querySelector('#pdp-add [name="variant"]')?.value === 'black'`), 'native selected product');
  console.log('PASS native option URL renders the selected purchase form');
  purchases = [];
  await evaluate(`document.querySelector('#pdp-add button[type="submit"]').click()`);
  await until(() => purchases.length === 1, 'native purchase POST');
  assert.equal(purchases[0].variant, 'black', 'native POST keeps the selected variant');
  release(purchases[0]);
  await until(() => evaluate(`document.readyState === 'complete' && !!document.querySelector('#added') && document.querySelector('#cart-count')?.textContent === '1'`), 'native redirected confirmation');
  assert.equal(purchases.length, 1, 'native purchase is submitted once');
  console.log('PASS native purchase follows the 303 to its selected product and confirmation');
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
