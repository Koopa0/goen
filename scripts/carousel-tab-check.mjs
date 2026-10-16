import assert from 'node:assert/strict';

const [origin, port] = process.argv.slice(2);
const debugging = `http://127.0.0.1:${port}`;
const response = await fetch(`${debugging}/json/new?about:blank`, { method: 'PUT' });
assert.equal(response.ok, true, 'Chrome must create the carousel test page');
const target = await response.json();
const socket = new WebSocket(target.webSocketDebuggerUrl);
try {
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let id = 0;
  const pending = new Map();
  socket.onmessage = ({ data }) => {
    const message = JSON.parse(data), request = pending.get(message.id);
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
    const state = await evaluate(`(() => {
      const track = document.querySelector('.goen-hero__track');
      return { current: document.querySelector('.goen-hero__tabs [aria-current="true"]')?.getAttribute('href'),
        pageX: scrollX, pageY: scrollY, trackX: track?.scrollLeft,
        trackWidth: track?.clientWidth, slideWidths: [...(track?.children ?? [])].map(slide => slide.getBoundingClientRect().width) };
    })()`);
    throw new Error(`${label} did not settle: ${JSON.stringify(state)}`);
  };
  // The ring is drawn outside the tab by its width and offset; a scrolling row
  // clips it at the row's edge, so the first tab's ring must start inside the row.
  const firstTabRing = async label => {
    const ring = await evaluate(`(() => {
      const row = document.querySelector('.goen-hero__tabs'), tab = row.querySelector('a');
      tab.focus({ preventScroll: true, focusVisible: true });
      const style = getComputedStyle(tab), box = tab.getBoundingClientRect();
      const reach = parseFloat(style.outlineWidth) + parseFloat(style.outlineOffset);
      const pill = getComputedStyle(row, '::before');
      const result = { focused: document.activeElement === tab, visible: tab.matches(':focus-visible'),
        style: style.outlineStyle, ringLeft: box.left - reach, rowLeft: row.getBoundingClientRect().left,
        scrollLeft: row.scrollLeft, ringInner: box.left - parseFloat(style.outlineOffset),
        pillLeft: row.getBoundingClientRect().left + parseFloat(pill.translate) - row.scrollLeft };
      tab.blur();
      return result;
    })()`);
    assert.equal(ring.focused && ring.visible, true, `${label}: first tab must take the keyboard focus ring: ${JSON.stringify(ring)}`);
    assert.notEqual(ring.style, 'none', `${label}: first tab draws a focus ring`);
    assert.ok(ring.ringLeft >= ring.rowLeft - 0.01,
      `${label}: first tab's focus ring is cut at the row's start: ${JSON.stringify(ring)}`);
    // The ring lies outside the pill's edge, not on it.
    assert.ok(ring.ringInner <= ring.pillLeft - 1,
      `${label}: first tab's focus ring overlaps the pill's edge: ${JSON.stringify(ring)}`);
  };
  // The pill is drawn on the row's ::before from --pill-x and --pill-w; once its
  // transitions have finished it must sit on the current tab's box.
  const pillFollows = async label => {
    const got = await evaluate(`(async () => {
      await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      const row = document.querySelector('.goen-hero__tabs'), tab = row.querySelector('a[aria-current="true"]');
      await Promise.all(row.getAnimations({ subtree: true }).map(animation => animation.finished.catch(() => {})));
      const pill = getComputedStyle(row, '::before'), box = row.getBoundingClientRect(), current = tab.getBoundingClientRect();
      return { marked: row.hasAttribute('data-pill'), href: tab.getAttribute('href'),
        pill: { left: box.left + parseFloat(pill.translate) - row.scrollLeft, width: parseFloat(pill.width),
          top: box.top + parseFloat(pill.top), height: parseFloat(pill.height) },
        tab: { left: current.left, width: current.width, top: current.top, height: current.height } };
    })()`);
    assert.equal(got.marked, true, `${label}: the row is not marked for the pill`);
    for (const side of ['left', 'width', 'top', 'height'])
      assert.ok(Math.abs(got.pill[side] - got.tab[side]) <= 0.5,
        `${label}: the pill's ${side} is not the current tab's: ${JSON.stringify(got)}`);
  };
  await send('Page.enable');
  for (const locale of ['zh-Hant', 'en']) for (const width of [375, 390, 1440]) {
    const label = `carousel ${locale} ${width}px`;
    await send('Emulation.setDeviceMetricsOverride', { width, height: 1000, deviceScaleFactor: 1, mobile: width < 768 });
    await send('Emulation.setTouchEmulationEnabled', { enabled: width < 768, maxTouchPoints: 1 });
    const url = `${origin}/carousel?lang=${locale}&width=${width}`;
    await send('Page.navigate', { url });
    await send('Page.bringToFront');
    await waitFor(`location.href === ${JSON.stringify(url)} && document.readyState === 'complete'`, label);
    assert.equal(await evaluate('document.visibilityState'), 'visible', `${label}: active native test page`);
    await evaluate('document.fonts.ready.then(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))');
    assert.equal(await evaluate('document.documentElement.lang'), locale, label);
    await waitFor(`document.querySelector('.goen-hero__nav')?.hidden === false`, `${label} enhancement`);
    await evaluate("document.body.style.minHeight = '3000px'; window.scrollTo({top: 64, behavior: 'instant'})");
    await evaluate('new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
    const before = await evaluate(`(() => {
      const track = document.querySelector('.goen-hero__track').getBoundingClientRect();
      return { y: scrollY, x: scrollX, left: track.left, width: track.width,
        distance: track.width * 0.75 };
    })()`);
    assert.equal(before.y, 64, `${label}: nonzero reading position`);
    await firstTabRing(`${label} at load`);
    // One native gesture per slide: scroll-snap-stop prevents skipping a snap point.
    for (const [index, direction] of [[1, -1], [2, -1], [1, 1], [0, 1]]) {
      const x = before.left + before.width * (direction < 0 ? 0.875 : 0.125);
      const origin = await evaluate(`(() => {
        const track = document.querySelector('.goen-hero__track'), box = track.getBoundingClientRect();
        for (const fraction of [0.5, 0.75, 0.9, 0.25, 0.1]) {
          const y = box.top + box.height * fraction;
          if (document.elementFromPoint(${x}, y)?.closest('.goen-hero__track') === track) return { y };
        }
        return { top: box.top, bottom: box.bottom, hit: document.elementFromPoint(${x}, box.top + box.height / 2)?.outerHTML };
      })()`);
      assert.equal(typeof origin.y, 'number', `${label}: native gesture must start on the carousel: ${JSON.stringify(origin)}`);
      if (width < 768) {
        await send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ id: 1, x, y: origin.y }] });
        try {
          for (let step = 1; step <= 12; step++) {
            await send('Input.dispatchTouchEvent', { type: 'touchMove',
              touchPoints: [{ id: 1, x: x + direction * before.distance * step / 12, y: origin.y }] });
            await evaluate('new Promise(resolve => requestAnimationFrame(resolve))');
          }
          // Release a stationary finger so momentum does not choose the snap point.
          await new Promise(resolve => setTimeout(resolve, 150));
        } finally {
          await send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
        }
      } else {
        await send('Input.synthesizeScrollGesture', { x, y: origin.y,
          xDistance: direction * before.distance, yDistance: 0, speed: 1500, preventFling: true,
          gestureSourceType: 'mouse' });
      }
      await waitFor(`(() => {
        const track = document.querySelector('.goen-hero__track');
        return document.querySelector('.goen-hero__tabs a[aria-current="true"]')?.getAttribute('href') === '#hero-${index + 1}' &&
          Math.abs(track.scrollLeft - ${index} * track.clientWidth) <= 1;
      })()`, `${label} swipe ${index}`);
      await evaluate('new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
      const got = await evaluate(`(() => {
        const row = document.querySelector('.goen-hero__tabs'), tab = row.querySelector('[aria-current="true"]');
        const box = row.getBoundingClientRect(), current = tab.getBoundingClientRect();
        const endPadding = parseFloat(getComputedStyle(row).paddingInlineEnd);
        const right = box.left + row.clientWidth - endPadding;
        const centre = (current.left + current.right) / 2, faults = [];
        if (centre < box.left - 1 || centre > right + 1) faults.push('current state is outside the unfaded tab row');
        if (current.width <= right - box.left && (current.left < box.left - 1 || current.right > right + 1))
          faults.push('current tab is clipped or under the fade');
        const track = document.querySelector('.goen-hero__track');
        return { y: scrollY, x: scrollX, rowY: row.scrollTop, left: row.scrollLeft,
          slideOffset: track.scrollLeft, slideWidth: track.clientWidth,
          overflow: row.scrollWidth > row.clientWidth, current: tab.getAttribute('href'), faults };
      })()`);
      assert.ok(Math.abs(got.slideOffset - index * got.slideWidth) <= 1,
        `${label}: native gesture must actually settle on slide ${index + 1}`);
      assert.equal(got.y, before.y, `${label}: page vertical position`);
      assert.equal(got.x, before.x, `${label}: page horizontal position`);
      assert.equal(got.rowY, 0, `${label}: tab row vertical position`);
      assert.deepEqual(got.faults, [], label);
      if (width < 768) {
        assert.equal(got.overflow, true, `${label}: narrow fixture must require tab scrolling`);
        if (index === 2) assert.ok(got.left > 0, `${label}: third tab must scroll horizontally`);
        else if (index === 0) assert.equal(got.left, 0, `${label}: first tab returns to the start`);
      } else assert.equal(got.left, 0, `${label}: fitting desktop tabs stay still`);
      await pillFollows(`${label} after swipe ${index}`);
      if (index === 0) await firstTabRing(`${label} back on slide 1`);
      console.log(`${label}: ${JSON.stringify(got)}`);
    }
    // A tab is a link the script turns into a scroll to its slide.
    for (const index of [2, 1, 0, 2]) {
      await evaluate(`document.querySelectorAll('.goen-hero__tabs a')[${index}].click()`);
      await waitFor(`(() => {
        const track = document.querySelector('.goen-hero__track');
        return document.querySelector('.goen-hero__tabs a[aria-current="true"]')?.getAttribute('href') === '#hero-${index + 1}' &&
          Math.abs(track.scrollLeft - ${index} * track.clientWidth) <= 1;
      })()`, `${label} click ${index}`);
      await pillFollows(`${label} after click ${index}`);
    }
    // Larger text widens every tab, and a narrower window changes the row.
    await evaluate(`document.documentElement.style.fontSize = '200%'`);
    await pillFollows(`${label} at 200% text`);
    await evaluate(`document.documentElement.style.fontSize = ''`);
    await pillFollows(`${label} back at normal text`);
    await send('Emulation.setDeviceMetricsOverride', { width: width - 40, height: 1000, deviceScaleFactor: 1, mobile: width < 768 });
    await pillFollows(`${label} resized to ${width - 40}px`);
    await send('Emulation.setDeviceMetricsOverride', { width, height: 1000, deviceScaleFactor: 1, mobile: width < 768 });
    await pillFollows(`${label} resized back`);
    const duration = () => evaluate(`getComputedStyle(document.querySelector('.goen-hero__tabs'), '::before').transitionDuration`);
    const milliseconds = value => value.split(',').map(part => Math.round(parseFloat(part) * (part.trim().endsWith('ms') ? 1 : 1000) * 1e6) / 1e6);
    assert.ok(milliseconds(await duration()).includes(280), `${label}: the pill does not move in 280ms: ${await duration()}`);
    await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'reduce' }] });
    const reduced = milliseconds(await duration());
    await send('Emulation.setEmulatedMedia', { features: [] });
    assert.ok(reduced.every(value => value <= 0.01), `${label}: the pill still moves with reduced motion: ${reduced}`);
  }
} finally {
  socket.close();
  await fetch(`${debugging}/json/close/${target.id}`);
}
