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
// One execute call reads one document: a new timeOrigin means another document answered, and an empty inline size on the same one means a script reset it.
// Before each 200 percent case the previous case's locale POST leaves its 303 target on the other language, so a lang that is not the case's locale means the setup ran on that document.
const rootSnapshot = 'return {timeOrigin: performance.timeOrigin, url: location.href, lang: document.documentElement.lang, readyState: document.readyState, inline: document.documentElement.style.fontSize, computed: getComputedStyle(document.documentElement).fontSize}';
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
// A press: the pointer down and up on one point.
async function press(point) {
  await command('POST', `/session/${session}/actions`, {actions: [{
    type: 'pointer', id: 'mouse', parameters: {pointerType: 'mouse'},
    actions: [{type: 'pointerMove', duration: 0, origin: 'viewport', ...point}, {type: 'pointerDown', button: 0}, {type: 'pointerUp', button: 0}],
  }]});
}
// The shelf's filters stand beside the results at 100% text and fold into the
// modal drawer at 200%, where the page is too narrow in rem for their column.
// A pointer on an option's words ticks it either way; the drawer opens from
// its button by the keyboard and gives focus back on Escape.
try {
  const started = await command('POST', '/session', {capabilities: {alwaysMatch: {browserName: 'safari'}}});
  session = started.sessionId;
  console.log('Safari capabilities ' + JSON.stringify(started.capabilities));
  await command('POST', `/session/${session}/window/rect`, {width: 1440, height: 1000});
  await command('POST', `/session/${session}/url`, {url: origin + '/c/audio'});
  for (const locale of ['zh-Hant', 'en']) for (const text of [100, 200]) {
    const at = `${locale}-${text}`;
    // Native locale POSTs set an HttpOnly cookie, which document.cookie cannot replace.
    await command('DELETE', `/session/${session}/cookie/goen_locale`);
    await evaluate(`document.cookie = 'goen_locale=${locale}; path=/'; return document.cookie`);
    await command('POST', `/session/${session}/url`, {url: origin + '/c/audio?brand=nimbus'});
    await until("return document.readyState === 'complete' && typeof htmx !== 'undefined'", at + ': listing did not load');
    const prepared = await evaluate(`document.documentElement.style.fontSize = '${text * 16 / 100}px'; ${rootSnapshot}`);
    console.log('Prepared root text ' + at + ' ' + JSON.stringify(prepared));
    assert.equal(await evaluate('return document.documentElement.lang'), locale);
    assert.ok(await evaluate('return innerWidth >= 1024'), at + ': desktop layout required');
    const root = await evaluate(rootSnapshot);
    assert.equal(parseFloat(root.computed), text * 16 / 100, at + ': root text size was lost; prepared ' + JSON.stringify(prepared) + ', now ' + JSON.stringify(root));
    await evaluate("window.checkedOpener = document.querySelector('[data-filters-open]'); window.checkedBox = document.querySelector('.goen-filters input[name=brand][value=nimbus]'); return true");
    const drawer = await evaluate('return checkedOpener.getClientRects().length > 0');
    assert.equal(drawer, text === 200, at + ': the filters are a drawer only where the text is large');
    if (drawer) {
      await evaluate('checkedOpener.focus(); return true');
      await key('');
      await until("const d = document.getElementById('filters-drawer'); return d.open && d.contains(document.activeElement) && d.contains(checkedBox)", at + ': Enter does not open the drawer with focus and the filters in it');
      await delay(600);
    }
    const checked = await evaluate('return checkedBox.checked');
    const labelPoint = await evaluate("const e = checkedBox.closest('label').querySelector('.goen-filters__label'); e.scrollIntoView({block: 'nearest'}); const range = document.createRange(); range.selectNodeContents(e); const r = range.getBoundingClientRect(); return {x:Math.round(r.x + r.width / 2), y:Math.round(r.y + r.height / 2)}");
    await press(labelPoint);
    assert.equal(await evaluate('return checkedBox.checked'), !checked, at + ': pressing an option\'s words ticks it');
    await press(labelPoint);
    assert.equal(await evaluate('return checkedBox.checked'), checked, at + ': a second press restores the filter');
    if (drawer) {
      await key('');
      await until("return !document.getElementById('filters-drawer').open && document.activeElement === checkedOpener", at + ': Escape does not close the drawer and give focus back to its button');
    }
    // From 1024 the bar's language is one button that posts the other
    // language, outside any popover (the menu is the drawer's, below 1024, and
    // popover-focus-check holds it at 375), so it is pressed, not held open.
    const language = locale === 'en' ? 'zh-Hant' : 'en';
    const buttonPoint = await evaluate(`const b = document.querySelector('.goen-header__lang button[value="${language}"]'); if (!b) return null; const r = b.getBoundingClientRect(); return {x:Math.round(r.x+r.width/2), y:Math.round(r.y+r.height/2)}`);
    assert.ok(buttonPoint, at + ': the bar has no button for ' + language);
    await press(buttonPoint);
    await until(`return document.readyState === 'complete' && document.documentElement.lang === '${language}'`, at + ': native language POST did not follow its 303');
    console.log('PASS Safari pointer, keyboard and locale POST ' + at);
  }
} finally {
  if (session) await command('DELETE', `/session/${session}`).catch(error => console.error(error.message));
}
