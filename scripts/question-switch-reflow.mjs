export async function checkQuestionSwitchReflow({ send, evaluate, navigate, fail, origin, adminToken }) {
  if (!adminToken) { fail('question switch reflow', 'staff fixture is missing'); return; }
  const { cookies: priorCookies } = await send('Network.getCookies', { urls: [origin] });
  try {
    for (const lang of ['zh-Hant', 'en']) for (const width of [320, 375, 1440]) for (const scale of [100, 200]) {
      for (const hidden of [false, true]) {
        const path = '/admin/questions' + (hidden ? '?hidden=1' : '');
        const label = `question switch ${lang} ${width} text${scale} hidden=${hidden}`;
        await send('Network.clearBrowserCookies');
        for (const [name, value] of [['goen_session', adminToken], ['goen_locale', lang]])
          await send('Network.setCookie', { name, value, url: origin, path: '/' });
        await send('Emulation.setDeviceMetricsOverride', { width, height: 812, deviceScaleFactor: 1, mobile: width < 768 });
        await navigate(origin + path);
        await evaluate(`document.documentElement.style.fontSize = '${scale}%'`);
        await evaluate('document.fonts.ready.then(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))))', true);
        const got = await evaluate(`(() => {
          const link = document.querySelector('.goen-admin .ui-page-head > a');
          const faults = [];
          if (!link) return { faults: ['queue switch is missing'] };
          const box = link.getBoundingClientRect(), head = link.parentElement.getBoundingClientRect();
          const range = document.createRange(); range.selectNodeContents(link);
          const rects = [...range.getClientRects()].filter(r => r.width);
          const viewport = document.documentElement.clientWidth;
          if (box.left < head.left - 1 || box.right > head.right + 1 || box.left < -1 || box.right > viewport + 1)
            faults.push('queue switch exceeds its heading or viewport');
          if (!rects.length || rects.some(r => r.left < box.left - 1 || r.right > box.right + 1 || r.top < box.top - 1 || r.bottom > box.bottom + 1))
            faults.push('queue-switch label is clipped');
          if (box.width < 44 || box.height < 44) faults.push('touch target is below 44px');
          return { path: location.pathname + location.search, lang: document.documentElement.lang,
            root: getComputedStyle(document.documentElement).fontSize, viewport, bodyWidth: document.body.scrollWidth,
            href: link.getAttribute('href'), text: link.textContent.trim(), width: box.width, height: box.height, right: box.right, faults };
        })()`);
        const other = '/admin/questions' + (hidden ? '' : '?hidden=1');
        if (got.path !== path || got.lang !== lang || got.href !== other) fail(label, `wrong queue/locale/destination: ${JSON.stringify(got)}`);
        else if (got.faults.length) fail(label, got.faults.join('; '));
        console.log(`${label}: ${JSON.stringify(got)}`);
      }
    }
  } finally {
    await send('Network.clearBrowserCookies');
    await send('Network.setCookies', { cookies: priorCookies.map(c => ({
      name: c.name, value: c.value, domain: c.domain, path: c.path, secure: c.secure, httpOnly: c.httpOnly,
      ...(c.sameSite ? { sameSite: c.sameSite } : {}), ...(c.expires > 0 ? { expires: c.expires } : {}),
    })) });
    const { cookies: restored } = await send('Network.getCookies', { urls: [origin] });
    if (restored.length !== priorCookies.length || priorCookies.some(c => !restored.some(r =>
      r.name === c.name && r.domain === c.domain && r.path === c.path && r.value === c.value && r.secure === c.secure && r.httpOnly === c.httpOnly))) {
      fail('browser state', 'the original cookies were not restored for subsequent audits');
    }
  }
}
