import { randomSeed } from 'k6';
import http from 'k6/http';
import { check, sleep } from 'k6';
import { slugs, thresholds, seed, baseURL } from '../lib/config.js';
import { jar, getText, addToCart, checkoutFlow } from '../lib/common.js';

const staffToken = __ENV.LOAD_STAFF_TOKEN || '';

export const options = {
  scenarios: {
    storefront_closed_loop: {
      executor: 'constant-vus',
      vus: Number(__ENV.LOAD_K6_VUS_MAX || 10),
      duration: __ENV.LOAD_K6_DURATION || '2m',
    },
    staff_edits: {
      executor: 'constant-vus',
      vus: 2,
      duration: __ENV.LOAD_K6_DURATION || '2m',
      exec: 'staffWork',
    },
  },
  thresholds: { ...thresholds, checks: ['rate==1'] },
  tags: { profile: 'mixed-ops', seed: String(seed) },
};

export function setup() {
  randomSeed(seed);
  return { staffToken };
}

export default function () {
  const cookies = jar();
  getText('/');
  getText(`/p/${slugs.hot}`);
  addToCart(cookies, __ENV.LOAD_HOT_VARIANT_ID || 'a3330004-0000-4000-8000-000000000004', 1);
  checkoutFlow(cookies, `mixed-${seed}-${__VU}-${__ITER}@goen.invalid`);
  sleep(0.5);
}

export function staffWork() {
  if (!staffToken) {
    return;
  }
  const res = http.get(`${baseURL}/admin`, {
    cookies: { goen_session: staffToken },
    tags: { name: 'GET /admin' },
  });
  check(res, { 'staff dashboard reachable': (r) => r.status === 200 || r.status === 303 });
  http.get(`${baseURL}/admin/products`, {
    cookies: { goen_session: staffToken },
    tags: { name: 'GET /admin/products' },
  });
}

export function handleSummary(data) {
  return {
    [`/load/evidence/mixed-ops-${seed}.json`]: JSON.stringify(data, null, 2),
  };
}
