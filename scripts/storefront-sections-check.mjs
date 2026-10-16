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
      return { background: style.backgroundColor, weight: style.fontWeight, outline: style.outlineStyle, shadow: style.boxShadow };
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
      return { background: value('background-color'), weight: value('font-weight'), outline: value('outline-style'), shadow: value('box-shadow') };
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

  // The home hero is two rounded panels inside the page column, the copy on its
  // tone and the photograph, with a seam of page white between them: side by
  // side from 64rem, the photograph above the copy below it, one layout at
  // every width. Boxes are read relative to their own slide, so a slide the
  // track has not scrolled to needs no scrolling.
  {
    const corner = '24px';
    const seam = 8;
    const near = (got, want, tolerance = 0.5) => Math.abs(got - want) <= tolerance;
    for (const width of [375, 800, 1024, 1440]) {
      const label = `hero panels ${width}px`;
      await open('/home', width);
      const hero = await evaluate(`(() => {
        const number = value => parseFloat(value);
        const box = element => {
          const r = element.getBoundingClientRect();
          return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width, height: r.height };
        };
        const corners = style => [style.borderTopLeftRadius, style.borderTopRightRadius, style.borderBottomRightRadius, style.borderBottomLeftRadius];
        const home = document.querySelector('.goen-home');
        const homeStyle = getComputedStyle(home);
        const homeBox = box(home);
        const slides = [...document.querySelectorAll('.goen-hero__slide')].map(slide => {
          const slideStyle = getComputedStyle(slide);
          const body = slide.querySelector('.goen-hero__body');
          const media = slide.querySelector('.goen-hero__media');
          const bodyStyle = getComputedStyle(body);
          const got = {
            tone: slide.dataset.tone,
            slide: box(slide),
            background: slideStyle.backgroundColor,
            padding: slideStyle.padding,
            body: box(body),
            bodyCorners: corners(bodyStyle),
            bodyBackground: bodyStyle.backgroundColor,
            bodyShadow: bodyStyle.boxShadow,
            bodyPadding: [bodyStyle.paddingTop, bodyStyle.paddingRight, bodyStyle.paddingBottom, bodyStyle.paddingLeft].map(number),
            focusableOutsideBody: [...slide.querySelectorAll('a, button, input, select, textarea, [tabindex]')].filter(element => !body.contains(element)).length,
            media: null,
          };
          if (media) {
            const mediaStyle = getComputedStyle(media);
            got.media = {
              box: box(media),
              img: box(media.querySelector('img')),
              corners: corners(mediaStyle),
              background: mediaStyle.backgroundColor,
              shadow: mediaStyle.boxShadow,
              overflow: mediaStyle.overflow,
              border: [mediaStyle.borderTopWidth, mediaStyle.borderRightWidth, mediaStyle.borderBottomWidth, mediaStyle.borderLeftWidth].join(' '),
              pseudo: ['::before', '::after'].map(pseudo => getComputedStyle(media, pseudo).content),
            };
          }
          return got;
        });
        const ground = [...document.querySelectorAll('.goen-hero__slide')].map(slide => {
          const probe = document.createElement('i');
          probe.style.position = 'absolute';
          probe.style.backgroundColor = 'var(--tone-ground)';
          slide.append(probe);
          const color = getComputedStyle(probe).backgroundColor;
          probe.remove();
          return color;
        });
        return {
          viewport: innerWidth,
          column: { left: homeBox.left + number(homeStyle.paddingLeft), right: homeBox.right - number(homeStyle.paddingRight) },
          track: box(document.querySelector('.goen-hero__track')),
          slides, ground,
        };
      })()`);
      assert.equal(hero.slides.length, 5, `${label}: five slides drawn`);
      assert.deepEqual(hero.slides.map(slide => slide.tone), ['stone', 'ink', 'sage', 'sage', 'ink'], `${label}: tones of the slides`);
      assert.equal(hero.slides.filter(slide => slide.media).length, 3, `${label}: three slides carry a photograph`);

      // Inside the page column, the slides one track wide and touching.
      assert.ok(near(hero.track.left, hero.column.left) && near(hero.track.right, hero.column.right),
        `${label}: the track is ${hero.track.left}-${hero.track.right}, the page column ${hero.column.left}-${hero.column.right}`);
      hero.slides.forEach((slide, index) => {
        assert.ok(near(slide.slide.width, hero.track.width), `${label}: slide ${index + 1} is ${slide.slide.width}px in a ${hero.track.width}px track`);
        if (index > 0) {
          const gap = slide.slide.left - hero.slides[index - 1].slide.right;
          assert.ok(near(gap, 0), `${label}: ${gap}px between slides ${index} and ${index + 1}`);
        }
      });

      const wide = width >= 1024;
      const rgb = /^rgb\((\d+), (\d+), (\d+)\)$/;
      hero.slides.forEach((slide, index) => {
        const name = `${label}: slide ${index + 1} (${slide.tone}${slide.media ? '' : ', no photograph'})`;
        const ground = hero.ground[index];
        const [, red, green, blue] = rgb.exec(ground) ?? [];
        assert.ok(red !== undefined, `${name}: tone ground ${ground}`);
        const sum = Number(red) + Number(green) + Number(blue);
        if (slide.tone === 'ink') assert.ok(sum < 150, `${name}: ink ground is ${ground}`);
        else assert.ok(sum > 500 && sum < 765, `${name}: ground is ${ground}, not a light tone`);

        // The seam is page white: the slide itself has no ground, padding or ring.
        assert.equal(slide.background, 'rgba(0, 0, 0, 0)', `${name}: the slide has a ground of ${slide.background}`);
        assert.equal(slide.padding, '0px', `${name}: the slide has padding ${slide.padding}`);
        assert.deepEqual(slide.bodyCorners, [corner, corner, corner, corner], `${name}: body corners`);
        assert.equal(slide.bodyBackground, ground, `${name}: body is ${slide.bodyBackground}, the tone ground is ${ground}`);
        assert.equal(slide.bodyShadow, 'none', `${name}: body has a ring: ${slide.bodyShadow}`);
        assert.equal(slide.focusableOutsideBody, 0, `${name}: something focusable lies outside the body, off the tone`);
        const inline = wide ? Math.min(56, Math.max(32, hero.viewport * 0.04)) : 24;
        const block = wide ? 48 : 24;
        assert.ok(near(slide.bodyPadding[1], inline) && near(slide.bodyPadding[3], inline) && near(slide.bodyPadding[0], block),
          `${name}: body padding ${slide.bodyPadding.join(' ')}, want ${block} ${inline} ${slide.bodyPadding[2]} ${inline}`);

        if (!slide.media) {
          assert.ok(near(slide.body.left, slide.slide.left) && near(slide.body.right, slide.slide.right), `${name}: the body does not fill the slide`);
          return;
        }
        const { media } = slide;
        assert.deepEqual(media.corners, [corner, corner, corner, corner], `${name}: photograph corners`);
        assert.equal(media.background, ground, `${name}: photograph panel is ${media.background}, the tone ground is ${ground}`);
        assert.equal(media.shadow, 'none', `${name}: photograph panel has a ring: ${media.shadow}`);
        assert.equal(media.overflow, 'hidden', `${name}: photograph panel overflow is ${media.overflow}, so its corners do not clip the photograph`);
        assert.equal(media.border, '0px 0px 0px 0px', `${name}: photograph panel has a border: ${media.border}`);
        assert.ok(media.pseudo.every(content => content === 'none' || content === 'normal'), `${name}: photograph panel draws a pseudo-element: ${media.pseudo}`);
        for (const edge of ['left', 'right', 'top', 'bottom']) {
          assert.ok(near(media.img[edge], media.box[edge], 1), `${name}: the photograph's ${edge} is ${media.img[edge]}, its panel's ${media.box[edge]}`);
        }
        if (wide) {
          assert.ok(media.box.left > slide.body.left, `${name}: the photograph is not to the right of the copy`);
          assert.ok(near(slide.body.top, media.box.top, 1) && near(slide.body.bottom, media.box.bottom, 1), `${name}: the two panels are not the same height`);
          assert.ok(near(media.box.left - slide.body.right, seam), `${name}: ${media.box.left - slide.body.right}px between the panels, want ${seam}px`);
          assert.ok(near(slide.body.left, slide.slide.left) && near(media.box.right, slide.slide.right), `${name}: the panels do not span the slide`);
          assert.ok(near(slide.body.width, (slide.slide.width - seam) * 5 / 12, 1), `${name}: copy is ${slide.body.width}px of ${slide.slide.width}px, not 5 of 12`);
        } else {
          assert.ok(media.box.top < slide.body.top, `${name}: the photograph is not above the copy`);
          assert.ok(near(slide.body.top - media.box.bottom, seam), `${name}: ${slide.body.top - media.box.bottom}px between the panels, want ${seam}px`);
          for (const panel of [slide.body, media.box]) {
            assert.ok(near(panel.left, slide.slide.left) && near(panel.right, slide.slide.right), `${name}: a panel does not span the slide`);
          }
        }
      });

      // A focus ring pushed 2px off a tab needs 4px of the scrolling row above
      // and below it, or the row clips it; the words stay where they were.
      const tabs = await evaluate(`(() => {
        const box = element => { const r = element.getBoundingClientRect(); return { left: r.left, top: r.top, bottom: r.bottom }; };
        const probe = document.createElement('i');
        probe.style.color = 'var(--accent)';
        document.body.append(probe);
        const accent = getComputedStyle(probe).color;
        probe.remove();
        const row = document.querySelector('.goen-hero__tabs');
        const rowStyle = getComputedStyle(row);
        const current = row.querySelector('a[aria-current="true"]');
        const other = row.querySelector('a:not([aria-current="true"])');
        return { accent, row: box(row), current: box(current), words: box(current).left + parseFloat(getComputedStyle(current).paddingLeft),
          padding: [rowStyle.paddingTop, rowStyle.paddingBottom],
          currentShadow: getComputedStyle(current).boxShadow, otherShadow: getComputedStyle(other).boxShadow };
      })()`);
      // From 48rem the first tab hangs 16px into the gutter so its words sit on the column edge.
      const hang = width >= 768 ? 0 : 16;
      assert.ok(near(tabs.words, hero.column.left + hang), `${label}: the first tab's words start at ${tabs.words}, want ${hero.column.left + hang}`);
      assert.deepEqual(tabs.padding, ['4px', '4px'], `${label}: the tab row's padding above and below`);
      assert.ok(tabs.current.top - tabs.row.top >= 4 - 0.5 && tabs.row.bottom - tabs.current.bottom >= 4 - 0.5,
        `${label}: the current tab sits closer than 4px to the row's top or bottom, which would clip its focus ring`);
      assert.equal(tabs.currentShadow, `${tabs.accent} 0px 0px 0px 1px inset`, `${label}: the current tab's edge`);
      assert.equal(tabs.otherShadow, 'none', `${label}: another tab has an edge: ${tabs.otherShadow}`);
      await force('.goen-hero__tabs a[aria-current="true"]', ['focus', 'focus-visible']);
      assert.equal(await evaluate(`getComputedStyle(document.querySelector('.goen-hero__tabs a[aria-current="true"]')).outlineOffset`), '2px', `${label}: the focused tab's ring offset`);
      await force('.goen-hero__tabs a[aria-current="true"]', []);

      // The white ring of an ink slide is drawn on its body, which is ink, and
      // never on the white around it: the call to action sits, ring and all,
      // inside the body.
      const ctaSelector = '.goen-hero__slide[data-tone="ink"] .goen-hero__actions a';
      await force(ctaSelector, ['focus', 'focus-visible']);
      const cta = await evaluate(`(() => {
        const link = document.querySelector(${JSON.stringify(ctaSelector)});
        const style = getComputedStyle(link);
        const r = link.getBoundingClientRect();
        const body = link.closest('.goen-hero__body').getBoundingClientRect();
        const reach = parseFloat(style.outlineWidth) + Math.max(0, parseFloat(style.outlineOffset));
        return { colour: style.outlineColor, style: style.outlineStyle,
          room: Math.min(r.left - reach - body.left, body.right - (r.right + reach), r.top - reach - body.top, body.bottom - (r.bottom + reach)) };
      })()`);
      await force(ctaSelector, []);
      assert.deepEqual([cta.style, cta.colour], ['solid', 'rgb(255, 255, 255)'], `${label}: the ink slide's focus ring`);
      assert.ok(cta.room >= 0, `${label}: the ink slide's ring leaves the body by ${-cta.room}px, onto the page white`);
      console.log(`${label}: ${hero.slides.length} slides, ${wide ? 'side by side' : 'stacked'}, panels ${corner} apart by ${seam}px`);
    }
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

  // A department's head lays out as the hero does, inside the page column: the
  // photograph and the copy are two rounded panels with a seam of page white
  // between them, side by side from 48rem and the photograph above the copy
  // below it, and a running campaign's notice is a third panel under the two.
  // The tone is the copy's own panel and nothing behind the photograph, so no
  // white L runs along the photograph's edge. The sub-categories mark the
  // current one as a white pill with a 2px edge inside it, and its focus ring
  // is a separate 2px ring outside that edge.
  {
    const corner = '24px';
    const seam = 8;
    const near = (got, want, tolerance = 0.5) => Math.abs(got - want) <= tolerance;
    const rgb = /^rgb\((\d+), (\d+), (\d+)\)$/;
    const measure = () => evaluate(`(() => {
      const box = element => {
        const r = element.getBoundingClientRect();
        return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width, height: r.height };
      };
      const corners = style => [style.borderTopLeftRadius, style.borderTopRightRadius, style.borderBottomRightRadius, style.borderBottomLeftRadius];
      const colour = value => {
        const probe = document.createElement('i');
        probe.style.color = value;
        document.body.append(probe);
        const got = getComputedStyle(probe).color;
        probe.remove();
        return got;
      };
      const panel = element => {
        const style = getComputedStyle(element);
        return { box: box(element), corners: corners(style), background: style.backgroundColor, shadow: style.boxShadow,
          padding: [style.paddingTop, style.paddingRight, style.paddingBottom, style.paddingLeft].map(parseFloat),
          overflow: style.overflow, border: [style.borderTopWidth, style.borderRightWidth, style.borderBottomWidth, style.borderLeftWidth].join(' ') };
      };
      const page = document.querySelector('.goen-listing');
      const pageStyle = getComputedStyle(page);
      const pageBox = box(page);
      const wrapper = document.querySelector('.goen-listing__head');
      const wrapperStyle = getComputedStyle(wrapper);
      const wrapperBox = box(wrapper);
      const pagehead = wrapper.querySelector('.goen-pagehead');
      const band = pagehead.querySelector('.goen-band');
      const bandStyle = getComputedStyle(band);
      const media = band.querySelector('.goen-band__media');
      const body = band.querySelector('.goen-band__body');
      const notice = band.querySelector('.goen-deptnotice');
      const probe = document.createElement('i');
      probe.style.backgroundColor = 'var(--tone-ground)';
      probe.style.position = 'absolute';
      pagehead.append(probe);
      const ground = getComputedStyle(probe).backgroundColor;
      probe.remove();
      const row = body.querySelector('.goen-pagehead__chips');
      const current = row.querySelector('a[aria-current="page"]');
      const other = row.querySelector('a:not([aria-current])');
      const chip = element => {
        const style = getComputedStyle(element);
        return { background: style.backgroundColor, shadow: style.boxShadow, box: box(element) };
      };
      const rowStyle = getComputedStyle(row);
      const got = {
        viewport: innerWidth,
        column: { left: pageBox.left + parseFloat(pageStyle.paddingLeft), right: pageBox.right - parseFloat(pageStyle.paddingRight) },
        wrapper: { left: wrapperBox.left + parseFloat(wrapperStyle.paddingLeft), right: wrapperBox.right - parseFloat(wrapperStyle.paddingRight) },
        band: { background: bandStyle.backgroundColor, image: bandStyle.backgroundImage },
        ground, accent: colour('var(--accent)'), onInk: colour('var(--on-ink-accent)'),
        body: panel(body), media: media ? { ...panel(media), img: box(media.querySelector('img')) } : null,
        notice: notice ? panel(notice) : null,
        row: { box: box(row), overflowX: rowStyle.overflowX }, current: chip(current), other: chip(other),
      };
      return got;
    })()`);
    const ringOf = async selector => {
      await force(selector, ['focus', 'focus-visible']);
      const got = await evaluate(`(() => {
        const element = document.querySelector(${JSON.stringify(selector)});
        const style = getComputedStyle(element);
        const r = element.getBoundingClientRect();
        return { style: style.outlineStyle, width: style.outlineWidth, offset: style.outlineOffset, colour: style.outlineColor,
          box: { left: r.left, right: r.right, top: r.top, bottom: r.bottom } };
      })()`);
      await force(selector, []);
      return got;
    };
    // How far a ring leaves a panel: negative when the ring stays inside it.
    const overhang = (ring, within) => {
      const reach = parseFloat(ring.width) + Math.max(0, parseFloat(ring.offset));
      return Math.max(within.left - (ring.box.left - reach), ring.box.right + reach - within.right,
        within.top - (ring.box.top - reach), ring.box.bottom + reach - within.bottom);
    };
    const cases = [
      { tone: 'ink', photo: 1, notice: 1 },
      { tone: 'sage', photo: 1, notice: 0 },
      { tone: 'sage', photo: 0, notice: 0 },
    ];
    for (const width of [375, 800, 1024, 1440]) {
      for (const c of cases) {
        const label = `department head ${c.tone}${c.photo ? '' : ' without a photograph'}${c.notice ? ' with a notice' : ''} ${width}px`;
        await open(`/department?tone=${c.tone}&photo=${c.photo}&notice=${c.notice}`, width);
        const head = await measure();
        const [, red, green, blue] = rgb.exec(head.ground) ?? [];
        assert.ok(red !== undefined, `${label}: tone ground ${head.ground}`);
        const sum = Number(red) + Number(green) + Number(blue);
        if (c.tone === 'ink') assert.ok(sum < 150, `${label}: ink ground is ${head.ground}`);
        else assert.ok(sum > 500 && sum < 765, `${label}: ground is ${head.ground}, not a light tone`);

        // Inside the page column, the panels spanning it, and nothing behind them.
        assert.ok(near(head.wrapper.left, head.column.left) && near(head.wrapper.right, head.column.right),
          `${label}: the head is ${head.wrapper.left}-${head.wrapper.right}, the page column ${head.column.left}-${head.column.right}`);
        assert.equal(head.band.background, 'rgba(0, 0, 0, 0)', `${label}: the band has a ground of ${head.band.background}`);
        assert.equal(head.band.image, 'none', `${label}: the band paints ${head.band.image} behind its panels`);
        const panels = [head.body, head.media, head.notice].filter(Boolean);
        assert.ok(near(Math.min(...panels.map(p => p.box.left)), head.column.left) && near(Math.max(...panels.map(p => p.box.right)), head.column.right),
          `${label}: the panels do not span the page column`);
        for (const p of panels) {
          assert.deepEqual(p.corners, [corner, corner, corner, corner], `${label}: panel corners ${p.corners}`);
          assert.equal(p.border, '0px 0px 0px 0px', `${label}: a panel has a border: ${p.border}`);
        }

        // The copy is the tone; a corner is never larger than the padding inside it.
        assert.equal(head.body.background, head.ground, `${label}: the copy is ${head.body.background}, the tone ground is ${head.ground}`);
        assert.equal(head.body.shadow, 'none', `${label}: the copy has a ring: ${head.body.shadow}`);
        const inline = Math.min(48, Math.max(24, head.viewport * 0.035));
        assert.ok(near(head.body.padding[0], 24) && near(head.body.padding[2], 24), `${label}: copy padding above and below ${head.body.padding}`);
        assert.ok(near(head.body.padding[1], inline) && near(head.body.padding[3], inline), `${label}: copy padding beside ${head.body.padding}, want ${inline}px`);
        if (c.notice) {
          assert.equal(head.notice.background, head.ground, `${label}: the notice is ${head.notice.background}, the tone ground is ${head.ground}`);
          assert.ok(Math.min(...head.notice.padding) >= 24, `${label}: the notice's padding ${head.notice.padding} is less than its corner`);
          const above = Math.max(head.body.box.bottom, head.media?.box.bottom ?? 0);
          assert.ok(near(head.notice.box.top - above, seam), `${label}: ${head.notice.box.top - above}px between the notice and the panels above, want ${seam}px`);
        }

        if (!c.photo) {
          assert.equal(head.media, null, `${label}: a photograph is drawn`);
          assert.ok(near(head.body.box.left, head.column.left) && near(head.body.box.right, head.column.right), `${label}: the copy does not fill the column`);
        } else {
          const { media: photo } = head;
          assert.equal(photo.overflow, 'hidden', `${label}: the photograph's panel overflow is ${photo.overflow}, so its corners do not clip the picture`);
          assert.deepEqual(photo.padding, [0, 0, 0, 0], `${label}: the photograph's panel has padding ${photo.padding}`);
          for (const edge of ['left', 'right', 'top', 'bottom']) {
            assert.ok(near(photo.img[edge], photo.box[edge], 1), `${label}: the picture's ${edge} is ${photo.img[edge]}, its panel's ${photo.box[edge]}`);
          }
          if (width >= 768) {
            assert.ok(photo.box.left < head.body.box.left, `${label}: the photograph is not to the left of the copy`);
            assert.ok(near(head.body.box.left - photo.box.right, seam), `${label}: ${head.body.box.left - photo.box.right}px between the panels, want ${seam}px`);
            assert.ok(near(photo.box.top, head.body.box.top, 1) && near(photo.box.bottom, head.body.box.bottom, 1), `${label}: the two panels are not the same height`);
            const whole = head.column.right - head.column.left - seam;
            assert.ok(near(photo.box.width, whole * 7 / 12, 1) && near(head.body.box.width, whole * 5 / 12, 1),
              `${label}: photograph ${photo.box.width}px and copy ${head.body.box.width}px, not 7 and 5 of ${whole}px`);
          } else {
            assert.ok(photo.box.top < head.body.box.top, `${label}: the photograph is not above the copy`);
            assert.ok(near(head.body.box.top - photo.box.bottom, seam), `${label}: ${head.body.box.top - photo.box.bottom}px between the panels, want ${seam}px`);
            for (const p of [photo, head.body]) assert.ok(near(p.box.left, head.column.left) && near(p.box.right, head.column.right), `${label}: a panel does not span the column`);
          }
        }

        // The current sub-category: a white pill (a clear one on ink) with a 2px edge inside it; the others have neither.
        const edge = c.tone === 'ink' ? head.onInk : head.accent;
        assert.equal(head.current.shadow, `${edge} 0px 0px 0px 2px inset`, `${label}: the current sub-category's edge`);
        assert.equal(head.current.background, c.tone === 'ink' ? 'rgba(255, 255, 255, 0.1)' : 'rgb(255, 255, 255)', `${label}: the current sub-category's ground`);
        assert.equal(head.other.shadow, 'none', `${label}: another sub-category has an edge: ${head.other.shadow}`);
        assert.equal(head.other.background, 'rgba(0, 0, 0, 0)', `${label}: another sub-category is filled: ${head.other.background}`);

        // Its focus ring is the shared 2px ring, drawn outside the edge with the
        // ground between them, and the scrolling row leaves it room to be seen.
        const chipRing = await ringOf('.goen-pagehead__chip[aria-current="page"]');
        assert.deepEqual([chipRing.style, chipRing.width, chipRing.offset], ['solid', '2px', '2px'], `${label}: the focused sub-category's ring`);
        if (c.tone === 'ink') assert.notEqual(chipRing.colour, head.onInk, `${label}: the ring is the edge's own colour`);
        if (head.row.overflowX !== 'visible') {
          const room = Math.min(head.current.box.top - head.row.box.top, head.row.box.bottom - head.current.box.bottom, head.current.box.left - head.row.box.left);
          assert.ok(room >= 4 - 0.5, `${label}: the scrolling row leaves ${room}px round the first sub-category, which would clip its ring`);
        }
        // The white ring of an ink head sits on the ink: the sub-categories' and the notice's stay inside their panels.
        if (c.tone === 'ink') {
          assert.equal(chipRing.colour, 'rgb(255, 255, 255)', `${label}: the ink head's ring is ${chipRing.colour}`);
          assert.ok(overhang(chipRing, head.body.box) <= 0, `${label}: the ring leaves the copy by ${overhang(chipRing, head.body.box)}px, onto the page white`);
          if (c.notice) {
            const noticeRing = await ringOf('.goen-deptnotice__name');
            assert.equal(noticeRing.colour, 'rgb(255, 255, 255)', `${label}: the notice's ring is ${noticeRing.colour}`);
            assert.ok(overhang(noticeRing, head.notice.box) <= 0, `${label}: the notice's ring leaves its panel by ${overhang(noticeRing, head.notice.box)}px, onto the page white`);
          }
        }

        // Forced colours drop the tone, so each panel draws an edge of its own.
        await media([{ name: 'forced-colors', value: 'active' }]);
        const edges = await evaluate(`['.goen-pagehead .goen-band__media', '.goen-pagehead .goen-band__body', '.goen-pagehead .goen-band > .goen-deptnotice']
          .map(selector => document.querySelector(selector)).filter(Boolean).map(element => {
            const style = getComputedStyle(element);
            return [style.outlineStyle, style.outlineWidth, style.outlineOffset].join(' ');
          })`);
        await media([]);
        assert.equal(edges.length, 1 + c.photo + c.notice, `${label}: panels drawn in forced colours`);
        for (const edgeOf of edges) assert.equal(edgeOf, 'solid 1px -1px', `${label}: a panel's edge in forced colours is ${edgeOf}`);
        console.log(`${label}: ${panels.length} panels ${corner} in the column, ${c.photo ? (width >= 768 ? 'side by side' : 'stacked') : 'copy only'}, seam ${seam}px, edge 2px`);
      }
    }
  }

  // Six Chinese sub-category names stay on one line beside the photograph at
  // 1400px and 1440px, so the last one is never left alone on a second line.
  // Narrower windows, where the row wraps as it did before, are not asserted.
  for (const width of [1400, 1440]) {
    const label = `department head sub-categories ${width}px`;
    await open('/department?tone=sage&photo=1&zh=1', width);
    const tops = await evaluate(`[...document.querySelectorAll('.goen-listing__head .goen-pagehead__chip')].map(chip => chip.getBoundingClientRect().top)`);
    assert.equal(tops.length, 6, `${label}: six sub-categories drawn`);
    assert.ok(tops.every(top => Math.abs(top - tops[0]) < 1), `${label}: the sub-categories are on more than one line: ${tops}`);
    console.log(`${label}: six names on one line`);
  }

  // The home page's shelf is one rounded panel on the department's tone inside
  // the page column, and its sideways scroller of products is cut at the
  // panel's straight edge.
  {
    const corner = '24px';
    const near = (got, want, tolerance = 0.5) => Math.abs(got - want) <= tolerance;
    for (const width of [375, 800, 1024, 1440]) {
      for (const tone of ['ink', 'sage']) {
        const label = `home shelf ${tone} ${width}px`;
        await open(`/home?band=${tone}`, width);
        const shelf = await evaluate(`(() => {
          const box = element => { const r = element.getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top }; };
          const home = document.querySelector('.goen-home');
          const homeStyle = getComputedStyle(home);
          const homeBox = box(home);
          const panel = document.querySelector('.goen-band--shelf');
          const style = getComputedStyle(panel);
          const body = panel.querySelector('.goen-band__body');
          const bodyStyle = getComputedStyle(body);
          const tiles = panel.querySelector('.goen-band__tiles');
          const probe = document.createElement('i');
          probe.style.backgroundColor = 'var(--tone-ground)';
          probe.style.position = 'absolute';
          panel.append(probe);
          const ground = getComputedStyle(probe).backgroundColor;
          probe.remove();
          return {
            viewport: innerWidth,
            column: { left: homeBox.left + parseFloat(homeStyle.paddingLeft), right: homeBox.right - parseFloat(homeStyle.paddingRight) },
            panel: box(panel), width: panel.getBoundingClientRect().width,
            corners: [style.borderTopLeftRadius, style.borderTopRightRadius, style.borderBottomRightRadius, style.borderBottomLeftRadius],
            background: style.backgroundColor, ground,
            padding: [bodyStyle.paddingTop, bodyStyle.paddingRight, bodyStyle.paddingBottom, bodyStyle.paddingLeft].map(parseFloat),
            tiles: { box: box(tiles), overflowX: getComputedStyle(tiles).overflowX },
            columns: bodyStyle.gridTemplateColumns.trim().split(' ').length,
          };
        })()`);
        assert.ok(near(shelf.panel.left, shelf.column.left) && near(shelf.panel.right, shelf.column.right),
          `${label}: the shelf is ${shelf.panel.left}-${shelf.panel.right}, the page column ${shelf.column.left}-${shelf.column.right}`);
        assert.ok(shelf.panel.left > 0 && shelf.panel.right < shelf.viewport, `${label}: the shelf runs edge to edge`);
        assert.deepEqual(shelf.corners, [corner, corner, corner, corner], `${label}: corners ${shelf.corners}`);
        assert.equal(shelf.background, shelf.ground, `${label}: the shelf is ${shelf.background}, the tone ground is ${shelf.ground}`);
        const inline = Math.min(48, Math.max(24, shelf.viewport * 0.04));
        assert.ok(shelf.padding[0] >= 24 && shelf.padding[2] >= 24, `${label}: padding above and below ${shelf.padding}, less than the corner`);
        assert.ok(near(shelf.padding[1], inline) && near(shelf.padding[3], inline), `${label}: padding beside ${shelf.padding}, want ${inline}px`);
        // The layout switches at the window widths it did before the shelf had a panel: a row of four from 768px, six columns from 960px.
        assert.equal(shelf.tiles.overflowX === 'auto', width < 768, `${label}: the products ${shelf.tiles.overflowX === 'auto' ? 'scroll' : 'are a grid'}`);
        assert.equal(shelf.columns, width >= 960 ? 6 : 1, `${label}: ${shelf.columns} columns`);
        if (shelf.tiles.overflowX === 'auto') {
          assert.ok(near(shelf.tiles.box.left, shelf.panel.left) && near(shelf.tiles.box.right, shelf.panel.right),
            `${label}: the scroller is ${shelf.tiles.box.left}-${shelf.tiles.box.right}, the shelf ${shelf.panel.left}-${shelf.panel.right}`);
        } else {
          assert.ok(shelf.tiles.box.left >= shelf.panel.left + inline - 0.5 && shelf.tiles.box.right <= shelf.panel.right - inline + 0.5,
            `${label}: the products reach the shelf's padding`);
        }
        await media([{ name: 'forced-colors', value: 'active' }]);
        const edge = await evaluate(`(() => {
          const style = getComputedStyle(document.querySelector('.goen-band--shelf'));
          return [style.outlineStyle, style.outlineWidth, style.outlineOffset, style.borderTopWidth, style.borderBottomWidth].join(' ');
        })()`);
        await media([]);
        assert.equal(edge, 'solid 1px -1px 0px 0px', `${label}: the shelf's edge in forced colours is ${edge}`);
        console.log(`${label}: panel ${corner} in the page column, ${shelf.tiles.overflowX === 'auto' ? 'products scroll to its edge' : 'products in a grid'}`);
      }
    }
  }

  // A chosen option, a chosen delivery card and a refused field are marked in
  // one language: a tinted fill, a 2px edge and a check for what is chosen; a
  // white face and a 1px edge for what is not; the error colour and an icon for
  // what was refused. Fields share one 12px corner, and the checkout is two
  // layers, a white page and the groups on it.
  {
    const label = 'selected state';
    const rgb = text => {
      const got = /^rgba?\((\d+),\s*(\d+),\s*(\d+)(?:,\s*([\d.]+))?\)$/.exec(text);
      assert.ok(got, `${label}: ${text} is not an rgb colour`);
      assert.equal(got[4] ?? '1', '1', `${label}: ${text} is not opaque`);
      return [got[1], got[2], got[3]].map(Number);
    };
    // WCAG 2.2 relative luminance: each sRGB channel c/255 becomes c/12.92 when
    // at most 0.04045 and ((c + 0.055) / 1.055) ** 2.4 above it, then
    // L = 0.2126 R + 0.7152 G + 0.0722 B. The ratio is (lighter + 0.05) / (darker + 0.05).
    const luminance = channels => {
      const [r, g, b] = channels.map(value => {
        const c = value / 255;
        return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
      });
      return 0.2126 * r + 0.7152 * g + 0.0722 * b;
    };
    const ratio = (a, b) => {
      const [light, dark] = [luminance(rgb(a)), luminance(rgb(b))].sort((x, y) => y - x);
      return (light + 0.05) / (dark + 0.05);
    };
    // A probe turns a custom property, written as any colour syntax, into the
    // rgb() text the browser paints it as, so no expected colour is typed here.
    const tokens = names => evaluate(`Object.fromEntries(${JSON.stringify(names)}.map(name => {
      const probe = document.createElement('div');
      probe.style.position = 'absolute';
      probe.style.visibility = 'hidden';
      probe.style.backgroundColor = 'var(' + name + ')';
      document.body.append(probe);
      const colour = getComputedStyle(probe).backgroundColor;
      probe.remove();
      return [name, colour];
    }))`);
    const sides = ['Top', 'Right', 'Bottom', 'Left'];
    const corners = ['TopLeft', 'TopRight', 'BottomRight', 'BottomLeft'];
    const rootSize = size => evaluate(`document.documentElement.style.fontSize = ${JSON.stringify(size)}`);
    const names = ['--n-0', '--n-50', '--n-500', '--accent', '--accent-faint', '--accent-deep', '--control-boundary', '--error', '--fg'];

    // Fields share one corner, in px: a corner in rem would grow with the text
    // and turn a tall textarea into a pill.
    for (const size of ['', '200%']) {
      const where = `field corners at ${size || '100%'}`;
      const measure = selectors => evaluate(`Object.fromEntries(${JSON.stringify(selectors)}.map(selector => {
        const element = document.querySelector(selector);
        if (!element) return [selector, null];
        const style = getComputedStyle(element);
        return [selector, ${JSON.stringify(corners)}.map(corner => style['border' + corner + 'Radius'])];
      }))`);
      const expectCorners = (got, page) => {
        for (const [selector, radii] of Object.entries(got)) {
          assert.ok(radii, `${where}: ${selector} is not on ${page}`);
          assert.deepEqual(radii, Array(4).fill('12px'), `${where}: ${selector} on ${page} has corners ${radii}, not 12px`);
        }
      };
      await open('/checkout', 1440);
      await rootSize(size);
      expectCorners(await measure(['#name', '#note', '#address-book']), '/checkout');
      await open('/search', 1440);
      await rootSize(size);
      expectCorners(await measure(['#sort']), '/search');
      await open('/c/audio', 1440);
      await rootSize(size);
      expectCorners(await measure(['#min-price']), '/c/audio');
    }
    console.log(`${label}: inputs, textarea, selects and the filter and sort controls have 12px corners at 100% and 200% text`);

    // A refused field: its name takes the error colour, the sentence that says
    // why leads with an icon, and a field nobody refused is left alone.
    for (const width of [375, 1440]) {
      const where = `refused field ${width}px`;
      await open('/checkout', width);
      const t = await tokens(names);
      const got = await evaluate(`(() => {
        const colour = selector => getComputedStyle(document.querySelector(selector)).color;
        const sentence = document.querySelector('#email-error');
        const icon = getComputedStyle(sentence, '::before');
        const group = document.querySelector('#address');
        return { refused: colour('label[for="email"]'), other: colour('label[for="name"]'),
          sentence: colour('#email-error'), text: sentence.textContent.trim(),
          invalid: document.querySelector('#email').getAttribute('aria-invalid'),
          content: icon.content, display: icon.display, image: icon.backgroundImage, width: parseFloat(icon.width),
          ground: getComputedStyle(group).backgroundColor };
      })()`);
      assert.equal(got.invalid, 'true', `${where}: the email field is not marked refused`);
      assert.ok(got.text.length > 0, `${where}: the refusal has no sentence`);
      assert.equal(got.refused, t['--error'], `${where}: the refused field's name is ${got.refused}, not the error colour ${t['--error']}`);
      assert.equal(got.other, t['--fg'], `${where}: a field nobody refused has the name colour ${got.other}, not ${t['--fg']}`);
      assert.equal(got.sentence, t['--error'], `${where}: the sentence is ${got.sentence}, not the error colour`);
      assert.equal(got.content, '""', `${where}: no icon before the sentence (content ${got.content})`);
      assert.ok(got.display === 'inline-block' && got.width > 0, `${where}: the icon is ${got.display} and ${got.width}px wide`);
      assert.match(got.image, /^url\("data:image\/svg\+xml,/, `${where}: the icon is not a data: image: ${got.image}`);
      // The icon is an image, so it cannot read --error: its stroke is decoded
      // from the image itself and must paint as the error colour.
      const drawn = /^url\("?data:image\/svg\+xml,(.*?)"?\)$/.exec(got.image);
      const stroke = drawn && /stroke=['"]([^'"]+)['"]/.exec(decodeURIComponent(drawn[1]));
      assert.ok(stroke, `${where}: the icon has no stroke colour to read: ${got.image}`);
      const strokeColour = await evaluate(`(() => {
        const probe = document.createElement('div');
        probe.style.backgroundColor = ${JSON.stringify(stroke[1])};
        document.body.append(probe);
        const colour = getComputedStyle(probe).backgroundColor;
        probe.remove();
        return colour;
      })()`);
      assert.equal(strokeColour, t['--error'], `${where}: the icon is drawn in ${stroke[1]} (${strokeColour}), not the error colour ${t['--error']}`);
      assert.equal(got.ground, t['--n-50'], `${where}: the group the field sits in is ${got.ground}`);
      for (const [what, colour] of [['name', got.refused], ['sentence', got.sentence]]) {
        assert.ok(ratio(colour, got.ground) >= 4.5, `${where}: the refused ${what} ${colour} on ${got.ground} is ${ratio(colour, got.ground).toFixed(2)}:1, under 4.5:1`);
      }
      console.log(`${where}: name and sentence ${ratio(got.refused, got.ground).toFixed(2)}:1 in the error colour on the group, icon drawn`);
    }

    // The text option: white with a 1px edge, or tinted with a 2px edge and a
    // check. Choosing one does not move the text or change the height.
    const swatches = ids => evaluate(`${JSON.stringify(ids)}.map(id => {
      const element = document.getElementById(id);
      if (!element) return null;
      const style = getComputedStyle(element);
      const before = getComputedStyle(element, '::before');
      const box = element.getBoundingClientRect();
      const words = element.querySelector('s') ?? (() => {
        const node = [...element.childNodes].find(child => child.nodeType === 3 && child.textContent.trim());
        const range = document.createRange();
        range.selectNodeContents(node);
        return range;
      })();
      const text = words.getBoundingClientRect();
      const note = element.querySelector('.goen-swatch__note');
      let parent = element.parentElement, ground = 'rgba(0, 0, 0, 0)';
      for (; parent && /, 0\\)$/.test(ground); parent = parent.parentElement) ground = getComputedStyle(parent).backgroundColor;
      const tick = ['none', 'normal'].includes(before.content) ? null : {
        content: before.content, position: before.position,
        widths: ['Right', 'Bottom', 'Top', 'Left'].map(side => before['border' + side + 'Width']),
        colour: before.borderBottomColor };
      return { id, current: element.getAttribute('aria-current'), height: box.height, left: box.left,
        background: style.backgroundColor, color: style.color, shadow: style.boxShadow, outline: style.outlineStyle,
        radius: style.borderTopLeftRadius, borderStyle: style.borderTopStyle,
        widths: ${JSON.stringify(sides)}.map(side => style['border' + side + 'Width']),
        colours: ${JSON.stringify(sides)}.map(side => style['border' + side + 'Color']),
        tick, textLeft: text.left, textRight: text.right, noteColour: note && getComputedStyle(note).color,
        ground, inside: box.left >= -0.5 && box.right <= innerWidth + 0.5 };
    })`);
    for (const size of ['', '200%']) {
      for (const width of [320, 375, 1440]) {
        const where = `text option ${width}px at ${size || '100%'}`;
        await open('/product?options=in', width);
        await rootSize(size);
        const t = await tokens(names);
        const press = await evaluate(`(() => {
          const probe = document.createElement('div');
          probe.style.borderTopLeftRadius = 'var(--r-press)';
          document.body.append(probe);
          const got = getComputedStyle(probe).borderTopLeftRadius;
          probe.remove();
          return got;
        })()`);
        const [plain, chosen, soldOut] = await swatches(['swatch-0-0', 'swatch-0-1', 'swatch-0-2']);
        for (const [name, got] of [['plain', plain], ['chosen', chosen], ['sold out', soldOut]]) assert.ok(got, `${where}: no ${name} option`);
        assert.equal(chosen.current, 'true', `${where}: the second option is not the current one`);
        assert.equal(plain.background, t['--n-0'], `${where}: an unchosen option is ${plain.background}, not white`);
        assert.deepEqual(plain.widths, Array(4).fill('1px'), `${where}: an unchosen edge is ${plain.widths}`);
        assert.deepEqual(plain.colours, Array(4).fill(t['--control-boundary']), `${where}: an unchosen edge is ${plain.colours}, not the control edge ${t['--control-boundary']}`);
        assert.equal(plain.radius, press, `${where}: an unchosen option has corner ${plain.radius}, not --r-press ${press}`);
        assert.equal(plain.tick, null, `${where}: an unchosen option is ticked`);
        assert.equal(chosen.background, t['--accent-faint'], `${where}: the chosen option is ${chosen.background}, not the tint ${t['--accent-faint']}`);
        assert.deepEqual(chosen.widths, Array(4).fill('2px'), `${where}: the chosen edge is ${chosen.widths}`);
        assert.deepEqual(chosen.colours, Array(4).fill(t['--accent']), `${where}: the chosen edge is ${chosen.colours}, not the accent ${t['--accent']}`);
        assert.equal(chosen.color, t['--accent-deep'], `${where}: the chosen text is ${chosen.color}, not ${t['--accent-deep']}`);
        assert.equal(chosen.shadow, 'none', `${where}: the chosen option draws its edge with a shadow: ${chosen.shadow}`);
        assert.equal(chosen.radius, press, `${where}: the chosen option has corner ${chosen.radius}, not --r-press ${press}`);
        assert.ok(Math.abs(chosen.height - plain.height) < 0.5, `${where}: choosing resizes the option from ${plain.height}px to ${chosen.height}px`);
        assert.ok(chosen.tick, `${where}: the chosen option has no check`);
        assert.deepEqual([chosen.tick.content, chosen.tick.position, chosen.tick.widths.slice(0, 2)], ['""', 'absolute', ['2px', '2px']], `${where}: the check is ${JSON.stringify(chosen.tick)}`);
        assert.equal(chosen.tick.colour, chosen.color, `${where}: the check is ${chosen.tick.colour}, not the text colour ${chosen.color}`);
        assert.ok(chosen.inside && plain.inside, `${where}: an option is cut by the edge of the screen`);
        // Contrast, by the formula above: the edge against the page it sits
        // on (1.4.11, at least 3:1), the text against the tint (1.4.3, at
        // least 4.5:1) and the edge against the tint it encloses.
        const edge = ratio(chosen.colours[0], chosen.ground), text = ratio(chosen.color, chosen.background), inner = ratio(chosen.colours[0], chosen.background);
        assert.ok(edge >= 3, `${where}: the chosen edge on the page is ${edge.toFixed(2)}:1, under 3:1`);
        assert.ok(text >= 4.5, `${where}: the chosen text on the tint is ${text.toFixed(2)}:1, under 4.5:1`);
        assert.ok(inner >= 3, `${where}: the chosen edge on the tint is ${inner.toFixed(2)}:1, under 3:1`);
        // The check is a box drawn after the turn: its left stays inside the
        // option and its right stays clear of the words.
        const tick = await evaluate(`(() => {
          const element = document.getElementById('swatch-0-1');
          const box = element.getBoundingClientRect();
          const tick = getComputedStyle(element, '::before');
          const inset = parseFloat(tick.left), width = parseFloat(tick.width), height = parseFloat(tick.height);
          const left = box.left + parseFloat(getComputedStyle(element).borderLeftWidth) + inset;
          const turned = (width + height) * Math.SQRT1_2;
          return { left: left + width / 2 - turned / 2, right: left + width / 2 + turned / 2, box: box.left };
        })()`);
        assert.ok(tick.left >= tick.box, `${where}: the check starts ${tick.box - tick.left}px outside the option`);
        assert.ok(tick.right <= chosen.textLeft - 2, `${where}: the check reaches ${tick.right}px, over the words that start at ${chosen.textLeft}px`);
        console.log(`${where}: chosen edge ${edge.toFixed(2)}:1, text ${text.toFixed(2)}:1, edge on tint ${inner.toFixed(2)}:1, check clear of the words by ${(chosen.textLeft - tick.right).toFixed(1)}px`);
      }
    }
    {
      await open('/product?options=in', 1440);
      const t = await tokens(names);
      const hovered = id => evaluate(`getComputedStyle(document.getElementById(${JSON.stringify(id)})).backgroundColor`);
      await force('#swatch-0-1', ['hover']);
      assert.equal(await hovered('swatch-0-1'), t['--accent-faint'], `${label}: the chosen option loses its tint under the pointer`);
      await force('#swatch-0-1', []);
      console.log(`${label}: the chosen option keeps its tint under the pointer`);
    }

    // A chosen option that is sold out is chosen the same way, still dashed,
    // and holds what product-options-check measures: a light fill, an edge of
    // at least 3:1 and words of at least 4.5:1.
    for (const size of ['', '200%']) {
      for (const width of [320, 1440]) {
        const where = `sold-out chosen option ${width}px at ${size || '100%'}`;
        await open('/product?options=out', width);
        await rootSize(size);
        const t = await tokens(names);
        const [got] = await swatches(['swatch-0-1']);
        assert.ok(got, `${where}: no chosen option`);
        assert.equal(got.current, 'true', `${where}: not the current option`);
        assert.equal(got.background, t['--accent-faint'], `${where}: the fill is ${got.background}, not the tint`);
        assert.ok(rgb(got.background).every(channel => channel > 200), `${where}: the fill ${got.background} is not light`);
        assert.deepEqual(got.widths, Array(4).fill('2px'), `${where}: the edge is ${got.widths}`);
        assert.deepEqual(got.colours, Array(4).fill(t['--accent']), `${where}: the edge is ${got.colours}, not the accent`);
        assert.equal(got.borderStyle, 'dashed', `${where}: the edge is ${got.borderStyle}, so a sold-out option no longer looks sold out`);
        assert.equal(got.shadow, 'none', `${where}: a shadow draws the edge: ${got.shadow}`);
        const edge = ratio(got.colours[0], got.ground), text = ratio(got.color, got.background), note = ratio(got.noteColour, got.background);
        assert.ok(edge >= 3, `${where}: the edge on the page is ${edge.toFixed(2)}:1, under 3:1`);
        assert.ok(text >= 4.5, `${where}: the words on the fill are ${text.toFixed(2)}:1, under 4.5:1`);
        assert.ok(note >= 4.5, `${where}: the sold-out note on the fill is ${note.toFixed(2)}:1, under 4.5:1`);
        assert.ok(got.inside, `${where}: the option is cut by the edge of the screen`);
        console.log(`${where}: dashed accent edge ${edge.toFixed(2)}:1, words ${text.toFixed(2)}:1, note ${note.toFixed(2)}:1 on the tint`);
      }
    }

    // The delivery card: the same language, with its radio drawn in every
    // scheme, and a name that keeps room for words at any text size.
    const cards = ids => evaluate(`${JSON.stringify(ids)}.map(id => {
      const radio = document.getElementById(id);
      const card = radio?.closest('.goen-checkout__ship');
      if (!card) return null;
      const style = getComputedStyle(card);
      const box = card.getBoundingClientRect();
      const name = card.querySelector('.goen-checkout__shipname');
      const nameBox = name.getBoundingClientRect();
      const dot = radio.getBoundingClientRect();
      const radioStyle = getComputedStyle(radio);
      return { checked: radio.checked, background: style.backgroundColor, shadow: style.boxShadow,
        widths: ${JSON.stringify(sides)}.map(side => style['border' + side + 'Width']),
        colours: ${JSON.stringify(sides)}.map(side => style['border' + side + 'Color']),
        nameLeft: nameBox.left - box.left, nameTop: nameBox.top - box.top, nameWidth: nameBox.width,
        em: parseFloat(getComputedStyle(name).fontSize),
        radioDrawn: dot.width >= 8 && dot.height >= 8 && radioStyle.clipPath === 'none' &&
          radioStyle.visibility === 'visible' && Number(radioStyle.opacity) === 1,
        forced: matchMedia('(forced-colors: active)').matches,
        inside: box.left >= -0.5 && box.right <= innerWidth + 0.5 };
    })`);
    for (const size of ['', '200%']) {
      for (const width of [320, 375, 1440]) {
        const where = `delivery card ${width}px at ${size || '100%'}`;
        await open('/checkout', width);
        await rootSize(size);
        const t = await tokens(names);
        const [chosen, plain] = await cards(['shipping-home', 'shipping-store']);
        assert.ok(chosen && plain, `${where}: the two cards are not on the page`);
        assert.equal(chosen.checked, true, `${where}: the first card is not the chosen one`);
        assert.equal(chosen.forced, false, `${where}: forced colours are on, so this does not measure the ordinary look`);
        assert.equal(chosen.background, t['--accent-faint'], `${where}: the chosen card is ${chosen.background}, not the tint`);
        assert.deepEqual(chosen.widths, Array(4).fill('2px'), `${where}: the chosen edge is ${chosen.widths}`);
        assert.deepEqual(chosen.colours, Array(4).fill(t['--accent']), `${where}: the chosen edge is ${chosen.colours}, not the accent`);
        assert.equal(chosen.shadow, 'none', `${where}: a shadow draws the chosen edge: ${chosen.shadow}`);
        assert.equal(plain.background, t['--n-0'], `${where}: an unchosen card is ${plain.background}, not white`);
        assert.deepEqual(plain.widths, Array(4).fill('1px'), `${where}: an unchosen edge is ${plain.widths}`);
        assert.equal(chosen.radioDrawn && plain.radioDrawn, true, `${where}: a radio is not drawn: chosen ${chosen.radioDrawn}, unchosen ${plain.radioDrawn}`);
        assert.ok(Math.abs(chosen.nameLeft - plain.nameLeft) < 0.5, `${where}: choosing moves the name from ${plain.nameLeft}px to ${chosen.nameLeft}px`);
        for (const [what, card] of [['chosen', chosen], ['unchosen', plain]]) {
          assert.ok(card.nameWidth >= 2.5 * card.em, `${where}: the ${what} card leaves its name ${card.nameWidth}px, under two and a half letters of ${card.em}px`);
          assert.ok(card.inside, `${where}: the ${what} card is cut by the edge of the screen`);
        }
        // Every choice card this page renders shares the padding: the two
        // delivery cards here, and the invoice and pickup ones where a fixture
        // shows them.
        const choices = await evaluate(`[...document.querySelectorAll('.goen-checkout__ship')]
          .filter(card => card.getClientRects().length > 0)
          .map(card => {
            const name = card.querySelector('.goen-checkout__shipname');
            return { text: name.textContent.trim().slice(0, 24), width: name.getBoundingClientRect().width,
              em: parseFloat(getComputedStyle(name).fontSize) };
          })`);
        assert.ok(choices.length >= 2, `${where}: ${choices.length} choice cards are on the page, not at least the two delivery ones`);
        for (const card of choices) {
          assert.ok(card.width >= 2.5 * card.em, `${where}: the card "${card.text}" leaves its name ${card.width}px, under two and a half letters of ${card.em}px`);
        }
        console.log(`${where}: tint, 2px accent edge, radio drawn, name ${chosen.nameWidth.toFixed(0)}px wide, narrowest of ${choices.length} cards ${Math.min(...choices.map(card => card.width)).toFixed(0)}px`);
      }
    }
    // Forced colours replace the fills, so the chosen card is the 2px edge
    // among 1px ones and the radio, which the page draws in every scheme.
    await open('/checkout', 1440);
    await media([{ name: 'forced-colors', value: 'active' }]);
    {
      const [chosen, plain] = await cards(['shipping-home', 'shipping-store']);
      assert.equal(chosen.forced, true, `${label}: forced colours did not activate`);
      assert.equal(chosen.radioDrawn && plain.radioDrawn, true, `${label}: forced colours: a radio is not drawn`);
      assert.deepEqual([chosen.widths[0], plain.widths[0]], ['2px', '1px'], `${label}: forced colours: edges ${chosen.widths[0]} and ${plain.widths[0]}`);
    }
    await open('/product?options=in', 1440);
    await media([{ name: 'forced-colors', value: 'active' }]);
    {
      const [plain, chosen] = await swatches(['swatch-0-0', 'swatch-0-1']);
      assert.ok(chosen.tick, `${label}: forced colours: the chosen option has no check`);
      assert.notEqual(chosen.tick.colour, 'rgba(0, 0, 0, 0)', `${label}: forced colours: the check is transparent`);
      assert.deepEqual(chosen.tick.widths.slice(0, 2), ['2px', '2px'], `${label}: forced colours: the check is ${chosen.tick.widths}`);
      assert.deepEqual([chosen.widths[0], plain.widths[0]], ['2px', '1px'], `${label}: forced colours: option edges ${chosen.widths[0]} and ${plain.widths[0]}`);
    }
    await media([]);
    console.log(`${label}: forced colours keep the 2px edge, the check and the radio`);

    // Keyboard focus shows one ring, on the card; the radio inside it draws
    // none. A real Tab key moves focus to the chosen radio, so :focus-visible is
    // what a keyboard user gets, and a ring that cannot be measured fails.
    await open('/checkout', 1440);
    await send('Emulation.setFocusEmulationEnabled', { enabled: true });
    try {
      await evaluate(`(() => {
        const heading = document.getElementById('shipping-heading');
        heading.tabIndex = -1;
        heading.focus();
      })()`);
      await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 });
      await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 });
      const ring = await evaluate(`(() => {
        const radio = document.activeElement;
        const card = getComputedStyle(radio.closest('.goen-checkout__ship') ?? radio);
        return { id: radio.id, visible: radio.matches(':focus-visible'), radio: getComputedStyle(radio).outlineStyle,
          card: card.outlineStyle, width: card.outlineWidth };
      })()`);
      assert.equal(ring.id, 'shipping-home', `${label}: focus: Tab from the heading reached ${ring.id || 'nothing'}, not the chosen radio`);
      assert.equal(ring.visible, true, `${label}: focus: the radio reached by Tab is not :focus-visible`);
      assert.equal(ring.radio, 'none', `${label}: focus: the radio draws its own ring inside the card's`);
      assert.deepEqual([ring.card, ring.width], ['solid', '2px'], `${label}: focus: the card's ring is ${ring.width} ${ring.card}`);
      console.log(`${label}: focus: Tab reaches the chosen radio, one 2px ring, on the card`);
    } finally {
      await send('Emulation.setFocusEmulationEnabled', { enabled: false });
    }

    // The checkout is two layers: a white page, and the form's groups and the
    // summary on it as panels with a 16px corner and an edge forced colours draw.
    for (const width of [375, 1440]) {
      const where = `checkout layers ${width}px`;
      await open('/checkout', width);
      const t = await tokens(names);
      const got = await evaluate(`(() => {
        const painted = element => {
          const layers = [];
          for (let node = element; node; node = node.parentElement) {
            const colour = getComputedStyle(node).backgroundColor;
            if (!/, 0\\)$/.test(colour) && !layers.includes(colour)) layers.push(colour);
          }
          return layers;
        };
        const panel = selector => {
          const element = document.querySelector(selector);
          if (!element) return null;
          const style = getComputedStyle(element);
          const shadows = [];
          for (let node = element; node; node = node.parentElement) {
            const shadow = getComputedStyle(node).boxShadow;
            if (shadow !== 'none') shadows.push((node.className || node.tagName) + ': ' + shadow);
          }
          return { layers: painted(element), shadows, background: style.backgroundColor,
            corners: ${JSON.stringify(corners)}.map(corner => style['border' + corner + 'Radius']),
            edges: ${JSON.stringify(sides)}.map(side => style['border' + side + 'Width'] + ' ' + style['border' + side + 'Style'] + ' ' + style['border' + side + 'Color']) };
        };
        const bare = selector => getComputedStyle(document.querySelector(selector)).backgroundColor;
        const field = getComputedStyle(document.querySelector('#name'));
        return { page: bare('.goen-checkout'), main: bare('.goen-checkout__main'),
          groups: ['#shipping', '#address', '#invoice'].map(panel), summary: panel('#summary'),
          field: field.borderTopColor };
      })()`);
      assert.equal(got.page, 'rgba(0, 0, 0, 0)', `${where}: the checkout paints its own page: ${got.page}`);
      assert.equal(got.main, 'rgba(0, 0, 0, 0)', `${where}: the form is a block of ${got.main}`);
      for (const [index, panel] of [...got.groups, got.summary].entries()) {
        assert.ok(panel, `${where}: panel ${index} is not on the page`);
        assert.equal(panel.background, t['--n-50'], `${where}: panel ${index} is ${panel.background}, not ${t['--n-50']}`);
        assert.deepEqual(panel.layers, [t['--n-50'], t['--n-0']], `${where}: panel ${index} sits on ${panel.layers}, not a white page and its own ${t['--n-50']}`);
        assert.deepEqual(panel.corners, Array(4).fill('16px'), `${where}: panel ${index} has corners ${panel.corners}`);
        assert.deepEqual(panel.shadows, [], `${where}: panel ${index} or what holds it draws a shadow, so the page is not white: ${panel.shadows}`);
        assert.deepEqual(panel.edges, Array(4).fill('1px solid rgba(0, 0, 0, 0)'), `${where}: panel ${index} has edges ${panel.edges}`);
      }
      // A field is white on the group, so its edge is read against the group.
      const edge = ratio(got.field, t['--n-50']);
      assert.ok(edge >= 3, `${where}: a field's edge on the group is ${edge.toFixed(2)}:1, under 3:1`);
      console.log(`${where}: white page, ${got.groups.length} groups and the summary on ${t['--n-50']}, 16px corners, field edge ${edge.toFixed(2)}:1`);
    }
  }
} finally {
  socket.close();
  await fetch(`${debugging}/json/close/${target.id}`);
}
