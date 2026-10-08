import assert from 'node:assert/strict';
import test from 'node:test';
import { parseEntry, screenshotVisitor } from './screenshot-entry.mjs';

const env = { PRODUCT_SLUG: 'a', COMPARE_SLUG: 'b', RETURN_FORM_ORDER: 'GO-1', CUST_TOKEN: 'secret' };

test('the second compare product is a fixture name', () => {
  const entry = parseEntry('/compare?p={PRODUCT_SLUG}&p={COMPARE_SLUG}@375', env);
  assert.equal(entry.path, '/compare?p=a&p=b');
  assert.equal(parseEntry('/p/{COMPARE_SLUG}@375', env).path, '/p/b');
});

test('a token is not a fixture name', () => {
  assert.throws(() => parseEntry('/p/{CUST_TOKEN}@375', env), /not a fixture name/);
});

test('member is a flag and is off by default', () => {
  assert.equal(parseEntry('/orders/{RETURN_FORM_ORDER}@375@member', env).member, true);
  assert.equal(parseEntry('/orders/{RETURN_FORM_ORDER}@375', env).member, false);
});

test('a guest placer cookie is the default for /orders and the customer session with member', () => {
  const guest = screenshotVisitor(parseEntry('/orders/GO-1/return@375', env));
  assert.deepEqual([guest.cookie, guest.token], ['goen_placed', 'PLACED_TOKEN']);
  const member = screenshotVisitor(parseEntry('/orders/GO-1/return@375@member', env));
  assert.deepEqual([member.cookie, member.token, member.prefix], ['goen_session', 'CUST_TOKEN', '/orders/GO-1/return']);
  assert.equal(screenshotVisitor(parseEntry('/p/a@375', env)), undefined);
});

test('member on a path outside /account keeps the query out of the guarded prefix', () => {
  assert.equal(screenshotVisitor(parseEntry('/orders/GO-1?x=1@375@member', env)).prefix, '/orders/GO-1');
  assert.equal(screenshotVisitor(parseEntry('/account/orders/GO-1@375@member', env)).prefix, '/account');
});
