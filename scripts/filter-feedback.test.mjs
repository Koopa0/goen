import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { runInNewContext } from 'node:vm';

const source = readFileSync(new URL('../assets/js/goen.js', import.meta.url), 'utf8');

class Surface {
  listeners = new Map();

  addEventListener(type, listener) {
    if (!this.listeners.has(type)) this.listeners.set(type, []);
    this.listeners.get(type).push(listener);
  }

  dispatch(type, detail = {}, target = this) {
    const event = { detail, target, defaultPrevented: false, preventDefault() { this.defaultPrevented = true; } };
    for (const listener of this.listeners.get(type) || []) listener(event);
    return event;
  }
}

class Element extends Surface {
  attributes = new Map();
  isConnected = true;
  dataset = {};

  getAttribute(name) { return this.attributes.get(name) ?? null; }
  setAttribute(name, value) { this.attributes.set(name, value); }
  removeAttribute(name) { this.attributes.delete(name); }
  hasAttribute(name) { return this.attributes.has(name); }
  matches() { return false; }
  querySelector() { return null; }
  querySelectorAll() { return []; }
}

class Form extends Element {
  constructor(filters = false) {
    super();
    this.filters = filters;
    this.button = new Element();
  }

  matches(selector) { return this.filters && selector === '.goen-filters'; }
  querySelectorAll() { return [this.button]; }
}

function page() {
  const document = new Surface();
  const window = new Surface();
  const note = { hidden: true };
  document.querySelector = (selector) => selector === '.goen-filters__error' ? note : null;
  document.querySelectorAll = () => [];
  document.getElementById = () => null;
  document.documentElement = new Element();
  runInNewContext(source, {
    document, window, Element, Node: Element, HTMLFormElement: Form,
    HTMLInputElement: class extends Element {}, HTMLButtonElement: class extends Element {},
    HTMLDetailsElement: class extends Element {},
  }, { filename: 'assets/js/goen.js' });
  const filters = new Form(true);
  const start = (form = filters, method = form.filters ? 'GET' : 'POST') => {
    const controller = new AbortController();
    const ctx = { sourceElement: form, request: { form, method, signal: controller.signal, abort: () => controller.abort() } };
    document.dispatch('htmx:config:request', { ctx });
    const event = document.dispatch('htmx:before:request', { ctx });
    return { ctx, event };
  };
  const finish = (ctx, ok) => {
    if (ok !== undefined) ctx.response = { raw: { ok } };
    document.dispatch('htmx:finally:request', { ctx });
  };
  return { document, window, note, filters, start, finish };
}

test('an explicitly replaceable read keeps its latest request and busy state', () => {
  const view = page();
  const form = new Form();
  form.setAttribute('hx-sync', 'this:replace');
  const first = view.start(form, 'GET');
  const second = view.start(form, 'GET');
  assert.equal(second.event.defaultPrevented, false, 'the newer read must reach fetch');
  assert.equal(first.ctx.request.signal.aborted, true);
  view.finish(first.ctx);
  assert.equal(form.getAttribute('aria-busy'), 'true', 'old cleanup cannot clear the pending replacement');
  assert.equal(form.button.getAttribute('aria-disabled'), 'true');
  view.finish(second.ctx, true);
  assert.equal(form.hasAttribute('data-request-pending'), false);
  assert.equal(form.getAttribute('aria-busy'), null);
  assert.equal(form.button.getAttribute('aria-disabled'), null);
});

test('a third replaceable read aborts the second after the first finishes', () => {
  const view = page();
  const form = new Form();
  form.setAttribute('hx-sync', 'this:replace');
  const first = view.start(form, 'GET').ctx;
  const second = view.start(form, 'GET').ctx;
  view.finish(first);
  const third = view.start(form, 'GET');
  assert.equal(third.event.defaultPrevented, false);
  assert.equal(second.request.signal.aborted, true);
  view.finish(third.ctx, true);
  const late = view.document.dispatch('htmx:after:request', { ctx: second });
  assert.equal(late.defaultPrevented, true, 'a superseded read cannot apply its late response');
  view.finish(second);
  assert.equal(form.getAttribute('aria-busy'), null);
});

test('replacement policy never exempts writes or an ordinary read', () => {
  for (const method of ['GET', 'POST', 'PUT', 'PATCH', 'DELETE']) {
    const view = page();
    const form = new Form();
    if (method !== 'GET') form.setAttribute('hx-sync', 'this:replace');
    view.start(form, method);
    assert.equal(view.start(form, method).event.defaultPrevented, true, `${method} retains duplicate prevention`);
  }
});

test('latest transport rejection reveals the existing filter error', () => {
  const view = page();
  const { ctx, event } = view.start();
  assert.equal(event.defaultPrevented, false, 'filter requests remain replaceable');
  view.finish(ctx);
  assert.equal(view.note.hidden, false, 'a missing response must reveal feedback');
});

test('latest timeout reveals the existing filter error', () => {
  const view = page();
  const { ctx } = view.start();
  ctx.request.abort();
  view.finish(ctx);
  assert.equal(ctx.request.signal.aborted, true);
  assert.equal(view.note.hidden, false, 'a timed-out request is not a superseded request');
});

