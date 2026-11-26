import assert from 'node:assert/strict';

// The search field lists products as the shopper types. At 1440px in both
// languages: typing opens the list with the combobox expanded; ArrowDown and
// ArrowUp move the active option, named by aria-activedescendant; Escape
// closes the list and keeps the words; Enter on an active option opens that
// product; and a press outside the form closes the list.
export async function checkSearchSuggest({ origin, send, evaluate, navigate }) {
  const { cookies } = await send('Network.getCookies', { urls: [origin] });
  const saved = cookies.filter(({ name }) => name === 'goen_locale');
  const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  const key = async (name, code) => {
    for (const type of ['keyDown', 'keyUp']) await send('Input.dispatchKeyEvent', { type, key: name, code: name, windowsVirtualKeyCode: code });
    await wait(120);
  };
  const state = () => evaluate(`(() => {
    const field = document.getElementById('site-search');
    const panel = document.getElementById('site-search-suggest');
    const options = [...panel.querySelectorAll('[role="option"]')];
    return { shown: !panel.hidden && panel.getClientRects().length > 0, expanded: field.getAttribute('aria-expanded'),
      options: options.length, active: field.getAttribute('aria-activedescendant'),
      selected: options.filter((o) => o.getAttribute('aria-selected') === 'true').map((o) => o.id),
      value: field.value, focus: document.activeElement === field };
  })()`);
  const type = async (text) => {
    await evaluate(`(() => { const f = document.getElementById('site-search'); f.value = ''; f.focus(); })()`);
    await send('Input.insertText', { text });
    for (let i = 0; i < 30; i++) {
      await wait(100);
      const got = await state();
      if (got.options > 0) return got;
    }
    return state();
  };
  try {
    await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
    for (const locale of ['zh-Hant', 'en']) {
      await send('Network.setCookie', { name: 'goen_locale', value: locale, url: origin + '/' });
      await navigate('/');
      const label = `search suggest ${locale}`;

      let got = await type('pixel');
      assert.ok(got.shown && got.expanded === 'true' && got.options > 0, `${label}: typing did not open the list: ${JSON.stringify(got)}`);
      assert.equal(got.active, null, `${label}: an option is active before the arrows were pressed`);

      await key('ArrowDown', 40);
      got = await state();
      assert.deepEqual([got.active, got.selected, got.focus], ['site-search-option-0', ['site-search-option-0'], true], `${label}: ArrowDown did not make the first option active: ${JSON.stringify(got)}`);
      if (got.options > 1) {
        await key('ArrowDown', 40);
        await key('ArrowUp', 38);
        got = await state();
        assert.equal(got.active, 'site-search-option-0', `${label}: ArrowUp did not move back: ${JSON.stringify(got)}`);
      }

      await key('Escape', 27);
      got = await state();
      assert.deepEqual([got.shown, got.expanded, got.active, got.value], [false, 'false', null, 'pixel'], `${label}: Escape did not close the list and keep the words: ${JSON.stringify(got)}`);

      await key('ArrowDown', 40);
      const href = await evaluate(`document.querySelector('#site-search-option-0 a').getAttribute('href')`);
      await key('Enter', 13);
      for (let i = 0; i < 40 && (await evaluate('location.pathname')) !== href; i++) await wait(100);
      assert.equal(await evaluate('location.pathname'), href, `${label}: Enter did not open the active option`);

      await navigate('/');
      got = await type('pixel');
      assert.ok(got.shown, `${label}: the list did not open again: ${JSON.stringify(got)}`);
      const outside = await evaluate(`(() => { const r = document.querySelector('main').getBoundingClientRect(); return { x: r.left + 20, y: Math.min(innerHeight - 20, r.top + 400) }; })()`);
      await send('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, ...outside });
      await send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...outside });
      await wait(200);
      got = await state();
      assert.deepEqual([got.shown, got.expanded], [false, 'false'], `${label}: a press outside did not close the list: ${JSON.stringify(got)}`);
    }
  } finally {
    const current = await send('Network.getCookies', { urls: [origin] });
    for (const cookie of current.cookies.filter(({ name }) => name === 'goen_locale')) {
      await send('Network.deleteCookies', { name: cookie.name, domain: cookie.domain, path: cookie.path });
    }
    for (const cookie of saved) await send('Network.setCookie', {
      name: cookie.name, value: cookie.value, domain: cookie.domain, path: cookie.path,
      secure: cookie.secure, httpOnly: cookie.httpOnly, sameSite: cookie.sameSite,
    });
  }
}
