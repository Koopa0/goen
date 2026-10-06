import assert from 'node:assert/strict';
import { setTimeout as delay } from 'node:timers/promises';
import { pathToFileURL } from 'node:url';

const origin = (process.env.GOEN_URL || 'http://127.0.0.1:9700/').replace(/\/$/, '');
const debugging = `http://127.0.0.1:${Number(process.env.CDP_PORT || 9222)}`;
const failures = [];

export async function waitForDebuggingEndpoint(endpoint, { timeoutMs = 30000, retryMs = 200 } = {}) {
  const deadline = performance.now() + timeoutMs;
  let attempts = 0;
  let cause;
  while (performance.now() < deadline) {
    attempts++;
    try {
      const remaining = Math.max(1, Math.ceil(deadline - performance.now()));
      const response = await fetch(endpoint, { signal: AbortSignal.timeout(remaining), redirect: 'error' });
      if (!response.ok) {
        await response.body?.cancel();
        throw new Error(`debugging endpoint answered HTTP ${response.status}`);
      }
      const version = await response.json();
      assert.equal(typeof version.webSocketDebuggerUrl, 'string', 'debugging endpoint must provide a WebSocket URL');
      assert.ok(version.webSocketDebuggerUrl.length > 0, 'debugging endpoint must provide a nonempty WebSocket URL');
      return version;
    } catch (error) {
      // A final deadline abort must not hide an earlier startup failure.
      if (!cause || !['TimeoutError', 'AbortError'].includes(error.name)) cause = error;
    }
    const remaining = deadline - performance.now();
    if (remaining > 0) await delay(Math.min(retryMs, remaining));
  }
  throw new Error(`Chrome debugging endpoint ${endpoint} was not ready within ${timeoutMs}ms after ${attempts} attempts; inspect chrome.log and .layout-chrome/pid for startup evidence`, { cause });
}

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

const setup = `(() => {
  const box = document.querySelector('.goen-filters input[name="in_stock"]');
  const form = box?.form;
  const note = document.querySelector('.goen-filters__error');
  if (!box || !form || !note) throw new Error('filter feedback landmarks missing');
  const shell = document.querySelector('.goen-filters__shell');
  if (shell) shell.open = true;
  const probe = window.filterFeedbackCheck = {
    box, form, note, requests: [], finished: new Set(),
    message: note.textContent.trim(), timeout: htmx.config.defaultTimeout,
  };
  document.addEventListener('htmx:before:request', ({ detail }) => {
    if (detail.ctx?.request?.form === form) probe.requests.push(detail.ctx);
  });
  document.addEventListener('htmx:finally:request', ({ detail }) => {
    if (detail.ctx?.request?.form === form) probe.finished.add(detail.ctx);
  });
  return { lang: document.documentElement.lang, message: probe.message, role: note.getAttribute('role'), hidden: note.hidden };
})()`;

