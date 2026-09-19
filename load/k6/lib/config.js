export const baseURL = __ENV.LOAD_BASE_URL || 'http://127.0.0.1:19700';
export const seed = Number(__ENV.LOAD_SEED || 7292);
export const fixtureID = __ENV.LOAD_FIXTURE_ID || 'load-catalog-v1';

export const slugs = {
  hot: 'load-hot-phone',
  flash: 'load-flash-sale',
  fillers: Array.from({ length: 8 }, (_, i) => `load-filler-${i + 1}`),
};

export const flashVariantSKU = 'LOAD-FLASH-001';

export const thresholds = {
  http_req_failed: ['rate<0.15'],
  http_req_duration: ['p(95)<5000', 'p(99)<10000'],
  dropped_iterations: ['count<1000'],
};
