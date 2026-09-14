import { randomSeed } from 'k6';
import { slugs, thresholds, seed, baseURL } from '../lib/config.js';
import { getText, randomSlug } from '../lib/common.js';

export const options = {
  scenarios: {
    browse_arrival: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.LOAD_K6_ARRIVAL_RATE || 20),
      timeUnit: '1s',
      duration: __ENV.LOAD_K6_DURATION || '2m',
      preAllocatedVUs: 10,
      maxVUs: Number(__ENV.LOAD_K6_VUS_MAX || 40),
    },
  },
  thresholds,
  tags: { profile: 'browse', seed: String(seed) },
};

export function setup() {
  randomSeed(seed);
  const mode = __ENV.LOAD_CACHE_MODE || 'warm';
  if (mode === 'cold') {
    // First pass warms nothing; iterations themselves measure cold reads.
    return { mode };
  }
  if (mode === 'disabled') {
    // Application cache is not implemented yet (#330); record the requested mode.
    return { mode };
  }
  getText('/');
  getText(`/p/${slugs.hot}`);
  getText(`/search?q=load`);
  return { mode };
}

export default function () {
  const slug = randomSlug(slugs, 0.7);
  getText('/');
  getText(`/search?q=load+${__VU}`);
  getText(`/p/${slug}`);
  getText('/deals');
  getText(`/c/load-phones`);
}

export function handleSummary(data) {
  return {
    stdout: textSummary(data, { indent: ' ', enableColors: false }),
    [`/load/evidence/browse-${seed}.json`]: JSON.stringify(data, null, 2),
  };
}

function textSummary(data, opts) {
  const metrics = data.metrics || {};
  const lines = [
    `profile=browse base=${baseURL} seed=${seed}`,
    `http_reqs=${metrics.http_reqs?.values?.count || 0}`,
    `http_req_failed_rate=${metrics.http_req_failed?.values?.rate || 0}`,
    `http_req_duration_p95=${metrics.http_req_duration?.values?.['p(95)'] || 0}`,
    `http_req_duration_p99=${metrics.http_req_duration?.values?.['p(99)'] || 0}`,
    `dropped_iterations=${metrics.dropped_iterations?.values?.count || 0}`,
    `vus_max=${metrics.vus_max?.values?.max || 0}`,
  ];
  return lines.join('\n') + '\n';
}
