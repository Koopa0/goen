import assert from 'node:assert/strict';
import { setTimeout as delay } from 'node:timers/promises';

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
async function key(value) {
  const element = await command('GET', `/session/${session}/element/active`);
  const id = element['element-6066-11e4-a52e-4f735466cecf'];
  await command('POST', `/session/${session}/element/${id}/value`, {text: value});
}
async function pointer(point, message, navigating = false) {
  await command('POST', `/session/${session}/actions`, {actions: [{
    type: 'pointer', id: 'mouse', parameters: {pointerType: 'mouse'},
    actions: [{type: 'pointerMove', duration: 0, origin: 'viewport', ...point}, {type: 'pointerDown', button: 0}],
  }]});
  assert.equal(await evaluate('return checkedPopover.open'), true, message + ': pointerdown keeps the panel open');
  await command('POST', `/session/${session}/actions`, {actions: [{
    type: 'pointer', id: 'mouse', parameters: {pointerType: 'mouse'}, actions: [{type: 'pointerUp', button: 0}],
  }]});
  if (!navigating) assert.equal(await evaluate('return checkedPopover.open'), true, message + ': release keeps the panel open');
}
try {
  const started = await command('POST', '/session', {capabilities: {alwaysMatch: {browserName: 'safari'}}});
  session = started.sessionId;
  console.log('Safari capabilities ' + JSON.stringify(started.capabilities));
  await command('POST', `/session/${session}/window/rect`, {width: 1440, height: 1000});
  await command('POST', `/session/${session}/url`, {url: origin + '/c/audio'});
  for (const locale of ['zh-Hant', 'en']) for (const text of [100, 200]) {
    const at = `${locale}-${text}`;
    await command('POST', `/session/${session}/cookie`, {cookie: {name: 'goen_locale', value: locale, path: '/'}});
    await command('POST', `/session/${session}/url`, {url: origin + '/c/audio?brand=nimbus'});
    await until("return document.readyState === 'complete' && typeof htmx !== 'undefined'", at + ': listing did not load');
    await evaluate(`document.documentElement.style.fontSize = '${text * 16 / 100}px'; return true`);
    assert.equal(await evaluate('return document.documentElement.lang'), locale);
    assert.ok(await evaluate('return innerWidth >= 1024'), at + ': desktop layout required');
    assert.equal(await evaluate('return parseFloat(getComputedStyle(document.documentElement).fontSize)'), text * 16 / 100);
    await evaluate("window.checkedPopover = document.querySelector('.goen-filters__group:has(input[name=brand])'); checkedPopover.querySelector('summary').focus(); return true");
    await key('\uE007');
    assert.equal(await evaluate('return checkedPopover.open'), true, at + ': Enter opens the group');
    const checked = await evaluate("return checkedPopover.querySelector('input').checked");
    const labelPoint = await evaluate("const range = document.createRange(); range.selectNodeContents(checkedPopover.querySelector('.goen-filters__label')); const r = range.getBoundingClientRect(); return {x:Math.round(r.x + r.width / 2), y:Math.round(r.y + r.height / 2)}");
    await pointer(labelPoint, at + ': native label');
    assert.equal(await evaluate("return checkedPopover.querySelector('input').checked"), !checked, at + ': label toggles its checkbox');
    await evaluate("checkedPopover.querySelector('summary').focus(); return true");
    const padding = await evaluate("const panel = checkedPopover.querySelector('.goen-filters__options'), r = panel.getBoundingClientRect(), s = getComputedStyle(panel); return {x:Math.round(r.x + parseFloat(s.borderLeftWidth) + parseFloat(s.paddingLeft) / 2), y:Math.round(r.y + parseFloat(s.borderTopWidth) + parseFloat(s.paddingTop) / 2)}");
    await pointer(padding, at + ': panel padding');
    assert.equal(await evaluate("return checkedPopover.querySelector('input').checked"), !checked, at + ': padding leaves the checkbox unchanged');
    await evaluate("checkedPopover.querySelector('summary').focus(); return true");
    await key('\uE004');
    assert.equal(await evaluate('return checkedPopover.open && checkedPopover.contains(document.activeElement)'), true, at + ': Tab reaches an option');
    await evaluate("[...checkedPopover.querySelectorAll('input')].at(-1).focus(); return true");
    await key('\uE004');
    assert.equal(await evaluate('return !checkedPopover.open && !checkedPopover.contains(document.activeElement)'), true, at + ': Tab out closes the group');
    await evaluate("checkedPopover.querySelector('summary').focus(); return true");
    await key('\uE007'); await key('\uE004'); await key('\uE00C');
    assert.equal(await evaluate("return !checkedPopover.open && document.activeElement === checkedPopover.querySelector('summary')"), true, at + ': Escape restores summary focus');
    await evaluate("window.checkedPopover = [...document.querySelectorAll('.goen-langmenu')].find(e => e.getBoundingClientRect().width > 0); checkedPopover.querySelector('summary').focus(); return true");
    await key('\uE007');
    const language = locale === 'en' ? 'zh-Hant' : 'en';
    const buttonPoint = await evaluate(`const r = checkedPopover.querySelector('button[value="${language}"]').getBoundingClientRect(); return {x:Math.round(r.x+r.width/2), y:Math.round(r.y+r.height/2)}`);
    await pointer(buttonPoint, at + ': native language button', true);
    await until(`return document.readyState === 'complete' && document.documentElement.lang === '${language}'`, at + ': native language POST did not follow its 303');
    console.log('PASS Safari pointer, keyboard and locale POST ' + at);
  }
} finally {
  if (session) await command('DELETE', `/session/${session}`).catch(error => console.error(error.message));
}
