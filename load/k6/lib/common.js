import http from 'k6/http';
import { check } from 'k6';
import { baseURL } from './config.js';

export function jar() {
  return http.cookieJar();
}

export function getText(path, params = {}) {
  const res = http.get(`${baseURL}${path}`, { tags: { name: path }, ...params });
  check(res, { [`${path} status 200`]: (r) => r.status === 200 });
  return res;
}

export function addToCart(cookies, variantID, quantity = 1) {
  const res = http.post(`${baseURL}/cart/items`, { variant: variantID, quantity: String(quantity) }, {
    jar: cookies, redirects: 0, tags: { name: 'POST /cart/items' },
  });
  if (!check(res, { 'cart addition redirects': (r) => r.status === 303 })) {
    throw new Error(`cart precondition failed: HTTP ${res.status}`);
  }
  return res;
}

export function prepareCheckout(cookies, email) {
  const page = http.get(`${baseURL}/checkout`, { jar: cookies, redirects: 0, tags: { name: 'GET /checkout' } });
  if (!check(page, { 'checkout form available': (r) => r.status === 200 })) {
    throw new Error(`checkout precondition failed: HTTP ${page.status}`);
  }
  const form = page.html('form[action="/checkout"]');
  const value = (selector) => form.find(selector).first().attr('value') || '';
  const quote = value('input[name="checkout_quote"]');
  const attempt = value('input[name="idempotency"]');
  const shipping = value('input[name="shipping"][checked]');
  const invoice = value('input[name="invoice_type"][checked]');
  if (!check(null, { 'checkout required fields present': () => Boolean(quote && attempt && shipping && invoice) })) {
    throw new Error('checkout precondition failed: missing quote, attempt, shipping or invoice');
  }
  const payload = {
    email, name: 'Load Buyer', phone: '0912345678', postal_code: '110',
    city: '台北市', district: '信義區', street: '松高路 1 號',
    shipping, invoice_type: invoice, checkout_quote: quote, idempotency: attempt,
  };
  const body = Object.keys(payload).sort().map((key) => `${encodeURIComponent(key)}=${encodeURIComponent(payload[key])}`).join('&');
  return { body, idempotency: attempt, email, shipping };
}

export function submitCheckout(cookies, prepared) {
  const res = http.post(`${baseURL}/checkout`, prepared.body, {
    jar: cookies, redirects: 0,
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    tags: { name: 'POST /checkout' },
  });
  const location = res.headers.Location || '';
  const match = /^\/orders\/(GO-[0-9]{6}-[0-9]{6})\/pay$/.exec(location);
  return { ok: res.status === 303 && Boolean(match), status: res.status, order: match ? match[1] : '', location };
}

export function checkoutFlow(cookies, email) {
  const prepared = prepareCheckout(cookies, email);
  const result = submitCheckout(cookies, prepared);
  // A validation response is not successful checkout work for mixed-ops.
  if (!check(result, { 'checkout placed an order': (r) => r.ok })) {
    throw new Error(`checkout placement failed: HTTP ${result.status}, location ${result.location}`);
  }
  return { ...prepared, ...result };
}

export function randomSlug(slugs, hotWeight = 0.6) {
  if (Math.random() < hotWeight) return slugs.hot;
  return slugs.fillers[Math.floor(Math.random() * slugs.fillers.length)];
}
