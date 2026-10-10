import assert from 'node:assert/strict';

export async function checkMenuFallback({ origin, send, evaluate, navigate }) {
  const { cookies } = await send('Network.getCookies', { urls: [origin] });
  const saved = cookies.filter(({ name }) => name === 'goen_locale');
  const failures = [];
  const click = async (selector) => {
    const point = await evaluate(`(() => {
      const target = document.querySelector(${JSON.stringify(selector)});
      if (!target) throw new Error('menu control missing');
      const r = target.getBoundingClientRect();
      if (!r.width || !r.height) throw new Error('menu control is hidden');
      return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
    })()`);
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, ...point });
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...point });
    await new Promise((resolve) => setTimeout(resolve, 400));
  };
  try {
    await send('Network.setCacheDisabled', { cacheDisabled: true });
    await send('Emulation.setDeviceMetricsOverride', { width: 320, height: 800, deviceScaleFactor: 1, mobile: true });
    for (const disabled of [true, false]) {
      await send('Emulation.setScriptExecutionDisabled', { value: disabled });
      for (const locale of ['zh-Hant', 'en']) {
        await send('Network.setCookie', { name: 'goen_locale', value: locale, url: origin + '/' });
        await navigate('/');
        assert.equal(await evaluate('document.documentElement.lang'), locale);
        for (const scale of [100, 200]) {
          const label = `${locale} ${scale}% scripting=${!disabled}`;
          await evaluate(`document.documentElement.style.fontSize = '${scale}%'`);
          await click('[data-menu] > summary');
          assert.equal(await evaluate("document.querySelector('[data-menu]').open"), true, label + ': native menu did not open');
          const visible = await evaluate(`(() => {
            const button = document.querySelector('[data-menu-close]');
            return !!button && button.getClientRects().length > 0 && getComputedStyle(button).visibility !== 'hidden';
          })()`);
          if (disabled) {
            if (visible) failures.push(label + ': a script-only close button is still offered');
            await click('[data-menu] > summary');
          } else {
            assert.equal(visible, true, label + ': enhanced close button missing');
            await click('[data-menu-close]');
            assert.equal(await evaluate("document.activeElement === document.querySelector('[data-menu] > summary')"), true, label + ': close did not return focus');
          }
          assert.equal(await evaluate("document.querySelector('[data-menu]').open"), false, label + ': menu did not close');
        }
      }
    }
    assert.deepEqual(failures, [], 'scripting-off menus must not offer a dead control');
  } finally {
    await send('Emulation.setScriptExecutionDisabled', { value: false });
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
