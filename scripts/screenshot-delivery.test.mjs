import assert from 'node:assert/strict';
import test from 'node:test';
import vm from 'node:vm';
import { captureDeliveryRefusal, deliveryRefusalForm, deliveryRefusalPage, deliveryRefusalTarget, requireDeliveryRefusal } from './screenshot-delivery.mjs';

const origin = 'http://127.0.0.1:9700';
const path = '/admin/orders/GO-20261008-1234';
const target = path + '/delivery';
const order = 'GO-20261008-1234';
const before = { draft: [{ name: 'recipient', value: ' Proposed <recipient> ' }], summary: ['Saved recipient'] };
const response = { method: 'POST', path: target, status: 422 };
const page = (lang = 'en') => ({
  ...structuredClone(before), origin, finalPath: target, lang, visible: true, oppositeControl: false,
  lead: lang === 'en' ? 'Not saved' : '未儲存',
  alert: lang === 'en' ? 'Not saved Contains characters that are not allowed' : '未儲存 含有不允許的字元',
});

test('only the canonical layout order can opt into delivery submission', () => {
  assert.equal(deliveryRefusalTarget(path, order), target);
  for (const [requested, fixture] of [[path, undefined], [path, '../1234'], ['/admin/orders/another', order],
    [path + '?refused=1', order], [path + '/delivery', order], ['/orders/' + order, order]]) {
    assert.throws(() => deliveryRefusalTarget(requested, fixture), /layout PLACED_ORDER/);
  }
});

for (const lang of ['en', 'zh-Hant']) {
  test(`${lang}: actual localized refusal preserves the draft and saved summary`, () => {
    requireDeliveryRefusal(before, page(lang), response, target, lang, origin);
  });
  test(`${lang}: an absent, hidden or untranslated alert cannot become a successful capture`, () => {
    for (const delta of [{ visible: false }, { lead: '' }, { alert: '' }, { alert: 'Not saved' }, { lang: 'fr' }]) {
      assert.throws(() => requireDeliveryRefusal(before, { ...page(lang), ...delta }, response, target, lang, origin), /localized refusal/);
    }
  });
}

test('GET, successful POST, other order, other origin and redirects do not stand in for the refusal', () => {
  for (const delta of [{ method: 'GET' }, { status: 200 }, { status: 303 }, { status: 500 }, { path: path }]) {
    assert.throws(() => requireDeliveryRefusal(before, page(), { ...response, ...delta }, target, 'en', origin), /same-order POST 422/);
  }
  for (const delta of [{ finalPath: '/admin/orders/other/delivery' }, { origin: 'https://elsewhere.invalid' }]) {
    assert.throws(() => requireDeliveryRefusal(before, { ...page(), ...delta }, response, target, 'en', origin), /same-order POST 422/);
  }
});

test('refusal cannot introduce the absent control, discard the raw draft or change the saved summary', () => {
  for (const delta of [{ oppositeControl: true }, { draft: [{ name: 'recipient', value: 'Proposed <recipient>' }] },
    { summary: ['Proposed recipient'] }, { draft: [] }]) {
    assert.throws(() => requireDeliveryRefusal(before, { ...page(), ...delta }, response, target, 'en', origin), /saved summary, lost the draft/);
  }
});

function formPage({ status = 200, invalid = false, pickup = false, action = origin + target } = {}) {
  const names = ['recipient', 'phone', 'email', 'postal_code', 'city', 'district', 'street'];
  const fields = new Map(names.map((name) => [name, { value: 'saved ' + name }]));
  if (pickup) fields.set('pickup_chain', { value: 'seven_eleven' });
  const appended = [];
  let submitted = 0;
  const form = {
    method: 'post', action, elements: { namedItem: (name) => fields.get(name) || null },
    checkValidity: () => !invalid, append: (field) => appended.push(field), requestSubmit: () => { submitted++; },
  };
  const document = {
    querySelectorAll: (selector) => selector === 'form' ? [form] : [{ textContent: 'Saved recipient' }],
    createElement: () => ({}),
  };
  return {
    appended, submitted: () => submitted,
    run: (submit) => vm.runInNewContext(`(${deliveryRefusalForm.toString()})(${JSON.stringify(path)}, ${submit})`, {
      document, location: { origin, pathname: path, search: '' },
      performance: { getEntriesByType: () => [{ responseStatus: status }] },
    }),
  };
}