async function journey(connection, locale, width) {
  const { browserContextId } = await connection.send('Target.createBrowserContext', { disposeOnDetach: true });
  let targetId;
  let stopPaused;
  let stopFailed;
  try {
    ({ targetId } = await connection.send('Target.createTarget', { url: 'about:blank', browserContextId }));
    const { sessionId } = await connection.send('Target.attachToTarget', { targetId, flatten: true });
    const send = (method, params) => connection.send(method, params, sessionId);
    const evaluate = async (expression) => {
      const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
      if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
      return result.result.value;
    };
    const waitFor = async (predicate, description) => {
      const deadline = Date.now() + 15000;
      while (!await predicate()) {
        if (protocolFailure) throw protocolFailure;
        if (Date.now() >= deadline) throw new Error(description);
        await new Promise((resolve) => setTimeout(resolve, 20));
      }
      if (protocolFailure) throw protocolFailure;
    };
    const held = new Map();
    let mode = 'real';
    let protocolFailure;
    const intercept = async ({ params }) => {
      const { requestId, request, networkId } = params;
      assert.equal(new URL(request.url).origin, origin);
      assert.equal(new URL(request.url).pathname, '/c/audio');
      assert.equal(request.headers['HX-Request'] || request.headers['hx-request'], 'true');
      if (mode === 'reject') await send('Fetch.failRequest', { requestId, errorReason: 'ConnectionClosed' });
      else if (mode === 'http') await send('Fetch.fulfillRequest', { requestId, responseCode: 500, body: '' });
      else if (mode === 'hold' || mode === 'timeout') held.set(requestId, { networkId, url: request.url });
      else await send('Fetch.continueRequest', { requestId });
    };
    stopPaused = connection.on('Fetch.requestPaused', (event) => {
      if (event.sessionId !== sessionId) return;
      intercept(event).catch((error) => { protocolFailure = error; });
    });
    stopFailed = connection.on('Network.loadingFailed', (event) => {
      if (event.sessionId !== sessionId) return;
      for (const [requestId, request] of held) {
        if (request.networkId === event.params.requestId) held.delete(requestId);
      }
    });
    await send('Page.enable');
    await send('Network.enable');
    await send('Network.setCacheDisabled', { cacheDisabled: true });
    await send('Network.setCookie', { name: 'goen_locale', value: locale, url: origin + '/' });
    await send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false });
    const load = async () => {
      await send('Page.navigate', { url: origin + '/c/audio' });
      await waitFor(() => evaluate(`document.readyState === 'complete' && !!document.querySelector('.goen-filters')?._htmx?.initialized`), 'listing did not initialize');
      const initial = await evaluate(setup);
      assert.equal(initial.lang, locale);
      assert.equal(initial.hidden, true);
      assert.equal(initial.role, 'alert');
      assert.ok(initial.message.length > 0);
      if (locale === 'en') assert.equal(initial.message, 'We cannot show the product list right now. Please try again shortly.');
    };
    await send('Fetch.enable', { patterns: [{ urlPattern: origin + '/c/audio*', resourceType: 'Fetch', requestStage: 'Request' }] });
    await load();
    const change = async () => {
      if (protocolFailure) throw protocolFailure;
      const index = await evaluate(`(() => {
        const p = window.filterFeedbackCheck;
        p.before = document.getElementById('listing-results');
        p.markup = p.before.innerHTML;
        p.url = location.href;
        p.checked = p.box.checked;
        const index = p.requests.length;
        p.box.focus(); p.box.click();
        return index;
      })()`);
      await waitFor(() => evaluate(`filterFeedbackCheck.requests.length > ${index}`), 'filter request did not start');
      return index;
    };
    const finished = (index) => waitFor(() => evaluate(`filterFeedbackCheck.finished.has(filterFeedbackCheck.requests[${index}])`), 'filter request did not finish');
    const state = (index) => evaluate(`(() => {
      const p = filterFeedbackCheck, ctx = p.requests[${index}];
      const results = document.getElementById('listing-results');
      const chips = document.getElementById('filters-applied');
      const count = document.getElementById('listing-status');
      const server = ctx.text ? new DOMParser().parseFromString(ctx.text, 'text/html') : null;
      const stockLabel = p.box.closest('label').querySelector('.goen-filters__label').textContent.trim();
      return {
        hidden: p.note.hidden, visible: !p.note.hidden && getComputedStyle(p.note).display !== 'none' && p.note.getBoundingClientRect().height > 0,
        message: p.note.textContent.trim() === p.message, role: p.note.getAttribute('role'),
        response: !!ctx.response, ok: ctx.response?.raw?.ok === true, aborted: ctx.request.signal.aborted,
        stale: results === p.before && results.innerHTML === p.markup,
        swapped: results !== p.before, changed: p.box.checked !== p.checked,
        focused: document.activeElement === p.box, urlUnchanged: location.href === p.url,
        stockURL: new URL(location.href).searchParams.get('in_stock') === (p.box.checked ? p.box.value : null),
        landmarks: !!results && !!chips && !!count,
        serverLandmarks: !!server?.getElementById('listing-results') && !!server?.getElementById('filters-applied') && !!server?.getElementById('listing-status'),
        chips: chips?.textContent, count: count?.textContent,
        stockChip: [...chips.querySelectorAll('.goen-filters__chip > span')].some(chip => chip.textContent.trim() === stockLabel) === p.box.checked,
        serverChips: server?.getElementById('filters-applied')?.textContent,
        serverCount: server?.getElementById('listing-status')?.textContent,
      };
    })()`);
    const report = (name, value, expected) => {
      const differences = Object.entries(expected).filter(([key, want]) => value[key] !== want)
        .map(([key, want]) => `${key} = ${JSON.stringify(value[key])}, want ${JSON.stringify(want)}`);
      const label = `filter feedback / ${locale} /${width} / ${name}`;
      if (differences.length) {
        const failure = `${label}: ${differences.join('; ')}; state = ${JSON.stringify(value)}`;
        failures.push(failure);
        console.error('FAIL ' + failure);
      } else console.log('PASS ' + label);
    };
    const recover = async (name) => {
      mode = 'real';
      await evaluate('htmx.config.defaultTimeout = filterFeedbackCheck.timeout');
      const index = await change();
      await finished(index);
      const result = await state(index);
      report(name + ' recovery', result, { ok: true, swapped: true, hidden: true, focused: true, stockURL: true, stockChip: true, landmarks: true, serverLandmarks: true });
      assert.equal(result.chips, result.serverChips, 'chips must match the successful server response');
      assert.equal(result.count, result.serverCount, 'count must match the successful server response');
      assert.ok(result.count.trim().length > 0, 'the successful server response must carry its result count');
    };
    for (const failure of ['transport rejection', 'timeout', 'HTTP failure']) {
      mode = failure === 'transport rejection' ? 'reject' : failure === 'timeout' ? 'timeout' : 'http';
      if (mode === 'timeout') await evaluate('htmx.config.defaultTimeout = 500');
      const index = await change();
      await finished(index);
      report(failure, await state(index), {
        hidden: false, visible: true, message: true, role: 'alert',
        response: mode === 'http', stale: true, changed: true, focused: true,
        ...(mode !== 'http' ? { urlUnchanged: true } : {}),
        ...(mode === 'timeout' ? { aborted: true } : {}),
      });
      await recover(failure);
    }
    mode = 'hold';
    await evaluate('htmx.config.defaultTimeout = 15000');
    const older = await change();
    await waitFor(() => held.size === 1, 'first replacement request did not reach fetch');
    const newer = await change();
    await finished(older);
    report('superseded request', await state(older), { hidden: true, response: false, aborted: true, stale: true, focused: true, urlUnchanged: true });
    assert.equal(await evaluate(`filterFeedbackCheck.finished.has(filterFeedbackCheck.requests[${newer}])`), false, 'replacement must still be pending');
    const replacementURL = await evaluate(`new URL(filterFeedbackCheck.requests[${newer}].request.action, location.origin).href`);
    const replacement = () => [...held].find(([, request]) => request.url === replacementURL);
    await waitFor(() => !!replacement(), 'replacement request did not reach fetch');
    const [replacementID] = replacement();
    await send('Fetch.failRequest', { requestId: replacementID, errorReason: 'ConnectionClosed' });
    held.delete(replacementID);
    await finished(newer);
    report('replacement failure', await state(newer), { hidden: false, visible: true, response: false, stale: true, focused: true });
    await recover('replacement failure');
    if (protocolFailure) throw protocolFailure;
  } finally {
    stopPaused?.();
    stopFailed?.();
    // Closing this target cancels held fetches even when htmx already aborted
    // their protocol identifiers; no request or cookie reaches the main audit.
    try {
      if (targetId) await connection.send('Target.closeTarget', { targetId });
    } finally {
      await connection.send('Target.disposeBrowserContext', { browserContextId });
    }
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const version = await waitForDebuggingEndpoint(debugging + '/json/version');
  const connection = await Connection.open(version.webSocketDebuggerUrl);
  try {
    for (const locale of ['zh-Hant', 'en']) {
      for (const width of [375, 1440]) await journey(connection, locale, width);
    }
  } finally {
    connection.close();
  }
  if (failures.length) throw new Error(`${failures.length} filter feedback assertions failed`);
}
