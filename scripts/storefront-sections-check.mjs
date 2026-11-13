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
  // A phone's scrollbar overlays the page; a desktop's takes 15px of it.
  const open = async (path, width, mobile = false) => {
    await send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile });
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

  // The home page's departments are the index the missing page and the empty
  // search draw: a square photograph keeping the 4:3 it is shot in beside the
  // name in the editorial face, one above another on a phone, two across from
  // 768 and three across a desktop. The heading over it is the editorial face
  // too, and the photograph's link is out of the tab order beside the name's.
  for (const [width, columns] of [[375, 1], [768, 2], [1024, 2], [1440, 3]]) {
    await open('/home', width);
    const index = await evaluate(`(() => {
      const items = [...document.querySelectorAll('.goen-deptindex__item')];
      const photos = items.map(item => {
        const link = item.querySelector('.goen-deptindex__photo');
        const box = link.getBoundingClientRect();
        return { width: box.width, height: box.height, radius: getComputedStyle(link).borderTopLeftRadius,
          hidden: link.getAttribute('aria-hidden'), tabindex: link.getAttribute('tabindex'),
          name: getComputedStyle(item.querySelector('.goen-deptindex__name')).fontFamily };
      });
      const heading = document.querySelector('#cats-heading');
      const tops = new Set(items.map(item => Math.round(item.getBoundingClientRect().left)));
      return { photos, columns: tops.size, heading: getComputedStyle(heading).fontFamily,
        old: document.querySelectorAll('.goen-cats, .goen-cat').length };
    })()`);
    assert.equal(index.photos.length, 3, `departments ${width}px: entries drawn`);
    assert.equal(index.columns, columns, `departments ${width}px: ${index.columns} columns, want ${columns}`);
    assert.equal(index.old, 0, `departments ${width}px: the page still draws the grid the index replaced`);
    assert.match(index.heading, /Noto Serif TC/, `departments ${width}px: the heading is set in ${index.heading}`);
    for (const photo of index.photos) {
      assert.ok(photo.width > 0 && Math.abs(photo.height - photo.width * 3 / 4) <= 1, `departments ${width}px: a photograph is ${photo.width}x${photo.height}, not 4:3`);
      assert.equal(photo.radius, '0px', `departments ${width}px: a photograph has a ${photo.radius} corner, not square`);
      assert.deepEqual([photo.hidden, photo.tabindex], ['true', '-1'], `departments ${width}px: the photograph's link is aria-hidden=${photo.hidden} tabindex=${photo.tabindex}`);
      assert.match(photo.name, /Noto Serif TC/, `departments ${width}px: a name is set in ${photo.name}`);
    }
    console.log(`departments ${width}px: ${index.photos.length} square photographs at 4:3 in ${columns} column${columns === 1 ? '' : 's'}, editorial names`);
  }

  // A department without a photograph shows its first letter on its tone, a
  // picture rather than words in a link, so no underline runs under it, and its
  // sub-categories are set apart with the dot between spaces that a screen reader
  // skips.
  for (const width of [375, 1440]) {
    await open('/home?depts=mark', width);
    const mark = await evaluate(`(() => {
      const tile = document.querySelector('.goen-deptindex__photo--mark');
      if (!tile) return null;
      const box = tile.getBoundingClientRect(), style = getComputedStyle(tile);
      const letter = tile.querySelector('.goen-deptindex__mark');
      const subs = document.querySelector('.goen-deptindex__subs');
      return { width: box.width, height: box.height, line: style.textDecorationLine, background: style.backgroundColor,
        hidden: tile.getAttribute('aria-hidden'), tabindex: tile.getAttribute('tabindex'), letter: letter?.textContent,
        letterLine: letter && getComputedStyle(letter).textDecorationLine, size: letter && parseFloat(getComputedStyle(letter).fontSize),
        seps: subs ? [...subs.querySelectorAll('.goen-sep')].map(sep => sep.getAttribute('aria-hidden')) : [],
        text: subs?.textContent.replace(/\\s+/g, ' ').trim() };
    })()`);
    const where = `department without a photograph ${width}px`;
    assert.ok(mark, `${where}: no mark drawn`);
    assert.equal(mark.letter, 'B', `${where}: the mark reads ${mark.letter}`);
    assert.ok(Math.abs(mark.height - mark.width * 3 / 4) <= 1, `${where}: the mark is ${mark.width}x${mark.height}, not 4:3`);
    assert.equal(mark.line, 'none', `${where}: the mark's link is ${mark.line}`);
    assert.notEqual(mark.background, 'rgba(0, 0, 0, 0)', `${where}: the mark has no tone behind it`);
    assert.ok(mark.size <= 56, `${where}: the letter is ${mark.size}px, past the 3.5rem cap`);
    assert.deepEqual([mark.hidden, mark.tabindex], ['true', '-1'], `${where}: the mark's link is aria-hidden=${mark.hidden} tabindex=${mark.tabindex}`);
    assert.deepEqual(mark.seps, ['true', 'true'], `${where}: the sub-categories' dots are ${JSON.stringify(mark.seps)}, want two hidden from a screen reader`);
    assert.equal(mark.text, 'Wearables · Speakers · Cables and chargers', `${where}: the sub-categories read ${JSON.stringify(mark.text)}`);
    console.log(`${where}: ${mark.letter} on its tone, no underline, sub-categories spaced around a dot`);
  }

  // Only the script that moves between slides marks a tab aria-current, so the
  // current tab exists, and is underlined, only where scripting runs: the
  // script sets data-pill on the row and the stylesheet draws the 2px line on
  // its ::before, as every current page is marked. Without script no tab is
  // current, and the first two stand in for the current and another tab.
  const tabsEvaluated = () => evaluate(`(() => {
    const row = document.querySelector('.goen-hero__tabs');
    const current = row?.querySelector('a[aria-current="true"]');
    const other = row?.querySelector('a:not([aria-current="true"])');
    if (!current || !other) return null;
    const marked = row.querySelectorAll('[aria-current]').length;
    const look = tab => {
      const style = getComputedStyle(tab);
      return { background: style.backgroundColor, weight: style.fontWeight, outline: style.outlineStyle, shadow: style.boxShadow, color: style.color };
    };
    const colour = value => {
      const probe = document.createElement('span');
      probe.style.color = value;
      document.body.append(probe);
      const resolved = getComputedStyle(probe).color;
      probe.remove();
      return resolved;
    };
    const pill = getComputedStyle(row, '::before');
    return { scripting: matchMedia('(scripting: enabled)').matches, current: look(current), other: look(other),
      currents: marked, marked: row.hasAttribute('data-pill'), ink: colour('var(--sys-on-surface)'), muted: colour('var(--sys-on-surface-variant)'),
      pill: { display: pill.display, background: pill.backgroundColor, top: pill.borderTopWidth, width: pill.borderBottomWidth, style: pill.borderBottomStyle, colour: pill.borderBottomColor } };
  })()`);
  // Whether (scripting: none) holds cannot be read without running script, so
  // the page is asserted by what it draws: a current tab drawn apart, or a row
  // marked for the pill, means the media feature did not turn off.
  const tabsRead = async () => {
    const ids = await queryNodes(['.goen-hero__tabs', '.goen-hero__tabs a:nth-child(1)', '.goen-hero__tabs a:nth-child(2)']);
    if (!ids) return null;
    const look = async nodeId => {
      const { computedStyle } = await send('CSS.getComputedStyleForNode', { nodeId });
      const value = name => computedStyle.find(property => property.name === name)?.value;
      return { background: value('background-color'), weight: value('font-weight'), outline: value('outline-style'), shadow: value('box-shadow') };
    };
    const { attributes } = await send('DOM.getAttributes', { nodeId: ids[0] });
    const read = { marked: attributes.includes('data-pill'), current: await look(ids[1]), other: await look(ids[2]) };
    // DOM.getDocument discards every node id handed out before it.
    const { root } = await send('DOM.getDocument', { depth: 0 });
    const { nodeIds: currents } = await send('DOM.querySelectorAll', { nodeId: root.nodeId, selector: '.goen-hero__tabs [aria-current]' });
    return { currents: currents.length, ...read };
  };
  for (const scriptingOff of [false, true]) {
    const label = `hero tabs scripting ${scriptingOff ? 'off' : 'on'}`;
    const tabs = scriptingOff ? tabsRead : tabsEvaluated;
    await send('Emulation.setScriptExecutionDisabled', { value: scriptingOff });
    if (scriptingOff) await openWithoutScripts('/home', 1440);
    else await open('/home', 1440);
    const drawn = await tabs();
    assert.ok(drawn, `${label}: no tabs to compare`);
    assert.equal(drawn.currents, scriptingOff ? 0 : 1, `${label}: ${drawn.currents} tabs are marked current, want ${scriptingOff ? 'none' : 'one'}`);
    if (scriptingOff) {
      assert.equal(drawn.marked, false, `${label}: the row is marked for a pill`);
      assert.deepEqual(drawn.current, drawn.other, `${label}: the current tab is drawn apart`);
    } else {
      assert.equal(drawn.scripting, true, `${label}: the page does not report scripting on`);
      assert.equal(drawn.marked, true, `${label}: the script did not mark the row for the pill`);
      assert.equal(drawn.pill.background, 'rgba(0, 0, 0, 0)', `${label}: the current tab's mark has a fill`);
      assert.deepEqual([drawn.pill.top, drawn.pill.width, drawn.pill.style, drawn.pill.colour], ['0px', '2px', 'solid', drawn.ink], `${label}: the current tab is not underlined 2px in the ink`);
      assert.deepEqual([drawn.current.weight, drawn.other.weight], ['500', '500'], `${label}: the tabs' weight`);
      assert.equal(drawn.current.color, drawn.ink, `${label}: the current tab's text is not --sys-on-surface`);
      assert.equal(drawn.other.color, drawn.muted, `${label}: another tab's text is not --sys-on-surface-variant`);
    }
    await media([{ name: 'forced-colors', value: 'active' }]);
    const forced = await tabs();
    await media([]);
    assert.ok(forced, `${label}: no current and other tab in forced colours`);
    assert.equal(forced.current.outline === 'solid', !scriptingOff, `${label}: forced colours ${scriptingOff ? 'draw' : 'do not draw'} the current tab's edge`);
    if (!scriptingOff) assert.equal(forced.pill.display, 'none', `${label}: forced colours draw the pill`);
    console.log(`${label}: ${scriptingOff ? 'no line' : 'current tab underlined'}, forced-colours edge ${forced.current.outline}${scriptingOff ? '' : `, pill ${forced.pill.display}`}`);
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

  // The filter chips are still a pill with one corner, in rem so it grows with
  // the text. A button is a seal, --r-lg (0.5rem), in rem too; the quantity
  // field and the footer's newsletter field are fields, 4px at any text size,
  // as the field corners below are, and the footer's button sits 4px inside
  // its field, so square. The header's words, the back office's among them,
  // have no corner at all. The hero arrows are square outlines, a field's corner,
  // with no fill.
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
          page: getComputedStyle(document.body).backgroundColor,
          rem: parseFloat(getComputedStyle(document.documentElement).fontSize) }];
      }));
    })()`);
    const length = (got, what) => {
      assert.match(got.radius, /^\d+(\.\d+)?px$/, `${label}: ${what} has a ${got.radius} corner, not a length`);
      return parseFloat(got.radius);
    };
    const field = '.goen-footer__field > .goen-input';
    const submit = '.goen-footer__field > .goen-btn';
    const filled = '.goen-summary__actions .goen-btn--primary';
    const stepper = '.goen-stepper';
    const words = '.goen-header__backoffice';
    const chip = '.goen-filters__chip';
    const arrow = '.goen-hero__arrow';
    const corner = {};
    for (const rootSize of ['', '200%']) {
      const scale = rootSize || '100%';
      await open('/cart', 1440);
      const cart = await measure([words, field, filled, stepper, submit], rootSize);
      for (const selector of [words, field, filled, stepper, submit]) assert.ok(cart[selector]?.height > 0, `${label}: ${selector} is not drawn in the cart at ${scale}`);
      assert.equal(length(cart[filled], filled), cart[filled].rem * 0.5, `${label}: the filled button has a ${cart[filled].radius} corner at ${scale}, not --r-lg (${cart[filled].rem * 0.5}px)`);
      assert.equal(length(cart[stepper], stepper), 4, `${label}: the quantity field has a ${cart[stepper].radius} corner at ${scale}, not a field's 4px`);
      assert.equal(length(cart[field], field), 4, `${label}: the newsletter field has a ${cart[field].radius} corner at ${scale}, not a field's 4px`);
      assert.equal(length(cart[submit], submit), 0, `${label}: the footer button's corner at ${scale} is ${cart[submit].radius}, not its field's 4px less 4px`);
      assert.equal(length(cart[words], words), 0, `${label}: the header's back office has a ${cart[words].radius} corner at ${scale}, not none`);
      assert.equal(cart[filled].shadow, 'none', `${label}: the filled button at ${scale} casts a shadow: ${cart[filled].shadow}`);

      await open('/c/audio', 1440);
      const listing = await measure([chip], rootSize);
      assert.ok(listing[chip]?.height > 0, `${label}: no filter chip is drawn at ${scale}`);
      corner[scale] = length(listing[chip], chip);
      assert.equal(corner[scale], 1.625 * listing[chip].rem, `${label}: a filter chip has a ${listing[chip].radius} corner at ${scale}, not --r-press (${1.625 * listing[chip].rem}px)`);
      assert.ok(corner[scale] >= listing[chip].height / 2 - 0.5, `${label}: a filter chip is ${listing[chip].height}px tall with a ${corner[scale]}px corner at ${scale}, not a full pill`);

      await open('/home', 1440);
      const hero = await measure([arrow], rootSize);
      assert.ok(hero[arrow]?.height > 0, `${label}: no hero arrow is drawn at ${scale}`);
      assert.equal(length(hero[arrow], arrow), hero[arrow].rem * 0.25, `${label}: a hero arrow at ${scale} has a ${hero[arrow].radius} corner, not --r-sm (${hero[arrow].rem * 0.25}px)`);
      assert.notEqual(hero[arrow].border, 'rgba(0, 0, 0, 0)', `${label}: a hero arrow's edge at ${scale} is transparent, not an outline`);
      assert.equal(hero[arrow].background, 'rgba(0, 0, 0, 0)', `${label}: a hero arrow at ${scale} has a ${hero[arrow].background} fill`);
    }
    assert.equal(corner['200%'], corner['100%'] * 2, `${label}: ${corner['100%']}px at 100% text is ${corner['200%']}px at 200%, not double`);
    console.log(`${label}: chips ${corner['100%']}px at 100% text, ${corner['200%']}px at 200%; fields 4px, footer button square inside its field, header words cornerless, hero arrows square outlines`);
  }

  // The page frame. The header is one row of words in Chinese at 1440 and two
  // in English, the departments on the second; the current department is
  // underlined 2px in its own colour; the cart reads 購物車 2 in figures of one
  // width. Below 1024 the departments scroll sideways and the right edge of a
  // 320, 375 or 414 screen cuts a department at least 12px in, so the row shows
  // it goes on. A department's panel is white by day under its shadow, in
  // 200px, the middle and 280px. The footer's links are ink and underlined,
  // and its subscribe is an ink word with no arrow.
  {
    const label = 'page frame';
    const header = () => evaluate(`(() => {
      const box = element => { const r = element.getBoundingClientRect(); return { top: r.top, bottom: r.bottom, left: r.left, right: r.right }; };
      const nav = document.querySelector('.goen-header__nav');
      const current = nav.querySelector('[aria-current="page"]');
      const style = getComputedStyle(current);
      const count = document.querySelector('#cart-link .goen-header__count');
      // The word and the figure are two flex items, so the space between them
      // in the markup collapses; what a reader sees is the gap the box leaves.
      const word = [...document.querySelector('#cart-link').childNodes].find(node => node.nodeType === Node.TEXT_NODE && node.textContent.trim());
      const range = document.createRange();
      if (word) range.selectNodeContents(word);
      return { bar: box(document.querySelector('.goen-header__bar')), nav: box(nav), search: box(document.querySelector('.goen-header__search')),
        actions: box(document.querySelector('.goen-header__actions')), cart: document.querySelector('#cart-link').textContent.replace(/\s+/g, ' ').trim(),
        figures: count && getComputedStyle(count).fontVariantNumeric,
        gap: word && count ? count.getBoundingClientRect().left - range.getBoundingClientRect().right : null,
        current: { text: current.textContent.trim(), line: style.textDecorationLine, thickness: style.textDecorationThickness,
          lineColour: style.textDecorationColor, colour: style.color } };
    })()`);
    for (const [path, rows, cart] of [['/frame', 1, '購物車 2'], ['/frame?lang=en', 2, 'Cart 2']]) {
      await open(path, 1440);
      const got = await header();
      const where = `${label} ${path} 1440px`;
      const oneRow = got.nav.top < got.search.bottom && got.search.top < got.nav.bottom && got.nav.right <= got.search.left;
      assert.equal(oneRow ? 1 : 2, rows, `${where}: the departments are on ${oneRow ? 'the bar\'s row' : 'a row of their own'}: ${JSON.stringify(got)}`);
      if (rows === 2) assert.ok(got.nav.top >= got.search.bottom && got.nav.top >= got.actions.bottom - 0.5, `${where}: the departments' row is not under the others: ${JSON.stringify(got)}`);
      assert.equal(got.cart, cart, `${where}: the cart reads ${JSON.stringify(got.cart)}`);
      assert.match(got.figures, /tabular-nums/, `${where}: the cart's count is set in ${got.figures}, not tabular figures`);
      assert.ok(got.gap >= 3.5, `${where}: the cart's word and its count are ${got.gap}px apart, run together`);
      assert.deepEqual([got.current.line, got.current.thickness, got.current.lineColour], ['underline', '2px', got.current.colour],
        `${where}: the current department ${got.current.text} is marked ${JSON.stringify(got.current)}, not a 2px underline in its own colour`);
      console.log(`${where}: ${rows} row${rows === 1 ? '' : 's'}, ${got.cart}, current department underlined 2px`);
    }
    await open('/frame', 1024);
    const narrow = await header();
    assert.ok(narrow.nav.top >= narrow.search.bottom, `${label} /frame 1024px: the Chinese departments do not step down where one row cannot hold them: ${JSON.stringify(narrow)}`);

    for (const width of [320, 375, 414]) {
      await open('/frame', width, true);
      // goen.js centres the current department; the cut is measured at the
      // row's start, as a page with no current department opens.
      await evaluate(`document.querySelector('.goen-header__nav').scrollLeft = 0; new Promise(done => requestAnimationFrame(() => requestAnimationFrame(done)))`);
      const row = await evaluate(`(() => {
        const nav = document.querySelector('.goen-header__nav');
        const edge = document.documentElement.clientWidth;
        const links = [...nav.querySelectorAll(':scope > a, :scope > .goen-dept > a')];
        const cut = links.map(a => a.getBoundingClientRect()).find(r => r.left < edge && r.right > edge);
        return { edge, scroll: nav.scrollWidth > nav.clientWidth, snap: getComputedStyle(nav).scrollSnapType,
          margin: getComputedStyle(links[0]).scrollMarginInlineStart, into: cut ? edge - cut.left : null,
          page: document.documentElement.scrollWidth };
      })()`);
      const where = `${label} /frame ${width}px`;
      assert.ok(row.scroll, `${where}: the departments' row does not scroll: ${JSON.stringify(row)}`);
      assert.equal(row.page, row.edge, `${where}: the page scrolls sideways: ${JSON.stringify(row)}`);
      // proximity is the default strictness, so it computes to plain x.
      assert.match(row.snap, /^x( proximity)?$/, `${where}: the row snaps ${row.snap}, not x proximity`);
      assert.equal(row.margin, '16px', `${where}: a link's scroll margin is ${row.margin}, not 16px`);
      assert.ok(row.into !== null && row.into >= 12, `${where}: the right edge cuts no department at least 12px in: ${JSON.stringify(row)}`);
      console.log(`${where}: the edge cuts a department ${row.into}px in`);
    }

    await open('/frame', 1440);
    const panel = await evaluate(`(() => {
      const dept = document.querySelector('.goen-dept');
      dept.setAttribute('data-open', '');
      const panel = dept.querySelector('.goen-dept__panel');
      const style = getComputedStyle(panel);
      return { shown: panel.getClientRects().length > 0, background: style.backgroundColor, shadow: style.boxShadow,
        columns: getComputedStyle(panel.querySelector('.goen-dept__inner')).gridTemplateColumns.split(' '),
        sizes: panel.querySelector('.goen-dept__picks img').getAttribute('sizes') };
    })()`);
    assert.ok(panel.shown, `${label}: the department panel does not open`);
    assert.equal(panel.background, 'rgb(255, 255, 255)', `${label}: the department panel's ground is ${panel.background}, not white`);
    assert.notEqual(panel.shadow, 'none', `${label}: the department panel casts no shadow`);
    assert.deepEqual([panel.columns.length, panel.columns[0], panel.columns[2]], [3, '200px', '280px'], `${label}: the panel's columns are ${panel.columns}`);
    console.log(`${label}: the panel is white under its shadow in ${panel.columns.join(' ')}`);

    const footer = await evaluate(`(() => {
      const ink = getComputedStyle(document.body).color;
      const submit = document.querySelector('#newsletter-submit');
      const link = getComputedStyle(document.querySelector('.goen-footer__links .goen-footer__link'));
      return { ink, submit: getComputedStyle(submit).color, words: submit.textContent.trim(), font: getComputedStyle(submit).fontSize,
        link: link.color, line: link.textDecorationLine };
    })()`);
    assert.equal(footer.submit, footer.ink, `${label}: the subscribe word is ${footer.submit}, not the ink ${footer.ink}`);
    assert.equal(footer.words, '訂閱', `${label}: the subscribe button reads ${JSON.stringify(footer.words)}, not the word alone`);
    assert.equal(footer.font, '15px', `${label}: the subscribe word is ${footer.font}, not label-l's 15px`);
    assert.deepEqual([footer.link, footer.line], [footer.ink, 'underline'], `${label}: a footer link is ${footer.link} with ${footer.line}, not ink and underlined`);
    console.log(`${label}: footer links and the subscribe word in ink`);

    // On a phone the link groups stand two across, the brand and the newsletter
    // across the whole width, as the board draws it; on a desktop each group
    // takes a column of its own.
    for (const width of [320, 375, 1440]) {
      await open('/frame', width);
      const grid = await evaluate(`(() => {
        const box = element => element.getBoundingClientRect();
        const groups = [...document.querySelectorAll('.goen-footer__grid > .goen-footer__links')].map(box);
        const brand = box(document.querySelector('.goen-footer__brand')), news = box(document.querySelector('.goen-footer__news'));
        const gridElement = document.querySelector('.goen-footer__grid'), gridStyle = getComputedStyle(gridElement);
        const column = box(gridElement).width - parseFloat(gridStyle.paddingLeft) - parseFloat(gridStyle.paddingRight);
        const top = Math.min(...groups.map(group => group.top));
        return { groups: groups.length, first: groups.filter(group => Math.abs(group.top - top) < 1).length,
          brand: Math.abs(brand.width - column) < 1, news: Math.abs(news.width - column) < 1,
          page: document.documentElement.scrollWidth, viewport: document.documentElement.clientWidth };
      })()`);
      const where = `footer ${width}px`;
      assert.equal(grid.groups, 3, `${where}: ${grid.groups} link groups`);
      assert.equal(grid.page, grid.viewport, `${where}: the page scrolls sideways`);
      if (width < 768) {
        assert.equal(grid.first, 2, `${where}: ${grid.first} link groups on the first row, want 2 across`);
        assert.ok(grid.brand && grid.news, `${where}: the brand or the newsletter does not take the whole width`);
      } else assert.equal(grid.first, 3, `${where}: ${grid.first} link groups on one row, want 3`);
    }
    console.log(`${label}: footer link groups two across on a phone, the brand and newsletter across the width`);
  }

  // The home hero is a printed page: each slide's photograph square and across
  // the window, a quarter of it tall from 768 (never more than 40% of the
  // screen) and 15:8 of it on a phone; under it, in the page column, the tabs,
  // then the name in the display size, the end and its days in vermilion, and
  // the campaign's products, beside the words where the column is 64rem. The
  // page has no filled button. Boxes are read relative to their own slide, so a
  // slide the track has not scrolled to needs no scrolling.
  {
    const near = (got, want, tolerance = 0.5) => Math.abs(got - want) <= tolerance;
    for (const width of [375, 800, 1024, 1440]) {
      const label = `hero ${width}px`;
      await open('/home', width);
      const hero = await evaluate(`(() => {
        const box = element => {
          const r = element.getBoundingClientRect();
          return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width, height: r.height };
        };
        const colour = value => {
          const probe = document.createElement('i');
          probe.style.color = value;
          document.body.append(probe);
          const resolved = getComputedStyle(probe).color;
          probe.remove();
          return resolved;
        };
        const corners = style => [style.borderTopLeftRadius, style.borderTopRightRadius, style.borderBottomRightRadius, style.borderBottomLeftRadius];
        const home = document.querySelector('.goen-home');
        const homeStyle = getComputedStyle(home);
        const homeBox = box(home);
        const tabs = document.querySelector('.goen-hero__tabs');
        const firstTab = tabs.querySelector('a');
        const slides = [...document.querySelectorAll('.goen-hero__slide')].map(slide => {
          const style = getComputedStyle(slide);
          const media = slide.querySelector('.goen-hero__media');
          const mediaStyle = getComputedStyle(media);
          const img = media.querySelector('img');
          const body = slide.querySelector('.goen-hero__body');
          const bodyStyle = getComputedStyle(body);
          const bodyBox = box(body);
          const title = slide.querySelector('.goen-hero__title');
          const tiles = slide.querySelector('.goen-hero__tiles');
          const inner = slide.querySelector('.goen-hero__inner');
          const days = slide.querySelector('.goen-days');
          const ends = slide.querySelector('.goen-hero__ends');
          return {
            slide: box(slide), background: style.backgroundColor, padding: style.padding,
            media: box(media), corners: corners(mediaStyle), img: img && box(img),
            // Relative to the slide, which the track may hold beside the window.
            column: { left: bodyBox.left + parseFloat(bodyStyle.paddingLeft) - slide.getBoundingClientRect().left,
              right: bodyBox.right - parseFloat(bodyStyle.paddingRight) - slide.getBoundingClientRect().left },
            title: { top: box(title).top - box(slide).top, size: getComputedStyle(title).fontSize, family: getComputedStyle(title).fontFamily },
            ends: ends && getComputedStyle(ends).color,
            days: days && { hidden: days.getAttribute('aria-hidden'), cells: [...days.children].map(cell => [cell.dataset.cell || '', getComputedStyle(cell).backgroundColor]) },
            tiles: tiles && { columns: getComputedStyle(tiles).gridTemplateColumns.trim().split(/\\s+/).length, beside: Math.abs(box(inner).top - box(tiles).top) < 1 && box(tiles).left > box(inner).right },
          };
        });
        return {
          viewport: document.documentElement.clientWidth,
          column: { left: homeBox.left + parseFloat(homeStyle.paddingLeft), right: homeBox.right - parseFloat(homeStyle.paddingRight) },
          track: box(document.querySelector('.goen-hero__track')),
          tabs: { top: box(tabs).top - box(document.querySelector('.goen-hero__slide')).top, bottom: box(tabs).bottom - box(document.querySelector('.goen-hero__slide')).top,
            words: box(firstTab).left + parseFloat(getComputedStyle(firstTab).paddingLeft), padding: [getComputedStyle(tabs).paddingTop, getComputedStyle(tabs).paddingBottom] },
          vermilion: colour('var(--sys-tertiary)'), quiet: colour('var(--sys-outline-variant)'),
          buttons: document.querySelectorAll('main .goen-btn, main .ui-btn, .goen-btn--primary').length,
          slides,
        };
      })()`);
      assert.equal(hero.slides.length, 5, `${label}: five slides drawn`);
      assert.equal(hero.buttons, 0, `${label}: the home page draws ${hero.buttons} filled buttons, want none`);
      // Across the window, the slides one track wide and touching.
      assert.ok(near(hero.track.left, 0) && near(hero.track.right, hero.viewport), `${label}: the track is ${hero.track.left}-${hero.track.right} in a ${hero.viewport}px window`);
      const tall = width >= 768 ? Math.min(hero.viewport * 0.25, 900 * 0.4) : hero.viewport * 8 / 15;
      hero.slides.forEach((slide, index) => {
        const name = `${label}: slide ${index + 1}`;
        assert.ok(near(slide.slide.width, hero.track.width), `${name} is ${slide.slide.width}px in a ${hero.track.width}px track`);
        if (index > 0) assert.ok(near(slide.slide.left - hero.slides[index - 1].slide.right, 0), `${name} does not touch the one before`);
        assert.equal(slide.background, 'rgba(0, 0, 0, 0)', `${name} has a ground of ${slide.background}`);
        assert.equal(slide.padding, '0px', `${name} has padding ${slide.padding}`);
        assert.deepEqual(slide.corners, ['0px', '0px', '0px', '0px'], `${name}: the photograph's corners are ${slide.corners}`);
        assert.ok(near(slide.media.left, slide.slide.left) && near(slide.media.right, slide.slide.right), `${name}: the photograph does not span the slide`);
        assert.ok(near(slide.media.height, tall, 1), `${name}: the photograph is ${slide.media.height}px tall, want ${tall}`);
        if (slide.img) for (const edge of ['left', 'right', 'top', 'bottom']) {
          assert.ok(near(slide.img[edge], slide.media[edge], 1), `${name}: the picture's ${edge} is ${slide.img[edge]}, its frame's ${slide.media[edge]}`);
        }
        assert.ok(near(slide.column.left, hero.column.left) && near(slide.column.right, hero.column.right),
          `${name}: the words stand in ${slide.column.left}-${slide.column.right}, the page column is ${hero.column.left}-${hero.column.right}`);
        assert.ok(slide.title.top >= hero.tabs.bottom - 0.5, `${name}: the name starts at ${slide.title.top}, over the tabs that end at ${hero.tabs.bottom}`);
      });
      assert.ok(hero.tabs.top >= hero.slides[0].media.height - 0.5, `${label}: the tabs start at ${hero.tabs.top}, over the photograph that ends at ${hero.slides[0].media.height}`);
      assert.ok(near(hero.tabs.words, hero.column.left), `${label}: the first tab's words start at ${hero.tabs.words}, want ${hero.column.left}`);
      assert.deepEqual(hero.tabs.padding, ['4px', '4px'], `${label}: the tab row's padding above and below`);

      const first = hero.slides[0];
      if (width >= 1440) assert.equal(first.title.size, '56px', `${label}: the campaign's name is ${first.title.size}, not the display size`);
      assert.match(first.title.family, /Noto Serif TC/, `${label}: the campaign's name is set in ${first.title.family}`);
      assert.equal(first.ends, hero.vermilion, `${label}: the end is ${first.ends}, not vermilion`);
      assert.equal(first.days?.hidden, 'true', `${label}: the days are read aloud`);
      assert.deepEqual(first.days.cells, [['past', hero.quiet], ['past', hero.quiet], ['', hero.vermilion], ['', hero.vermilion], ['', hero.vermilion]],
        `${label}: the days are drawn ${JSON.stringify(first.days.cells)}`);
      assert.equal(first.tiles?.columns, width >= 768 ? 4 : 2, `${label}: the campaign's products stand in ${first.tiles?.columns} columns`);
      assert.equal(first.tiles.beside, width >= 1440, `${label}: the products ${first.tiles.beside ? 'stand beside' : 'stand under'} the words`);

      await force('.goen-hero__tabs a[aria-current="true"]', ['focus', 'focus-visible']);
      assert.equal(await evaluate(`getComputedStyle(document.querySelector('.goen-hero__tabs a[aria-current="true"]')).outlineOffset`), '2px', `${label}: the focused tab's ring offset`);
      await force('.goen-hero__tabs a[aria-current="true"]', []);
      console.log(`${label}: ${hero.slides.length} slides across the window, photograph ${Math.round(tall)}px and square, products ${first.tiles.beside ? 'beside' : 'under'} the words in ${first.tiles.columns} columns`);
    }
  }

  // The promotion strip keeps its words on a phone: the message, the code, the
  // call to action underlined, and the close. The arrow after the word needs the
  // room a phone does not have, so it stands from 768 only. Nothing in the strip
  // is cut by a hidden overflow other than the message's own ellipsis, which a
  // 375 phone does not need for this copy.
  for (const [width, chevron] of [[375, false], [1440, true]]) {
    const label = `promotion strip ${width}px`;
    await open('/home?promo=1', width);
    const strip = await evaluate(`(() => {
      const bar = document.querySelector('.goen-promo__bar');
      const shown = selector => { const element = bar.querySelector(selector); return !!element && element.getClientRects().length > 0; };
      const message = [...bar.querySelectorAll('.goen-promo__wide, .goen-promo__narrow')].find(element => element.getClientRects().length > 0);
      const cta = bar.querySelector('.goen-promo__cta');
      const close = bar.querySelector('.goen-promo__close').getBoundingClientRect();
      const box = bar.getBoundingClientRect();
      return { message: message && { text: message.textContent, cut: message.parentElement.scrollWidth > message.parentElement.clientWidth },
        code: shown('.goen-promo__code'), cta: shown('.goen-promo__cta'), ctaLine: cta && getComputedStyle(cta).textDecorationLine,
        chevron: shown('.goen-promo__chevron'), close: { width: close.width, right: close.right }, bar: { left: box.left, right: box.right },
        page: document.documentElement.scrollWidth, viewport: document.documentElement.clientWidth };
    })()`);
    assert.ok(strip.message, `${label}: no message is drawn`);
    assert.equal(strip.message.cut, false, `${label}: the message ${JSON.stringify(strip.message.text)} is cut off`);
    assert.equal(strip.code, true, `${label}: the code is not drawn`);
    assert.equal(strip.cta, true, `${label}: the call to action is not drawn, the strip is a message and an arrow`);
    assert.equal(strip.ctaLine, 'underline', `${label}: the call to action is ${strip.ctaLine}, not underlined`);
    assert.equal(strip.chevron, chevron, `${label}: the arrow is ${strip.chevron ? 'drawn' : 'not drawn'}`);
    assert.ok(strip.close.width >= 44 && strip.close.right <= strip.viewport + 0.5, `${label}: the close is ${strip.close.width}px wide ending at ${strip.close.right} in a ${strip.viewport}px window`);
    assert.equal(strip.page, strip.viewport, `${label}: the page scrolls sideways`);
    console.log(`${label}: message, code, underlined "Shop now" and close${chevron ? ' and the arrow' : ', no arrow'}`);
  }

  // A campaign with no photograph draws no empty slab: the slide has no photograph
  // field at all, and a hero with such a slide stands its tabs above the slides, in
  // flow, so the name never sits under them nor the tabs over a blank field. The
  // tabs are never clipped by the row's own ends at 320 or 375.
  for (const width of [320, 375, 1440]) {
    const label = `hero without photographs ${width}px`;
    await open('/home?bare=1', width);
    const bare = await evaluate(`(() => {
      const hero = document.querySelector('.goen-hero');
      const slides = [...hero.querySelectorAll('.goen-hero__slide')];
      const tabs = hero.querySelector('.goen-hero__tabs');
      const arrows = [...hero.querySelectorAll('.goen-hero__arrow')].map(arrow => arrow.getClientRects().length);
      const box = element => element.getBoundingClientRect();
      const first = box(slides[0]);
      return {
        bare: hero.classList.contains('goen-hero--bare'),
        media: slides.map(slide => slide.querySelector('.goen-hero__media') !== null),
        tabsBottom: box(tabs).bottom, tabsTop: box(tabs).top, slidesTop: first.top,
        titles: slides.map(slide => box(slide.querySelector('.goen-hero__title')).top),
        arrows, viewport: document.documentElement.clientWidth,
        row: { left: box(tabs).left, right: box(tabs).right },
      };
    })()`);
    assert.equal(bare.bare, true, `${label}: the hero is not marked bare`);
    assert.deepEqual(bare.media, [true, true, true, false, false], `${label}: photograph fields ${bare.media}, want one only where there is a photograph`);
    assert.ok(bare.tabsBottom <= bare.slidesTop + 0.5, `${label}: the tabs end at ${bare.tabsBottom}, under the slides that start at ${bare.slidesTop}`);
    for (const top of bare.titles) assert.ok(top >= bare.tabsBottom - 0.5, `${label}: a name starts at ${top}, over the tabs that end at ${bare.tabsBottom}`);
    if (width < 768) assert.deepEqual(bare.arrows, [0, 0], `${label}: the arrows are drawn on a phone and eat the tabs' room: ${bare.arrows}`);
    assert.ok(bare.row.left >= -0.5 && bare.row.right <= bare.viewport + 0.5, `${label}: the tab row runs ${bare.row.left}-${bare.row.right} in a ${bare.viewport}px window`);
    console.log(`${label}: tabs above the slides, photograph fields only where there is a photograph, ${bare.arrows.every(count => count === 0) ? 'no arrows' : 'arrows beside the tabs'}`);
  }

  // A product's main photograph sits on one square well in the photograph's
  // own ground. The product page carries no department tone, so nothing tones
  // the well: no tone frame around it, no white edge on the picture, and a
  // placeholder whose words can be read.
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
        toned: !!(gallery.closest('[data-tone]') || gallery.querySelector('[data-tone]')),
        photo: resolve('--photo'),
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
    for (const photo of [true, false]) {
      for (const width of [375, 1440]) {
        const label = `product photograph ${photo ? 'with' : 'without'} a photograph ${width}px`;
        await open(`/product?photo=${photo ? 1 : 0}`, width);
        const drawn = await measure();
        assert.ok(drawn, `${label}: no photograph well on the page`);
        assert.equal(drawn.toned, false, `${label}: a department tone reaches the photograph well`);
        assert.equal(drawn.background, drawn.photo, `${label}: the well is ${drawn.background}, not the photograph ground ${drawn.photo}`);
        assert.deepEqual(drawn.padding, Array(4).fill('0px'), `${label}: the well has padding ${drawn.padding}`);
        assert.deepEqual(drawn.shotBorder, Array(4).fill('0px'), `${label}: the well has a border ${drawn.shotBorder}`);
        assert.deepEqual(drawn.corners, Array(4).fill('0px'), `${label}: corners ${drawn.corners}, not square`);
        if (photo) {
          assert.deepEqual(drawn.imageBorder, Array(4).fill('0px'), `${label}: the picture has an edge ${drawn.imageBorder}`);
          assert.equal(drawn.imageClip, 'border-box', `${label}: the picture's background is clipped to ${drawn.imageClip}`);
          for (const gap of drawn.inset) assert.ok(Math.abs(gap) < 0.5, `${label}: the picture sits ${drawn.inset} inside the well`);
          console.log(`${label}: ${drawn.background} well, square corners, picture edge to edge`);
        } else {
          const contrast = ratio(channels(drawn.textColor), channels(drawn.background));
          assert.ok(contrast >= 4.5, `${label}: placeholder ${drawn.textColor} on ${drawn.background} is ${contrast.toFixed(2)}:1`);
          console.log(`${label}: ${drawn.background} well, square corners, placeholder text ${contrast.toFixed(2)}:1`);
        }
      }
    }
  }
  // The header's search is a white field with a 1px edge on all four sides and
  // the field's 4px corner at any text size. A page that opens with the hero
  // meets it without the header's rule; every other page keeps the rule.
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
        corner: resolve('border-top-left-radius', 'var(--r-field)'),
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
          assert.equal(got.corner, '4px', `${where}: --r-field resolves to ${got.corner}, not 4px`);
          assert.deepEqual(got.radii, Array(4).fill('4px'), `${where}: corners ${got.radii}, not the field's 4px`);
          assert.equal(got.paddingLeft, `${2.5 * got.rem}px`, `${where}: left padding ${got.paddingLeft}, not 2.5rem`);
          assert.ok(Math.abs(got.iconLeft - got.rem) < 0.5, `${where}: the magnifier starts ${got.iconLeft}px in, not 1rem (${got.rem}px)`);
          assert.ok(got.iconRight <= 2.5 * got.rem, `${where}: the magnifier ends ${got.iconRight}px in, over the text that starts at 2.5rem`);
          if (hero) assert.equal(got.rule.width, '0px', `${where}: the header keeps its rule over the hero`);
          else assert.deepEqual(got.rule, { width: '1px', style: 'solid' }, `${where}: the header's rule is ${JSON.stringify(got.rule)}`);
        }
        console.log(`${label} ${path} ${width}px: white field, 4px corner, 1px edge, 2.5rem inset, header rule ${hero ? 'gone' : 'kept'}`);
      }
    }
    // The focus ring is the shared 2px outline, drawn outside a field that
    // keeps its corners while it is focused.
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

  // A department's head is words on the page, inside the page column: the
  // crumbs, the name in the editorial face at 40px at most, the count, a running
  // campaign's notice under the name, and the sub-categories in one row under
  // them. It draws no photograph and no tone ground, whatever the department
  // has. The current sub-category is underlined 2px and the others are not; no
  // pill, no edge. On a phone the row scrolls sideways, stops at a name's start,
  // and keeps its focus ring inside the link, which the scroller would cut.
  {
    const near = (got, want, tolerance = 0.5) => Math.abs(got - want) <= tolerance;
    const measure = () => evaluate(`(() => {
      const box = element => {
        const r = element.getBoundingClientRect();
        return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width, height: r.height };
      };
      const page = document.querySelector('.goen-listing');
      const pageStyle = getComputedStyle(page);
      const pageBox = box(page);
      const head = document.querySelector('.goen-listing__head');
      const headStyle = getComputedStyle(head);
      const headBox = box(head);
      const pagehead = head.querySelector('.goen-pagehead');
      const title = head.querySelector('h1.goen-pagehead__title');
      const titleStyle = getComputedStyle(title);
      const notice = pagehead.querySelector('.goen-deptnotice');
      const row = head.querySelector('.goen-subindex');
      const rowStyle = getComputedStyle(row);
      const current = row.querySelector('a[aria-current="page"]');
      const other = row.querySelector('a:not([aria-current])');
      const link = element => {
        const style = getComputedStyle(element);
        return { line: style.textDecorationLine, thickness: style.textDecorationThickness, weight: Number(style.fontWeight),
          background: style.backgroundColor, shadow: style.boxShadow, snap: style.scrollSnapAlign, margin: style.scrollMarginLeft, box: box(element) };
      };
      return {
        viewport: innerWidth,
        column: { left: pageBox.left + parseFloat(pageStyle.paddingLeft), right: pageBox.right - parseFloat(pageStyle.paddingRight) },
        head: { left: headBox.left + parseFloat(headStyle.paddingLeft), right: headBox.right - parseFloat(headStyle.paddingRight) },
        grounds: [headStyle.backgroundColor, getComputedStyle(pagehead).backgroundColor],
        images: head.querySelectorAll('img').length,
        title: { box: box(title), size: parseFloat(titleStyle.fontSize), family: titleStyle.fontFamily },
        notice: notice ? { box: box(notice) } : null,
        row: { box: box(row), overflowX: rowStyle.overflowX, snap: rowStyle.scrollSnapType, after: getComputedStyle(row, '::after').flexBasis, top: box(row).top },
        current: link(current), other: link(other),
      };
    })()`);
    const ringOf = async selector => {
      await force(selector, ['focus', 'focus-visible']);
      const got = await evaluate(`(() => {
        const style = getComputedStyle(document.querySelector(${JSON.stringify(selector)}));
        return { style: style.outlineStyle, width: style.outlineWidth, offset: style.outlineOffset };
      })()`);
      await force(selector, []);
      return got;
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

        // In the page column, words on the page: no photograph, no ground.
        assert.ok(near(head.head.left, head.column.left) && near(head.head.right, head.column.right),
          `${label}: the head is ${head.head.left}-${head.head.right}, the page column ${head.column.left}-${head.column.right}`);
        assert.equal(head.images, 0, `${label}: the head draws ${head.images} image(s)`);
        for (const ground of head.grounds) assert.equal(ground, 'rgba(0, 0, 0, 0)', `${label}: the head has a ground of ${ground}`);

        // The name is the editorial face, 40px at most.
        assert.ok(head.title.size <= 40 + 0.5 && head.title.size >= 32 - 0.5, `${label}: the name is ${head.title.size}px, not 32 to 40`);
        assert.ok(/Serif/.test(head.title.family), `${label}: the name is set in ${head.title.family}`);

        // The notice is a line under the name, above the sub-categories.
        if (c.notice) {
          assert.ok(head.notice.box.top >= head.title.box.bottom - 0.5, `${label}: the notice starts at ${head.notice.box.top}px, above the name's bottom at ${head.title.box.bottom}px`);
          assert.ok(head.notice.box.bottom <= head.row.top + 0.5, `${label}: the notice runs into the sub-categories`);
        }

        // The current sub-category is underlined 2px, heavier; the others are plain words.
        assert.equal(head.current.line, 'underline', `${label}: the current sub-category is not underlined`);
        assert.equal(head.current.thickness, '2px', `${label}: the current sub-category's line is ${head.current.thickness}`);
        assert.ok(head.current.weight > head.other.weight, `${label}: the current sub-category weighs ${head.current.weight}, another ${head.other.weight}`);
        assert.equal(head.other.line, 'none', `${label}: another sub-category is underlined at rest`);
        for (const l of [head.current, head.other]) {
          assert.equal(l.background, 'rgba(0, 0, 0, 0)', `${label}: a sub-category is filled: ${l.background}`);
          assert.equal(l.shadow, 'none', `${label}: a sub-category has an edge: ${l.shadow}`);
        }

        const ring = await ringOf('.goen-subindex__link[aria-current="page"]');
        assert.deepEqual([ring.style, ring.width], ['solid', '2px'], `${label}: the focused sub-category's ring`);
        if (width < 768) {
          // One sideways row that stops at a name's start, with a 16px end, and
          // the ring inside the link so the scroller does not cut it.
          assert.equal(head.row.overflowX, 'auto', `${label}: the row does not scroll sideways`);
          assert.ok(/^x/.test(head.row.snap), `${label}: the row's snap is ${head.row.snap}`);
          assert.equal(head.current.snap, 'start', `${label}: a name snaps at ${head.current.snap}`);
          assert.equal(head.current.margin, '16px', `${label}: a name's scroll margin is ${head.current.margin}`);
          assert.equal(head.row.after, '16px', `${label}: the row ends in ${head.row.after}`);
          assert.ok(parseFloat(ring.offset) <= -2, `${label}: the ring is drawn ${ring.offset} outside the link, where the row cuts it`);
        } else {
          assert.equal(head.row.overflowX, 'visible', `${label}: the row scrolls at ${width}px`);
        }

        // Forced colours keep the underline that marks the current one.
        await media([{ name: 'forced-colors', value: 'active' }]);
        const forced = await evaluate(`getComputedStyle(document.querySelector('.goen-subindex__link[aria-current="page"]')).textDecorationLine`);
        await media([]);
        assert.equal(forced, 'underline', `${label}: the current sub-category loses its mark in forced colours`);
        console.log(`${label}: words in the column, name ${head.title.size}px, current underlined 2px${width < 768 ? ', row scrolls' : ''}`);
      }
    }
  }

  // Six Chinese sub-category names stay on one line at 1400px and 1440px, so
  // the last one is never left alone on a second line. Narrower windows, where
  // the row wraps, are not asserted.
  for (const width of [1400, 1440]) {
    const label = `department head sub-categories ${width}px`;
    await open('/department?tone=sage&photo=1&zh=1', width);
    const tops = await evaluate(`[...document.querySelectorAll('.goen-listing__head .goen-subindex__link')].map(link => link.getBoundingClientRect().top)`);
    assert.equal(tops.length, 6, `${label}: six sub-categories drawn`);
    assert.ok(tops.every(top => Math.abs(top - tops[0]) < 1), `${label}: the sub-categories are on more than one line: ${tops}`);
    console.log(`${label}: six names on one line`);
  }

  // The home page's band is the department's shelf on its tone, square and
  // across the window, its words and products in the page column, the products
  // a grid at every width. A Chinese name stands upright beside the products
  // where the column is 64rem, and lies down over them at a narrower column,
  // at 200% text or in English, without widening the page. A struck price says
  // its last day under it in vermilion, its comma read and not shown.
  {
    const near = (got, want, tolerance = 0.5) => Math.abs(got - want) <= tolerance;
    const measure = () => evaluate(`(() => {
      const box = element => { const r = element.getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom }; };
      const home = document.querySelector('.goen-home');
      const homeStyle = getComputedStyle(home);
      const homeBox = box(home);
      const band = document.querySelector('.goen-band');
      const style = getComputedStyle(band);
      const body = band.querySelector('.goen-band__body');
      const bodyStyle = getComputedStyle(body);
      const bodyBox = box(body);
      const name = band.querySelector('.goen-band__name');
      const tiles = band.querySelector('.goen-band__tiles');
      const probe = document.createElement('i');
      probe.style.backgroundColor = 'var(--tone-ground)';
      probe.style.color = 'var(--sys-tertiary)';
      probe.style.position = 'absolute';
      band.append(probe);
      const ground = getComputedStyle(probe).backgroundColor, vermilion = getComputedStyle(probe).color;
      probe.remove();
      const until = band.querySelector('.goen-tile__until');
      return {
        viewport: document.documentElement.clientWidth, page: document.documentElement.scrollWidth,
        column: { left: homeBox.left + parseFloat(homeStyle.paddingLeft), right: homeBox.right - parseFloat(homeStyle.paddingRight) },
        band: box(band), background: style.backgroundColor, ground,
        corners: [style.borderTopLeftRadius, style.borderTopRightRadius, style.borderBottomRightRadius, style.borderBottomLeftRadius],
        words: { left: bodyBox.left + parseFloat(bodyStyle.paddingLeft), right: bodyBox.right - parseFloat(bodyStyle.paddingRight) },
        writing: getComputedStyle(name).writingMode, name: box(name), tiles: box(tiles),
        overflowX: getComputedStyle(tiles).overflowX, columns: getComputedStyle(tiles).gridTemplateColumns.trim().split(/\\s+/).length,
        until: until && { shown: (() => { const copy = until.cloneNode(true); copy.querySelectorAll('.goen-sr-only').forEach(e => e.remove()); return copy.textContent.replace(/\\s+/g, ' ').trim(); })(), text: until.textContent.replace(/\\s+/g, ' ').trim(), colour: getComputedStyle(until).color, vermilion,
          datetime: until.querySelector('time')?.getAttribute('datetime') },
      };
    })()`);
    for (const width of [375, 800, 1024, 1440]) {
      for (const [query, lang] of [['band=ink', 'en'], ['band=sage', 'en'], ['band=stone&zh=1', 'zh']]) {
        const label = `home band ${query} ${width}px`;
        await open(`/home?${query}`, width);
        const got = await measure();
        assert.ok(near(got.band.left, 0) && near(got.band.right, got.viewport), `${label}: the band is ${got.band.left}-${got.band.right} in a ${got.viewport}px window`);
        assert.deepEqual(got.corners, ['0px', '0px', '0px', '0px'], `${label}: corners ${got.corners}`);
        assert.equal(got.background, got.ground, `${label}: the band is ${got.background}, the tone ground is ${got.ground}`);
        assert.ok(near(got.words.left, got.column.left) && near(got.words.right, got.column.right),
          `${label}: the band's words stand in ${got.words.left}-${got.words.right}, the page column is ${got.column.left}-${got.column.right}`);
        assert.equal(got.overflowX, 'visible', `${label}: the products scroll`);
        assert.equal(got.columns, width >= 768 ? 4 : 2, `${label}: the products stand in ${got.columns} columns`);
        const upright = lang === 'zh' && width >= 1440;
        assert.equal(got.writing, upright ? 'vertical-rl' : 'horizontal-tb', `${label}: the name is set ${got.writing}`);
        if (upright) assert.ok(got.name.right <= got.tiles.left && got.name.top >= got.tiles.top - 0.5, `${label}: the upright name is not beside the products: ${JSON.stringify([got.name, got.tiles])}`);
        else assert.ok(got.name.bottom <= got.tiles.top, `${label}: the name does not lie over the products: ${JSON.stringify([got.name, got.tiles])}`);
        assert.equal(got.page, got.viewport, `${label}: the page scrolls sideways`);
        if (lang === 'zh') {
          assert.equal(got.until?.shown, '特價至 11 月 22 日', `${label}: the sale's end reads ${JSON.stringify(got.until)}`);
        } else {
          assert.deepEqual([got.until?.shown, got.until?.text], ['On sale until Nov 22', 'On sale until Nov 22,'], `${label}: the sale's end reads ${JSON.stringify(got.until)}`);
        }
        assert.equal(got.until.datetime, '2026-11-22', `${label}: the sale's last day is not a time`);
        if (query !== 'band=ink') assert.equal(got.until.colour, got.until.vermilion, `${label}: the sale's end is ${got.until.colour}, not vermilion`);
        console.log(`${label}: square across the window, ${got.columns} columns, name ${got.writing}`);
      }
    }
    // At 200% text the 64rem column is out of reach: the name lies down again
    // and the page stays the window's width.
    await open('/home?band=stone&zh=1', 1440);
    await evaluate(`document.documentElement.style.fontSize = '200%'`);
    const large = await measure();
    assert.equal(large.writing, 'horizontal-tb', 'home band 1440px at 200% text: the name stays upright');
    assert.equal(large.page, large.viewport, 'home band 1440px at 200% text: the page scrolls sideways');
    await evaluate(`document.documentElement.style.fontSize = ''`);
    await media([{ name: 'forced-colors', value: 'active' }]);
    const edge = await evaluate(`(() => {
      const style = getComputedStyle(document.querySelector('.goen-band'));
      return [style.outlineStyle, style.outlineWidth, style.outlineOffset].join(' ');
    })()`);
    await media([]);
    assert.equal(edge, 'solid 1px -1px', `home band: the band's edge in forced colours is ${edge}`);
    console.log('home band at 200% text: the name lies down, the page keeps its width; forced colours draw its edge');
  }

  // A chosen option, a chosen delivery card and a refused field are marked in
  // one language: a tinted fill, a 2px edge and a check for what is chosen; a
  // white face and a 1px edge for what is not; the error colour and an icon for
  // what was refused. Fields share one 4px corner, and the checkout is two
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
    const names = ['--n-0', '--n-50', '--n-500', '--accent', '--accent-faint', '--accent-deep', '--control-boundary', '--error', '--fg',
      '--sys-surface', '--sys-on-surface', '--sys-outline'];

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
          assert.deepEqual(radii, Array(4).fill('4px'), `${where}: ${selector} on ${page} has corners ${radii}, not 4px`);
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
    console.log(`${label}: inputs, textarea, selects and the filter and sort controls have 4px corners at 100% and 200% text`);

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
          mask: icon.maskImage || icon.webkitMaskImage, paint: icon.backgroundColor,
          ground: getComputedStyle(group).backgroundColor };
      })()`);
      assert.equal(got.invalid, 'true', `${where}: the email field is not marked refused`);
      assert.ok(got.text.length > 0, `${where}: the refusal has no sentence`);
      assert.equal(got.refused, t['--error'], `${where}: the refused field's name is ${got.refused}, not the error colour ${t['--error']}`);
      assert.equal(got.other, t['--fg'], `${where}: a field nobody refused has the name colour ${got.other}, not ${t['--fg']}`);
      assert.equal(got.sentence, t['--error'], `${where}: the sentence is ${got.sentence}, not the error colour`);
      assert.equal(got.content, '""', `${where}: no icon before the sentence (content ${got.content})`);
      assert.ok(got.display === 'inline-block' && got.width > 0, `${where}: the icon is ${got.display} and ${got.width}px wide`);
      // The icon is a data: image, the one kind the policy admits. Used as a
      // mask, the shape is painted in the ground under it, which has to be the
      // error colour; used as an image, the colour is the stroke written into
      // it, decoded and painted to compare.
      const dataImage = /^url\("?data:image\/svg\+xml,(.*?)"?\)$/;
      const masked = dataImage.exec(got.mask || '');
      const drawn = masked || dataImage.exec(got.image);
      assert.ok(drawn, `${where}: the icon is not a data: image: ${got.image}, mask ${got.mask}`);
      const stroke = /stroke=['"]([^'"]+)['"]/.exec(decodeURIComponent(drawn[1]));
      assert.ok(stroke, `${where}: the icon has no stroked shape: ${drawn[0]}`);
      if (masked) {
        assert.equal(got.paint, t['--error'], `${where}: the icon's mask is painted ${got.paint}, not the error colour ${t['--error']}`);
      } else {
        const strokeColour = await evaluate(`(() => {
          const probe = document.createElement('div');
          probe.style.backgroundColor = ${JSON.stringify(stroke[1])};
          document.body.append(probe);
          const colour = getComputedStyle(probe).backgroundColor;
          probe.remove();
          return colour;
        })()`);
        assert.equal(strokeColour, t['--error'], `${where}: the icon is drawn in ${stroke[1]} (${strokeColour}), not the error colour ${t['--error']}`);
      }
      // The checkout's groups are words on the page, so the field's group is the
      // page's own ground; what is measured is the refusal against it.
      assert.equal(got.ground, 'rgba(0, 0, 0, 0)', `${where}: the group the field sits in paints ${got.ground}, not the page's ground`);
      got.ground = t['--sys-surface'];
      for (const [what, colour] of [['name', got.refused], ['sentence', got.sentence]]) {
        assert.ok(ratio(colour, got.ground) >= 4.5, `${where}: the refused ${what} ${colour} on ${got.ground} is ${ratio(colour, got.ground).toFixed(2)}:1, under 4.5:1`);
      }
      console.log(`${where}: name and sentence ${ratio(got.refused, got.ground).toFixed(2)}:1 in the error colour on the page, icon drawn`);
    }

    // The text option: a square box of the field corner on the page's ground,
    // chosen by a 2px ink frame (its 1px edge and an inset line of the same
    // ink), with no tint, no blue and no check. Choosing one does not change
    // the height.
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
        outlineWidth: style.outlineWidth, decoration: style.textDecorationLine,
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
        const corner = await evaluate(`(() => {
          const probe = document.createElement('div');
          probe.style.borderTopLeftRadius = 'var(--shape-xs)';
          document.body.append(probe);
          const got = getComputedStyle(probe).borderTopLeftRadius;
          probe.remove();
          return got;
        })()`);
        const [plain, chosen, soldOut] = await swatches(['swatch-0-0', 'swatch-0-1', 'swatch-0-2']);
        for (const [name, got] of [['plain', plain], ['chosen', chosen], ['sold out', soldOut]]) assert.ok(got, `${where}: no ${name} option`);
        assert.equal(chosen.current, 'true', `${where}: the second option is not the current one`);
        assert.equal(corner, '4px', `${where}: --shape-xs is ${corner}, not the 4px control corner`);
        assert.equal(plain.background, t['--sys-surface'], `${where}: an unchosen option is ${plain.background}, not the page's ground`);
        assert.deepEqual(plain.widths, Array(4).fill('1px'), `${where}: an unchosen edge is ${plain.widths}`);
        assert.deepEqual(plain.colours, Array(4).fill(t['--control-boundary']), `${where}: an unchosen edge is ${plain.colours}, not the control edge ${t['--control-boundary']}`);
        assert.equal(plain.radius, corner, `${where}: an unchosen option has corner ${plain.radius}, not ${corner}`);
        assert.equal(plain.tick, null, `${where}: an unchosen option is ticked`);
        assert.equal(plain.shadow, 'none', `${where}: an unchosen option draws a shadow: ${plain.shadow}`);
        assert.equal(chosen.background, plain.background, `${where}: the chosen option is ${chosen.background}, not the unchosen ground ${plain.background}: no tint`);
        assert.deepEqual(chosen.colours, Array(4).fill(t['--sys-on-surface']), `${where}: the chosen edge is ${chosen.colours}, not the ink ${t['--sys-on-surface']}`);
        assert.equal(chosen.shadow, `${t['--sys-on-surface']} 0px 0px 0px 1px inset`, `${where}: the chosen option's second pixel is ${chosen.shadow}, not an inset line of the ink`);
        assert.equal(chosen.color, plain.color, `${where}: the chosen text is ${chosen.color}, not the unchosen text ${plain.color}`);
        assert.equal(chosen.radius, corner, `${where}: the chosen option has corner ${chosen.radius}, not ${corner}`);
        assert.ok(Math.abs(chosen.height - plain.height) < 0.5, `${where}: choosing resizes the option from ${plain.height}px to ${chosen.height}px`);
        assert.equal(chosen.tick, null, `${where}: the chosen option is ticked: ${JSON.stringify(chosen.tick)}`);
        assert.ok(chosen.inside && plain.inside, `${where}: an option is cut by the edge of the screen`);
        // Contrast, by the formula above: the frame against the page it sits
        // on (1.4.11, at least 3:1) and the text against the ground (1.4.3, at
        // least 4.5:1).
        const edge = ratio(chosen.colours[0], chosen.ground), text = ratio(chosen.color, chosen.background);
        assert.ok(edge >= 3, `${where}: the chosen frame on the page is ${edge.toFixed(2)}:1, under 3:1`);
        assert.ok(text >= 4.5, `${where}: the chosen text on its ground is ${text.toFixed(2)}:1, under 4.5:1`);
        console.log(`${where}: chosen frame ${edge.toFixed(2)}:1, text ${text.toFixed(2)}:1, no tint, no check`);
      }
    }
    {
      await open('/product?options=in', 1440);
      const t = await tokens(names);
      const edge = id => evaluate(`getComputedStyle(document.getElementById(${JSON.stringify(id)})).borderTopColor`);
      await force('#swatch-0-1', ['hover']);
      assert.equal(await edge('swatch-0-1'), t['--sys-on-surface'], `${label}: the chosen option loses its ink frame under the pointer`);
      await force('#swatch-0-1', []);
      console.log(`${label}: the chosen option keeps its ink frame under the pointer`);
    }

    // A chosen option that is sold out is chosen the same way, still dashed,
    // and holds what product-options-check measures: a light ground, a frame
    // of at least 3:1 and words of at least 4.5:1.
    for (const size of ['', '200%']) {
      for (const width of [320, 1440]) {
        const where = `sold-out chosen option ${width}px at ${size || '100%'}`;
        await open('/product?options=out', width);
        await rootSize(size);
        const t = await tokens(names);
        const [got] = await swatches(['swatch-0-1']);
        assert.ok(got, `${where}: no chosen option`);
        assert.equal(got.current, 'true', `${where}: not the current option`);
        assert.equal(got.background, t['--sys-surface'], `${where}: the ground is ${got.background}, not the page's`);
        assert.ok(rgb(got.background).every(channel => channel > 200), `${where}: the ground ${got.background} is not light`);
        assert.deepEqual(got.colours, Array(4).fill(t['--sys-on-surface']), `${where}: the frame is ${got.colours}, not the ink`);
        assert.equal(got.borderStyle, 'dashed', `${where}: the edge is ${got.borderStyle}, so a sold-out option no longer looks sold out`);
        assert.equal(got.shadow, `${t['--sys-on-surface']} 0px 0px 0px 1px inset`, `${where}: the second pixel is ${got.shadow}`);
        assert.equal(got.tick, null, `${where}: the option is ticked`);
        const edge = ratio(got.colours[0], got.ground), text = ratio(got.color, got.background), note = ratio(got.noteColour, got.background);
        assert.ok(edge >= 3, `${where}: the frame on the page is ${edge.toFixed(2)}:1, under 3:1`);
        assert.ok(text >= 4.5, `${where}: the words on the ground are ${text.toFixed(2)}:1, under 4.5:1`);
        assert.ok(note >= 4.5, `${where}: the sold-out note on the ground is ${note.toFixed(2)}:1, under 4.5:1`);
        assert.ok(got.inside, `${where}: the option is cut by the edge of the screen`);
        console.log(`${where}: dashed ink frame ${edge.toFixed(2)}:1, words ${text.toFixed(2)}:1, note ${note.toFixed(2)}:1`);
      }
    }

    // The delivery card: a 2px ink edge on the page's own ground when chosen,
    // a 1px outline when not, with its radio drawn in every scheme, and a name
    // that keeps room for words at any text size.
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
        assert.equal(chosen.background, t['--sys-surface'], `${where}: the chosen card is ${chosen.background}, not the page's ground`);
        assert.deepEqual(chosen.widths, Array(4).fill('2px'), `${where}: the chosen edge is ${chosen.widths}`);
        assert.deepEqual(chosen.colours, Array(4).fill(t['--sys-on-surface']), `${where}: the chosen edge is ${chosen.colours}, not ink`);
        assert.equal(chosen.shadow, 'none', `${where}: a shadow draws the chosen edge: ${chosen.shadow}`);
        assert.equal(plain.background, t['--sys-surface'], `${where}: an unchosen card is ${plain.background}, not the page's ground`);
        assert.deepEqual(plain.widths, Array(4).fill('1px'), `${where}: an unchosen edge is ${plain.widths}`);
        assert.deepEqual(plain.colours, Array(4).fill(t['--sys-outline']), `${where}: an unchosen edge is ${plain.colours}, not the outline`);
        assert.ok(ratio(plain.colours[0], t['--sys-surface']) >= 3, `${where}: an unchosen edge on the page is ${ratio(plain.colours[0], t['--sys-surface']).toFixed(2)}:1, under 3:1`);
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
        console.log(`${where}: 2px ink edge on the page, 1px outline unchosen, radio drawn, name ${chosen.nameWidth.toFixed(0)}px wide, narrowest of ${choices.length} cards ${Math.min(...choices.map(card => card.width)).toFixed(0)}px`);
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
      // The inset line is a shadow, which forced colours drop, so the chosen
      // box is told by a 2px outline in the system's highlight and an underline.
      assert.equal(chosen.tick, null, `${label}: forced colours: the chosen option is ticked`);
      assert.deepEqual([chosen.outline, chosen.outlineWidth], ['solid', '2px'], `${label}: forced colours: the chosen option's outline is ${chosen.outline} ${chosen.outlineWidth}`);
      assert.equal(plain.outline, 'none', `${label}: forced colours: an unchosen option draws an outline: ${plain.outline}`);
      assert.match(chosen.decoration, /underline/, `${label}: forced colours: the chosen option is not underlined: ${chosen.decoration}`);
    }
    await media([]);
    console.log(`${label}: forced colours keep the 2px edge, the chosen option's outline and the radio`);

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

    // The checkout is one layer: a white page, and the form's groups and the
    // summary on it as words, with no ground, corner, edge or shadow of their
    // own, so nothing stacks a second layer on the page.
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
        assert.equal(panel.background, 'rgba(0, 0, 0, 0)', `${where}: group ${index} paints ${panel.background}`);
        assert.deepEqual(panel.layers, [t['--n-0']], `${where}: group ${index} sits on ${panel.layers}, not the white page alone`);
        assert.deepEqual(panel.corners, Array(4).fill('0px'), `${where}: group ${index} has corners ${panel.corners}`);
        assert.deepEqual(panel.shadows, [], `${where}: group ${index} or what holds it draws a shadow, so the page is not one layer: ${panel.shadows}`);
        assert.ok(panel.edges.every(edge => edge.startsWith('0px ')), `${where}: group ${index} has edges ${panel.edges}`);
      }
      // A field sits on the page, so its edge is read against the page.
      const edge = ratio(got.field, t['--n-0']);
      assert.ok(edge >= 3, `${where}: a field's edge on the page is ${edge.toFixed(2)}:1, under 3:1`);
      console.log(`${where}: one white layer, ${got.groups.length} groups and the summary as words on it, field edge ${edge.toFixed(2)}:1`);
    }
  }

  // The ways on. An empty cart, an empty wishlist and a missing page offer their
  // next step as the section's one blue link, unlined until hovered, with its
  // arrow; any other way on is ink with the faint underline. None of them draws
  // a solid button, and the checkout draws exactly one, the one that pays.
  {
    const label = 'ways on';
    const read = () => evaluate(`(() => {
      const resolve = value => {
        const probe = document.createElement('div');
        probe.style.color = value;
        document.body.append(probe);
        const got = getComputedStyle(probe).color;
        probe.remove();
        return got;
      };
      const action = (() => {
        const probe = document.createElement('div');
        probe.style.backgroundColor = 'var(--sys-action)';
        document.body.append(probe);
        const got = getComputedStyle(probe).backgroundColor;
        probe.remove();
        return got;
      })();
      const link = selector => {
        const element = document.querySelector(selector);
        if (!element || !element.getClientRects().length) return null;
        const style = getComputedStyle(element);
        return { text: element.textContent.replace(/\\s+/g, ' ').trim(), colour: style.color, line: style.textDecorationLine,
          lineColour: style.textDecorationColor, height: element.getBoundingClientRect().height };
      };
      return {
        blue: resolve('var(--accent-text)'), ink: resolve('var(--sys-on-surface)'),
        next: link('.goen-empty__next, .notice__next'), more: link('.goen-empty__more'),
        solid: [...document.querySelectorAll('a, button, span[role="link"]')]
          .filter(element => !element.closest('.goen-header, .goen-footer') && element.getClientRects().length > 0 &&
            getComputedStyle(element).backgroundColor === action)
          .map(element => element.textContent.trim()),
      };
    })()`);
    for (const [path, more] of [['/cart/empty', true], ['/wishlist', false], ['/missing', false]]) {
      for (const width of [375, 1440]) {
        const where = `${label} ${path} ${width}px`;
        await open(path, width);
        const got = await read();
        assert.ok(got.next, `${where}: no next-step link`);
        assert.equal(got.next.colour, got.blue, `${where}: the next step ${JSON.stringify(got.next.text)} is ${got.next.colour}, not the blue ${got.blue}`);
        assert.equal(got.next.line, 'none', `${where}: the next step is underlined (${got.next.line}) before it is hovered`);
        assert.match(got.next.text, /→$/, `${where}: the next step ${JSON.stringify(got.next.text)} has no arrow`);
        assert.ok(got.next.height >= 44, `${where}: the next step is ${got.next.height}px tall, under 44px`);
        if (more) {
          assert.ok(got.more, `${where}: no secondary link`);
          assert.equal(got.more.colour, got.ink, `${where}: the secondary link is ${got.more.colour}, not ink ${got.ink}`);
          assert.equal(got.more.line, 'underline', `${where}: the secondary link is not underlined`);
          assert.notEqual(got.more.lineColour, got.more.colour, `${where}: the secondary link's underline is not faint (${got.more.lineColour})`);
          assert.ok(got.more.height >= 44, `${where}: the secondary link is ${got.more.height}px tall, under 44px`);
        } else {
          assert.equal(got.more, null, `${where}: a secondary link is drawn`);
        }
        assert.deepEqual(got.solid, [], `${where}: solid buttons are drawn: ${JSON.stringify(got.solid)}`);
        console.log(`${where}: ${JSON.stringify(got.next.text)} blue and unlined${more ? `, ${JSON.stringify(got.more.text)} ink and faintly underlined` : ''}, no solid button`);
      }
    }
    for (const width of [375, 1440]) {
      await open('/checkout', width);
      const got = await read();
      assert.equal(got.solid.length, 1, `${label} /checkout ${width}px: ${got.solid.length} solid buttons, not one: ${JSON.stringify(got.solid)}`);
      assert.match(got.solid[0], /^Place order/, `${label} /checkout ${width}px: the one solid button is ${JSON.stringify(got.solid[0])}, not the one that pays`);
      console.log(`${label} /checkout ${width}px: one solid button, ${JSON.stringify(got.solid[0])}`);
    }
  }
} finally {
  socket.close();
  await fetch(`${debugging}/json/close/${target.id}`);
}
