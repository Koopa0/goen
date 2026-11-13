import assert from 'node:assert/strict';
import { reflowProbe } from './reflow-probe.mjs';

// Drives the compare bar TestCompareFormRowInBrowser renders: a listing whose
// boxes are fields of a GET form to /compare, and the row of ticked products
// goen.js adds to that form's bar. Every reading is of the live page.
const [origin, port] = process.argv.slice(2);
const debugging = `http://127.0.0.1:${port}`;
const response = await fetch(`${debugging}/json/new?about:blank`, { method: 'PUT' });
assert.equal(response.ok, true, 'Chrome must create the compare bar test page');
const target = await response.json();
const socket = new WebSocket(target.webSocketDebuggerUrl);
try {
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let id = 0;
  const pending = new Map();
  const listeners = new Map();
  socket.onmessage = ({ data }) => {
    const message = JSON.parse(data);
    if (message.method) {
      for (const listener of listeners.get(message.method) ?? []) listener(message.params);
      return;
    }
    const request = pending.get(message.id);
    if (!request) return;
    pending.delete(message.id);
    clearTimeout(request.timer);
    if (message.error) request.reject(new Error(JSON.stringify(message.error)));
    else request.resolve(message.result);
  };
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    const current = ++id;
    const timer = setTimeout(() => { pending.delete(current); reject(new Error(`${method} timed out: ${JSON.stringify(params)}`)); }, 20000);
    pending.set(current, { resolve, reject, timer });
    socket.send(JSON.stringify({ id: current, method, params }));
  });
  const nextEvent = (method, accept = () => true) => new Promise((resolve, reject) => {
    const set = listeners.get(method) ?? new Set();
    listeners.set(method, set);
    const timer = setTimeout(() => { set.delete(listener); reject(new Error(`${method} did not arrive`)); }, 20000);
    const listener = params => {
      if (!accept(params)) return;
      clearTimeout(timer); set.delete(listener); resolve(params);
    };
    set.add(listener);
  });
  const evaluate = async expression => {
    const got = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (got.exceptionDetails) throw new Error(JSON.stringify(got.exceptionDetails));
    return got.result.value;
  };
  const waitFor = async (expression, label) => {
    for (let attempt = 0; attempt < 100; attempt++) {
      if (await evaluate(expression)) return;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    throw new Error(`${label} did not settle`);
  };
  const settle = () => evaluate('document.fonts.ready.then(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))');
  const locale = async lang => {
    await send('Network.setCookie', { name: 'goen_locale', value: lang, url: origin, path: '/' });
  };
  const open = async (width, { text200 = false, path = '/c/audio' } = {}) => {
    await send('Emulation.setDeviceMetricsOverride', { width, height: 800, deviceScaleFactor: 1, mobile: width < 600 });
    const url = origin + path;
    await send('Page.navigate', { url });
    await waitFor(`location.href === ${JSON.stringify(url)} && document.readyState === 'complete' && !!document.getElementById('compare-pick')`, `${path} at ${width}px`);
    if (text200) await evaluate("document.documentElement.style.fontSize = '200%'");
    await settle();
  };
  const key = async (name, code, keyCode) => {
    await send('Input.dispatchKeyEvent', { type: 'keyDown', key: name, code, windowsVirtualKeyCode: keyCode, ...(name === 'Enter' ? { text: '\r' } : {}) });
    await send('Input.dispatchKeyEvent', { type: 'keyUp', key: name, code, windowsVirtualKeyCode: keyCode });
  };
  // A box is ticked the way a reader ticks it: a click on it, which fires change.
  const tick = slug => evaluate(`document.querySelector('#compare-pick input[name="p"][value=${JSON.stringify(slug)}]').click()`);
  // The row as a reader meets it: each entry's slug, name, photograph and remove button.
  const row = () => evaluate(`(() => {
    const row = document.querySelector('#compare-pick .goen-compare-pick__row');
    if (!row) return null;
    const box = el => { const r = el.getBoundingClientRect(); return { width: +r.width.toFixed(1), height: +r.height.toFixed(1) }; };
    return {
      tag: row.tagName, parent: row.parentElement.className, role: row.getAttribute('role'),
      items: [...row.children].map(li => {
        const img = li.querySelector('img');
        const button = li.querySelector('button');
        return {
          tag: li.tagName, slug: li.dataset.slug,
          name: li.querySelector('.goen-compare-pick__name')?.textContent,
          photo: img && { src: img.getAttribute('src'), alt: img.getAttribute('alt'), ...box(img) },
          remove: button && { type: button.type, label: button.getAttribute('aria-label'), text: button.textContent.trim(), ...box(button) },
        };
      }),
    };
  })()`);
  const ticked = () => evaluate(`[...document.querySelectorAll('#compare-pick input[name="p"]:checked')].map(box => box.value)`);
  const focused = () => evaluate(`(() => {
    const el = document.activeElement;
    if (!el || el === document.body) return 'body';
    if (el.matches('.goen-compare-pick__remove')) return 'remove:' + el.closest('li').dataset.slug;
    if (el.matches('#compare-pick [type="submit"]')) return 'submit';
    return el.tagName.toLowerCase() + '.' + el.className;
  })()`);
  const slugs = got => got?.items.map(item => item.slug) ?? [];

  await send('Page.enable');
  await send('DOM.enable');
  await send('Network.enable');

  const names = {
    'nimbus-speaker-s1': 'Nimbus Speaker S1', 'koto-over-ear-quiet': 'Koto Over-Ear Quiet',
    'nimbus-buds-pro': 'Nimbus Buds Pro', 'meridian-watch-s3': 'Meridian Watch S3 Titanium Edition with Sport Loop and Milanese Band',
    'pixelight-9-pro': 'Pixelight 9 Pro', 'orili-canvas-tote': 'Orili Canvas Tote',
  };
  const removeName = { 'zh-Hant': name => `移除「${name}」`, en: name => `Remove “${name}”` };

  for (const lang of ['zh-Hant', 'en']) {
    const label = `row ${lang}`;
    await locale(lang);
    await open(1440);
    assert.equal(await row(), null, `${label}: a row with nothing ticked`);

    // One box is not a comparison: the bar offers the submit, with no row.
    await tick('nimbus-speaker-s1');
    assert.equal(await row(), null, `${label}: a row for one ticked product`);

    // Two are: the row is a list in the bar, in the grid's order.
    await tick('koto-over-ear-quiet');
    let got = await row();
    assert.ok(got, `${label}: no row with two products ticked`);
    assert.equal(got.tag, 'UL', `${label}: the row is a ${got.tag}, not a list`);
    assert.equal(got.role, null, `${label}: the row carries role=${got.role}`);
    assert.match(got.parent, /\bgoen-compare-pick__go\b/, `${label}: the row is in ${got.parent}, not the bar`);
    assert.deepEqual(slugs(got), ['nimbus-speaker-s1', 'koto-over-ear-quiet'], `${label}: the row after two ticks`);
    for (const item of got.items) {
      assert.equal(item.tag, 'LI', `${label}: an entry is a ${item.tag}`);
      assert.equal(item.name, names[item.slug], `${label}: ${item.slug} is named ${JSON.stringify(item.name)}`);
      assert.ok(item.photo, `${label}: ${item.slug} has no photograph`);
      assert.match(item.photo.src, /-400\.webp(\?|$)/, `${label}: ${item.slug}'s photograph is ${item.photo.src}, not the 400px rendition`);
      assert.equal(item.photo.alt, '', `${label}: ${item.slug}'s photograph is not decorative`);
      assert.deepEqual([item.photo.width, item.photo.height], [56, 56], `${label}: ${item.slug}'s photograph is ${item.photo.width}x${item.photo.height}`);
      assert.equal(item.remove?.type, 'button', `${label}: ${item.slug} has no remove button`);
      assert.equal(item.remove.label, removeName[lang](item.name), `${label}: ${item.slug}'s remove button is named ${JSON.stringify(item.remove.label)}`);
      assert.equal(item.remove.text, '', `${label}: ${item.slug}'s remove button shows text ${JSON.stringify(item.remove.text)}`);
      assert.ok(item.remove.width >= 44 && item.remove.height >= 44, `${label}: ${item.slug}'s remove target is ${item.remove.width}x${item.remove.height}, under 44px`);
    }

    // A third joins; one unticked on its tile leaves the row.
    await tick('nimbus-buds-pro');
    assert.deepEqual(slugs(await row()), ['nimbus-speaker-s1', 'koto-over-ear-quiet', 'nimbus-buds-pro'], `${label}: the row after a third tick`);
    await tick('koto-over-ear-quiet');
    assert.deepEqual(slugs(await row()), ['nimbus-speaker-s1', 'nimbus-buds-pro'], `${label}: the row after a box is unticked on its tile`);

    // × unticks the box and drops its entry; focus goes to the next ×, else
    // the one before, else the submit.
    await tick('koto-over-ear-quiet');
    await tick('pixelight-9-pro');
    assert.deepEqual(slugs(await row()), ['nimbus-speaker-s1', 'koto-over-ear-quiet', 'nimbus-buds-pro', 'pixelight-9-pro'], `${label}: the row of four`);
    const press = async slug => {
      await evaluate(`document.querySelector('.goen-compare-pick__item[data-slug=${JSON.stringify(slug)}] .goen-compare-pick__remove').focus()`);
      await key('Enter', 'Enter', 13);
    };
    await press('koto-over-ear-quiet');
    assert.deepEqual(await ticked(), ['nimbus-speaker-s1', 'nimbus-buds-pro', 'pixelight-9-pro'], `${label}: × left its box ticked`);
    assert.deepEqual(slugs(await row()), ['nimbus-speaker-s1', 'nimbus-buds-pro', 'pixelight-9-pro'], `${label}: × left its entry`);
    assert.equal(await focused(), 'remove:nimbus-buds-pro', `${label}: focus after removing a middle entry`);
    await press('pixelight-9-pro');
    assert.deepEqual(slugs(await row()), ['nimbus-speaker-s1', 'nimbus-buds-pro'], `${label}: × left the last entry`);
    assert.equal(await focused(), 'remove:nimbus-buds-pro', `${label}: focus after removing the last entry`);
    await press('nimbus-speaker-s1');
    assert.deepEqual(await ticked(), ['nimbus-buds-pro'], `${label}: the boxes after the third ×`);
    assert.equal(await row(), null, `${label}: a row for the one product left`);
    assert.equal(await focused(), 'submit', `${label}: focus after the row goes`);
    console.log(`${label}: two ticks draw the row, × unticks and keeps focus`);

    // The results are swapped when a filter changes: the ticks go, as they do
    // today, and the row goes with them; ticks in the new results draw it again.
    await tick('nimbus-speaker-s1');
    assert.equal((await row())?.items.length, 2, `${label}: no row before the swap`);
    await evaluate(`document.getElementById('listing-results').dataset.before = 'swap'`);
    await evaluate(`document.querySelector('.goen-filters input[name="brand"][value="nimbus"]').click()`);
    await waitFor(`(() => { const results = document.getElementById('listing-results'); return results && !results.dataset.before && results.querySelectorAll('.goen-tile__cell').length === 2; })()`, `${label}: the swap`);
    await settle();
    assert.deepEqual(await ticked(), [], `${label}: boxes ticked after the swap`);
    assert.equal(await evaluate(`document.querySelectorAll('.goen-compare-pick__row').length`), 0, `${label}: a row is left after the swap`);
    await tick('nimbus-speaker-s1');
    await tick('nimbus-buds-pro');
    assert.deepEqual(slugs(await row()), ['nimbus-speaker-s1', 'nimbus-buds-pro'], `${label}: the row after ticking the swapped results`);
    console.log(`${label}: a swap leaves no entry, and the new results draw the row`);

    // Back from /compare the browser gives the boxes back; the row is drawn
    // from them, never from what it held before.
    await open(1440);
    await tick('koto-over-ear-quiet');
    await tick('meridian-watch-s3');
    await Promise.all([
      evaluate(`document.querySelector('#compare-pick [type="submit"]').click()`),
      nextEvent('Page.frameNavigated', params => !params.frame.parentId),
    ]);
    await waitFor(`location.pathname === '/compare' && document.readyState === 'complete'`, `${label}: the comparison`);
    assert.equal(await evaluate('location.search'), '?p=koto-over-ear-quiet&p=meridian-watch-s3', `${label}: the comparison's set`);
    const { currentIndex, entries } = await send('Page.getNavigationHistory');
    await send('Page.navigateToHistoryEntry', { entryId: entries[currentIndex - 1].id });
    await waitFor(`location.pathname === '/c/audio' && document.readyState === 'complete' && !!document.getElementById('compare-pick')`, `${label}: back to the listing`);
    await settle();
    const back = await ticked();
    assert.deepEqual(slugs(await row()), back.length >= 2 ? back : [], `${label}: back on the listing the row ${JSON.stringify(slugs(await row()))} is not the ticked boxes ${JSON.stringify(back)}`);
    console.log(`${label}: back from the comparison the row is the ticked boxes (${back.length})`);

    // Boxes ticked before goen.js has run, on a slow line, are in the row
    // once it does: the row is drawn from the boxes as the script starts.
    await send('Network.setCacheDisabled', { cacheDisabled: true });
    await send('Fetch.enable', { patterns: [{ urlPattern: '*/goen.js*', requestStage: 'Request' }] });
    const held = nextEvent('Fetch.requestPaused');
    await send('Page.navigate', { url: origin + '/c/audio' });
    const { requestId } = await held;
    await waitFor(`document.readyState !== 'loading' && !!document.getElementById('compare-pick')`, `${label}: the listing before its script`);
    await tick('nimbus-speaker-s1');
    await tick('pixelight-9-pro');
    assert.equal(await row(), null, `${label}: a row before goen.js has run`);
    await send('Fetch.continueRequest', { requestId });
    await send('Fetch.disable');
    await send('Network.setCacheDisabled', { cacheDisabled: false });
    await waitFor(`document.readyState === 'complete'`, `${label}: the listing with its script`);
    await settle();
    assert.deepEqual(slugs(await row()), ['nimbus-speaker-s1', 'pixelight-9-pro'], `${label}: boxes ticked before goen.js ran are not in the row`);
    console.log(`${label}: boxes ticked before the script ran are in the row`);
  }

  // With scripting off the bar is the server's tail and the form submits the
  // ticked products to /compare.
  {
    const label = 'no script';
    await locale('zh-Hant');
    await send('Emulation.setScriptExecutionDisabled', { value: true });
    await send('Emulation.setDeviceMetricsOverride', { width: 375, height: 800, deviceScaleFactor: 1, mobile: true });
    const loaded = nextEvent('Page.loadEventFired');
    await send('Page.navigate', { url: origin + '/c/audio' });
    await loaded;
    const { root } = await send('DOM.getDocument', { depth: 0 });
    const one = async selector => (await send('DOM.querySelector', { nodeId: root.nodeId, selector })).nodeId;
    const click = async selector => {
      const nodeId = await one(selector);
      assert.ok(nodeId, `${label}: ${selector} is not on the page`);
      await send('DOM.scrollIntoViewIfNeeded', { nodeId });
      const { model } = await send('DOM.getBoxModel', { nodeId });
      const [x1, y1, , , x3, y3] = model.border;
      const x = (x1 + x3) / 2, y = (y1 + y3) / 2;
      for (const type of ['mousePressed', 'mouseReleased']) await send('Input.dispatchMouseEvent', { type, x, y, button: 'left', clickCount: 1 });
    };
    await click('#compare-pick input[value="nimbus-speaker-s1"]');
    await click('#compare-pick input[value="nimbus-buds-pro"]');
    assert.equal(await one('.goen-compare-pick__row'), 0, `${label}: a row is drawn without script`);
    const tail = await send('DOM.getOuterHTML', { nodeId: await one('#compare-pick > .goen-compare-pick__go') });
    assert.match(tail.outerHTML, /goen-compare-pick__note[^>]*>最多比較 5 件<\/span>\s*<button[^>]*type="submit"[^>]*>\s*比較所選商品\s*<\/button>/, `${label}: the bar is not the limit and the submit: ${tail.outerHTML}`);
    const navigated = nextEvent('Page.frameNavigated', params => !params.frame.parentId);
    await click('#compare-pick button[type="submit"]');
    const { frame } = await navigated;
    assert.equal(frame.url, origin + '/compare?p=nimbus-speaker-s1&p=nimbus-buds-pro', `${label}: the form submitted to ${frame.url}`);
    await send('Emulation.setScriptExecutionDisabled', { value: false });
    console.log(`${label}: the bar is the server's tail and submits the ticked products to /compare`);
  }

  // With the bar open the page does not widen at any width, in either
  // language, at 100% and 200% text. From four ticks the row scrolls inside the
  // bar, and the long name is cut with an ellipsis and kept whole in the text.
  const sweep = [];
  for (const lang of ['zh-Hant', 'en']) {
    await locale(lang);
    for (const width of [320, 375, 414, 768, 1024, 1440, 1920]) {
      for (const text200 of [false, true]) {
        const label = `bar open ${width}px ${lang}${text200 ? ' 200%' : ''}`;
        await open(width, { text200 });
        for (const slug of ['nimbus-speaker-s1', 'koto-over-ear-quiet', 'meridian-watch-s3', 'pixelight-9-pro']) await tick(slug);
        await evaluate(`document.querySelector('.goen-compare-pick__go').scrollIntoView({ block: 'center' })`);
        await settle();
        const got = await evaluate(reflowProbe(width), true);
        assert.ok(got.scrollWidth <= width && got.scrollX === 0, `${label}: the page scrolls sideways (scrollWidth ${got.scrollWidth}, scrollX ${got.scrollX})`);
        assert.deepEqual(got.text, [], `${label}: text cut or past the viewport: ${JSON.stringify(got.text)}`);
        const bar = await evaluate(`(() => {
          const bar = document.querySelector('.goen-compare-pick__go');
          const row = bar.querySelector('.goen-compare-pick__row');
          const note = bar.querySelector('.goen-compare-pick__note');
          const submit = bar.querySelector('[type="submit"]');
          const long = row.querySelector('[data-slug="meridian-watch-s3"]');
          const name = long.querySelector('.goen-compare-pick__name');
          const r = el => el.getBoundingClientRect();
          const cs = getComputedStyle(row);
          const after = getComputedStyle(row, '::after');
          return {
            barRight: r(bar).right, viewport: document.documentElement.clientWidth,
            rowOwnLine: r(row).bottom <= Math.min(r(note).top, r(submit).top) + 0.5,
            noteWithSubmit: Math.abs(r(note).top + r(note).height / 2 - (r(submit).top + r(submit).height / 2)) < r(submit).height / 2,
            rowFull: Math.abs(r(row).width - (r(bar).width - parseFloat(getComputedStyle(bar).paddingLeft) - parseFloat(getComputedStyle(bar).paddingRight))) < 1,
            scrolls: row.scrollWidth > row.clientWidth + 0.5, overflowX: cs.overflowX, snap: cs.scrollSnapType,
            paddingRight: cs.paddingRight, spacer: after.content !== 'none' && after.content !== 'normal' ? after.flexBasis : null,
            itemSnap: getComputedStyle(long).scrollSnapAlign, itemMargin: getComputedStyle(long).scrollMarginLeft,
            cut: name.scrollWidth > name.clientWidth + 0.5, ellipsis: getComputedStyle(name).textOverflow,
            whole: name.textContent, removeLabel: long.querySelector('button').getAttribute('aria-label'),
          };
        })()`);
        assert.ok(bar.barRight <= bar.viewport + 0.5, `${label}: the bar ends at ${bar.barRight}, past ${bar.viewport}`);
        assert.equal(bar.overflowX, 'auto', `${label}: the row does not scroll inside the bar`);
        assert.equal(bar.paddingRight, '0px', `${label}: the row's end is padding-right ${bar.paddingRight}`);
        assert.equal(bar.spacer, '16px', `${label}: the row's end spacer is ${bar.spacer}, not --s-4`);
        assert.match(bar.snap, /^x( proximity)?$/, `${label}: the row snaps ${bar.snap}`);
        assert.deepEqual([bar.itemSnap, bar.itemMargin], ['start', '16px'], `${label}: an entry snaps ${bar.itemSnap} with margin ${bar.itemMargin}`);
        assert.ok(bar.cut && bar.ellipsis === 'ellipsis', `${label}: the long name is not cut with an ellipsis (cut ${bar.cut}, ${bar.ellipsis})`);
        assert.equal(bar.whole, names['meridian-watch-s3'], `${label}: the long name's text is ${JSON.stringify(bar.whole)}`);
        assert.ok(bar.removeLabel.includes(names['meridian-watch-s3']), `${label}: the long name's remove button is named ${JSON.stringify(bar.removeLabel)}`);
        if (width < 768) {
          assert.ok(bar.rowOwnLine && bar.rowFull, `${label}: the row does not take a line of its own (own line ${bar.rowOwnLine}, full ${bar.rowFull})`);
          if (!text200 && width >= 375) assert.ok(bar.noteWithSubmit, `${label}: the limit and the submit are not on one line under the row`);
          assert.ok(bar.scrolls, `${label}: four entries fit a phone's row, so nothing shows it scrolls`);
        }
        sweep.push(`${width}${lang === 'en' ? 'en' : ''}${text200 ? '@200' : ''}`);
      }
    }
  }
  console.log(`bar open: the page does not widen at ${sweep.join(' ')}`);
} finally {
  socket.close();
  await fetch(`${debugging}/json/close/${target.id}`).catch(() => {});
}