test('serialized browser recipe submits the real form with exactly one invalid opposite-half field', () => {
  const browser = formPage();
  const baseline = browser.run(false);
  assert.equal(browser.submitted(), 0);
  const submitted = browser.run(true);
  assert.equal(browser.submitted(), 1);
  assert.deepEqual(browser.appended, [{ type: 'hidden', name: 'pickup_store_code', value: '\x01' }]);
  assert.equal(submitted.draft.find((f) => f.name === 'recipient').value, ' Proposed <recipient> ');
  assert.equal(JSON.stringify(submitted), JSON.stringify(baseline));
});

test('the browser recipe refuses the wrong baseline, pickup destination, invalid form or different action before submission', () => {
  for (const options of [{ status: 422 }, { status: 0 }, { pickup: true }, { invalid: true }, { action: origin + path }]) {
    const browser = formPage(options);
    assert.throws(() => browser.run(true));
    assert.equal(browser.submitted(), 0);
    assert.equal(browser.appended.length, 0);
  }
});

test('the serialized page reader requires a rendered role alert rather than text hidden elsewhere', () => {
  for (const visible of [true, false]) {
    const alert = { textContent: page().alert, getBoundingClientRect: () => ({ width: visible ? 300 : 0, height: 40 }) };
    const lead = { textContent: 'Not saved', closest: () => alert };
    const form = { action: origin + target, elements: { namedItem: () => ({ value: ' Proposed <recipient> ' }) } };
    const document = {
      documentElement: { lang: 'en' },
      querySelector: (selector) => selector === '.goen-notice__lead' ? lead : null,
      querySelectorAll: (selector) => selector === 'form' ? [form] : [{ textContent: 'Saved recipient' }],
    };
    const got = vm.runInNewContext(`(${deliveryRefusalPage.toString()})()`, {
      document, location: { origin, href: origin + target, pathname: target, search: '' },
      getComputedStyle: () => ({ visibility: 'visible' }),
    });
    assert.equal(got.visible, visible);
    assert.equal(got.oppositeControl, false);
    assert.equal(got.lead, 'Not saved');
    assert.equal(got.draft.length, 7);
  }
});

test('canonical CDP recipe binds the actual POST response and cleans up its observer', async () => {
  const ws = new EventTarget();
  let listeners = 0;
  const add = ws.addEventListener.bind(ws), remove = ws.removeEventListener.bind(ws);
  ws.addEventListener = (...args) => { listeners++; add(...args); };
  ws.removeEventListener = (...args) => { listeners--; remove(...args); };
  const message = (method, params) => ws.dispatchEvent(new MessageEvent('message', { data: JSON.stringify({ method, params }) }));
  let calls = 0;
  const evaluate = async () => {
    if (++calls === 1) return before;
    if (calls === 2) {
      message('Network.requestWillBeSent', { type: 'Fetch', requestId: 'irrelevant', request: { method: 'POST', url: origin + target } });
      message('Network.responseReceived', { type: 'Fetch', requestId: 'irrelevant', response: { url: origin + target, status: 422 } });
      message('Network.requestWillBeSent', { type: 'Document', requestId: 'actual', request: { method: 'POST', url: origin + target } });
      message('Page.loadEventFired', {});
      message('Network.responseReceived', { type: 'Document', requestId: 'actual', response: { url: origin + target, status: 422 } });
      return before;
    }
    return page();
  };
  const got = await captureDeliveryRefusal({ entry: { path, lang: 'en' }, placedOrder: order, origin, ws, evaluate });
  assert.deepEqual(got, { state: 'delivery-refused', ...response, lead: page().lead, alert: page().alert });
  assert.equal(calls, 3);
  assert.equal(listeners, 0);
});

test('a failed submission evaluation removes the native observer', async () => {
  const ws = new EventTarget();
  let listeners = 0, calls = 0;
  ws.addEventListener = () => { listeners++; };
  ws.removeEventListener = () => { listeners--; };
  await assert.rejects(captureDeliveryRefusal({ entry: { path, lang: 'en' }, placedOrder: order, origin, ws,
    evaluate: async () => { if (++calls === 1) return before; throw new Error('navigation failed'); } }), /navigation failed/);
  assert.equal(listeners, 0);
});
