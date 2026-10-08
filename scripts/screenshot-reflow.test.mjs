import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import { screenshotReflow } from './screenshot-reflow.mjs';

function page({ bodyWidth = 320, documentWidth = 573, maxX = 0, x = 0, y = 91, fault = false, drifting = false } = {}) {
  let position = { x, y };
  let probing = false;
  const calls = [];
  const window = {
    innerWidth: 320, innerHeight: 812, devicePixelRatio: 1,
    get scrollX() {
      if (probing && fault) throw new Error('sample failed');
      if (probing && drifting) position.x++;
      return position.x;
    },
    get scrollY() { return position.y; },
    scrollTo(options) {
      calls.push(options);
      probing = calls.length === 1;
      position = { x: Math.min(options.left, maxX), y: options.top };
    },
  };
  const document = {
    body: { scrollWidth: bodyWidth },
    documentElement: { scrollWidth: documentWidth, clientWidth: 320 },
  };
  const context = {
    window, document, getComputedStyle: () => ({ fontSize: '32px' }),
    requestAnimationFrame: (fn) => queueMicrotask(fn), setTimeout, clearTimeout,
  };
  return {
    calls, position: () => position,
    measure: () => vm.runInNewContext(`(${screenshotReflow.toString()})()`, context),
  };
}

test('a scrolling descendant does not become reported window scrolling', async () => {
  const browser = page();
  const got = await browser.measure();
  assert.equal(got.bodyScrollWidth, 320);
  assert.equal(got.scrollX, 0);
  assert.equal(got.viewportWidth, 320);
  assert.equal(got.innerWidth, 320);
  assert.equal(got.viewportHeight, 812);
  assert.equal(got.rootFontSize, '32px');
  assert.equal(got.devicePixelRatio, 1);
  assert.equal(got.settled, true);
  assert.equal(got.restored, true);
});

test('actual window scrolling is sampled and both prior offsets are restored', async () => {
  const browser = page({ bodyWidth: 360, maxX: 40, x: 7, y: 123 });
  const got = await browser.measure();
  assert.equal(got.bodyScrollWidth, 360);
  assert.equal(got.scrollX, 40);
  assert.equal(got.scrollY, 123);
  assert.equal(got.initialScrollX, 7);
  assert.equal(got.initialScrollY, 123);
  assert.equal(got.restoredScrollX, 7);
  assert.equal(got.restoredScrollY, 123);
  assert.equal(got.restored, true);
  assert.equal(browser.calls.length, 2);
  assert.equal(browser.calls[0].left, 573);
  assert.equal(browser.calls[0].top, 123);
  assert.equal(browser.calls[0].behavior, 'instant');
  assert.deepEqual(browser.position(), { x: 7, y: 123 });
});

test('sample failure still restores the prior scroll offsets', async () => {
  const browser = page({ maxX: 40, x: 7, y: 123, fault: true });
  await assert.rejects(browser.measure(), /sample failed/);
  assert.deepEqual(browser.position(), { x: 7, y: 123 });
  assert.equal(browser.calls.length, 2);
});

test('a moving offset is bounded and reported as unsettled', async () => {
  const browser = page({ maxX: 40, drifting: true });
  const got = await browser.measure();
  assert.equal(got.settled, false);
  assert.equal(got.restored, true);
  assert.equal(browser.calls.length, 2);
});

test('the runner captures first and keeps diagnostics outside capture refusal', () => {
  const source = readFileSync(new URL('./screenshots.mjs', import.meta.url), 'utf8');
  const shot = source.slice(source.indexOf('async function shoot('), source.indexOf('\nconst list ='));
  assert.ok(shot.indexOf("send('Page.captureScreenshot'") < shot.indexOf('screenshotReflow.toString()'));
  assert.match(shot, /reflow = await evaluate\([\s\S]+screenshotReflow\.toString\(\)[\s\S]+, true\)/);
  assert.match(shot, /catch \(e\) \{\s+reflow = \{ error: e\.message \};\s+\}/);
  assert.match(shot, /if \(facts\.status >= 400\) result\.error/);
  assert.match(shot, /else if \(visitor && !screenshotRouteMatches\(entry\.path, facts\.finalPath, visitor\.prefix\)\) result\.error/);
});
