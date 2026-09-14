import { randomSeed } from 'k6';
import http from 'k6/http';
import { check } from 'k6';
import { slugs, seed, baseURL } from '../lib/config.js';
import { getText } from '../lib/common.js';

const impairment = __ENV.LOAD_IMPAIRMENT || 'valkey-down';

export const options = {
  scenarios: {
    degraded_reads: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.LOAD_K6_ARRIVAL_RATE || 15),
      timeUnit: '1s',
      duration: __ENV.LOAD_K6_DURATION || '90s',
      preAllocatedVUs: 10,
      maxVUs: Number(__ENV.LOAD_K6_VUS_MAX || 30),
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.5'],
    checks: ['rate>0.3'],
  },
  tags: { profile: 'dependency-failure', impairment, seed: String(seed) },
};

export function setup() {
  randomSeed(seed);
  return { impairment };
}

export default function () {
  const res = http.get(`${baseURL}/p/${slugs.hot}`, { tags: { name: 'GET hot product during impairment' } });
  check(res, {
    'degraded storefront responds': (r) => r.status === 200 || r.status === 503 || r.status === 429,
  });
  if (Math.random() < 0.2) {
    getText('/readyz');
  }
}

export function handleSummary(data) {
  const successful = (data.metrics?.checks?.values?.passes || 0);
  const total = (data.metrics?.checks?.values?.passes || 0) + (data.metrics?.checks?.values?.fails || 0);
  return {
    stdout: JSON.stringify({
      profile: 'dependency-failure',
      impairment,
      seed,
      successful_checks: successful,
      total_checks: total,
      http_req_failed_rate: data.metrics?.http_req_failed?.values?.rate || 0,
      dropped_iterations: data.metrics?.dropped_iterations?.values?.count || 0,
    }, null, 2) + '\n',
    [`/load/evidence/dependency-failure-${seed}.json`]: JSON.stringify(data, null, 2),
  };
}
