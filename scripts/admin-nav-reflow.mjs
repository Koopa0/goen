// The back-office rail's "Background work" item carries a second line, the
// count of tasks that need attention. In English it is wider than the rail, so
// the item has to wrap with the count under its name; the other items stay on
// one line. The shared reflow probe misses this: nothing is clipped and the
// page does not scroll, so the text rectangles are compared with the item's box.
export async function checkAdminNavReflow({ send, evaluate, navigate, fail, origin, adminToken }) {
  if (!adminToken) {
    fail('admin nav reflow', 'the staff fixture is missing; no rail was measured');
    return;
  }
  const { cookies: priorCookies } = await send('Network.getCookies', { urls: [origin] });
  try {
    for (const lang of ['zh', 'en']) {
      for (const width of [1024, 1440]) {
        for (const scale of [100, 200]) {
          const label = `admin nav ${lang} ${width} text${scale}`;
          await send('Network.clearBrowserCookies');
          for (const [name, value] of [['goen_session', adminToken], ['goen_locale', lang === 'zh' ? 'zh-Hant' : lang]]) {
            await send('Network.setCookie', { name, value, url: origin, path: '/' });
          }
          await send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false });
          await navigate(origin + '/admin/health');
          await evaluate(`document.documentElement.style.fontSize = '${scale}%'`);
          await evaluate('document.fonts.ready.then(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))))', true);
          const got = await evaluate(`(() => {
            const rects = (element) => {
              const range = document.createRange();
              range.selectNodeContents(element);
              return [...range.getClientRects()].filter((r) => r.width > 0 && r.height > 0);
            };
            const faults = [];
            const items = [...document.querySelectorAll('.goen-admin__nav .ui-navitem')];
            const withMeta = items.filter((item) => item.querySelector('.goen-admin__meta'));
            for (const item of items) {
              const box = item.getBoundingClientRect();
              const name = item.textContent.trim().replace(/\\s+/g, ' ');
              if (rects(item).some((r) => r.left < box.left - 1 || r.right > box.right + 1))
                faults.push(name + ': text runs past its item');
              const meta = item.querySelector('.goen-admin__meta');
              if (meta) {
                const m = meta.getBoundingClientRect();
                const range = document.createRange();
                range.setStart(item, 0);
                range.setEndBefore(meta);
                const head = range.getBoundingClientRect();
                if (!head.height || m.top < head.bottom - 1) faults.push(name + ': the count is not on its own line');
              } else if (item.matches('a.ui-navitem') && rects(item).length > 1) {
                faults.push(name + ': an item without a count wrapped');
              }
            }
            return { lang: document.documentElement.lang, root: getComputedStyle(document.documentElement).fontSize,
              viewport: window.innerWidth, items: items.length, withMeta: withMeta.length, faults };
          })()`);
          if (got.viewport !== width || !got.items || got.withMeta !== 1 || got.lang !== (lang === 'zh' ? 'zh-Hant' : 'en')) {
            fail(label, `the rail was not rendered as requested: ${JSON.stringify(got)}`);
          } else if (got.faults.length) {
            fail(label, got.faults.join('; '));
          }
          console.log(`${label}: ${JSON.stringify(got)}`);
        }
      }
    }
  } finally {
    await send('Network.clearBrowserCookies');
    await send('Network.setCookies', { cookies: priorCookies.map(c => ({
      name: c.name, value: c.value, domain: c.domain, path: c.path, secure: c.secure, httpOnly: c.httpOnly,
      ...(c.sameSite ? { sameSite: c.sameSite } : {}), ...(c.expires > 0 ? { expires: c.expires } : {}),
    })) });
  }
}
