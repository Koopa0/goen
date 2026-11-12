import assert from 'node:assert/strict';

// The mobile buy bar's four moments, on the product page's own markup, CSS and
// goen.js, measured in Chrome. Each moment first checks the layout assumption
// it rests on (where the buy box's price and add button are), so a page that
// grew or shrank fails as a broken fixture, not as a wrong bar.
const [origin, port] = process.argv.slice(2);
const debugging = `http://127.0.0.1:${port}`;
const response = await fetch(`${debugging}/json/new?about:blank`, { method: 'PUT' });
assert.equal(response.ok, true, 'Chrome must create the buy bar test page');
const target = await response.json();
const socket = new WebSocket(target.webSocketDebuggerUrl);
try {
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let id = 0;
  const pending = new Map();
  socket.onmessage = ({ data }) => {
    const message = JSON.parse(data);
    const request = pending.get(message.id);
    if (!request) return;
    pending.delete(message.id);
    clearTimeout(request.timer);
    if (message.error) request.reject(new Error(JSON.stringify(message.error)));
    else request.resolve(message.result);
  };
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    const current = ++id;
    const timer = setTimeout(() => { pending.delete(current); reject(new Error(`${method} timed out`)); }, 20000);
    pending.set(current, { resolve, reject, timer });
    socket.send(JSON.stringify({ id: current, method, params }));
  });
  const evaluate = async (expression) => {
    const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
    return result.result.value;
  };
  // Two frames after the observer has had a chance to answer.
  const settle = () => evaluate('new Promise((resolve) => setTimeout(() => requestAnimationFrame(() => requestAnimationFrame(resolve)), 150))');
  const open = async (width, height, lang) => {
    await send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: true });
    const url = `${origin}/p/meridian-watch-s3?lang=${lang}`;
    await send('Page.navigate', { url });
    for (let attempt = 0; !await evaluate(`location.href === ${JSON.stringify(url)} && document.readyState === 'complete'`); attempt++) {
      if (attempt === 100) throw new Error(`${url} did not load`);
      await new Promise((resolve) => setTimeout(resolve, 30));
    }
    await evaluate('document.fonts.ready');
    await settle();
  };
  const state = () => evaluate(`(() => {
    const bar = document.querySelector('#buybar');
    const shown = (el) => !!el && getComputedStyle(el).display !== 'none' && el.getClientRects().length > 0;
    const onScreen = (el) => { const r = el.getBoundingClientRect(); return r.bottom > 0 && r.top < innerHeight; };
    // In sight: all of it on screen and clear of the bar, which covers the screen's foot.
    const inSight = (el) => { const r = el.getBoundingClientRect(); return r.top >= 0 && r.bottom <= innerHeight - bar.offsetHeight; };
    const price = bar.querySelector('.goen-buybar__price');
    return {
      root: document.documentElement.dataset.buybar ?? null,
      visible: getComputedStyle(bar).visibility === 'visible',
      dataPrice: bar.dataset.price ?? null,
      price: shown(price) ? price.textContent.trim() : null,
      priceHasStrike: !!price.querySelector('s'),
      was: shown(bar.querySelector('.goen-buybar__was')) ? bar.querySelector('.goen-buybar__was s').textContent.trim() : null,
      chosen: shown(bar.querySelector('.goen-buybar__chosen')) ? bar.querySelector('.goen-buybar__chosen').textContent.trim() : null,
      button: shown(bar.querySelector('button[form="pdp-add"]')),
      boxPriceInSight: inSight(document.querySelector('#buybox .goen-pdp__price')),
      boxPriceTop: Math.round(document.querySelector('#buybox .goen-pdp__price').getBoundingClientRect().top),
      barTop: Math.round(innerHeight - bar.offsetHeight),
      addOnScreen: onScreen(document.querySelector('#add-to-cart')),
      boxPrice: document.querySelector('#buybox .goen-pdp__price').textContent.trim(),
      overflow: document.documentElement.scrollWidth > innerWidth,
    };
  })()`);
  // Reduced motion, so a state is read without waiting out the slide; the slide
  // itself is held below to play only when motion is welcome.
  await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'reduce' }] });
  // A background tab's document has no focus, and :focus matches nothing there.
  await send('Emulation.setFocusEmulationEnabled', { enabled: true });
  for (const lang of ['zh-Hant', 'en']) {
    const chosen = '玫瑰金 · 45mm';

    await open(375, 812, lang);
    let got = await state();
    const at375 = `price at ${got.boxPriceTop}, bar at ${got.barTop}`;
    assert.deepEqual([got.boxPriceInSight, got.addOnScreen], [true, false], `${lang} 375 load: fixture assumption, price in sight and button below (price at ${got.boxPriceTop}, bar at ${got.barTop})`);
    assert.deepEqual({ root: got.root, visible: got.visible, dataPrice: got.dataPrice, price: got.price, was: got.was, chosen: got.chosen, button: got.button },
      { root: 'out', visible: true, dataPrice: 'hidden', price: null, was: null, chosen, button: true },
      `${lang} 375 load: the bar shows the options and the button, and no second price`);
    assert.equal(got.overflow, false, `${lang} 375 load: the page scrolls sideways`);

    await evaluate(`document.querySelector('#add-to-cart').scrollIntoView({ block: 'center' })`);
    await settle();
    got = await state();
    assert.equal(got.addOnScreen, true, `${lang} 375 in view: fixture assumption, the button is on screen`);
    assert.deepEqual({ root: got.root, visible: got.visible }, { root: 'in', visible: false }, `${lang} 375 in view: the bar hides while the add button is on screen`);

    await evaluate(`scrollBy(0, document.querySelector('#add-to-cart').getBoundingClientRect().bottom + 200)`);
    await settle();
    got = await state();
    assert.deepEqual([got.boxPriceInSight, got.addOnScreen], [false, false], `${lang} 375 scrolled: fixture assumption, price and button above`);
    assert.deepEqual({ root: got.root, visible: got.visible, dataPrice: got.dataPrice, price: got.price, priceHasStrike: got.priceHasStrike, was: got.was, chosen: got.chosen, button: got.button },
      { root: 'out', visible: true, dataPrice: null, price: got.boxPrice, priceHasStrike: false, was: 'NT$12,900', chosen, button: true },
      `${lang} 375 scrolled: the bar shows the price, the struck original outside it, and the options`);

    // A field being typed in brings up the on-screen keyboard: the bar steps aside.
    await evaluate(`document.querySelector('#site-search, input[type="search"]').focus()`);
    await settle();
    got = await state();
    assert.equal(got.visible, false, `${lang} 375 scrolled: the bar stays over a focused field`);
    await evaluate('document.activeElement.blur()');

    await open(320, 640, lang);
    got = await state();
    const at320 = `price at ${got.boxPriceTop}, bar at ${got.barTop}`;
    assert.deepEqual([got.boxPriceInSight, got.addOnScreen], [false, false], `${lang} 320x640 load: fixture assumption, price under the bar and button below (price at ${got.boxPriceTop}, bar at ${got.barTop})`);
    assert.deepEqual({ root: got.root, visible: got.visible, dataPrice: got.dataPrice, price: got.price, priceHasStrike: got.priceHasStrike, was: got.was, chosen: got.chosen, button: got.button },
      { root: 'out', visible: true, dataPrice: null, price: got.boxPrice, priceHasStrike: false, was: 'NT$12,900', chosen, button: true },
      `${lang} 320x640 load: the bar shows the price, the struck original outside it, and the options`);
    assert.equal(got.overflow, false, `${lang} 320x640 load: the page scrolls sideways`);
    assert.ok(Math.max(...(await evaluate(`getComputedStyle(document.querySelector('#buybar')).transitionDuration`)).split(',').map(parseFloat)) < 0.01, `${lang}: the bar slides under reduced motion`);
    console.log(`${lang}: load with the price in sight (${at375}), load at 320x640 (${at320}), button in view, scrolled past, field focused`);
  }
  await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'no-preference' }] });
  await open(375, 812, 'zh-Hant');
  assert.ok(Math.max(...(await evaluate(`getComputedStyle(document.querySelector('#buybar')).transitionDuration`)).split(',').map(parseFloat)) >= 0.1, 'the bar does not slide when motion is welcome');
} finally {
  socket.close();
  await fetch(`${debugging}/json/close/${target.id}`);
}
