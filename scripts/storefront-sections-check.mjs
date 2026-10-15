import assert from 'node:assert/strict';

// Measures the storefront sections TestStorefrontSectionsAsDrawn renders:
// computed style and boxes in a real page, never the stylesheet's text.
const [origin, port] = process.argv.slice(2);
const debugging = `http://127.0.0.1:${port}`;
const response = await fetch(`${debugging}/json/new?about:blank`, { method: 'PUT' });
assert.equal(response.ok, true, 'Chrome must create the storefront sections test page');
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
  const nextEvent = method => new Promise((resolve, reject) => {
    const set = listeners.get(method) ?? new Set();
    listeners.set(method, set);
    const timer = setTimeout(() => { set.delete(listener); reject(new Error(`${method} did not arrive`)); }, 20000);
    const listener = params => { clearTimeout(timer); set.delete(listener); resolve(params); };
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
  const open = async (path, width) => {
    await send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false });
    const url = origin + path;
    await send('Page.navigate', { url });
    await waitFor(`location.href === ${JSON.stringify(url)} && document.readyState === 'complete'`, `${path} at ${width}px`);
    await evaluate('document.fonts.ready.then(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))');
  };
  // With scripting disabled Runtime.evaluate cannot settle a page promise, so
  // that page is opened and read through the DOM and CSS domains alone.
  const openWithoutScripts = async (path, width) => {
    await send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false });
    const loaded = nextEvent('Page.loadEventFired');
    const [navigated] = await Promise.all([send('Page.navigate', { url: origin + path }), loaded]);
    assert.equal(navigated.errorText, undefined, `${path} at ${width}px: ${navigated.errorText}`);
  };
  const queryNodes = async selectors => {
    for (let attempt = 0; attempt < 100; attempt++) {
      const { root } = await send('DOM.getDocument', { depth: 0 });
      const ids = [];
      for (const selector of selectors) ids.push((await send('DOM.querySelector', { nodeId: root.nodeId, selector })).nodeId);
      if (ids.every(Boolean)) return ids;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    return null;
  };
  // :hover and :active are forced through DevTools, the way the inspector
  // does, so no pointer has to land on a link that would navigate away.
  const force = async (selector, classes) => {
    const { root } = await send('DOM.getDocument', { depth: 0 });
    const { nodeId } = await send('DOM.querySelector', { nodeId: root.nodeId, selector });
    assert.ok(nodeId, `${selector} is not on the page`);
    await send('CSS.forcePseudoState', { nodeId, forcedPseudoClasses: classes });
  };
  const media = features => send('Emulation.setEmulatedMedia', { features });
  await send('Page.enable');
  await send('DOM.enable');
  await send('CSS.enable');

  // The rules row sits a section margin below what precedes it; padding above
  // its content would stack a second gap on that margin.
  for (const width of [375, 1440]) {
    await open('/home', width);
    const rules = await evaluate(`(() => {
      const section = document.querySelector('.goen-rules');
      const content = section && [...section.children].find(child => getComputedStyle(child).position !== 'absolute');
      if (!content) return null;
      return { padding: getComputedStyle(section).paddingTop,
        offset: content.getBoundingClientRect().top - section.getBoundingClientRect().top };
    })()`);
    assert.ok(rules, `rules row ${width}px: no section with visible content`);
    assert.equal(rules.padding, '0px', `rules row ${width}px: padding above the content`);
    assert.ok(Math.abs(rules.offset) < 0.5, `rules row ${width}px: content starts ${rules.offset}px below the section's top`);
    console.log(`rules row ${width}px: content at the section's top`);
  }

  // The department photographs keep the 4:3 they are shot in.
  for (const width of [375, 768, 1440]) {
    await open('/home', width);
    const wells = await evaluate(`[...document.querySelectorAll('.goen-cat__well')].map(well => {
      const box = well.getBoundingClientRect();
      return { width: box.width, height: box.height };
    })`);
    assert.equal(wells.length, 3, `departments ${width}px: wells drawn`);
    for (const well of wells) {
      assert.ok(well.width > 0 && Math.abs(well.height - well.width * 3 / 4) <= 1, `departments ${width}px: a well is ${well.width}x${well.height}, not 4:3`);
    }
    console.log(`departments ${width}px: ${wells.length} wells at 4:3`);
  }

  // Under a hovering pointer a department photograph grows a little instead of
  // dimming, a press puts it straight back, and reduced motion keeps it still.
  // A browser without a hovering pointer (headless Chrome on CI) never applies
  // those rules, so there the rules are read from the parsed sheet instead.
  {
    const label = 'department zoom';
    const measurable = await evaluate(`matchMedia('(hover: hover)').matches && matchMedia('(prefers-reduced-motion: no-preference)').matches`);
    if (measurable) {
      const photo = () => evaluate(`(() => {
        const photo = document.querySelector('.goen-cat__photo');
        getComputedStyle(photo).scale;
        photo.getAnimations().forEach(animation => animation.finish());
        const style = getComputedStyle(photo);
        return { scale: style.scale, opacity: style.opacity };
      })()`);
      assert.deepEqual(await photo(), { scale: 'none', opacity: '1' }, `${label}: at rest`);
      await force('.goen-cat', ['hover']);
      assert.deepEqual(await photo(), { scale: '1.03', opacity: '1' }, `${label}: on hover`);
      await force('.goen-cat', ['hover', 'active']);
      assert.deepEqual(await photo(), { scale: '1', opacity: '1' }, `${label}: pressed`);
      await force('.goen-cat', []);
      await media([{ name: 'prefers-reduced-motion', value: 'reduce' }]);
      await force('.goen-cat', ['hover']);
      assert.deepEqual(await photo(), { scale: 'none', opacity: '1' }, `${label}: on hover with reduced motion`);
      await force('.goen-cat', []);
      await media([]);
      console.log(`${label}: measured in the page: 1.03 on hover, 1 pressed, none with reduced motion, never dimmed`);
    } else {
      const sheet = await evaluate(`(() => {
        const rules = [];
        const walk = (list, conditions) => {
          for (const rule of list) {
            const inner = rule.media?.mediaText;
            if (rule.cssRules) walk(rule.cssRules, inner === undefined ? conditions : [...conditions, inner]);
            if (rule.selectorText !== undefined) {
              rules.push({ selector: rule.selectorText, conditions, scale: rule.style.scale,
                opacity: rule.style.opacity, transition: rule.style.transition });
            }
          }
        };
        for (const sheet of document.styleSheets) {
          try { walk(sheet.cssRules, []); } catch { /* a sheet from another origin cannot be read */ }
        }
        return rules;
      })()`);
      const gated = rule => rule.conditions.some(text => /\(hover:\s*hover\)/.test(text) && /\(prefers-reduced-motion:\s*no-preference\)/.test(text));
      const find = selector => sheet.find(rule => rule.selector === selector && gated(rule));
      const hover = find('.goen-cat:hover .goen-cat__photo');
      assert.ok(hover, `${label}: no (hover: hover) and (prefers-reduced-motion: no-preference) rule for .goen-cat:hover .goen-cat__photo`);
      assert.equal(hover.scale, '1.03', `${label}: on hover`);
      const pressed = find('.goen-cat:active .goen-cat__photo');
      assert.ok(pressed, `${label}: no (hover: hover) and (prefers-reduced-motion: no-preference) rule for .goen-cat:active .goen-cat__photo`);
      assert.equal(pressed.scale, '1', `${label}: pressed`);
      assert.match(pressed.transition, /\bnone\b/, `${label}: pressed still eases back`);
      const dimming = sheet.filter(rule => rule.opacity !== '' && rule.selector.includes('goen-cat__photo'));
      assert.deepEqual(dimming, [], `${label}: a rule dims the photograph`);
      console.log(`${label}: this browser reports no hovering pointer; read the media rules instead: 1.03 on hover, 1 pressed without easing, no opacity on the photograph`);
    }
  }

  // Only the script that moves between slides keeps aria-current true, so the
  // current tab is drawn only where scripting runs, in forced colours as well.
  const tabsEvaluated = () => evaluate(`(() => {
    const current = document.querySelector('.goen-hero__tabs a[aria-current="true"]');
    const other = document.querySelector('.goen-hero__tabs a:not([aria-current="true"])');
    if (!current || !other) return null;
    const look = tab => {
      const style = getComputedStyle(tab);
      return { background: style.backgroundColor, weight: style.fontWeight, outline: style.outlineStyle };
    };
    return { scripting: matchMedia('(scripting: enabled)').matches, current: look(current), other: look(other) };
  })()`);
  // Whether (scripting: none) holds cannot be read without running script, so
  // the page is asserted by what it draws: a current tab drawn apart means the
  // media feature did not turn off.
  const tabsRead = async () => {
    const ids = await queryNodes(['.goen-hero__tabs a[aria-current="true"]', '.goen-hero__tabs a:not([aria-current="true"])']);
    if (!ids) return null;
    const look = async nodeId => {
      const { computedStyle } = await send('CSS.getComputedStyleForNode', { nodeId });
      const value = name => computedStyle.find(property => property.name === name)?.value;
      return { background: value('background-color'), weight: value('font-weight'), outline: value('outline-style') };
    };
    return { current: await look(ids[0]), other: await look(ids[1]) };
  };
  for (const scriptingOff of [false, true]) {
    const label = `hero tabs scripting ${scriptingOff ? 'off' : 'on'}`;
    const tabs = scriptingOff ? tabsRead : tabsEvaluated;
    await send('Emulation.setScriptExecutionDisabled', { value: scriptingOff });
    if (scriptingOff) await openWithoutScripts('/home', 1440);
    else await open('/home', 1440);
    const drawn = await tabs();
    assert.ok(drawn, `${label}: no current and other tab`);
    if (scriptingOff) {
      assert.deepEqual(drawn.current, drawn.other, `${label}: the current tab is drawn apart`);
    } else {
      assert.equal(drawn.scripting, true, `${label}: the page does not report scripting on`);
      assert.notEqual(drawn.current.background, drawn.other.background, `${label}: the current tab has no fill`);
      assert.notEqual(drawn.current.weight, drawn.other.weight, `${label}: the current tab has no weight`);
    }
    await media([{ name: 'forced-colors', value: 'active' }]);
    const forced = await tabs();
    await media([]);
    assert.ok(forced, `${label}: no current and other tab in forced colours`);
    assert.equal(forced.current.outline === 'solid', !scriptingOff, `${label}: forced colours ${scriptingOff ? 'draw' : 'do not draw'} the current tab's edge`);
    console.log(`${label}: current tab ${scriptingOff ? 'not drawn' : 'drawn'}, forced-colours edge ${forced.current.outline}`);
  }
  await send('Emulation.setScriptExecutionDisabled', { value: false });

  // The running campaigns are plain links under a heading: no frame around
  // them, no box around each, and as many columns of at least 20rem as fit.
  for (const width of [375, 768, 1024, 1440]) {
    const label = `campaigns ${width}px`;
    await open('/deals', width);
    const strip = await evaluate(`(() => {
      const strip = document.querySelector('.goen-campaigns');
      const list = strip?.querySelector('.goen-campaigns__list');
      const items = [...(strip?.querySelectorAll('.goen-campaigns__item') ?? [])];
      if (!list) return null;
      const frame = box => {
        const style = getComputedStyle(box);
        return { border: [style.borderTopWidth, style.borderRightWidth, style.borderBottomWidth, style.borderLeftWidth].join(' '),
          background: style.backgroundColor, padding: style.padding };
      };
      const style = getComputedStyle(list);
      return { items: items.length, frames: [strip, ...items].map(frame),
        tracks: style.gridTemplateColumns.trim().split(/\\s+/).map(parseFloat), gap: parseFloat(style.columnGap) || 0,
        width: list.getBoundingClientRect().width, rem: parseFloat(getComputedStyle(document.documentElement).fontSize) };
    })()`);
    assert.ok(strip && strip.items === 2, `${label}: two campaign links`);
    for (const frame of strip.frames) {
      assert.deepEqual(frame, { border: '0px 0px 0px 0px', background: 'rgba(0, 0, 0, 0)', padding: '0px' }, `${label}: a frame or a box is drawn`);
    }
    const least = Math.min(strip.width, 20 * strip.rem), n = strip.tracks.length;
    assert.ok(strip.tracks.every(track => track >= least - 0.5), `${label}: a column under ${least}px in ${JSON.stringify(strip.tracks)}`);
    assert.ok((n + 1) * least + n * strip.gap > strip.width + 0.5, `${label}: ${n} columns leave room for another in ${strip.width}px`);
    console.log(`${label}: unframed, ${n} column${n === 1 ? '' : 's'} in ${strip.width}px`);
  }
  {
    const underline = () => evaluate(`getComputedStyle(document.querySelector('.goen-campaigns__item .goen-campaigns__title')).textDecorationLine`);
    assert.equal(await underline(), 'none', 'campaigns: a name is underlined at rest');
    await force('.goen-campaigns__item', ['hover']);
    assert.equal(await underline(), 'underline', 'campaigns: a name is not underlined on hover');
    await force('.goen-campaigns__item', []);
    console.log('campaigns: name underlined on hover');
  }

  // The compare button acts on the ticked boxes: with none ticked it is not offered.
  {
    const label = 'compare button';
    await open('/search', 1440);
    const button = () => evaluate(`(() => {
      const go = document.querySelector('.goen-compare-pick__go');
      return go && { display: getComputedStyle(go).display, height: go.getBoundingClientRect().height };
    })()`);
    const tick = () => evaluate(`document.querySelector('.goen-compare-pick input[type="checkbox"]').click()`);
    assert.deepEqual(await button(), { display: 'none', height: 0 }, `${label}: offered with nothing ticked`);
    await tick();
    const ticked = await button();
    assert.ok(ticked.display !== 'none' && ticked.height > 0, `${label}: not offered with a product ticked: ${JSON.stringify(ticked)}`);
    await tick();
    assert.equal((await button()).display, 'none', `${label}: still offered once the box is unticked`);
    console.log(`${label}: hidden until a product is ticked`);
  }
} finally {
  socket.close();
  await fetch(`${debugging}/json/close/${target.id}`);
}
