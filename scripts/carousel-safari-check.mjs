import assert from 'node:assert/strict';
import { setTimeout as delay } from 'node:timers/promises';

// Only the end state is asserted: Safari's frames are not read, so a timing
// flake cannot fail the job.
const [origin, driver] = process.argv.slice(2);
let session;
async function command(method, path, data) {
  const response = await fetch(driver + path, {
    method, headers: {'Content-Type': 'application/json'},
    ...(data === undefined ? {} : {body: JSON.stringify(data)}),
    signal: AbortSignal.timeout(15000),
  });
  const {value} = await response.json();
  assert.ok(response.ok && !value?.error, JSON.stringify(value));
  return value;
}
const evaluate = script => command('POST', `/session/${session}/execute/sync`, {script, args: []});
async function until(script, message) {
  const deadline = performance.now() + 10000;
  while (performance.now() < deadline) {
    if (await evaluate(script)) return;
    await delay(50);
  }
  throw new Error(message);
}
const settled = (index, message) => until(`
  const track = document.querySelector('.goen-hero__track'), shown = document.querySelectorAll('[data-shown]');
  return document.querySelector('.goen-hero__tabs a[aria-current="true"]')?.getAttribute('href') === '#hero-${index + 1}' &&
    Math.abs(track.scrollLeft - ${index} * track.clientWidth) <= 1 && !document.querySelector('[data-leaving]') &&
    shown.length === 1 && shown[0].id === 'hero-${index + 1}'`, message);
try {
  const started = await command('POST', '/session', {capabilities: {alwaysMatch: {browserName: 'safari'}}});
  session = started.sessionId;
  console.log('Safari capabilities ' + JSON.stringify(started.capabilities));
  await command('POST', `/session/${session}/window/rect`, {width: 1440, height: 1000});
  await command('POST', `/session/${session}/url`, {url: origin + '/carousel?lang=en&photos=1'});
  await until("return document.readyState === 'complete' && document.querySelector('.goen-hero__nav')?.hidden === false", 'carousel did not load');
  await until('return [...document.images].every(image => image.complete)', 'the slides\' photographs did not load');
  await evaluate(`
    window.slideRuns = []; window.pageErrors = [];
    addEventListener('error', event => pageErrors.push(String(event.message)));
    addEventListener('unhandledrejection', event => pageErrors.push(String(event.reason)));
    const start = document.startViewTransition.bind(document);
    document.startViewTransition = options => { slideRuns.push([...(options?.types ?? [])]); return start(options); };
    return true`);
  await settled(0, 'the first slide is not the one shown');
  // A photograph to a photograph, a photograph to words, and the way back.
  for (const index of [1, 2, 0]) {
    await evaluate(`document.querySelectorAll('.goen-hero__tabs a')[${index}].click(); return true`);
    await settled(index, `tab ${index + 1} did not leave its slide shown`);
    console.log(`PASS Safari chose slide ${index + 1}`);
  }
  assert.deepEqual(await evaluate('return slideRuns'), [['slide'], ['slide'], ['slide']], 'each chosen slide is a view transition of type slide');
  assert.deepEqual(await evaluate('return pageErrors'), [], 'page errors');
} finally {
  if (session) await command('DELETE', `/session/${session}`).catch(error => console.error(error.message));
}
