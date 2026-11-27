import assert from 'node:assert/strict';

// The menu is a modal dialog the storefront's script opens. At 320px, in both
// languages and at 100% and 200% text, a real press on the menu's button opens
// it modal; its close button, Escape and a press on the backdrop each close it;
// and each time focus is back on the button that opened it.
export async function checkMenu({ origin, send, evaluate, navigate }) {
  const { cookies } = await send('Network.getCookies', { urls: [origin] });
  const saved = cookies.filter(({ name }) => name === 'goen_locale');
  const press = async (point) => {
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, ...point });
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...point });
    await new Promise((resolve) => setTimeout(resolve, 400));
  };
  const centre = (selector) => evaluate(`(() => {
    const target = document.querySelector(${JSON.stringify(selector)});
    if (!target) throw new Error(${JSON.stringify(selector + ' is missing')});
    const r = target.getBoundingClientRect();
    if (!r.width || !r.height) throw new Error(${JSON.stringify(selector + ' is hidden')});
    return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
  })()`);
  const state = () => evaluate(`(() => {
    const dialog = document.getElementById('site-menu');
    const opener = document.querySelector('[data-menu-open]');
    return { open: dialog.open, modal: dialog.matches(':modal'), expanded: opener.getAttribute('aria-expanded'),
      onOpener: document.activeElement === opener };
  })()`);
  const escape = async () => {
    for (const type of ['keyDown', 'keyUp']) {
      await send('Input.dispatchKeyEvent', { type, key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
    }
    await new Promise((resolve) => setTimeout(resolve, 400));
  };
  try {
    await send('Network.setCacheDisabled', { cacheDisabled: true });
    await send('Emulation.setDeviceMetricsOverride', { width: 320, height: 800, deviceScaleFactor: 1, mobile: true });
    for (const locale of ['zh-Hant', 'en']) {
      await send('Network.setCookie', { name: 'goen_locale', value: locale, url: origin + '/' });
      await navigate('/');
      assert.equal(await evaluate('document.documentElement.lang'), locale);
      for (const scale of [100, 200]) {
        const label = `${locale} ${scale}%`;
        await evaluate(`document.documentElement.style.fontSize = '${scale}%'`);
        for (const how of ['close button', 'Escape', 'backdrop']) {
          await press(await centre('[data-menu-open]'));
          const opened = await state();
          assert.deepEqual([opened.open, opened.modal, opened.expanded], [true, true, 'true'], `${label}: the menu did not open modal: ${JSON.stringify(opened)}`);
          if (how === 'close button') await press(await centre('[data-menu-close]'));
          else if (how === 'Escape') await escape();
          else await press(await evaluate(`(() => {
            const r = document.getElementById('site-menu').getBoundingClientRect();
            return { x: Math.min(window.innerWidth - 2, r.right + 8), y: window.innerHeight / 2 };
          })()`));
          const closed = await state();
          assert.deepEqual([closed.open, closed.expanded, closed.onOpener], [false, 'false', true],
            `${label}: ${how} did not close the menu and return focus to its button: ${JSON.stringify(closed)}`);
        }
      }
    }
  } finally {
    await send('Network.setCacheDisabled', { cacheDisabled: false });
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
