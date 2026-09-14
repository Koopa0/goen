import { randomSeed } from 'k6';
import { SharedArray } from 'k6/data';
import exec from 'k6/execution';
import { thresholds, seed, baseURL, flashVariantSKU } from '../lib/config.js';
import { jar, addToCart, checkoutFlow } from '../lib/common.js';

const variantID = __ENV.LOAD_FLASH_VARIANT_ID || 'a3330006-0000-4000-8000-000000000006';

export const options = {
  scenarios: {
    competing_buyers: {
      executor: 'per-vu-iterations',
      vus: Number(__ENV.LOAD_K6_VUS_MAX || 12),
      iterations: 1,
      maxDuration: '3m',
    },
    repeat_submit: {
      executor: 'shared-iterations',
      vus: 3,
      iterations: 6,
      startTime: '10s',
      maxDuration: '3m',
    },
  },
  thresholds: {
    ...thresholds,
    checks: ['rate>0.5'],
  },
  tags: { profile: 'stock-contention', seed: String(seed) },
};

const buyers = new SharedArray('buyers', function () {
  return Array.from({ length: 20 }, (_, i) => `load-buyer-${seed}-${i}@goen.invalid`);
});

export function setup() {
  randomSeed(seed);
  return { variantID, sku: flashVariantSKU };
}

export default function () {
  const cookies = jar();
  const email = buyers[exec.vu.idInTest % buyers.length];
  addToCart(cookies, variantID, 1);
  const key = __ENV.LOAD_IDEMPOTENCY_KEY || '';
  const result = checkoutFlow(cookies, email, key);
  if (!result.ok) {
    console.warn(`checkout vu=${__VU} iter=${__ITER} status=${result.status} reason=${result.reason || ''}`);
  }
}

export function handleSummary(data) {
  return {
    stdout: JSON.stringify({
      profile: 'stock-contention',
      seed,
      variantID,
      http_req_failed_rate: data.metrics?.http_req_failed?.values?.rate || 0,
      checks_rate: data.metrics?.checks?.values?.rate || 0,
    }, null, 2) + '\n',
    [`/load/evidence/stock-contention-${seed}.json`]: JSON.stringify(data, null, 2),
  };
}
