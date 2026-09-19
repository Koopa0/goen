import http from 'k6/http';
import { check } from 'k6';
import { baseURL } from './config.js';

export function jar() {
  return http.cookieJar();
}

export function getText(path, params = {}) {
  const res = http.get(`${baseURL}${path}`, {
    tags: { name: path },
    ...params,
  });
  check(res, { [`${path} status 200`]: (r) => r.status === 200 });
  return res;
}

export function extractInput(html, name) {
  const re = new RegExp(`name="${name}" value="([^"]*)"`, 'i');
  const match = html.match(re);
  return match ? match[1] : '';
}

export function extractCheckedRadio(html, name) {
  const re = new RegExp(`name="${name}" value="([^"]*)" checked`, 'i');
  const match = html.match(re);
  return match ? match[1] : '';
}

export function addToCart(jar, variantID, quantity = 1) {
  const res = http.post(
    `${baseURL}/cart/items`,
    { variant: variantID, quantity: String(quantity) },
    { jar, tags: { name: 'POST /cart/items' } },
  );
  check(res, { 'add to cart redirects or ok': (r) => r.status === 200 || r.status === 303 });
  return res;
}

export function checkoutFlow(jar, email, idempotencyKey) {
  const page = http.get(`${baseURL}/checkout`, { jar, tags: { name: 'GET /checkout' } });
  if (page.status !== 200) {
    return { ok: false, status: page.status };
  }
  const body = page.body;
  const quote = extractInput(body, 'checkout_quote');
  const attempt = extractInput(body, 'idempotency') || idempotencyKey;
  const shipping = extractCheckedRadio(body, 'shipping');
  if (!quote || !attempt || !shipping) {
    return { ok: false, status: page.status, reason: 'missing checkout fields' };
  }
  const payload = {
    email,
    name: 'Load Buyer',
    phone: '0912345678',
    postal_code: '110',
    city: '台北市',
    district: '信義區',
    street: '松高路 1 號',
    shipping,
    checkout_quote: quote,
    idempotency: attempt,
  };
  const res = http.post(`${baseURL}/checkout`, payload, {
    jar,
    tags: { name: 'POST /checkout' },
  });
  const ok = res.status === 303 || res.status === 422;
  return { ok, status: res.status, idempotency: attempt };
}

export function randomSlug(slugs, hotWeight = 0.6) {
  if (Math.random() < hotWeight) {
    return slugs.hot;
  }
  const fillers = slugs.fillers;
  return fillers[Math.floor(Math.random() * fillers.length)];
}
