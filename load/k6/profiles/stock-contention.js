import http from 'k6/http';
import crypto from 'k6/crypto';
import exec from 'k6/execution';
import { check } from 'k6';
import { Counter } from 'k6/metrics';
import { thresholds, seed, baseURL } from '../lib/config.js';
import { addToCart, prepareCheckout, submitCheckout } from '../lib/common.js';

const variantID = 'a3330006-0000-4000-8000-000000000006';
const runID = __ENV.LOAD_RUN_ID || '';
const buyers = Number(__ENV.LOAD_K6_VUS_MAX || 12);
const replays = 6;
const placements = new Counter('competing_placements');
const attempts = new Counter('competing_attempts');
const replaySuccess = new Counter('replay_successes');

export const options = {
  scenarios: {
    competing_buyers: { exec: 'competingBuyer', executor: 'shared-iterations', vus: buyers, iterations: buyers, maxDuration: '3m' },
    repeat_submit: { exec: 'repeatSubmit', executor: 'shared-iterations', vus: 3, iterations: replays, startTime: '10s', maxDuration: '3m' },
  },
  thresholds: {
    ...thresholds, checks: ['rate==1'],
    competing_placements: ['count>0'], competing_attempts: [`count==${buyers}`], replay_successes: [`count==${replays}`],
  },
  tags: { profile: 'stock-contention', seed: String(seed), run_id: runID },
};

function require(ok, message) {
  if (!check(null, { [message]: () => Boolean(ok) })) exec.test.abort(message);
}

function snapshot(cookies) {
  const values = cookies.cookiesForURL(`${baseURL}/checkout`);
  require(Object.keys(values).length > 0, 'checkout cookies exist');
  Object.keys(values).forEach((name) => require(values[name].length === 1, 'cookie snapshot is unambiguous'));
  return Object.keys(values).sort().map((name) => ({ name, value: values[name][0] }));
}

function restore(values) {
  const cookies = new http.CookieJar();
  values.forEach((cookie) => cookies.set(baseURL, cookie.name, cookie.value, { path: '/' }));
  return cookies;
}

function prepareBuyer(index) {
  const cookies = new http.CookieJar();
  addToCart(cookies, variantID, 1);
  const prepared = prepareCheckout(cookies, `load-${runID}-${index}@goen.invalid`);
  require(prepared.shipping === 'a33300f3-0000-4000-8000-0000000000f3', 'pinned home-delivery fixture selected');
  return { ...prepared, cookies: snapshot(cookies) };
}

function evidence(kind, prepared, result, extra = {}) {
  console.log(JSON.stringify({
    kind, run_id: runID, key: prepared.idempotency, email: prepared.email,
    order: result.order, variant_id: variantID, quantity: 1,
    total_cents: 107000, body_hash: crypto.sha256(prepared.body, 'hex'),
    cookie_hash: crypto.sha256(JSON.stringify(prepared.cookies), 'hex'), ...extra,
  }));
}

export function setup() {
  require(/^[A-Za-z0-9-]{8,80}$/.test(runID), 'unique LOAD_RUN_ID is required');
  require(Number.isInteger(buyers) && buyers >= 2 && buyers <= 40, 'buyers must be between 2 and 40');
  console.log(JSON.stringify({ kind: 'start', run_id: runID, started_at: new Date().toISOString(), buyers, replays }));
  // Prepare every cart before placement consumes the limited fixture stock.
  const contenders = Array.from({ length: buyers }, (_, index) => prepareBuyer(index));
  const anchor = prepareBuyer('replay');
  const result = submitCheckout(restore(anchor.cookies), anchor);
  require(result.ok, 'setup placed a new replay anchor order');
  evidence('anchor', anchor, result);
  return { anchor: { ...anchor, order: result.order }, contenders };
}

export function competingBuyer(data) {
  require(data && data.contenders, 'competing-buyer setup data exists');
  const prepared = data.contenders[exec.scenario.iterationInTest];
  require(prepared, 'competing-buyer identity exists');
  attempts.add(1);
  const result = submitCheckout(restore(prepared.cookies), prepared);
  require(result.ok || (result.status === 303 && result.location === '/cart'), 'buyer placed an order or was refused unavailable stock');
  if (result.ok) placements.add(1);
  evidence(result.ok ? 'placement' : 'rejected', prepared, result);
}

export function repeatSubmit(data) {
  require(data && data.anchor && data.anchor.order, 'replay anchor exists');
  const prepared = data.anchor;
  const cookies = restore(prepared.cookies);
  require(JSON.stringify(snapshot(cookies)) === JSON.stringify(prepared.cookies), 'replay restores identical cookies');
  const result = submitCheckout(cookies, prepared);
  require(result.ok && result.order === prepared.order, 'replay returned the original order');
  replaySuccess.add(1);
  evidence('replay', prepared, result, { replay_index: exec.scenario.iterationInTest });
}

export function handleSummary(data) {
  return {
    stdout: JSON.stringify({ profile: 'stock-contention', run_id: runID, metrics: data.metrics }, null, 2) + '\n',
    [`/load/evidence/stock-contention-${runID}.json`]: JSON.stringify(data, null, 2),
  };
}
