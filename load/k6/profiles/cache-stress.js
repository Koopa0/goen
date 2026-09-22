import { randomSeed } from 'k6';
import http from 'k6/http';
import { check } from 'k6';
import { slugs, thresholds, seed, baseURL } from '../lib/config.js';
import { getText } from '../lib/common.js';

export const options = {
  scenarios: {
    promotion_spike: {
      executor: 'ramping-arrival-rate',
      startRate: 5,
      timeUnit: '1s',
      preAllocatedVUs: 20,
      maxVUs: Number(__ENV.LOAD_K6_VUS_MAX || 60),
      stages: [
        { duration: '30s', target: 10 },
        { duration: '30s', target: 40 },
        { duration: '30s', target: 10 },
      ],
    },
  },
  thresholds: {
    ...thresholds,
    http_req_failed: ['rate<0.35'],
  },
  tags: { profile: 'cache-stress', seed: String(seed) },
};

const bogusSlugs = Array.from({ length: 50 }, (_, i) => `missing-${seed}-${i}`);

export function setup() {
  randomSeed(seed);
  return {};
}

export default function () {
  const pick = Math.random();
  if (pick < 0.5) {
    getText(`/p/${slugs.hot}`);
    getText(`/s/load-promo-${(__VU % 3) + 1}`);
    return;
  }
  if (pick < 0.8) {
    const slug = bogusSlugs[Math.floor(Math.random() * bogusSlugs.length)];
    const res = http.get(`${baseURL}/p/${slug}`, { tags: { name: 'GET bogus slug' } });
    check(res, { 'bogus slug handled': (r) => r.status === 404 || r.status === 200 });
    return;
  }
  getText('/deals');
  getText(`/search?q=flash+${__ITER}`);
}

export function handleSummary(data) {
  return {
    stdout: JSON.stringify({
      profile: 'cache-stress',
      seed,
      dropped_iterations: data.metrics?.dropped_iterations?.values?.count || 0,
      http_req_failed_rate: data.metrics?.http_req_failed?.values?.rate || 0,
      p95: data.metrics?.http_req_duration?.values?.['p(95)'] || 0,
      p99: data.metrics?.http_req_duration?.values?.['p(99)'] || 0,
    }, null, 2) + '\n',
    [`/load/evidence/cache-stress-${seed}.json`]: JSON.stringify(data, null, 2),
  };
}
