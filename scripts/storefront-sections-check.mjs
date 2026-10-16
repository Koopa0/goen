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

  // Anything that can be pressed is a pill with one corner, in rem so it grows
  // with the text. The wishlist heart and the hero arrows are circles, and the
  // footer's button sits 4px inside its field.
  {
    const label = 'pressable corners';
    const measure = (selectors, rootSize) => evaluate(`(() => {
      document.documentElement.style.fontSize = ${JSON.stringify(rootSize)};
      return Object.fromEntries(${JSON.stringify(selectors)}.map(selector => {
        const element = document.querySelector(selector);
        if (!element) return [selector, null];
        const style = getComputedStyle(element);
        return [selector, { radius: style.borderRadius, height: element.getBoundingClientRect().height,
          shadow: style.boxShadow, border: style.borderTopColor, background: style.backgroundColor,
          page: getComputedStyle(document.body).backgroundColor }];
      }));
    })()`);
    const length = (got, what) => {
      assert.match(got.radius, /^\d+(\.\d+)?px$/, `${label}: ${what} has a ${got.radius} corner, not a length`);
      return parseFloat(got.radius);
    };
    const field = '.goen-footer__field > .goen-input';
    const submit = '.goen-footer__field > .goen-btn';
    const filled = '.goen-summary__actions .goen-btn--primary';
    const cartPills = [filled, '.goen-stepper', '.goen-header__lang > summary', '.goen-header__backoffice', field];
    const heart = '.goen-header__action--wide';
    const chip = '.goen-filters__chip';
    const arrow = '.goen-hero__arrow';
    const corner = {};
    for (const rootSize of ['', '200%']) {
      const scale = rootSize || '100%';
      await open('/cart', 1440);
      const cart = await measure([...cartPills, submit, heart], rootSize);
      for (const selector of [...cartPills, submit, heart]) assert.ok(cart[selector]?.height > 0, `${label}: ${selector} is not drawn in the cart at ${scale}`);
      corner[scale] = length(cart[field], field);
      for (const selector of cartPills) {
        assert.equal(length(cart[selector], selector), corner[scale], `${label}: ${selector} has a ${cart[selector].radius} corner at ${scale}, not the footer field's ${corner[scale]}px`);
        assert.ok(corner[scale] >= cart[selector].height / 2 - 0.5, `${label}: ${selector} is ${cart[selector].height}px tall with a ${corner[scale]}px corner at ${scale}, not a full pill`);
      }
      assert.equal(length(cart[submit], submit), corner[scale] - 4, `${label}: the footer button's corner at ${scale} is ${cart[submit].radius}, not its field's ${corner[scale]}px less 4px`);
      assert.equal(cart[heart].radius, '50%', `${label}: the wishlist heart at ${scale} is ${cart[heart].radius}, not a circle`);
      assert.equal(cart[filled].shadow, 'none', `${label}: the filled button at ${scale} casts a shadow: ${cart[filled].shadow}`);

      await open('/c/audio', 1440);
      const listing = await measure([chip], rootSize);
      assert.ok(listing[chip]?.height > 0, `${label}: no filter chip is drawn at ${scale}`);
      assert.equal(length(listing[chip], chip), corner[scale], `${label}: a filter chip has a ${listing[chip].radius} corner at ${scale}, not ${corner[scale]}px`);
      assert.ok(corner[scale] >= listing[chip].height / 2 - 0.5, `${label}: a filter chip is ${listing[chip].height}px tall with a ${corner[scale]}px corner at ${scale}, not a full pill`);

      await open('/home', 1440);
      const hero = await measure([arrow], rootSize);
      assert.ok(hero[arrow]?.height > 0, `${label}: no hero arrow is drawn at ${scale}`);
      assert.equal(hero[arrow].radius, '50%', `${label}: a hero arrow at ${scale} is ${hero[arrow].radius}, not a circle`);
      assert.equal(hero[arrow].border, 'rgba(0, 0, 0, 0)', `${label}: a hero arrow's border at ${scale} is ${hero[arrow].border}, not transparent`);
      assert.notEqual(hero[arrow].background, hero[arrow].page, `${label}: a hero arrow at ${scale} is the page's own ${hero[arrow].page}`);
      assert.notEqual(hero[arrow].background, 'rgba(0, 0, 0, 0)', `${label}: a hero arrow at ${scale} has no fill`);
    }
    assert.equal(corner['200%'], corner['100%'] * 2, `${label}: ${corner['100%']}px at 100% text is ${corner['200%']}px at 200%, not double`);
    console.log(`${label}: ${corner['100%']}px at 100% text, ${corner['200%']}px at 200%; heart and hero arrows round, footer button 4px inside its field`);
  }
  // A product's main photograph sits on one rounded well in the photograph's
  // own ground, whatever the department's tone: no tone frame around it, no
  // white edge on the picture, and a placeholder whose words can be read.
  {
    const channels = text => {
      const got = /^rgba?\((\d+),\s*(\d+),\s*(\d+)(?:,\s*([\d.]+))?\)$/.exec(text);
      assert.ok(got, `product photograph: ${text} is not an rgb colour`);
      assert.equal(got[4] ?? '1', '1', `product photograph: ${text} is not opaque`);
      return [got[1], got[2], got[3]].map(Number);
    };
    // WCAG 2.2 relative luminance: each sRGB channel c/255 becomes c/12.92 when
    // at most 0.04045 and ((c + 0.055) / 1.055) ** 2.4 above it, then
    // L = 0.2126 R + 0.7152 G + 0.0722 B. The ratio is (lighter + 0.05) / (darker + 0.05).
    const luminance = rgb => {
      const [r, g, b] = rgb.map(value => {
        const c = value / 255;
        return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
      });
      return 0.2126 * r + 0.7152 * g + 0.0722 * b;
    };
    const ratio = (a, b) => {
      const [light, dark] = [luminance(a), luminance(b)].sort((x, y) => y - x);
      return (light + 0.05) / (dark + 0.05);
    };
    const measure = () => evaluate(`(() => {
      const gallery = document.querySelector('#gallery');
      const shot = gallery?.querySelector('.goen-pdp__shot');
      if (!shot) return null;
      // A probe turns the custom properties, which may be written as any colour
      // syntax, into the rgb() text the shot's own background computes to.
      const probe = document.createElement('div');
      document.body.append(probe);
      const resolve = name => {
        probe.style.backgroundColor = '';
        probe.style.backgroundColor = getComputedStyle(gallery).getPropertyValue(name);
        return getComputedStyle(probe).backgroundColor;
      };
      const style = getComputedStyle(shot);
      const corners = ['TopLeft', 'TopRight', 'BottomRight', 'BottomLeft'].map(corner => style['border' + corner + 'Radius']);
      const sides = ['Top', 'Right', 'Bottom', 'Left'];
      const image = shot.querySelector('.goen-pdp__shotimg');
      const placeholder = shot.querySelector('.goen-ph');
      const result = {
        tone: gallery.dataset.tone,
        photo: resolve('--photo'), toneGround: resolve('--tone-ground'),
        background: style.backgroundColor, corners,
        padding: sides.map(side => style['padding' + side]),
        shotBorder: sides.map(side => style['border' + side + 'Width']),
      };
      if (image) {
        const picture = getComputedStyle(image);
        const outer = shot.getBoundingClientRect(), inner = image.getBoundingClientRect();
        Object.assign(result, {
          imageBorder: sides.map(side => picture['border' + side + 'Width']),
          imageClip: picture.backgroundClip,
          inset: [inner.left - outer.left, inner.top - outer.top, outer.right - inner.right, outer.bottom - inner.bottom],
        });
      }
      if (placeholder) result.textColor = getComputedStyle(placeholder).color;
      probe.remove();
      return result;
    })()`);
    for (const tone of ['ink', 'sage']) {
      for (const photo of [true, false]) {
        for (const width of [375, 1440]) {
          const label = `product photograph ${tone} ${photo ? 'with' : 'without'} a photograph ${width}px`;
          await open(`/product?tone=${tone}&photo=${photo ? 1 : 0}`, width);
          const drawn = await measure();
          assert.ok(drawn, `${label}: no photograph well on the page`);
          assert.equal(drawn.tone, tone, `${label}: the gallery carries tone ${drawn.tone}`);
          assert.notEqual(drawn.toneGround, drawn.photo, `${label}: the tone's ground equals the photograph ground, so this proves nothing`);
          assert.equal(drawn.background, drawn.photo, `${label}: the well is ${drawn.background}, not the photograph ground ${drawn.photo}`);
          assert.notEqual(drawn.background, drawn.toneGround, `${label}: the well wears the tone ${drawn.toneGround}`);
          assert.deepEqual(drawn.padding, Array(4).fill('0px'), `${label}: the well has padding ${drawn.padding}`);
          assert.deepEqual(drawn.shotBorder, Array(4).fill('0px'), `${label}: the well has a border ${drawn.shotBorder}`);
          assert.deepEqual(drawn.corners, Array(4).fill('16px'), `${label}: corners ${drawn.corners}, not 16px`);
          if (photo) {
            assert.deepEqual(drawn.imageBorder, Array(4).fill('0px'), `${label}: the picture has an edge ${drawn.imageBorder}`);
            assert.equal(drawn.imageClip, 'border-box', `${label}: the picture's background is clipped to ${drawn.imageClip}`);
            for (const gap of drawn.inset) assert.ok(Math.abs(gap) < 0.5, `${label}: the picture sits ${drawn.inset} inside the well`);
            console.log(`${label}: ${drawn.background} well, 16px corners, picture edge to edge`);
          } else {
            const contrast = ratio(channels(drawn.textColor), channels(drawn.background));
            assert.ok(contrast >= 4.5, `${label}: placeholder ${drawn.textColor} on ${drawn.background} is ${contrast.toFixed(2)}:1`);
            console.log(`${label}: ${drawn.background} well, 16px corners, placeholder text ${contrast.toFixed(2)}:1`);
          }
        }
      }
    }
  }
  // The header's search is a white pill with a 1px edge on all four sides, in
  // rem so it grows with the text. A page that opens with the hero meets it
  // without the header's rule; every other page keeps the rule.
  {
    const label = 'header search';
    const measure = rootSize => evaluate(`(() => {
      document.documentElement.style.fontSize = ${JSON.stringify(rootSize)};
      const resolve = (property, value) => {
        const probe = document.createElement('div');
        probe.style.position = 'absolute';
        probe.style.visibility = 'hidden';
        probe.style.setProperty(property, value);
        document.body.append(probe);
        const got = getComputedStyle(probe).getPropertyValue(property);
        probe.remove();
        return got;
      };
      const field = document.querySelector('#site-search');
      const icon = document.querySelector('.goen-header__searchicon svg');
      const header = getComputedStyle(document.querySelector('.goen-header'));
      if (!field || !icon) return null;
      const style = getComputedStyle(field);
      const box = field.getBoundingClientRect();
      const sides = ['Top', 'Right', 'Bottom', 'Left'];
      return {
        hero: document.querySelector('.goen-hero') !== null,
        rem: parseFloat(getComputedStyle(document.documentElement).fontSize),
        edge: resolve('color', 'var(--edge)'),
        press: resolve('border-top-left-radius', 'var(--r-press)'),
        background: style.backgroundColor,
        widths: sides.map(side => style[\`border\${side}Width\`]),
        styles: sides.map(side => style[\`border\${side}Style\`]),
        colors: sides.map(side => style[\`border\${side}Color\`]),
        radii: ['TopLeft', 'TopRight', 'BottomRight', 'BottomLeft'].map(corner => style[\`border\${corner}Radius\`]),
        paddingLeft: style.paddingLeft,
        height: box.height,
        iconLeft: icon.getBoundingClientRect().left - box.left,
        iconRight: icon.getBoundingClientRect().right - box.left,
        rule: { width: header.borderBottomWidth, style: header.borderBottomStyle },
      };
    })()`);
    for (const width of [375, 1440]) {
      for (const [path, hero] of [['/home', true], ['/search', false], ['/cart', false]]) {
        await open(path, width);
        for (const rootSize of ['', '200%']) {
          const scale = rootSize || '100%';
          const where = `${label} ${path} ${width}px at ${scale}`;
          const got = await measure(rootSize);
          assert.ok(got, `${where}: no search field or icon`);
          assert.equal(got.hero, hero, `${where}: the fixture ${hero ? 'has no' : 'has a'} hero`);
          assert.ok(got.height > 0, `${where}: the field is not drawn`);
          assert.equal(got.background, 'rgb(255, 255, 255)', `${where}: background is ${got.background}, not white`);
          assert.deepEqual(got.widths, ['1px', '1px', '1px', '1px'], `${where}: border widths ${got.widths}`);
          assert.deepEqual(got.styles, ['solid', 'solid', 'solid', 'solid'], `${where}: border styles ${got.styles}`);
          assert.deepEqual(got.colors, Array(4).fill(got.edge), `${where}: border colours ${got.colors}, not the control edge ${got.edge}`);
          assert.match(got.press, /^\d+(\.\d+)?px$/, `${where}: --r-press resolves to ${got.press}, not a length`);
          assert.deepEqual(got.radii, Array(4).fill(got.press), `${where}: corners ${got.radii}, not --r-press ${got.press}`);
          assert.ok(parseFloat(got.press) >= got.height / 2 - 0.5, `${where}: ${got.height}px tall with a ${got.press} corner, not a full pill`);
          assert.equal(got.paddingLeft, `${2.5 * got.rem}px`, `${where}: left padding ${got.paddingLeft}, not 2.5rem`);
          assert.ok(Math.abs(got.iconLeft - got.rem) < 0.5, `${where}: the magnifier starts ${got.iconLeft}px in, not 1rem (${got.rem}px)`);
          assert.ok(got.iconRight <= 2.5 * got.rem, `${where}: the magnifier ends ${got.iconRight}px in, over the text that starts at 2.5rem`);
          if (hero) assert.equal(got.rule.width, '0px', `${where}: the header keeps its rule over the hero`);
          else assert.deepEqual(got.rule, { width: '1px', style: 'solid' }, `${where}: the header's rule is ${JSON.stringify(got.rule)}`);
        }
        console.log(`${label} ${path} ${width}px: white pill, 1px edge, 2.5rem inset, header rule ${hero ? 'gone' : 'kept'}`);
      }
    }
    // The focus ring is the shared 2px outline, drawn outside a field that
    // keeps its pill corners while it is focused.
    await open('/search', 1440);
    await send('Emulation.setFocusEmulationEnabled', { enabled: true });
    try {
      const ring = await evaluate(`(() => {
        const field = document.querySelector('#site-search');
        const style = getComputedStyle(field);
        const unfocused = style.borderTopLeftRadius;
        field.focus();
        return { focused: document.activeElement === field, visible: field.matches(':focus-visible'),
          width: style.outlineWidth, style: style.outlineStyle, offset: style.outlineOffset,
          radius: style.borderTopLeftRadius, resting: unfocused };
      })()`);
      assert.ok(ring.focused && ring.visible, `${label}: focus: the field did not take focus-visible: ${JSON.stringify(ring)}`);
      assert.equal(ring.width, '2px', `${label}: focus: outline is ${ring.width}, not 2px`);
      assert.equal(ring.style, 'solid', `${label}: focus: outline style ${ring.style}`);
      assert.ok(parseFloat(ring.offset) >= 0, `${label}: focus: the ring is drawn ${ring.offset} inside the field`);
      assert.equal(ring.radius, ring.resting, `${label}: focus: corner ${ring.radius} while focused, not the resting ${ring.resting}`);
      console.log(`${label}: focus ring ${ring.width} ${ring.style}, offset ${ring.offset}, corner kept`);
    } finally {
      await send('Emulation.setFocusEmulationEnabled', { enabled: false });
    }
  }
} finally {
  socket.close();
  await fetch(`${debugging}/json/close/${target.id}`);
}
