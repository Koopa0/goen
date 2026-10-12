export async function checkWishlistFocus({ send, evaluate, navigate, fail, origin }) {
  const { cookies: priorCookies } = await send('Network.getCookies', { urls: [origin] });
  const selector = '.goen-pdp__links .goen-btn, .goen-pdp__similar .goen-btn';
  try {
    await send('Network.clearBrowserCookies');
    for (const lang of ['zh-Hant', 'en']) for (const width of [320, 375, 1440])
      for (const scale of [100, 200]) for (const scheme of ['light', 'dark']) {
        const label = `wishlist focus ${lang} ${width} text${scale} ${scheme}`;
        await send('Network.setCookie', { name: 'goen_locale', value: lang, url: origin, path: '/' });
        await send('Emulation.setDeviceMetricsOverride', { width, height: 812, deviceScaleFactor: 1, mobile: false });
        await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: scheme }] });
        // A new document gives native Tab a fresh starting point for every case.
        await send('Page.navigate', { url: 'about:blank' });
        let blankLoaded = false;
        for (let attempt = 0; attempt < 100; attempt++) {
          try {
            blankLoaded = await evaluate("location.href === 'about:blank' && document.readyState === 'complete'");
          } catch {
            // Navigation can replace the execution context between polls.
          }
          if (blankLoaded) break;
          await new Promise(resolve => setTimeout(resolve, 100));
        }
        if (!blankLoaded) throw new Error(`${label}: blank page did not load`);
        await navigate(origin + '/p/aurora-charger-65');
        await evaluate(`document.documentElement.style.fontSize = '${scale}%'`);
        await evaluate('document.fonts.ready.then(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))))', true);
        const initial = await evaluate(`({ count: document.querySelectorAll(${JSON.stringify(selector)}).length,
          lang: document.documentElement.lang, root: getComputedStyle(document.documentElement).fontSize })`);
        if (initial.count !== 2 || initial.lang !== lang || initial.root !== `${16 * scale / 100}px`) {
          fail(label, `wrong product controls, locale or text size: ${JSON.stringify(initial)}`);
          continue;
        }
        const reached = new Set();
        for (let tab = 0; tab < 200 && reached.size < 2; tab++) {
          await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 });
          await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 });
          const got = await evaluate(`(() => {
            const control = document.activeElement;
            if (!control.matches(${JSON.stringify(selector)})) return null;
            const box = control.getBoundingClientRect(), style = getComputedStyle(control);
            const row = control.closest('.goen-pdp__links, .goen-pdp__similar');
            const offset = -parseFloat(getComputedStyle(row).marginLeft);
            const padding = parseFloat(style.paddingLeft);
            const ring = Math.max(0, parseFloat(style.outlineWidth) + parseFloat(style.outlineOffset));
            const viewport = document.documentElement.clientWidth, faults = [];
            if (!control.matches(':focus-visible') || style.outlineStyle === 'none' || !ring)
              faults.push('native Tab focus has no visible outline');
            if (box.left - ring < -0.5 || box.right + ring > viewport + 0.5)
              faults.push('focused control or outline exceeds the viewport');
            if (document.documentElement.scrollWidth > viewport || Math.abs(scrollX) > 0.5)
              faults.push('focusing the control causes horizontal scroll');
            if (offset !== 12 || padding !== offset || parseFloat(style.paddingRight) !== offset)
              faults.push('label alignment does not retain equal fixed-pixel offset and padding');
            const column = control.closest('.goen-pdp__buy, .goen-pdp__more');
            if (!column || offset + ring > column.getBoundingClientRect().left + 0.5)
              faults.push('negative margin exceeds the column gutter minus its focus outline');
            return { control: row.className, left: box.left, right: box.right, ring, viewport, offset, padding, faults };
          })()`);
          if (!got) continue;
          reached.add(got.control);
          if (got.faults.length) fail(label, `${got.control}: ${got.faults.join('; ')}`);
          console.log(`${label}: ${JSON.stringify(got)}`);
        }
        if (reached.size !== 2) fail(label, 'native Tab did not reach both wishlist and compare controls');
      }
  } finally {
    await send('Emulation.setEmulatedMedia', { features: [] });
    await send('Network.clearBrowserCookies');
    await send('Network.setCookies', { cookies: priorCookies.map(c => ({
      name: c.name, value: c.value, domain: c.domain, path: c.path, secure: c.secure, httpOnly: c.httpOnly,
      ...(c.sameSite ? { sameSite: c.sameSite } : {}), ...(c.expires > 0 ? { expires: c.expires } : {}),
    })) });
    const { cookies: restored } = await send('Network.getCookies', { urls: [origin] });
    if (restored.length !== priorCookies.length || priorCookies.some(c => !restored.some(r =>
      r.name === c.name && r.domain === c.domain && r.path === c.path && r.value === c.value && r.secure === c.secure && r.httpOnly === c.httpOnly)))
      fail('browser state', 'the original cookies were not restored for subsequent audits');
  }
}
