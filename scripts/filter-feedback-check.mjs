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
    let protocolFailure;
    const intercept = async ({ params }) => {
      const { requestId, request, networkId } = params;
      assert.equal(new URL(request.url).origin, origin);
      assert.equal(new URL(request.url).pathname, '/c/audio');
      assert.equal(request.headers['HX-Request'] || request.headers['hx-request'], 'true');
      if (mode === 'reject') await send('Fetch.failRequest', { requestId, errorReason: 'ConnectionClosed' });
      else if (mode === 'http') {
        const canonical = new URL(request.url);
        for (const [name, value] of [...canonical.searchParams]) {
          if (value === '') canonical.searchParams.delete(name);
        }
        await send('Fetch.fulfillRequest', {
          requestId, responseCode: 500, body: '',
          responseHeaders: [{ name: 'HX-Push-Url', value: canonical.pathname + canonical.search }],
        });
      }
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
      const result = await state(index);
      if (mode === 'http') assert.ok(result.pushURL, 'the controlled 500 must carry the handler canonical history header');
      report(failure, result, {
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

async function searchSortJourney(connection, locale, width) {
  const { browserContextId } = await connection.send('Target.createBrowserContext', { disposeOnDetach: true });
  let targetId;
  try {
    ({ targetId } = await connection.send('Target.createTarget', { url: 'about:blank', browserContextId }));
    const { sessionId } = await connection.send('Target.attachToTarget', { targetId, flatten: true });
    const send = (method, params) => connection.send(method, params, sessionId);
    const evaluate = async (expression) => {
      const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
      if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
      return result.result.value;
    };
    await send('Page.enable');
    await send('Network.enable');
    await send('Network.setCacheDisabled', { cacheDisabled: true });
    await send('Network.setCookie', { name: 'goen_locale', value: locale, url: origin + '/' });
    await send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false });
    for (const changes of [2, 3]) {
      await send('Page.navigate', { url: origin + '/search?q=pixelight' });
      const deadline = Date.now() + 15000;
      while (!await evaluate(`document.readyState === 'complete' && !!document.querySelector('.goen-search-sort')?._htmx?.initialized`)) {
        if (Date.now() >= deadline) throw new Error('search sort did not initialize');
        await delay(20);
      }
      assert.equal(await evaluate('document.documentElement.lang'), locale);
      const result = await evaluate(`(async () => {
        const form = document.querySelector('.goen-search-sort');
        const select = form.querySelector('select[name=sort]');
        const before = document.getElementById('search-results');
        if (!select || !before) throw new Error('search sort landmarks missing');
        const originalFetch = window.fetch;
        const links = root => [...root.querySelectorAll('#search-results a.goen-tile')].map(a => a.getAttribute('href'));
        const reference = async sort => {
          const response = await originalFetch('/search?q=pixelight&sort=' + sort);
          if (!response.ok) throw new Error('sort reference returned ' + response.status);
          return links(new DOMParser().parseFromString(await response.text(), 'text/html'));
        };
        const ascending = await reference('price_asc');
        const descending = await reference('price_desc');
        if (ascending.length < 2 || JSON.stringify(ascending) === JSON.stringify(descending)) {
          throw new Error('search fixture must distinguish price ordering');
        }
        const contexts = [], fetched = [], finished = new Set();
        let staleApplied = false;
        const belongs = event => event.detail?.ctx?.request?.form === form;
        const started = event => { if (belongs(event)) contexts.push(event.detail.ctx); };
        const ended = event => { if (belongs(event)) finished.add(event.detail.ctx); };
        const applied = event => {
          if (belongs(event) && event.detail.ctx !== contexts[${changes - 1}] && !event.defaultPrevented) staleApplied = true;
        };
        document.addEventListener('htmx:before:request', started);
        document.addEventListener('htmx:finally:request', ended);
        document.addEventListener('htmx:after:request', applied);
        // Hold response delivery, including abort rejection, until the next
        // request owns the queue. Network responses still come from the server.
        window.fetch = (...args) => {
          let release;
          const gate = new Promise(resolve => { release = resolve; });
          const outcome = originalFetch(...args).then(value => ({ value }), error => ({ error }));
          const signal = args[1].signal;
          fetched.push({ ctx: contexts.at(-1), signal, release });
          return gate.then(async () => {
            const response = await outcome;
            if (signal.aborted) throw new DOMException('Superseded sort', 'AbortError');
            if (response.error) throw response.error;
            return response.value;
          });
        };
        const waitFor = async (predicate, why) => {
          const deadline = Date.now() + 15000;
          while (!predicate()) {
            if (Date.now() >= deadline) throw new Error(why);
            await new Promise(resolve => setTimeout(resolve, 10));
          }
        };
        const change = async value => {
          const index = contexts.length;
          select.value = value;
          select.dispatchEvent(new Event('change', { bubbles: true }));
          await waitFor(() => contexts.length > index, 'sort request did not reach before-request');
          await new Promise(resolve => setTimeout(resolve, 0));
          return contexts[index];
        };
        try {
          const first = await change('price_asc');
          const second = await change('price_desc');
          if (fetched.length !== 2) return { fetchedCount: fetched.length, expectedFetches: ${changes} };
          fetched[0].release();
          await waitFor(() => finished.has(first), 'superseded first sort did not finish');
          if (${changes} === 3) await change('price_asc');
          if (fetched.length !== ${changes}) return { fetchedCount: fetched.length, expectedFetches: ${changes} };
          const pendingBeforeLatest = form.getAttribute('aria-busy') === 'true';
          const latest = contexts.at(-1);
          fetched.at(-1).release();
          await waitFor(() => finished.has(latest), 'latest sort did not finish');
          // Releasing B after C exposes any late swap or history update.
          if (${changes} === 3) {
            fetched[1].release();
            await waitFor(() => finished.has(second), 'superseded second sort did not finish');
          }
          const expected = ${changes} === 3 ? ascending : descending;
          const sort = ${changes} === 3 ? 'price_asc' : 'price_desc';
          const url = new URL(location.href);
          const actual = links(document);
          return {
            fetchedCount: fetched.length, expectedFetches: ${changes},
            supersededAborted: fetched.slice(0, -1).every(request => request.signal.aborted),
            staleApplied, pendingBeforeLatest,
            resultOrdering: JSON.stringify(actual) === JSON.stringify(expected),
            urlAndSelection: url.searchParams.get('q') === 'pixelight' && url.searchParams.get('sort') === sort && select.value === sort,
            replaced: document.getElementById('search-results') !== before,
            cleared: !form.hasAttribute('data-request-pending') && form.getAttribute('aria-busy') !== 'true' && !form.querySelector('[aria-disabled="true"]'),
            actual, expected, href: location.href,
          };
        } finally {
          for (const request of fetched) request.release();
          window.fetch = originalFetch;
          document.removeEventListener('htmx:before:request', started);
          document.removeEventListener('htmx:finally:request', ended);
          document.removeEventListener('htmx:after:request', applied);
        }
      })()`);
      const expected = { fetchedCount: changes, supersededAborted: true, staleApplied: false, pendingBeforeLatest: true, resultOrdering: true, urlAndSelection: true, replaced: true, cleared: true };
      const differences = Object.entries(expected).filter(([key, value]) => result[key] !== value)
        .map(([key, value]) => key + ' = ' + JSON.stringify(result[key]) + ', want ' + JSON.stringify(value));
      const label = `search sort / ${locale} / ${width} / ${changes} changes`;
      if (differences.length) {
        const failure = label + ': ' + differences.join('; ') + '; state = ' + JSON.stringify(result);
        failures.push(failure);
        console.error('FAIL ' + failure);
      } else console.log('PASS ' + label + ': ' + JSON.stringify(result));
    }
  } finally {
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
      for (const width of [375, 1440]) {
        await journey(connection, locale, width);
        await searchSortJourney(connection, locale, width);
      }
    }
  } finally {
    connection.close();
  }
  if (failures.length) throw new Error(`${failures.length} filter feedback assertions failed`);
}
