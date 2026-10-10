import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { runInNewContext } from 'node:vm';

const source = readFileSync(new URL('../assets/js/goen.js', import.meta.url), 'utf8');
const origin = 'https://shop.test';

class Surface {
  listeners = new Map();

  addEventListener(type, listener) {
    if (!this.listeners.has(type)) this.listeners.set(type, []);
    this.listeners.get(type).push(listener);
  }

  dispatch(type, fields = {}) {
    const event = { defaultPrevented: false, preventDefault() { this.defaultPrevented = true; }, ...fields };
    for (const listener of this.listeners.get(type) || []) listener(event);
    return event;
  }
}

class Element extends Surface {
  attributes = new Map();

  getAttribute(name) { return this.attributes.get(name) ?? null; }
  setAttribute(name, value) { this.attributes.set(name, String(value)); }
  removeAttribute(name) { this.attributes.delete(name); }
  hasAttribute(name) { return this.attributes.has(name); }
  matches() { return false; }
  closest() { return null; }
  querySelector() { return null; }
  querySelectorAll() { return []; }
}

class Link extends Element {
  constructor(href, attributes = {}) {
    super();
    this.setAttribute('href', href);
    for (const [name, value] of Object.entries(attributes)) this.setAttribute(name, value);
  }

  get href() { return new URL(this.getAttribute('href'), `${origin}/c/tech`).href; }
  get target() { return this.getAttribute('target') ?? ''; }
  closest(selector) { return selector === 'a[href]' ? this : null; }
}

class Form extends Element {
  constructor(attributes = {}) {
    super();
    for (const [name, value] of Object.entries(attributes)) this.setAttribute(name, value);
  }
}

function page() {
  const document = new Surface();
  const window = new Surface();
  const navigation = new Surface();
  const timers = [];
  document.documentElement = new Element();
  document.querySelector = () => null;
  document.querySelectorAll = () => [];
  document.getElementById = () => null;
  window.navigation = navigation;
  runInNewContext(source, {
    document, window, URL, Element, Node: Element, HTMLFormElement: Form,
    HTMLInputElement: class extends Element {}, HTMLButtonElement: class extends Element {},
    HTMLDetailsElement: class extends Element {},
    location: new URL(`${origin}/c/tech`),
    setTimeout: (fn) => timers.push(fn),
  }, { filename: 'assets/js/goen.js' });
  const click = (target, fields = {}) => document.dispatch('click', { target, button: 0, ...fields });
  const submit = (form, submitter = null) => document.dispatch('submit', { target: form, submitter });
  // A navigation the browser starts after the press, with the signal it aborts on a stop.
  const navigate = () => {
    const controller = new AbortController();
    navigation.dispatch('navigate', { signal: controller.signal });
    return () => {
      controller.abort();
      for (const run of timers.splice(0)) run();
    };
  };
  const navigating = () => document.documentElement.hasAttribute('data-navigating');
  return { document, window, click, submit, navigate, navigating };
}

const pending = (link) => link.hasAttribute('data-navigation-pending');

test('a plain press on a link to another page marks the link and the page', () => {
  const view = page();
  const chip = new Link('/c/audio');
  view.click(chip);
  assert.equal(view.navigating(), true);
  assert.equal(pending(chip), true);
});

test('a press that does not leave this page in this tab marks nothing', () => {
  const cases = {
    'a modified click opens a tab': [new Link('/c/audio'), { metaKey: true }],
    'a middle click opens a tab': [new Link('/c/audio'), { button: 1 }],
    'another target': [new Link('/c/audio', { target: '_blank' }), {}],
    'a download': [new Link('/static/a.pdf', { download: '' }), {}],
    'another origin': [new Link('https://example.com/'), {}],
    'a fragment of this page': [new Link('#reviews'), {}],
    'a press something else took over': [new Link('/c/audio'), { defaultPrevented: true }],
  };
  for (const [name, [link, fields]] of Object.entries(cases)) {
    const view = page();
    view.click(link, fields);
    assert.equal(view.navigating(), false, name);
    assert.equal(pending(link), false, name);
  }
});

test('a second press moves the mark to the newer link', () => {
  const view = page();
  const first = new Link('/c/phones');
  const second = new Link('/c/laptops');
  view.click(first);
  view.click(second);
  assert.equal(pending(first), false);
  assert.equal(pending(second), true);
});

test('returning to a kept page clears the marks', () => {
  const view = page();
  const chip = new Link('/c/audio');
  view.click(chip);
  view.window.dispatch('pageshow', { persisted: true });
  assert.equal(view.navigating(), false);
  assert.equal(pending(chip), false);
});

test('a stopped load clears the marks', () => {
  const view = page();
  const chip = new Link('/c/audio');
  view.click(chip);
  const stop = view.navigate();
  stop();
  assert.equal(view.navigating(), false);
  assert.equal(pending(chip), false);
});

test('the navigation a second press replaces does not clear the newer marks', () => {
  const view = page();
  view.click(new Link('/c/phones'));
  const replaced = view.navigate();
  const second = new Link('/c/laptops');
  view.click(second);
  view.navigate();
  replaced();
  assert.equal(view.navigating(), true);
  assert.equal(pending(second), true);
});

test('a form that loads a page marks the page; one that does not, nothing', () => {
  const loads = {
    'a search': [new Form({ action: '/search' }), null],
    'a post to this page': [new Form({ method: 'post' }), null],
  };
  for (const [name, [form, submitter]] of Object.entries(loads)) {
    const view = page();
    view.submit(form, submitter);
    assert.equal(view.navigating(), true, name);
  }
  const stays = {
    'a dialog form': [new Form({ method: 'dialog' }), null],
    'another target': [new Form({ action: '/search', target: '_blank' }), null],
    'another origin': [new Form({ action: 'https://checkout.example.com/pay', method: 'post' }), null],
  };
  for (const [name, [form, submitter]] of Object.entries(stays)) {
    const view = page();
    view.submit(form, submitter);
    assert.equal(view.navigating(), false, name);
  }
  const view = page();
  const event = view.document.dispatch('submit', { target: new Form({ action: '/search' }), submitter: null, defaultPrevented: true });
  assert.equal(event.defaultPrevented, true);
  assert.equal(view.navigating(), false, 'a submit something else took over');
});