test('HTTP failure remains visible and success clears it', () => {
  const view = page();
  view.finish(view.start().ctx, false);
  assert.equal(view.note.hidden, false);
  view.finish(view.start().ctx, true);
  assert.equal(view.note.hidden, true);
});

test('a superseded request stays quiet while its replacement is pending', () => {
  const view = page();
  const older = view.start().ctx;
  const newer = view.start().ctx;
  older.request.abort();
  view.finish(older);
  assert.equal(view.note.hidden, true);
  view.finish(newer);
  assert.equal(view.note.hidden, false, 'the replacement failure must be reported');
});

test('stale completions cannot show or clear the latest feedback', () => {
  for (const staleOK of [true, false, undefined]) {
    const view = page();
    const older = view.start().ctx;
    const newer = view.start().ctx;
    view.finish(older, staleOK);
    assert.equal(view.note.hidden, true, `stale ${staleOK} while replacement pending`);
    view.finish(newer);
    assert.equal(view.note.hidden, false);
    view.finish(older, true);
    assert.equal(view.note.hidden, false, 'an old success cannot clear a new failure');
  }
});

test('completed latest request cannot be overwritten by an older completion', () => {
  const view = page();
  const older = view.start().ctx;
  const newer = view.start().ctx;
  view.finish(newer, true);
  view.finish(older, false);
  assert.equal(view.note.hidden, true, 'an old HTTP failure cannot replace successful feedback');
});

test('duplicate writes and disconnected sources remain refused', () => {
  const view = page();
  const form = new Form();
  const first = view.start(form);
  assert.equal(first.event.defaultPrevented, false);
  assert.equal(form.getAttribute('aria-busy'), 'true');
  assert.equal(form.button.getAttribute('aria-disabled'), 'true');
  assert.equal(view.start(form).event.defaultPrevented, true);
  view.finish(first.ctx, true);
  form.isConnected = false;
  assert.equal(view.start(form).event.defaultPrevented, true);
});

test('reset and pageshow restore the original request attributes', () => {
  for (const finishWith of ['finally', 'reset', 'pageshow']) {
    for (const original of [null, 'false', 'true']) {
      const view = page();
      const form = new Form();
      if (original !== null) {
        form.setAttribute('aria-busy', original);
        form.button.setAttribute('aria-disabled', original);
      }
      const { ctx } = view.start(form);
      if (finishWith === 'finally') view.finish(ctx, true);
      if (finishWith === 'reset') view.document.dispatch('reset', {}, form);
      if (finishWith === 'pageshow') view.window.dispatch('pageshow');
      assert.equal(form.getAttribute('aria-busy'), original, `${finishWith} restores aria-busy`);
      assert.equal(form.button.getAttribute('aria-disabled'), original, `${finishWith} restores aria-disabled`);
      assert.equal(form.hasAttribute('data-request-pending'), false);
    }
  }
});

test('three filter changes abort both superseded requests and cancel the late second response', () => {
  const view = page();
  const first = view.start().ctx;
  const second = view.start().ctx;
  view.finish(first);
  const third = view.start().ctx;
  assert.equal(second.request.signal.aborted, true, 'the third change must abort the second after the first finishes');
  assert.equal(first.request.signal.aborted, true, 'the second change must abort the first');
  assert.equal(third.request.signal.aborted, false);
  assert.equal(view.document.dispatch('htmx:after:request', { ctx: second }).defaultPrevented, true,
    'a late second response must not swap or push history');
  assert.equal(view.document.dispatch('htmx:after:request', { ctx: third }).defaultPrevented, false,
    'the third response must remain accepted');
});

test('a failed filter response cannot push its canonical URL and a successful recovery can', () => {
  const view = page();
  const headers = new Headers({ 'HX-Push-Url': '/c/audio?in_stock=1' });
  const failed = view.start().ctx;
  failed.response = { status: 500, raw: { ok: false, headers } };
  assert.equal(view.document.dispatch('htmx:before:history:update', {
    sourceElement: view.filters, response: failed.response, history: { type: 'push', path: headers.get('HX-Push-Url') },
  }).defaultPrevented, true, 'a 500 carrying HX-Push-Url must keep the accepted URL');
  view.finish(failed);
  const recovery = view.start().ctx;
  recovery.response = { status: 200, raw: { ok: true, headers } };
  assert.equal(view.document.dispatch('htmx:after:request', { ctx: recovery }).defaultPrevented, false);
  assert.equal(view.document.dispatch('htmx:before:history:update', {
    sourceElement: view.filters, response: recovery.response, history: { type: 'push', path: headers.get('HX-Push-Url') },
  }).defaultPrevented, false, 'a 200 recovery must be allowed to update history');
  view.finish(recovery);
  assert.equal(view.note.hidden, true);
});

test('only filter requests disable queued view transitions', () => {
  const view = page();
  assert.equal(view.start().ctx.transition, false, 'filter content must swap without a queued transition');
  assert.equal(view.start(new Form()).ctx.transition, undefined);
});

test('unrelated forms retain their response and history handling', () => {
  const view = page();
  const form = new Form();
  const { ctx } = view.start(form);
  assert.equal(view.document.dispatch('htmx:after:request', { ctx }).defaultPrevented, false);
  assert.equal(view.document.dispatch('htmx:before:history:update', {
    sourceElement: form, response: { status: 422 }, history: { type: 'push', path: '/cart' },
  }).defaultPrevented, false);
});
