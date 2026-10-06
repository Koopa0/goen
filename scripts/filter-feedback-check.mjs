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
    box, form, note, requests: [], finished: new Set(), snapshots: new WeakMap(),
    message: note.textContent.trim(), timeout: htmx.config.defaultTimeout,
  };
  document.addEventListener('htmx:before:request', ({ detail }) => {
    if (detail.ctx?.request?.form === form) {
      probe.requests.push(detail.ctx);
      probe.snapshots.set(detail.ctx, probe.nextSnapshot);
    }
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
    let responseControl;
    let protocolFailure;
    const intercept = async ({ params }) => {
      const { requestId, request, networkId } = params;
      assert.equal(new URL(request.url).origin, origin);
      assert.equal(new URL(request.url).pathname, '/c/audio');
      assert.equal(request.headers['HX-Request'] || request.headers['hx-request'], 'true');
      if (mode === 'control') await send('Fetch.fulfillRequest', {
        requestId, responseCode: responseControl.status,
        responseHeaders: [{ name: 'Content-Type', value: 'text/html; charset=utf-8' }, ...responseControl.headers],
        body: Buffer.from(responseControl.body).toString('base64'),
      });
      else if (mode === 'reject') await send('Fetch.failRequest', { requestId, errorReason: 'ConnectionClosed' });
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
    const change = async (minPrice) => {
      if (protocolFailure) throw protocolFailure;
      const index = await evaluate(`(() => {
        const p = window.filterFeedbackCheck;
        const before = document.getElementById('listing-results');
        p.nextSnapshot = { before, markup: before.innerHTML, url: location.href, checked: p.box.checked };
        const index = p.requests.length;
        const minPrice = ${JSON.stringify(minPrice ?? null)};
        if (minPrice !== null) p.form.elements.namedItem('min_price').value = String(minPrice);
        p.box.closest('details').open = true; p.box.focus(); p.box.click();
        return index;
      })()`);
      await waitFor(() => evaluate(`filterFeedbackCheck.requests.length > ${index}`), 'filter request did not start');
      return index;
    };
    const finished = (index) => waitFor(() => evaluate(`filterFeedbackCheck.finished.has(filterFeedbackCheck.requests[${index}])`), 'filter request did not finish');
    const state = (index) => evaluate(`(() => {
      const p = filterFeedbackCheck, ctx = p.requests[${index}];
      const before = p.snapshots.get(ctx);
      const results = document.getElementById('listing-results');
      const chips = document.getElementById('filters-applied');
      const count = document.getElementById('listing-status');
      const server = ctx.text ? new DOMParser().parseFromString(ctx.text, 'text/html') : null;
      const stockLabel = p.box.closest('label').querySelector('.goen-filters__label').textContent.trim();
      const responseURL = ctx.response?.raw?.url;
      const pushURL = ctx.response?.raw?.headers.get('HX-Push-Url') ?? null;
      const replaceURL = ctx.response?.raw?.headers.get('HX-Replace-Url') ?? null;
      const directive = (pushURL === 'false' ? null : pushURL) || (replaceURL === 'false' ? null : replaceURL);
      let expectedURL;
      if ((pushURL || replaceURL) && !directive) expectedURL = before.url;
      else if (directive && directive !== 'true') expectedURL = new URL(directive, before.url).href;
      else if (responseURL) {
        const url = new URL(responseURL, before.url);
        url.hash = ctx.request.anchor || '';
        expectedURL = url.href;
      }
      return {
        hidden: p.note.hidden, visible: !p.note.hidden && getComputedStyle(p.note).display !== 'none' && p.note.getBoundingClientRect().height > 0,
        message: p.note.textContent.trim() === p.message, role: p.note.getAttribute('role'),
        response: !!ctx.response, ok: ctx.response?.raw?.ok === true, aborted: ctx.request.signal.aborted,
        stale: results === before.before && results.innerHTML === before.markup,
        swapped: results !== before.before, changed: p.box.checked !== before.checked,
        focused: document.activeElement === p.box, urlUnchanged: location.href === before.url,
        stockURL: new URL(location.href).searchParams.get('in_stock') === (p.box.checked ? p.box.value : null),
        urlMatchesResponse: !!expectedURL && location.href === expectedURL,
        actualURL: location.href, expectedURL, responseURL, pushURL, replaceURL, requestURL: ctx.request.action,
        landmarks: !!results && !!chips && !!count,
        serverLandmarks: !!server?.getElementById('listing-results') && !!server?.getElementById('filters-applied') && !!server?.getElementById('listing-status'),
        chips: chips?.textContent, count: count?.textContent,
        stockChip: [...chips.querySelectorAll('.goen-filters__chip > span')].some(chip => chip.textContent.trim() === stockLabel) === p.box.checked,
        serverChips: server?.getElementById('filters-applied')?.textContent,
        serverCount: server?.getElementById('listing-status')?.textContent,
        resultText: results?.textContent,
        serverResultText: server?.getElementById('listing-results')?.textContent,
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
      if (value.responseURL) {
        const { requestURL, responseURL, pushURL, replaceURL, expectedURL, actualURL } = value;
        console.log('URL evidence ' + label + ': ' + JSON.stringify({ requestURL, responseURL, pushURL, replaceURL, expectedURL, actualURL }));
      }
    };
    const recover = async (name) => {
      mode = 'real';
      await evaluate('htmx.config.defaultTimeout = filterFeedbackCheck.timeout');
      const index = await change();
      await finished(index);
      const result = await state(index);
      report(name + ' recovery', result, { ok: true, swapped: true, hidden: true, focused: true, stockURL: true, stockChip: true, urlMatchesResponse: true, landmarks: true, serverLandmarks: true });
      assert.equal(result.chips, result.serverChips, 'chips must match the successful server response');
      assert.equal(result.count, result.serverCount, 'count must match the successful server response');
      assert.equal(result.resultText, result.serverResultText, 'results must match the successful server response');
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
        urlUnchanged: true,
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

    const requestURL = (index) => evaluate(`new URL(filterFeedbackCheck.requests[${index}].request.action, location.origin).href`);
    const heldRequest = async (index) => {
      const url = await requestURL(index);
      return [...held].find(([, request]) => request.url === url);
    };
    const continueHeld = async (index) => {
      const request = await heldRequest(index);
      assert.ok(request, 'the controlled request must still be held');
      await send('Fetch.continueRequest', { requestId: request[0] });
      held.delete(request[0]);
    };
    const accepted = async (name, index) => {
      const result = await state(index);
      report(name, result, { ok: true, hidden: true, focused: true, stockURL: true, stockChip: true, urlMatchesResponse: true, landmarks: true, serverLandmarks: true });
      report(name + ' response agreement', {
        results: result.resultText === result.serverResultText,
        chips: result.chips === result.serverChips,
        count: result.count === result.serverCount,
      }, { results: true, chips: true, count: true });
    };

    mode = 'real';
    await load();
    mode = 'hold';
    await evaluate('htmx.config.defaultTimeout = 60000');
    const first = await change(1);
    await waitFor(async () => !!await heldRequest(first), 'first of three requests did not reach fetch');
    const second = await change(2);
    await waitFor(async () => !!await heldRequest(second), 'second of three requests did not reach fetch');
    await finished(first);
    const third = await change(3);
    await waitFor(async () => !!await heldRequest(third), 'third of three requests did not reach fetch');
    report('third replacement owns the queue', await state(second), { aborted: true });
    await continueHeld(third);
    await finished(third);
    await accepted('third replacement success', third);
    // On the defect, the second request is still live. Let its real response
    // arrive last so the same journey records the stale swap as well.
    if (await heldRequest(second)) await continueHeld(second);
    await finished(second);
    await accepted('third replacement survives older completion', third);

    mode = 'real';
    await load();
    await evaluate(`(() => {
      const p = filterFeedbackCheck;
      p.delayNextBody = true;
      p.staleTriggers = 0;
      document.addEventListener('filter-feedback-old-completed', () => { p.staleTriggers++; });
      document.addEventListener('htmx:before:request', ({ detail: { ctx } }) => {
        if (ctx.request?.form !== p.form || !p.delayNextBody) return;
        p.delayNextBody = false;
        const fetch = window.fetch.bind(window);
        ctx.fetch = async (...args) => {
          const response = await fetch(...args);
          const text = await response.text();
          const headers = new Headers(response.headers);
          headers.set('HX-Trigger', 'filter-feedback-old-completed');
          let release;
          const body = new Promise(resolve => { release = () => resolve(text); });
          p.delayedBody = { ctx, release };
          // The real response bytes finish late despite cancellation. The
          // probe header makes any stale response action independently visible.
          return new Proxy(response, { get(target, key) {
            if (key === 'text') return () => body;
            if (key === 'headers') return headers;
            const value = Reflect.get(target, key, target);
            return typeof value === 'function' ? value.bind(target) : value;
          } });
        };
      });
      htmx.config.defaultTimeout = 60000;
    })()`);
    const delayed = await change(4);
    await waitFor(() => evaluate('!!filterFeedbackCheck.delayedBody'), 'real response body did not reach its delay');
    mode = 'hold';
    const middle = await change(5);
    await waitFor(async () => !!await heldRequest(middle), 'middle replacement did not reach fetch');
    mode = 'real';
    const latest = await change(6);
    await finished(middle);
    await finished(latest);
    await accepted('latest response before delayed body', latest);
    await evaluate('filterFeedbackCheck.delayedBody.release()');
    await finished(delayed);
    report('delayed body belongs to an aborted request', await state(delayed), { aborted: true, response: true });
    report('delayed response cannot fire its old trigger', await evaluate('({ triggers: filterFeedbackCheck.staleTriggers })'), { triggers: 0 });
    await accepted('latest response survives delayed body', latest);

    mode = 'real';
    await load();
    await evaluate(`(() => {
      const p = filterFeedbackCheck;
      p.deferNextTransition = true;
      document.addEventListener('htmx:before:swap', ({ detail: { ctx, tasks } }) => {
        if (ctx.request?.form !== p.form) return;
        const transition = p.deferNextTransition;
        p.deferNextTransition = false;
        for (const task of tasks) task.swapSpec = { ...task.swapSpec, transition };
      });
      p.originalTransition = document.startViewTransition;
      let deferred = false;
      document.startViewTransition = task => {
        if (deferred) return p.originalTransition ? p.originalTransition.call(document, task) : { finished: Promise.resolve().then(task) };
        deferred = true;
        let resolve, reject;
        const finished = new Promise((yes, no) => { resolve = yes; reject = no; });
        p.releaseTransition = async () => {
          try { await task(); resolve(); } catch (error) { reject(error); }
        };
        return { finished };
      };
      htmx.config.defaultTimeout = 60000;
    })()`);
    const transition = await change(7);
    await waitFor(() => evaluate('!!filterFeedbackCheck.releaseTransition'), 'real response did not queue its transition');
    report('queued swap has not committed history', await state(transition), { response: true, stale: true, urlUnchanged: true });
    mode = 'http';
    const refusal = await change(8);
    await finished(refusal);
    report('replacement refusal keeps the successful page', await state(refusal), { response: true, stale: true, urlUnchanged: true, visible: true, message: true });
    await evaluate('filterFeedbackCheck.releaseTransition()');
    await finished(transition);
    report('superseded transition cannot commit after refusal', await state(transition), { aborted: true, stale: true, urlUnchanged: true, visible: true, message: true });
    await evaluate('document.startViewTransition = filterFeedbackCheck.originalTransition');
    await recover('superseded transition');

    mode = 'real';
    await load();
    await evaluate(`(() => {
      const p = filterFeedbackCheck;
      p.holdNextAnimation = true;
      document.addEventListener('htmx:before:swap', ({ detail: { ctx, tasks } }) => {
        if (ctx.request?.form !== p.form) return;
        const transition = p.holdNextAnimation;
        p.holdNextAnimation = false;
        for (const task of tasks) task.swapSpec = { ...task.swapSpec, transition };
      });
      p.originalTransition = document.startViewTransition;
      let heldAnimation = false;
      document.startViewTransition = task => {
        if (heldAnimation) return p.originalTransition ? p.originalTransition.call(document, task) : { finished: Promise.resolve().then(task) };
        heldAnimation = true;
        const update = Promise.resolve().then(task);
        const finished = update.then(() => new Promise(resolve => { p.releaseAnimation = resolve; }));
        return { finished };
      };
      htmx.config.defaultTimeout = 60000;
    })()`);
    const committed = await change(9);
    await waitFor(() => evaluate('!!filterFeedbackCheck.releaseAnimation'), 'real response did not commit before its held animation');
    await accepted('committed swap updates history before animation finishes', committed);
    mode = 'http';
    const refusedAfterCommit = await change(10);
    await finished(refusedAfterCommit);
    report('refusal preserves the committed swap during its animation', await state(refusedAfterCommit), { stale: true, urlUnchanged: true, visible: true, message: true });
    await evaluate('filterFeedbackCheck.releaseAnimation()');
    await finished(committed);
    const committedState = await state(committed);
    report('aborted animation retains committed response agreement', {
      urlMatchesResponse: committedState.urlMatchesResponse,
      results: committedState.resultText === committedState.serverResultText,
      chips: committedState.chips === committedState.serverChips,
      count: committedState.count === committedState.serverCount,
      visible: committedState.visible,
    }, { urlMatchesResponse: true, results: true, chips: true, count: true, visible: true });
    await evaluate('document.startViewTransition = filterFeedbackCheck.originalTransition');
    await recover('committed animation');

    mode = 'real';
    await load();
    await evaluate(`(() => {
      const p = filterFeedbackCheck;
      p.holdNextSettle = true;
      p.sortProcessed = false;
      p.originalTimeout = htmx.timeout;
      document.getElementById('sort').setAttribute('data-filter-feedback-settle', 'old');
      document.addEventListener('htmx:before:swap', ({ detail: { ctx, tasks } }) => {
        if (ctx.request?.form !== p.form) return;
        const hold = p.holdNextSettle;
        p.holdNextSettle = false;
        for (const task of tasks) {
          const target = typeof task.target === 'string' ? document.querySelector(task.target) : task.target;
          task.swapSpec = { ...task.swapSpec, transition: false,
            ...(hold && target?.id === 'listing-sort' ? { settle: 60001 } : {}) };
        }
      });
      document.addEventListener('htmx:after:process', ({ target }) => {
        if (p.settlingSort && (target === p.settlingSort || target.contains?.(p.settlingSort))) p.sortProcessed = true;
      });
      htmx.timeout = function(interval) {
        if (interval !== 60001) return p.originalTimeout.call(this, interval);
        p.settlingSort = document.getElementById('sort');
        return new Promise(resolve => { p.releaseSettle = resolve; });
      };
      htmx.config.defaultTimeout = 60000;
    })()`);
    const settling = await change(11);
    await waitFor(() => evaluate('!!filterFeedbackCheck.releaseSettle'), 'real content did not enter its controlled CSS settle');
    await accepted('committed swap updates history before CSS settle finishes', settling);
    report('CSS settle retains old attributes until restoration', await evaluate(`({
      oldAttribute: filterFeedbackCheck.settlingSort.getAttribute('data-filter-feedback-settle') === 'old',
      processed: filterFeedbackCheck.sortProcessed,
    })`), { oldAttribute: true, processed: false });
    mode = 'http';
    const refusedDuringSettle = await change(12);
    await finished(refusedDuringSettle);
    report('refusal preserves committed content during CSS settle', await state(refusedDuringSettle), { stale: true, urlUnchanged: true, visible: true, message: true });
    await evaluate('filterFeedbackCheck.releaseSettle()');
    await finished(settling);
    report('superseded committed CSS settle completes restoration and processing', await evaluate(`({
      connected: filterFeedbackCheck.settlingSort.isConnected,
      oldAttribute: filterFeedbackCheck.settlingSort.hasAttribute('data-filter-feedback-settle'),
      processed: filterFeedbackCheck.sortProcessed,
      settling: !!document.querySelector('.htmx-settling'),
    })`), { connected: true, oldAttribute: false, processed: true, settling: false });
    const settledState = await state(settling);
    report('superseded CSS settle retains committed response agreement', {
      urlMatchesResponse: settledState.urlMatchesResponse,
      results: settledState.resultText === settledState.serverResultText,
      chips: settledState.chips === settledState.serverChips,
      count: settledState.count === settledState.serverCount,
      visible: settledState.visible,
    }, { urlMatchesResponse: true, results: true, chips: true, count: true, visible: true });
    await evaluate('htmx.timeout = filterFeedbackCheck.originalTimeout');
    await recover('committed CSS settle');

    mode = 'real';
    await load();
    await evaluate(`(() => {
      const p = filterFeedbackCheck;
      p.swapDelays = [];
      p.originalTimeout = htmx.timeout;
      let delayed = 0;
      document.addEventListener('htmx:before:swap', ({ detail: { ctx, tasks } }) => {
        if (ctx.request?.form !== p.form) return;
        for (const task of tasks) task.swapSpec = { ...task.swapSpec, transition: false,
          ...(task.type === 'main' && delayed < 2 ? { swap: 10001 + delayed++ } : {}) };
      });
      htmx.timeout = function(interval) {
        if (interval !== 10001 && interval !== 10002) return p.originalTimeout.call(this, interval);
        return new Promise(resolve => { p.swapDelays.push({ interval, release: resolve }); });
      };
      htmx.config.defaultTimeout = 60000;
    })()`);
    const delayedSwapFirst = await change(13);
    await waitFor(() => evaluate('filterFeedbackCheck.swapDelays.length === 1'), 'first real swap did not enter its delay');
    const delayedSwapSecond = await change(14);
    await waitFor(() => evaluate('filterFeedbackCheck.swapDelays.length === 2'), 'replacement real swap did not enter its later delay');
    await evaluate('filterFeedbackCheck.swapDelays[0].release()');
    await finished(delayedSwapFirst);
    report('obsolete delayed swap preserves its replacement busy class', await evaluate(`({
      aborted: filterFeedbackCheck.requests[${delayedSwapFirst}].request.signal.aborted,
      busy: document.getElementById('listing-results').classList.contains('htmx-swapping'),
      pending: !filterFeedbackCheck.finished.has(filterFeedbackCheck.requests[${delayedSwapSecond}]),
    })`), { aborted: true, busy: true, pending: true });
    report('obsolete delayed swap cannot insert or publish history', await state(delayedSwapFirst), { stale: true, urlUnchanged: true });
    await evaluate('filterFeedbackCheck.swapDelays[1].release()');
    await finished(delayedSwapSecond);
    await accepted('replacement delayed swap commits its real response', delayedSwapSecond);
    report('replacement delayed swap releases its busy class', await evaluate(`({ busy: !!document.querySelector('.htmx-swapping') })`), { busy: false });
    await evaluate('htmx.timeout = filterFeedbackCheck.originalTimeout');
    await recover('replacement delayed swap');

    mode = 'real';
    await load();
    await evaluate(`(() => {
      const p = filterFeedbackCheck;
      p.holdNextSwap = true;
      p.originalTimeout = htmx.timeout;
      document.addEventListener('htmx:before:swap', ({ detail: { ctx, tasks } }) => {
        if (ctx.request?.form !== p.form) return;
        const hold = p.holdNextSwap;
        p.holdNextSwap = false;
        for (const task of tasks) task.swapSpec = { ...task.swapSpec, transition: false,
          ...(hold && task.type === 'main' ? { swap: 10003 } : {}) };
      });
      htmx.timeout = function(interval) {
        if (interval !== 10003) return p.originalTimeout.call(this, interval);
        return new Promise(resolve => { p.releaseSwap = resolve; });
      };
      htmx.config.defaultTimeout = 60000;
    })()`);
    const canceledSwap = await change(15);
    await waitFor(() => evaluate('!!filterFeedbackCheck.releaseSwap'), 'real swap did not enter its isolated delay');
    await evaluate(`filterFeedbackCheck.requests[${canceledSwap}].request.abort(); filterFeedbackCheck.releaseSwap()`);
    await finished(canceledSwap);
    report('isolated canceled delayed swap keeps its previous results and URL', await state(canceledSwap), { aborted: true, stale: true, urlUnchanged: true });
    report('isolated canceled delayed swap releases its busy class', await evaluate(`({ busy: !!document.querySelector('.htmx-swapping') })`), { busy: false });
    await evaluate('htmx.timeout = filterFeedbackCheck.originalTimeout');
    await recover('isolated canceled delayed swap');

    // These intercepted, isolated controls exercise the served vendor without
    // submitting a write to the shop or replacing its insertion methods.
    mode = 'real';
    await load();
    await evaluate(`(() => {
      const p = filterFeedbackCheck;
      p.form.removeAttribute('hx-get');
      p.form.setAttribute('hx-post', '/c/audio');
      p.form.setAttribute('method', 'post');
      p.form.setAttribute('hx-status:422', 'swap:outerHTML');
      p.form.removeAttribute('hx-select');
      p.form.removeAttribute('hx-select-oob');
    })()`);
    responseControl = { status: 422, headers: [], body: '<div id="listing-results"><form data-filter-feedback-refused><input name="retained" value="submitted value" aria-invalid="true"></form></div>' };
    mode = 'control';
    const refusedForm = await change(16);
    await finished(refusedForm);
    report('POST 422 retains its refused form body and field', await evaluate(`(() => {
      const p = filterFeedbackCheck, ctx = p.requests[${refusedForm}];
      const field = document.querySelector('[data-filter-feedback-refused] input');
      return { post: ctx.request.method === 'POST', status: ctx.response?.raw?.status,
        retained: field?.value, invalid: field?.getAttribute('aria-invalid'),
        urlUnchanged: location.href === p.snapshots.get(ctx).url };
    })()`), { post: true, status: 422, retained: 'submitted value', invalid: 'true', urlUnchanged: true });

    for (const action of ['false', 'push', 'replace']) {
      mode = 'real';
      await load();
      await evaluate(`(() => {
        const p = filterFeedbackCheck;
        p.historyPushes = 0; p.historyReplaces = 0;
        document.addEventListener('htmx:after:history:push', () => { p.historyPushes++; });
        document.addEventListener('htmx:after:history:replace', () => { p.historyReplaces++; });
      })()`);
      const path = '/c/audio?compatibility=' + action;
      responseControl = { status: 500, body: '', headers: action === 'false'
        ? [{ name: 'HX-Push-Url', value: 'false' }, { name: 'HX-Replace-Url', value: 'false' }]
        : [{ name: action === 'push' ? 'HX-Push-Url' : 'HX-Replace-Url', value: path }] };
      mode = 'control';
      const explicit = await change(17);
      await finished(explicit);
      report('explicit HX history ' + action + ' preserves its meaning', await evaluate(`(() => {
        const p = filterFeedbackCheck, ctx = p.requests[${explicit}];
        return { status: ctx.response?.raw?.status,
          url: location.href, pushes: p.historyPushes, replaces: p.historyReplaces };
      })()`), { status: 500, url: origin + (action === 'false' ? '/c/audio' : path),
        pushes: action === 'push' ? 1 : 0, replaces: action === 'replace' ? 1 : 0 });
    }

    mode = 'real';
    await load();
    report('manual swap without a request or signal inserts content', await evaluate(`(async () => {
      const p = filterFeedbackCheck, url = location.href;
      await htmx.swap({ sourceElement: p.form, target: document.getElementById('listing-results'),
        swap: 'innerHTML', text: '<p data-filter-feedback-manual>manual content</p>' });
      return { inserted: document.querySelector('[data-filter-feedback-manual]')?.textContent === 'manual content',
        urlUnchanged: location.href === url };
    })()`), { inserted: true, urlUnchanged: true });
    await recover('manual swap');
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
