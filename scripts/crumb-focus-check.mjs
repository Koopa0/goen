// At phone width the breadcrumb clips every link but the parent to a 1px box
// (app.css, `.ui-crumbs > *` under max-width: 600px). A clipped link is still a
// tab stop, so keyboard focus would land on nothing the reader can see (WCAG
// 2.4.7). Native Tab must reveal each link: an unclipped box, a drawn outline,
// the link on top at its own centre, and all of it inside the viewport.
export async function checkCrumbFocus({ send, evaluate, navigate, fail, origin }) {
  const { cookies: priorCookies } = await send('Network.getCookies', { urls: [origin] });
  try {
    await send('Network.clearBrowserCookies');
    for (const lang of ['zh-Hant', 'en']) for (const width of [320, 375, 1440]) for (const scale of [100, 200]) {
      const label = `crumb focus ${lang} ${width} text${scale}`;
      await send('Network.setCookie', { name: 'goen_locale', value: lang, url: origin, path: '/' });
      await send('Emulation.setDeviceMetricsOverride', { width, height: 812, deviceScaleFactor: 1, mobile: false });
      // A new document gives native Tab a fresh starting point for every case.
      await send('Page.navigate', { url: 'about:blank' });
      let blankLoaded = false;
      for (let attempt = 0; attempt < 100 && !blankLoaded; attempt++) {
        try {
          blankLoaded = await evaluate("location.href === 'about:blank' && document.readyState === 'complete'");
        } catch {
          // Navigation can replace the execution context between polls.
        }
        if (!blankLoaded) await new Promise(resolve => setTimeout(resolve, 100));
      }
      if (!blankLoaded) throw new Error(`${label}: blank page did not load`);
      await navigate(origin + '/p/aurora-charger-65');
      await evaluate(`document.documentElement.style.fontSize = '${scale}%'`);
      await evaluate('document.fonts.ready.then(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))))', true);
      const initial = await evaluate(`({ links: document.querySelectorAll('.ui-crumbs__link').length,
        lang: document.documentElement.lang, root: getComputedStyle(document.documentElement).fontSize })`);
      if (initial.links < 2 || initial.lang !== lang || initial.root !== `${16 * scale / 100}px`) {
        fail(label, `wrong breadcrumb, locale or text size: ${JSON.stringify(initial)}`);
        continue;
      }
      const reached = new Set();
      for (let tab = 0; tab < 200 && reached.size < initial.links; tab++) {
        await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 });
        await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 });
        const got = await evaluate(`(() => {
          const link = document.activeElement;
          if (!link.matches('.ui-crumbs__link')) return null;
          const box = link.getBoundingClientRect(), style = getComputedStyle(link);
          const hit = document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2);
          const viewport = document.documentElement.clientWidth, faults = [];
          if (box.width <= 1 || box.height <= 1 || style.clipPath !== 'none') faults.push('focused link is still clipped');
          if (!link.matches(':focus-visible') || style.outlineStyle === 'none' || !parseFloat(style.outlineWidth))
            faults.push('native Tab focus has no visible outline');
          if (hit !== link && !link.contains(hit)) faults.push('focused link is covered at its centre');
          if (box.left < -0.5 || box.right > viewport + 0.5) faults.push('focused link exceeds the viewport');
          if (document.documentElement.scrollWidth > viewport) faults.push('focusing the link causes horizontal scroll');
          return { href: link.getAttribute('href'), width: box.width, height: box.height, faults };
        })()`);
        if (!got) continue;
        reached.add(got.href);
        if (got.faults.length) fail(label, `${got.href}: ${got.faults.join('; ')}`);
        console.log(`${label}: ${JSON.stringify(got)}`);
      }
      if (reached.size !== initial.links) fail(label, `native Tab reached ${reached.size} of ${initial.links} breadcrumb links`);
    }
  } finally {
    await send('Network.clearBrowserCookies');
    await send('Network.setCookies', { cookies: priorCookies.map(c => ({
      name: c.name, value: c.value, domain: c.domain, path: c.path, secure: c.secure, httpOnly: c.httpOnly,
      ...(c.sameSite ? { sameSite: c.sameSite } : {}), ...(c.expires > 0 ? { expires: c.expires } : {}),
    })) });
  }
}
