import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import test from 'node:test';
import { setTimeout as delay } from 'node:timers/promises';
import { waitForDebuggingEndpoint } from './filter-feedback-check.mjs';

const version = { webSocketDebuggerUrl: 'ws://127.0.0.1:9222/devtools/browser/test' };

async function listen(server, port = 0) {
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(port, '127.0.0.1', resolve);
  });
  return `http://127.0.0.1:${server.address().port}/json/version`;
}

async function close(server) {
  server.closeAllConnections();
  await new Promise((resolve) => server.close(resolve));
}

test('a refused connection is retried until the existing debugging endpoint is ready', async (t) => {
  const server = createServer((req, res) => {
    assert.equal(req.url, '/json/version');
    res.end(JSON.stringify(version));
  });
  const endpoint = await listen(server);
  const port = server.address().port;
  await close(server);
  t.after(() => close(server));
  const startup = delay(60).then(() => listen(server, port));
  let ready;
  try {
    ready = await waitForDebuggingEndpoint(endpoint, { timeoutMs: 1500, retryMs: 10 });
  } finally {
    await startup;
  }
  assert.deepEqual(ready, version);
});

test('an endpoint that never listens fails within the deadline with the connection cause', async () => {
  const server = createServer();
  const endpoint = await listen(server);
  await close(server);
  const start = performance.now();
  await assert.rejects(waitForDebuggingEndpoint(endpoint, { timeoutMs: 150, retryMs: 10 }), (error) => {
    assert.match(error.message, /was not ready within 150ms/);
    assert.ok(error.message.includes(endpoint));
    assert.match(error.message, /chrome.log and .layout-chrome\/pid/);
    assert.equal(error.cause.cause.code, 'ECONNREFUSED');
    return true;
  });
  assert.ok(performance.now() - start < 1500, 'readiness failure must remain bounded');
});

test('a pending endpoint response cannot outlive the readiness deadline', async (t) => {
  let requests = 0;
  const server = createServer(() => { requests++; });
  const endpoint = await listen(server);
  t.after(() => close(server));
  const start = performance.now();
  await assert.rejects(waitForDebuggingEndpoint(endpoint, { timeoutMs: 150, retryMs: 10 }), (error) => {
    assert.match(error.message, /was not ready within 150ms/);
    assert.equal(error.cause.name, 'TimeoutError');
    return true;
  });
  assert.equal(requests, 1);
  assert.ok(performance.now() - start < 1500, 'a hung response must remain bounded');
});

test('temporary HTTP failures are retried before opening CDP', async (t) => {
  let requests = 0;
  const server = createServer((req, res) => {
    requests++;
    if (requests < 3) { res.writeHead(503); res.end(); }
    else res.end(JSON.stringify(version));
  });
  const endpoint = await listen(server);
  t.after(() => close(server));
  assert.deepEqual(await waitForDebuggingEndpoint(endpoint, { timeoutMs: 1500, retryMs: 10 }), version);
  assert.equal(requests, 3);
});

test('an HTTP response that never becomes ready retains its status cause', async (t) => {
  const server = createServer((req, res) => { res.writeHead(503); res.end(); });
  const endpoint = await listen(server);
  t.after(() => close(server));
  await assert.rejects(waitForDebuggingEndpoint(endpoint, { timeoutMs: 150, retryMs: 10 }), (error) => {
    assert.equal(error.cause.message, 'debugging endpoint answered HTTP 503');
    return true;
  });
});
