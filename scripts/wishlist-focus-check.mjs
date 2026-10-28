export async function checkWishlistFocus({ send, evaluate, navigate, fail, origin }) {
  const { cookies: priorCookies } = await send('Network.getCookies', { urls: [origin] });
  // The wishlist control is a button in the buy column; the comparison entry is a text link in the details.
  const selector = '.goen-pdp__links .goen-btn, a.goen-pdp__compare';
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
        const initial = await evaluate(`({
          wishlist: document.querySelectorAll('.goen-pdp__links .goen-btn').length,
          compare: document.querySelectorAll('a.goen-pdp__compare').length,
          lang: document.documentElement.lang, root: getComputedStyle(document.documentElement).fontSize })`);
        if (initial.wishlist !== 1 || initial.compare !== 1 || initial.lang !== lang || initial.root !== `${16 * scale / 100}px`) {
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
            const kind = control.matches('.goen-pdp__compare') ? 'compare' : 'wishlist';
            const column = control.closest('.goen-pdp__buy, .goen-pdp__more');
            if (!column) return { control: kind, faults: ['control is outside the buy and details columns'] };
            const columnBox = column.getBoundingClientRect(), columnStyle = getComputedStyle(column);
            // Where the label starts, and where the column's own text starts.
            const labelLeft = box.left + parseFloat(style.borderLeftWidth) + parseFloat(style.paddingLeft);
            const columnLeft = columnBox.left + parseFloat(columnStyle.borderLeftWidth) + parseFloat(columnStyle.paddingLeft);
            const outline = parseFloat(style.outlineWidth), ring = Math.max(0, outline + parseFloat(style.outlineOffset));
            const viewport = document.documentElement.clientWidth, faults = [];
            if (box.height < 44) faults.push('control height is below 44px');
            if (!control.matches(':focus-visible') || style.outlineStyle === 'none' || outline < 2)
              faults.push('native Tab focus has no outline of at least 2px');
            if (box.left - ring < -0.5 || box.right + ring > viewport + 0.5)
              faults.push('focused control or outline exceeds the viewport');
            if (document.documentElement.scrollWidth > viewport || Math.abs(scrollX) > 0.5)
              faults.push('focusing the control causes horizontal scroll');
            for (let node = control.parentElement; node && node !== document.body; node = node.parentElement) {
              const clip = getComputedStyle(node);
              if (clip.overflowX === 'visible' && clip.overflowY === 'visible') continue;
              const edge = node.getBoundingClientRect();
              if (box.left - ring < edge.left - 0.5 || box.right + ring > edge.right + 0.5
                || box.top - ring < edge.top - 0.5 || box.bottom + ring > edge.bottom + 0.5) {
                faults.push('focus outline is clipped by ' + (node.className || node.tagName));
                break;
              }
            }
            if (Math.abs(labelLeft - columnLeft) > 0.5)
              faults.push('label is not aligned with the column text: ' + (labelLeft - columnLeft).toFixed(1) + 'px off');
            if (kind === 'wishlist' && Math.abs(parseFloat(style.paddingRight) - parseFloat(style.paddingLeft)) > 0.5)
              faults.push('wishlist button side padding is not equal');
            const overhang = Math.max(0, columnBox.left - box.left);
            if (overhang + ring > columnBox.left + 0.5)
              faults.push('overhang exceeds the column gutter minus its focus outline');
            return { control: kind, left: box.left, right: box.right, height: box.height, outline, ring, viewport, overhang, labelLeft, columnLeft, faults };
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
