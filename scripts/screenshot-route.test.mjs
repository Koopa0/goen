import assert from 'node:assert/strict';
import test from 'node:test';
import { screenshotRouteMatches } from './screenshot-route.mjs';

const order = 'GO-261009-000001';
const alias = `/account/orders/${order}`;
const canonical = `/orders/${order}`;

test('the signed-in order alias may reach only the same canonical order', () => {
  for (const requested of [alias, `${alias}?from=history`]) {
    assert.equal(screenshotRouteMatches(requested, canonical, '/account'), true);
  }
});

test('the order alias refuses another order or another order surface', () => {
  for (const finalPath of [
    '/orders/GO-261009-000002',
    `${canonical}/pay`,
    `${canonical}/return`,
    `${canonical}?paid=1`,
    `${canonical}/`,
  ]) {
    assert.equal(screenshotRouteMatches(alias, finalPath, '/account'), false, finalPath);
  }
});

test('the order alias refuses sign-in and public redirects', () => {
  for (const finalPath of ['/signin', '/signin?next=' + alias, '/', '/about']) {
    assert.equal(screenshotRouteMatches(alias, finalPath, '/account'), false, finalPath);
  }
});

test('other requested paths cannot use the order alias exception', () => {
  for (const requested of [
    '/account',
    '/account/orders',
    '/account/orders/',
    `${alias}/`,
    `${alias}/return`,
    '/about',
  ]) {
    assert.equal(screenshotRouteMatches(requested, canonical, '/account'), false, requested);
  }
  assert.equal(screenshotRouteMatches('/cart', canonical, '/cart'), false);
});

test('existing visitor path boundaries remain accepted', () => {
  for (const [requested, finalPath, prefix] of [
    ['/account', '/account', '/account'],
    ['/account/wishlist', '/account/wishlist?added=1', '/account'],
    ['/admin/orders', '/admin/orders', '/admin'],
    ['/cart', '/cart?added=1', '/cart'],
    [canonical, canonical, '/orders'],
  ]) {
    assert.equal(screenshotRouteMatches(requested, finalPath, prefix), true);
  }
  assert.equal(screenshotRouteMatches('/admin', '/administrator', '/admin'), false);
  assert.equal(screenshotRouteMatches('/account', '/accounting', '/account'), false);
});
