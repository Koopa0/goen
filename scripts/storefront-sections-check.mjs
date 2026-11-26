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

  // The departments are the index the missing page and the empty search
  // draw: a square photograph keeping the 4:3 it is shot in beside the
  // name in the display face, one above another on a phone, two across from
  // 768 and three across a desktop. The heading over it is the display face
  // too, and the photograph's link is out of the tab order beside the name's.
  for (const [width, columns] of [[375, 1], [768, 2], [1024, 2], [1440, 3]]) {
    await open('/departments', width);
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
    assert.match(index.heading, /^"?Instrument Sans"?, "?Noto Sans TC"?/, `departments ${width}px: the heading is set in ${index.heading}`);
    for (const photo of index.photos) {
      assert.ok(photo.width > 0 && Math.abs(photo.height - photo.width * 3 / 4) <= 1, `departments ${width}px: a photograph is ${photo.width}x${photo.height}, not 4:3`);
      assert.equal(photo.radius, '0px', `departments ${width}px: a photograph has a ${photo.radius} corner, not square`);
      assert.deepEqual([photo.hidden, photo.tabindex], ['true', '-1'], `departments ${width}px: the photograph's link is aria-hidden=${photo.hidden} tabindex=${photo.tabindex}`);
      assert.match(photo.name, /^"?Instrument Sans"?, "?Noto Sans TC"?/, `departments ${width}px: a name is set in ${photo.name}`);
    }
    console.log(`departments ${width}px: ${index.photos.length} square photographs at 4:3 in ${columns} column${columns === 1 ? '' : 's'}, names in the display face`);
  }

  // A department without a photograph shows its first letter on its tone, a
  // picture rather than words in a link, so no underline runs under it, and its
  // sub-categories are set apart with the dot between spaces that a screen reader
  // skips.
  for (const width of [375, 1440]) {
    await open('/departments?depts=mark', width);
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

  // The home page runs in five rhythms. The bento hero: the dark tile, dark in
  // both schemes, beside the lifestyle tile and the quick index from 1024, one
  // over another below; its headline the page's one h1 in the display face, its
  // chips at least 44px tall. The shelf's cards are four across from 1024, two on
  // a phone, each a hairline frame with the photograph multiplied onto its
  // plate. The stage is dark in both schemes, its products on light plates, two
  // across from 864. The rail scrolls inside itself, each card stopping at its
  // start, under the meter that says what the header's rail says. The other
  // departments: the photograph beside two by two cards from 1024. The rules are
  // four framed cards, one column on a phone and four across a desktop. Nothing
  // widens the page, at 320 and 200% text either.
  const homeProbe = `(() => {
    const box = element => element ? element.getBoundingClientRect() : null;
    // Read where it is drawn: a dark band resolves its tokens in its own scheme.
    const resolve = (property, value, within = document.body) => {
      const probe = document.createElement('span');
      probe.style.setProperty(property, value);
      within.append(probe);
      const got = getComputedStyle(probe).getPropertyValue(property);
      probe.remove();
      return got;
    };
    const tops = items => {
      const first = Math.min(...items.map(item => box(item).top));
      return items.filter(item => Math.abs(box(item).top - first) < 1).length;
    };
    const lead = document.querySelector('.goen-bento__lead');
    const life = document.querySelector('.goen-bento__life');
    const index = document.querySelector('.goen-bento__index');
    const stage = document.querySelector('.goen-stage');
    const plate = document.querySelector('.goen-stage__plate');
    const rail = document.querySelector('.goen-home__rail');
    const shelfCells = [...document.querySelectorAll('.goen-home__shelf > .goen-tile__cell')];
    const media = document.querySelector('.goen-home__shelf .goen-tile__media');
    const img = document.querySelector('.goen-home__shelf .goen-tile__img');
    const cards = [...document.querySelectorAll('.goen-home__rules .ui-statline > div')];
    const chips = [...document.querySelectorAll('.goen-bento__chip')];
    return {
      page: document.body.scrollWidth, viewport: document.documentElement.clientWidth,
      wide: [...document.querySelectorAll('body *')].filter(e => {
        if (box(e).right <= document.documentElement.clientWidth + 0.5) return false;
        for (let p = e.parentElement; p && p !== document.body; p = p.parentElement) {
          if (['auto', 'hidden', 'scroll', 'clip'].includes(getComputedStyle(p).overflowX)) return false;
        }
        return true;
      }).slice(0, 4).map(e => e.tagName.toLowerCase() + '.' + String(e.className).split(' ')[0]),
      h1: [...document.querySelectorAll('h1')].map(h => h.className),
      headline: getComputedStyle(document.querySelector('.goen-bento__headline')).fontFamily,
      headlineSize: parseFloat(getComputedStyle(document.querySelector('.goen-bento__headline')).fontSize),
      lead: { ...box(lead).toJSON(), scheme: getComputedStyle(lead).colorScheme, ground: getComputedStyle(lead).backgroundColor },
      life: box(life).toJSON(), index: box(index).toJSON(),
      stageGround: resolve('background-color', 'var(--stage)', stage),
      leadGround: resolve('background-color', 'var(--stage)', lead),
      stage: { ground: getComputedStyle(stage).backgroundColor, scheme: getComputedStyle(stage).colorScheme,
        plate: getComputedStyle(plate).backgroundColor, platePaint: resolve('background-color', 'var(--stage-plate)', stage),
        blend: getComputedStyle(plate.querySelector('img')).mixBlendMode,
        across: tops([...document.querySelectorAll('.goen-stage__card')]) },
      chips: chips.map(chip => box(chip).height),
      shelf: { across: tops(shelfCells), frame: getComputedStyle(shelfCells[0]).borderTopWidth,
        plate: getComputedStyle(media).backgroundColor, platePaint: resolve('background-color', 'var(--photo-plate)', media),
        blend: getComputedStyle(img).mixBlendMode },
      rail: { overflow: getComputedStyle(rail).overflowX, snap: getComputedStyle(rail).scrollSnapType,
        align: getComputedStyle(rail.firstElementChild).scrollSnapAlign, scrolls: rail.scrollWidth > rail.clientWidth },
      meter: document.querySelector('.goen-meter__text')?.textContent.trim(),
      railWord: document.querySelector('.goen-rail__delivery')?.textContent.trim(),
      bar: (() => { const bar = document.querySelector('.goen-meter__bar'); return bar && [bar.value, bar.max, bar.getAttribute('aria-labelledby')]; })(),
      more: { photo: box(document.querySelector('.goen-more__photo')).toJSON(), grid: box(document.querySelector('.goen-more__grid')).toJSON(),
        columns: tops([...document.querySelectorAll('.goen-more__grid > li')]) },
      rules: { cards: cards.length, columns: tops(cards), edge: cards.map(card => getComputedStyle(card).borderTopWidth) },
    };
  })()`;
  for (const [width, mobile] of [[320, true], [375, true], [768, false], [1024, false], [1440, false]]) {
    for (const [rootSize, scheme] of [['', 'light'], ['', 'dark'], ['200%', 'light']]) {
      if (rootSize && width !== 320) continue;
      const label = `home ${width}px${rootSize ? ' at 200% text' : ''}${scheme === 'dark' ? ' by night' : ''}`;
      await media(scheme === 'dark' ? [{ name: 'prefers-color-scheme', value: 'dark' }] : []);
      await open('/home', width, mobile);
      if (rootSize) await evaluate(`document.documentElement.style.fontSize = ${JSON.stringify(rootSize)}`);
      const got = await evaluate(homeProbe);
      assert.ok(got.page <= got.viewport, `${label}: the page scrolls sideways (${got.page} > ${got.viewport}): ${got.wide}`);
      assert.deepEqual(got.h1, ['goen-bento__headline'], `${label}: the page's h1s are ${JSON.stringify(got.h1)}, want the hero's headline alone`);
      assert.match(got.headline, /^"?Instrument Sans"?, "?Noto Sans TC"?/, `${label}: the headline is set in ${got.headline}`);
      assert.ok(got.headlineSize <= 56, `${label}: the headline is ${got.headlineSize}px, past the 3.5rem cap`);
      assert.equal(got.lead.scheme, 'dark', `${label}: the dark tile does not read the night's tokens`);
      assert.equal(got.lead.ground, got.leadGround, `${label}: the dark tile's ground is ${got.lead.ground}, not the stage's ${got.leadGround}`);
      assert.equal(got.leadGround, got.stageGround, `${label}: the dark tile and the stage are not one ground`);
      const beside = !rootSize && width >= 1024;
      if (beside) {
        assert.ok(got.life.left > got.lead.right && got.index.left > got.lead.right, `${label}: the lifestyle tile and the index are not beside the dark tile`);
        assert.ok(Math.abs(got.lead.bottom - got.index.bottom) < 1 && Math.abs(got.lead.top - got.life.top) < 1, `${label}: the dark tile does not span the two tiles beside it`);
      } else {
        assert.ok(got.life.top >= got.lead.bottom && got.index.top >= got.life.bottom, `${label}: the hero's tiles do not stand one over another`);
      }
      for (const height of got.chips) assert.ok(height >= 44, `${label}: a quick-index chip is ${height}px tall, under 44`);
      assert.equal(got.stage.ground, got.stageGround, `${label}: the stage's ground is ${got.stage.ground}`);
      assert.equal(got.stage.scheme, 'dark', `${label}: the stage does not read the night's tokens`);
      assert.equal(got.stage.plate, got.stage.platePaint, `${label}: a stage plate is ${got.stage.plate}, not --stage-plate`);
      assert.equal(got.stage.blend, 'multiply', `${label}: a stage photograph is not multiplied onto its plate`);
      assert.equal(got.stage.across, width >= 864 && !rootSize ? 2 : 1, `${label}: ${got.stage.across} stage cards across`);
      assert.equal(got.shelf.across, rootSize ? 1 : width >= 1024 ? 4 : width >= 768 ? 3 : 2, `${label}: ${got.shelf.across} shelf cards across`);
      assert.equal(got.shelf.frame, '1px', `${label}: a card's frame is ${got.shelf.frame}`);
      assert.equal(got.shelf.plate, got.shelf.platePaint, `${label}: a card's photograph stands on ${got.shelf.plate}, not --photo-plate`);
      assert.equal(got.shelf.blend, 'multiply', `${label}: a card's photograph is not multiplied onto its plate`);
      assert.deepEqual([got.rail.overflow, got.rail.snap, got.rail.align], ['auto', 'x', 'start'], `${label}: the rail is ${JSON.stringify(got.rail)}`);
      if (width <= 768) assert.ok(got.rail.scrolls, `${label}: the rail does not scroll inside itself`);
      assert.ok(got.meter && got.meter === got.railWord, `${label}: the meter says ${JSON.stringify(got.meter)}, the header's rail ${JSON.stringify(got.railWord)}`);
      assert.deepEqual(got.bar, [190700, 300000, 'meter-text'], `${label}: the meter's bar is ${JSON.stringify(got.bar)}`);
      if (beside) assert.ok(got.more.grid.left > got.more.photo.right, `${label}: the showcase's cards are not beside its photograph`);
      else assert.ok(got.more.grid.top >= got.more.photo.bottom, `${label}: the showcase's cards are not under its photograph`);
      assert.equal(got.more.columns, rootSize ? 1 : 2, `${label}: the showcase's cards are ${got.more.columns} across, want two by two`);
      assert.equal(got.rules.cards, 4, `${label}: ${got.rules.cards} rule cards`);
      assert.equal(got.rules.columns, rootSize || width < 528 ? 1 : width < 896 ? 2 : 4, `${label}: the rules stand ${got.rules.columns} across`);
      assert.deepEqual(got.rules.edge, ['1px', '1px', '1px', '1px'], `${label}: the rule cards are not framed`);
      console.log(`${label}: hero ${beside ? 'beside' : 'stacked'}, shelf ${got.shelf.across} across, stage ${got.stage.across} across, rail scrolls inside, meter as the rail, rules ${got.rules.columns} across`);
    }
  }
  await media([]);

  // The shelf's pills are links to the page's own address: htmx takes the
  // shelf out of the page that address draws, keeps the address, marks the pill
  // chosen and hands focus to it. A colour swatch puts its photograph on the
  // card without leaving it and marks itself pressed; a card only offers its own
  // add to cart for a product with one variant, and a struck price says what it
  // saves in a seal on the photograph.
  {
    const label = 'home shelf';
    await open('/home', 1440);
    const before = await evaluate(`(() => {
      const card = document.querySelector('#shelf .goen-tile__cell:nth-child(2)');
      const swatch = card.querySelector('.goen-tile__swatch[aria-pressed="false"]');
      const img = card.querySelector('.goen-tile__img');
      const was = img.getAttribute('src');
      swatch.click();
      return { was, now: img.getAttribute('src'), want: swatch.dataset.swatch,
        pressed: [...card.querySelectorAll('.goen-tile__swatch')].map(button => button.getAttribute('aria-pressed')),
        href: location.href, adds: [...document.querySelectorAll('#shelf .goen-tile__cell')].map(cell => cell.querySelector('form[data-quick-add]') !== null),
        seal: document.querySelector('#shelf .goen-tile__cell:first-child .goen-tile__seal')?.textContent,
        seals: document.querySelectorAll('#shelf .goen-tile__seal').length };
    })()`);
    assert.notEqual(before.now, before.was, `${label}: a swatch did not change the card's photograph`);
    assert.equal(before.now, before.want, `${label}: the swatch put ${before.now} on the card, not its own ${before.want}`);
    assert.deepEqual(before.pressed, ['false', 'true'], `${label}: the swatches read pressed ${before.pressed}`);
    assert.equal(before.href, `${origin}/home`, `${label}: a swatch left the page`);
    assert.deepEqual(before.adds, [false, false, true, false], `${label}: quick adds ${before.adds}, want the one-variant card's alone`);
    assert.equal(before.seal, 'Save NT$1,000', `${label}: the sale's seal reads ${before.seal}`);
    assert.equal(before.seals, 1, `${label}: ${before.seals} seals, want the struck price's alone`);
    await evaluate(`(() => { const pill = document.querySelector('#shelf-audio'); pill.focus(); pill.click(); })()`);
    await waitFor(`document.querySelector('#shelf-audio')?.getAttribute('aria-current') === 'true' && document.activeElement?.id === 'shelf-audio'`, `${label}: the audio pill`);
    const after = await evaluate(`(() => ({ search: location.search, names: [...document.querySelectorAll('#shelf .goen-tile__name')].map(name => name.textContent),
      shelves: document.querySelectorAll('#shelf').length, stage: document.querySelectorAll('.goen-stage').length }))()`);
    assert.equal(after.search, '?shelf=audio', `${label}: the address after the pill is ${after.search}`);
    assert.deepEqual(after.names, ['Koto Speaker'], `${label}: the shelf after the pill shows ${after.names}`);
    assert.deepEqual([after.shelves, after.stage], [1, 1], `${label}: the swap drew ${after.shelves} shelves and ${after.stage} stages`);
    console.log(`${label}: a swatch swaps the photograph in place, the pill swaps the shelf and keeps focus, one quick add, one seal`);
  }

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
  // have no corner at all. The home hero's way in is a seal too, and the
  // shelf's pills are full pills.
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
    // The language button: a word of the bar that every width and text size draws.
    const words = '.goen-header__lang > .goen-header__tool';
    const chip = '.goen-filters__chip';
    const way = '.goen-bento__cta';
    const pill = '.goen-pill';
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
      assert.equal(length(cart[words], words), 8, `${label}: the header's language button has a ${cart[words].radius} corner at ${scale}, not a control's 8px`);
      assert.equal(cart[filled].shadow, 'none', `${label}: the filled button at ${scale} casts a shadow: ${cart[filled].shadow}`);

      await open('/c/audio', 1440);
      const listing = await measure([chip], rootSize);
      assert.ok(listing[chip]?.height > 0, `${label}: no filter chip is drawn at ${scale}`);
      corner[scale] = length(listing[chip], chip);
      assert.equal(corner[scale], 1.625 * listing[chip].rem, `${label}: a filter chip has a ${listing[chip].radius} corner at ${scale}, not --r-press (${1.625 * listing[chip].rem}px)`);
      assert.ok(corner[scale] >= listing[chip].height / 2 - 0.5, `${label}: a filter chip is ${listing[chip].height}px tall with a ${corner[scale]}px corner at ${scale}, not a full pill`);

      await open('/home', 1440);
      const home = await measure([way, pill], rootSize);
      assert.ok(home[way]?.height > 0 && home[pill]?.height > 0, `${label}: the hero's way in or a pill is not drawn at ${scale}`);
      assert.equal(length(home[way], way), home[way].rem * 0.5, `${label}: the hero's way in has a ${home[way].radius} corner at ${scale}, not --r-lg (${home[way].rem * 0.5}px)`);
      assert.ok(length(home[pill], pill) >= home[pill].height / 2 - 0.5, `${label}: a shelf pill is ${home[pill].height}px tall with a ${home[pill].radius} corner at ${scale}, not a full pill`);
    }
    assert.equal(corner['200%'], corner['100%'] * 2, `${label}: ${corner['100%']}px at 100% text is ${corner['200%']}px at 200%, not double`);
    console.log(`${label}: chips ${corner['100%']}px at 100% text, ${corner['200%']}px at 200%; fields 4px, footer button square inside its field, header words 8px, the hero's way in a seal and the shelf's pills full pills`);
  }

  // The page frame. The bar is one row at 1024 and up in either language, it
  // sticks to the top of the window and takes at most 12% of it, and the rail
  // of departments under it scrolls away with the page. The cart reads 購物車 2
  // and its subtotal in figures of one width; the current department is the
  // ink pill. Below 1024 the departments scroll sideways and the right edge of
  // a 320, 375 or 414 screen cuts a department at least 12px in, so the row
  // shows it goes on. The footer's links are ink and underlined, and its
  // subscribe is an ink word with no arrow.
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
      const shown = document.querySelector('#cart-link .goen-header__cartshown');
      const word = [...shown.childNodes].find(node => node.nodeType === Node.TEXT_NODE && node.textContent.trim());
      const range = document.createRange();
      if (word) range.selectNodeContents(word);
      const head = document.querySelector('.goen-header');
      const headStyle = getComputedStyle(head);
      return { bar: box(document.querySelector('.goen-header__bar')), nav: box(nav), search: box(document.querySelector('.goen-header__search')),
        actions: box(document.querySelector('.goen-header__actions')), cart: shown.textContent.replace(/\s+/g, ' ').trim(),
        figures: count && getComputedStyle(count).fontVariantNumeric,
        gap: word && count ? count.getBoundingClientRect().left - range.getBoundingClientRect().right : null,
        sticky: { position: headStyle.position, top: headStyle.top, rail: getComputedStyle(document.querySelector('.goen-rail')).position },
        height: head.getBoundingClientRect().height, window: window.innerHeight,
        current: { text: current.textContent.trim(), background: style.backgroundColor, colour: style.color } };
    })()`);
    for (const [path, cart] of [['/frame', '購物車 2 NT$1,907'], ['/frame?lang=en', 'Cart 2 NT$1,907']]) {
      for (const width of [1024, 1440]) {
        await open(path, width);
        const got = await header();
        const where = `${label} ${path} ${width}px`;
        const oneRow = got.search.top < got.actions.bottom && got.actions.top < got.search.bottom;
        assert.ok(oneRow, `${where}: the search field and the actions are not on one row: ${JSON.stringify(got)}`);
        assert.ok(got.nav.top >= got.bar.bottom - 0.5, `${where}: the departments are not on the rail under the bar: ${JSON.stringify(got)}`);
        assert.deepEqual(got.sticky, { position: 'sticky', top: '0px', rail: 'static' }, `${where}: the bar and the rail stick as ${JSON.stringify(got.sticky)}`);
        assert.ok(got.height <= got.window * 0.12, `${where}: the sticky bar is ${got.height}px of a ${got.window}px window, over 12%`);
        assert.equal(got.cart, cart, `${where}: the cart reads ${JSON.stringify(got.cart)}`);
        assert.match(got.figures, /tabular-nums/, `${where}: the cart's count is set in ${got.figures}, not tabular figures`);
        assert.ok(got.gap >= 3.5, `${where}: the cart's word and its count are ${got.gap}px apart, run together`);
        assert.deepEqual([got.current.background, got.current.colour], ['rgb(20, 20, 18)', 'rgb(255, 255, 255)'],
          `${where}: the current department ${got.current.text} is drawn ${JSON.stringify(got.current)}, not the ink pill`);
        console.log(`${where}: one sticky row of ${got.height}px, ${got.cart}, current department the ink pill`);
      }
    }

    for (const width of [320, 375, 414]) {
      await open('/frame', width, true);
      const bar = await header();
      assert.ok(bar.height <= bar.window * 0.12, `${label} /frame ${width}px: the sticky bar is ${bar.height}px of a ${bar.window}px window, over 12%`);
      // goen.js centres the current department; the cut is measured at the
      // row's start, as a page with no current department opens.
      await evaluate(`document.querySelector('.goen-header__nav').scrollLeft = 0; new Promise(done => requestAnimationFrame(() => requestAnimationFrame(done)))`);
      const row = await evaluate(`(() => {
        const nav = document.querySelector('.goen-header__nav');
        const edge = document.documentElement.clientWidth;
        const links = [...nav.querySelectorAll(':scope > a')];
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
      // The row's padding is the gutter; a margin at the start on top of it stood a link 32px in
      // and left a sliver of the word before it at the edge.
      assert.equal(row.margin, '0px', `${where}: a link's scroll margin at the start is ${row.margin}, not 0px`);
      assert.ok(row.into !== null && row.into >= 12, `${where}: the right edge cuts no department at least 12px in: ${JSON.stringify(row)}`);
      console.log(`${where}: a sticky bar of ${bar.height}px; the edge cuts a department ${row.into}px in`);
    }

    // At 200% text a phone's bar wraps past 12% of the window, and a bar that
    // tall must not stick over what has focus: it scrolls away with the page.
    {
      await open('/frame', 375, true);
      const big = await evaluate(`(async () => {
        document.documentElement.style.fontSize = '200%';
        await new Promise(done => requestAnimationFrame(() => requestAnimationFrame(done)));
        const head = document.querySelector('.goen-header');
        const got = { height: head.getBoundingClientRect().height, window: innerHeight, position: getComputedStyle(head).position };
        document.documentElement.style.fontSize = '';
        return got;
      })()`);
      const where = `${label} /frame 375px at 200%`;
      assert.ok(big.height > big.window * 0.12, `${where}: the bar is ${big.height}px, no taller than 12%, so this measured nothing`);
      assert.equal(big.position, 'relative', `${where}: a ${big.height}px bar still sticks: ${JSON.stringify(big)}`);
      console.log(`${where}: a ${big.height}px bar scrolls away with the page`);
    }

    // goen.js scrolls the row to the current department, whichever it is, in either language: the
    // current one starts at the gutter or, among the last, wherever the row ends, and the edge
    // never leaves half a word of another.
    for (const width of [320, 375, 414]) for (const lang of ['', '&lang=en']) for (const slug of ['books-stationery', 'home-living', 'beauty', 'fashion', 'food-drink', 'tech']) {
      await open(`/frame?nav=${slug}${lang}`, width, true);
      const row = await evaluate(`(() => {
        const nav = document.querySelector('.goen-header__nav');
        const box = nav.getBoundingClientRect();
        const at = link => { const r = link.getBoundingClientRect(); return { name: link.textContent.trim(), left: Math.round(r.left - box.left), right: Math.round(r.right - box.left) }; };
        const links = [...nav.querySelectorAll(':scope > a')].map(at);
        return { width: box.width, scrolled: nav.scrollLeft, current: at(nav.querySelector('[aria-current="page"]')),
          cut: links.filter(l => l.left < -0.5 && l.right > 0.5) };
      })()`);
      const where = `${label} /frame?nav=${slug}${lang} ${width}px`;
      assert.deepEqual(row.cut, [], `${where}: the row's start cuts a department in half: ${JSON.stringify(row)}`);
      assert.ok(row.current.left >= 0 && row.current.right <= row.width, `${where}: the current department is not whole in view: ${JSON.stringify(row)}`);
    }
    console.log(`${label}: the current department is whole and no other is cut at the row's start, 320, 375 and 414px, both languages`);

    await open('/frame', 1440);
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

  // A product's main photograph stands on the plate: the studio's pale ground
  // with a hairline edge and the photograph's 12px corner, the picture inset
  // from its edge and multiplied into it, so the near-white it was shot on
  // leaves no white box. The well the picture sits in, where a photograph
  // clicked on a card lands, stays square and draws nothing of its own. The
  // product page carries no department tone, so nothing tones the plate, and a
  // placeholder's words on it can be read.
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
      // syntax, into the rgb() text the plate's own background computes to.
      const probe = document.createElement('div');
      document.body.append(probe);
      const resolve = name => {
        probe.style.backgroundColor = '';
        probe.style.backgroundColor = getComputedStyle(gallery).getPropertyValue(name);
        return getComputedStyle(probe).backgroundColor;
      };
      const plate = getComputedStyle(gallery, '::before');
      const style = getComputedStyle(shot);
      const sides = ['Top', 'Right', 'Bottom', 'Left'];
      const corners = s => ['TopLeft', 'TopRight', 'BottomRight', 'BottomLeft'].map(corner => s['border' + corner + 'Radius']);
      const image = shot.querySelector('.goen-pdp__shotimg');
      const placeholder = shot.querySelector('.goen-ph');
      const outer = shot.getBoundingClientRect(), frame = gallery.getBoundingClientRect();
      const result = {
        toned: !!(gallery.closest('[data-tone]') || gallery.querySelector('[data-tone]')),
        paper: resolve('--photo-plate'),
        plate: plate.backgroundColor, plateCorners: corners(plate),
        plateEdge: sides.map(side => plate['border' + side + 'Width']),
        wellBackground: style.backgroundColor, wellCorners: corners(style), wellClip: style.overflowX,
        shotBorder: sides.map(side => style['border' + side + 'Width']),
        spans: [outer.left - frame.left, frame.right - outer.right],
      };
      if (image) {
        const picture = getComputedStyle(image);
        const inner = image.getBoundingClientRect();
        Object.assign(result, {
          imageBorder: sides.map(side => picture['border' + side + 'Width']),
          blend: picture.mixBlendMode,
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
        assert.equal(drawn.plate, drawn.paper, `${label}: the plate is ${drawn.plate}, not the photograph plate ${drawn.paper}`);
        assert.deepEqual(drawn.plateCorners, Array(4).fill('12px'), `${label}: the plate's corners are ${drawn.plateCorners}`);
        assert.deepEqual(drawn.plateEdge, Array(4).fill('1px'), `${label}: the plate's edge is ${drawn.plateEdge}`);
        assert.equal(drawn.wellBackground, 'rgba(0, 0, 0, 0)', `${label}: the well paints ${drawn.wellBackground} over the plate`);
        assert.deepEqual(drawn.wellCorners, Array(4).fill('0px'), `${label}: the well's corners are ${drawn.wellCorners}, not square`);
        assert.equal(drawn.wellClip, 'hidden', `${label}: the well does not clip, so a photograph landing in it has no corner to land on`);
        assert.deepEqual(drawn.shotBorder, Array(4).fill('0px'), `${label}: the well has a border ${drawn.shotBorder}`);
        for (const gap of drawn.spans) assert.ok(Math.abs(gap) < 0.5, `${label}: the well does not span the plate: ${drawn.spans}`);
        if (photo) {
          assert.deepEqual(drawn.imageBorder, Array(4).fill('0px'), `${label}: the picture has an edge ${drawn.imageBorder}`);
          assert.equal(drawn.blend, 'multiply', `${label}: the picture is not multiplied into the plate`);
          for (const gap of drawn.inset) assert.ok(gap >= 8, `${label}: the picture sits ${drawn.inset} inside the plate, want at least 8px from each edge`);
          console.log(`${label}: ${drawn.plate} plate, 12px corners, square well, picture inset and multiplied`);
        } else {
          const contrast = ratio(channels(drawn.textColor), channels(drawn.plate));
          assert.ok(contrast >= 4.5, `${label}: placeholder ${drawn.textColor} on ${drawn.plate} is ${contrast.toFixed(2)}:1`);
          console.log(`${label}: ${drawn.plate} plate, square well, placeholder text ${contrast.toFixed(2)}:1`);
        }
      }
    }
  }
  // The header's search is a field on the shelf with a 1px edge on all four
  // sides and the bar's 8px corner at any text size, under a header that keeps
  // its rule on every page, since it sticks over whatever scrolls under it.
  // Below 640 the field opens from its button first.
  {
    const label = 'header search';
    const measure = rootSize => evaluate(`(() => {
      document.documentElement.style.fontSize = ${JSON.stringify(rootSize)};
      const opener = document.querySelector('[data-search-open]');
      if (opener && getComputedStyle(opener).display !== 'none' && !document.querySelector('.goen-header[data-search-open]')) opener.click();
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
        hero: document.querySelector('.goen-bento') !== null,
        rem: parseFloat(getComputedStyle(document.documentElement).fontSize),
        edge: resolve('color', 'var(--edge)'),
        corner: resolve('border-top-left-radius', 'var(--shape-sm)'),
        shelf: resolve('background-color', 'var(--shelf)'),
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
          assert.equal(got.background, got.shelf, `${where}: background is ${got.background}, not the shelf ${got.shelf}`);
          assert.deepEqual(got.widths, ['1px', '1px', '1px', '1px'], `${where}: border widths ${got.widths}`);
          assert.deepEqual(got.styles, ['solid', 'solid', 'solid', 'solid'], `${where}: border styles ${got.styles}`);
          assert.deepEqual(got.colors, Array(4).fill(got.edge), `${where}: border colours ${got.colors}, not the control edge ${got.edge}`);
          assert.equal(got.corner, '8px', `${where}: --shape-sm resolves to ${got.corner}, not 8px`);
          assert.deepEqual(got.radii, Array(4).fill('8px'), `${where}: corners ${got.radii}, not the bar's 8px`);
          assert.equal(got.paddingLeft, `${2.5 * got.rem}px`, `${where}: left padding ${got.paddingLeft}, not 2.5rem`);
          assert.ok(Math.abs(got.iconLeft - got.rem) < 0.5, `${where}: the magnifier starts ${got.iconLeft}px in, not 1rem (${got.rem}px)`);
          assert.ok(got.iconRight <= 2.5 * got.rem, `${where}: the magnifier ends ${got.iconRight}px in, over the text that starts at 2.5rem`);
          assert.deepEqual(got.rule, { width: '1px', style: 'solid' }, `${where}: the header's rule is ${JSON.stringify(got.rule)}`);
        }
        console.log(`${label} ${path} ${width}px: shelf field, 8px corner, 1px edge, 2.5rem inset, header rule kept`);
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

  // A department's shelf opens with words on the page, inside the page column:
  // the crumbs, the name in the display face at 40px at most, the count, and
  // the tools. It draws no photograph and no tone ground, whatever the
  // department has. Where the page is too narrow for the filters' column the
  // sub-categories are a row under the name, the current one underlined 2px and
  // the others not, no pill and no edge; on a phone the row scrolls sideways,
  // stops at a name's start and keeps its focus ring inside the link, which the
  // scroller would cut. Where the column stands, the row goes and the tree in
  // it carries the sub-categories: the page's own filled ink, as is the
  // department it is in, and the ring drawn on focus.
  {
    const measure = () => evaluate(`(() => {
      const box = element => {
        const r = element.getBoundingClientRect();
        return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width, height: r.height };
      };
      const page = document.querySelector('.goen-listing');
      const pageStyle = getComputedStyle(page);
      const pageBox = box(page);
      const head = document.querySelector('.goen-shelf__head');
      const headStyle = getComputedStyle(head);
      const headBox = box(head);
      const title = head.querySelector('h1.goen-pagehead__title');
      const titleStyle = getComputedStyle(title);
      const row = head.querySelector('.goen-subindex');
      const rowStyle = getComputedStyle(row);
      const current = row.querySelector('a[aria-current="page"]');
      const other = row.querySelector('a:not([aria-current])');
      const link = element => {
        const style = getComputedStyle(element);
        return { line: style.textDecorationLine, thickness: style.textDecorationThickness, weight: Number(style.fontWeight),
          background: style.backgroundColor, color: style.color, shadow: style.boxShadow, snap: style.scrollSnapAlign, margin: style.scrollMarginLeft, box: box(element) };
      };
      const resolve = (property, value) => {
        const probe = document.createElement('span');
        probe.style.setProperty(property, value);
        document.body.append(probe);
        const got = getComputedStyle(probe).getPropertyValue(property);
        probe.remove();
        return got;
      };
      const tree = document.querySelector('.goen-facets');
      return {
        viewport: innerWidth,
        column: { left: pageBox.left + parseFloat(pageStyle.paddingLeft), right: pageBox.right - parseFloat(pageStyle.paddingRight) },
        head: { left: headBox.left, right: headBox.right },
        grounds: [headStyle.backgroundColor],
        images: head.querySelectorAll('img').length,
        title: { box: box(title), size: parseFloat(titleStyle.fontSize), family: titleStyle.fontFamily },
        row: { shown: rowStyle.display !== 'none', box: box(row), overflowX: rowStyle.overflowX, snap: rowStyle.scrollSnapType, after: getComputedStyle(row, '::after').flexBasis },
        current: link(current), other: link(other),
        tree: getComputedStyle(tree).display === 'none' ? null : {
          page: link(tree.querySelector('.goen-facets__link[aria-current="page"]')),
          department: link(tree.querySelector('.goen-facets__link[aria-current="true"]')),
          plain: link(tree.querySelector('.goen-facets__link:not([aria-current])')),
          subs: [...(document.getElementById('facets-subs')?.parentElement.querySelectorAll('.goen-facets__link') || [])].map(a => box(a).top),
          fill: resolve('background-color', 'var(--fill-ink)'),
          onFill: resolve('color', 'var(--on-fill-ink)'),
        },
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
    for (const width of [375, 800, 1024, 1440]) {
      for (const zh of [0, 1]) {
        const label = `department head ${zh ? 'zh' : 'en'} ${width}px`;
        await open(`/department?tone=ink&photo=1&zh=${zh}`, width);
        const head = await measure();

        // In the page column, words on the page: no photograph, no ground.
        assert.ok(head.head.left >= head.column.left - 0.5 && head.head.right <= head.column.right + 0.5,
          `${label}: the head is ${head.head.left}-${head.head.right}, outside the page column ${head.column.left}-${head.column.right}`);
        assert.equal(head.images, 0, `${label}: the head draws ${head.images} image(s)`);
        for (const ground of head.grounds) assert.equal(ground, 'rgba(0, 0, 0, 0)', `${label}: the head has a ground of ${ground}`);

        // The name is the display face, 40px at most.
        assert.ok(head.title.size <= 40 + 0.5 && head.title.size >= 32 - 0.5, `${label}: the name is ${head.title.size}px, not 32 to 40`);
        assert.match(head.title.family, /^"?Instrument Sans"?, "?Noto Sans TC"?/, `${label}: the name is set in ${head.title.family}`);

        if (head.tree) {
          // The column stands: the row has gone and the tree carries the six.
          assert.equal(head.row.shown, false, `${label}: the sub-categories' row shows beside the tree that carries them`);
          assert.equal(head.tree.subs.length, 6, `${label}: the tree lists ${head.tree.subs.length} sub-categories, not six`);
          assert.ok(head.tree.subs.every((top, i) => i === 0 || top > head.tree.subs[i - 1]), `${label}: the tree's sub-categories are not one to a line, in order: ${head.tree.subs}`);
          for (const [name, l] of [['the page', head.tree.page], ['its department', head.tree.department]]) {
            assert.equal(l.background, head.tree.fill, `${label}: ${name} is not filled ink in the tree: ${l.background}`);
            assert.equal(l.color, head.tree.onFill, `${label}: ${name}'s words are ${l.color} on the ink`);
          }
          assert.equal(head.tree.plain.background, 'rgba(0, 0, 0, 0)', `${label}: a line of the tree is filled at rest`);
          const ring = await ringOf('.goen-facets__link[aria-current="page"]');
          assert.deepEqual([ring.style, ring.width], ['solid', '2px'], `${label}: the focused line of the tree's ring`);
          await media([{ name: 'forced-colors', value: 'active' }]);
          const forced = await evaluate(`getComputedStyle(document.querySelector('.goen-facets__link[aria-current="page"]')).outlineStyle`);
          await media([]);
          assert.equal(forced, 'solid', `${label}: the page's line loses its mark in forced colours`);
          console.log(`${label}: words in the column, name ${head.title.size}px, the tree beside it`);
          continue;
        }

        // The current sub-category is underlined 2px, heavier; the others are plain words.
        assert.ok(head.row.shown, `${label}: the sub-categories have no row while the tree is folded away`);
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
      '--sys-surface', '--sys-on-surface', '--sys-outline', '--cta', '--cobalt-soft', '--text-strong', '--fill-ink', '--on-fill-ink'];

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
      expectCorners(await measure(['#sort']), '/c/audio');
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
      // The checkout's groups are panels on the page's own ground, so the
      // field's group paints the surface; what is measured is the refusal on it.
      assert.equal(got.ground, t['--sys-surface'], `${where}: the panel the field sits in paints ${got.ground}, not the page's surface`);
      for (const [what, colour] of [['name', got.refused], ['sentence', got.sentence]]) {
        assert.ok(ratio(colour, got.ground) >= 4.5, `${where}: the refused ${what} ${colour} on ${got.ground} is ${ratio(colour, got.ground).toFixed(2)}:1, under 4.5:1`);
      }
      console.log(`${where}: name and sentence ${ratio(got.refused, got.ground).toFixed(2)}:1 in the error colour on the page, icon drawn`);
    }

    // The text option: a square box of the field corner on the page's ground,
    // chosen by an ink fill with its words in the fill's own light, as drawn,
    // with no blue and no check. Choosing one does not change the height.
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
        assert.equal(chosen.background, t['--fill-ink'], `${where}: the chosen option is ${chosen.background}, not the ink fill ${t['--fill-ink']}`);
        assert.deepEqual(chosen.colours, Array(4).fill(t['--fill-ink']), `${where}: the chosen edge is ${chosen.colours}, not the fill ${t['--fill-ink']}`);
        assert.equal(chosen.color, t['--on-fill-ink'], `${where}: the chosen text is ${chosen.color}, not the fill's own ${t['--on-fill-ink']}`);
        assert.equal(chosen.radius, corner, `${where}: the chosen option has corner ${chosen.radius}, not ${corner}`);
        assert.ok(Math.abs(chosen.height - plain.height) < 0.5, `${where}: choosing resizes the option from ${plain.height}px to ${chosen.height}px`);
        assert.equal(chosen.tick, null, `${where}: the chosen option is ticked: ${JSON.stringify(chosen.tick)}`);
        assert.ok(chosen.inside && plain.inside, `${where}: an option is cut by the edge of the screen`);
        // Contrast, by the formula above: the fill against the page it sits on
        // (1.4.11, at least 3:1, so the chosen box is told from the others by
        // more than its hue) and the text against the fill (1.4.3, at least 4.5:1).
        const fill = ratio(chosen.background, chosen.ground), text = ratio(chosen.color, chosen.background);
        assert.ok(fill >= 3, `${where}: the chosen fill on the page is ${fill.toFixed(2)}:1, under 3:1`);
        assert.ok(text >= 4.5, `${where}: the chosen text on its fill is ${text.toFixed(2)}:1, under 4.5:1`);
        console.log(`${where}: chosen fill ${fill.toFixed(2)}:1 on the page, text ${text.toFixed(2)}:1, no check`);
      }
    }
    {
      await open('/product?options=in', 1440);
      const t = await tokens(names);
      const fill = id => evaluate(`getComputedStyle(document.getElementById(${JSON.stringify(id)})).backgroundColor`);
      await force('#swatch-0-1', ['hover']);
      assert.equal(await fill('swatch-0-1'), t['--fill-ink'], `${label}: the chosen option loses its ink fill under the pointer`);
      await force('#swatch-0-1', []);
      console.log(`${label}: the chosen option keeps its ink fill under the pointer`);
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

    // The delivery card: when chosen, a 2px edge in the commerce cobalt on its
    // soft tint, the edge at least 3:1 on the panel and the name at least 4.5:1
    // on the tint; when not, a 1px outline on the panel's own ground. Its radio
    // is drawn in every scheme, and its name keeps room for words at any text
    // size.
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
        nameColour: getComputedStyle(name).color,
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
        assert.equal(chosen.background, t['--cobalt-soft'], `${where}: the chosen card is ${chosen.background}, not the cobalt tint`);
        assert.deepEqual(chosen.widths, Array(4).fill('2px'), `${where}: the chosen edge is ${chosen.widths}`);
        assert.deepEqual(chosen.colours, Array(4).fill(t['--cta']), `${where}: the chosen edge is ${chosen.colours}, not the cobalt`);
        assert.ok(ratio(chosen.colours[0], t['--sys-surface']) >= 3, `${where}: the chosen edge on the panel is ${ratio(chosen.colours[0], t['--sys-surface']).toFixed(2)}:1, under 3:1`);
        assert.ok(ratio(chosen.nameColour, chosen.background) >= 4.5, `${where}: the chosen name on its tint is ${ratio(chosen.nameColour, chosen.background).toFixed(2)}:1, under 4.5:1`);
        assert.equal(chosen.shadow, 'none', `${where}: a shadow draws the chosen edge: ${chosen.shadow}`);
        assert.equal(plain.background, t['--sys-surface'], `${where}: an unchosen card is ${plain.background}, not the panel's ground`);
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
        console.log(`${where}: 2px cobalt edge on the tint, 1px outline unchosen, radio drawn, name ${chosen.nameWidth.toFixed(0)}px wide, narrowest of ${choices.length} cards ${Math.min(...choices.map(card => card.width)).toFixed(0)}px`);
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

    // The checkout is panels on the page: each group and the summary paint the
    // page's surface inside one 1px edge, with one corner and no shadow, so the
    // page has two layers and no more. The groups are numbered by a counter,
    // in the order they stand. At 1440 the summary stands beside the form and
    // stays in view; at 375 it follows the form.
    for (const width of [375, 1440]) {
      const where = `checkout panels ${width}px`;
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
          const heading = element.querySelector('h2');
          const box = element.getBoundingClientRect();
          return { layers: painted(element), shadows, background: style.backgroundColor, position: style.position,
            counter: heading ? getComputedStyle(heading, '::before').content : '', left: box.left, right: box.right, top: box.top,
            corners: ${JSON.stringify(corners)}.map(corner => style['border' + corner + 'Radius']),
            edges: ${JSON.stringify(sides)}.map(side => style['border' + side + 'Width'] + ' ' + style['border' + side + 'Style'] + ' ' + style['border' + side + 'Color']) };
        };
        const bare = selector => getComputedStyle(document.querySelector(selector)).backgroundColor;
        const field = getComputedStyle(document.querySelector('#name'));
        const main = document.querySelector('.goen-checkout__main').getBoundingClientRect();
        return { page: bare('.goen-checkout'), main: bare('.goen-checkout__main'), mainRight: main.right, mainBottom: main.bottom,
          groups: ['#shipping', '#address', '#invoice'].map(panel), summary: panel('#summary'),
          field: field.borderTopColor };
      })()`);
      assert.equal(got.page, 'rgba(0, 0, 0, 0)', `${where}: the checkout paints its own page: ${got.page}`);
      assert.equal(got.main, 'rgba(0, 0, 0, 0)', `${where}: the form is a block of ${got.main}`);
      const first = got.groups[0];
      for (const [index, panel] of [...got.groups, got.summary].entries()) {
        assert.ok(panel, `${where}: panel ${index} is not on the page`);
        assert.equal(panel.background, t['--sys-surface'], `${where}: panel ${index} paints ${panel.background}, not the page's surface`);
        assert.deepEqual(panel.layers, [t['--sys-surface']], `${where}: panel ${index} sits on ${panel.layers}, not the page's surface alone`);
        assert.ok(panel.corners.every(corner => corner === first.corners[0]) && parseFloat(first.corners[0]) > 0,
          `${where}: panel ${index} has corners ${panel.corners}, not the panels' one corner`);
        assert.deepEqual(panel.shadows, [], `${where}: panel ${index} or what holds it draws a shadow: ${panel.shadows}`);
        assert.ok(panel.edges.every(edge => edge === first.edges[0] && edge.startsWith('1px solid ')), `${where}: panel ${index} has edges ${panel.edges}`);
      }
      for (const [index, panel] of got.groups.entries()) {
        assert.match(panel.counter, /counter\(checkout-step\)/, `${where}: group ${index}'s heading is not numbered by the counter: ${panel.counter}`);
      }
      if (width === 1440) {
        assert.ok(got.summary.left >= got.mainRight, `${where}: the summary starts at ${got.summary.left}px, not beside the form ending at ${got.mainRight}px`);
        assert.equal(got.summary.position, 'sticky', `${where}: the summary is ${got.summary.position}, not held in view`);
      } else {
        assert.ok(got.summary.top >= got.mainBottom, `${where}: the summary does not follow the form`);
      }
      // A field sits on a panel, so its edge is read against the panel.
      const edge = ratio(got.field, t['--sys-surface']);
      assert.ok(edge >= 3, `${where}: a field's edge on the panel is ${edge.toFixed(2)}:1, under 3:1`);
      console.log(`${where}: ${got.groups.length} numbered panels and the summary on the page, one corner, no shadow, field edge ${edge.toFixed(2)}:1`);
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
      // A solid button is one filled in either action colour: the ink of
      // --sys-action or the cobalt of the commerce action, --cta.
      const fills = ['var(--sys-action)', 'var(--cta)'].map(fill => {
        const probe = document.createElement('div');
        probe.style.backgroundColor = fill;
        document.body.append(probe);
        const got = getComputedStyle(probe).backgroundColor;
        probe.remove();
        return got;
      });
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
            fills.includes(getComputedStyle(element).backgroundColor))
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
