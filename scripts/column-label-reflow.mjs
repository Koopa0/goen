export async function checkColumnLabelReflow({ send, evaluate, navigate, fail, origin, adminToken }) {
  if (!adminToken) { fail('column label reflow', 'staff fixture is missing'); return; }
  const { cookies: priorCookies } = await send('Network.getCookies', { urls: [origin] });
  try {
    for (const lang of ['zh-Hant', 'en']) for (const width of [320, 375, 1440]) for (const scale of [100, 200]) {
      for (const path of ['/admin/reports?days=7', '/admin/reports?days=30', '/admin/reports?days=90', '/admin/campaigns/layout-campaign']) {
        const label = `column labels ${lang} ${width} text${scale} ${path}`;
        await send('Network.clearBrowserCookies');
        for (const [name, value] of [['goen_session', adminToken], ['goen_locale', lang]])
          await send('Network.setCookie', { name, value, url: origin, path: '/' });
        await send('Emulation.setDeviceMetricsOverride', { width, height: 812, deviceScaleFactor: 1, mobile: width < 768 });
        await navigate(origin + path);
        await evaluate(`document.documentElement.style.fontSize = '${scale}%'`);
        await evaluate('document.fonts.ready.then(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))))', true);
        const got = await evaluate(`(() => {
          const faults = [], charts = [];
          for (const frame of document.querySelectorAll('.goen-chart__frame--columns')) {
            const plot = frame.querySelector('.goen-chart__plot'), box = plot.getBoundingClientRect();
            const ticks = [...plot.querySelectorAll(':scope > text.goen-chart__label')].filter(t => getComputedStyle(t).display !== 'none').map(t => {
              const b = t.getBoundingClientRect(); return { text: t.textContent, left: b.left, right: b.right, top: b.top, bottom: b.bottom };
            }).sort((a,b) => a.left - b.left);
            for (let i = 1; i < ticks.length; i++) if (ticks[i].left < ticks[i-1].right + 3)
              faults.push(ticks[i-1].text + ' overlaps ' + ticks[i].text);
            for (const t of ticks) if (t.left < -1 || t.right > document.documentElement.clientWidth + 1) faults.push(t.text + ' exceeds the viewport');
            const fig = frame.closest('figure'), table = fig.querySelector('.goen-chart__table');
            if (!fig.querySelector('figcaption')?.textContent.trim() || !table?.tHead || !table.tBodies[0]?.rows.length)
              faults.push('caption or text alternative is missing');
            if (table.tBodies[0].rows.length !== plot.querySelectorAll('.goen-chart__hit').length) faults.push('table loses chart rows');
            if ([...frame.querySelectorAll(':scope > svg')].some(s => s.getAttribute('aria-hidden') !== 'true')) faults.push('decorative SVG is exposed');
            if (parseFloat(getComputedStyle(table).fontSize) < parseFloat(getComputedStyle(document.documentElement).fontSize) * .8)
              faults.push('table text did not enlarge');
            charts.push({ plotWidth: box.width, ticks, rows: table.tBodies[0].rows.length, tableFont: getComputedStyle(table).fontSize });
          }
          if (!charts.length) faults.push('no column chart was rendered');
          return { path: location.pathname + location.search, lang: document.documentElement.lang,
            viewport: document.documentElement.clientWidth, root: getComputedStyle(document.documentElement).fontSize, charts, faults };
        })()`);
        if (got.path !== path || got.lang !== lang || got.faults.length) fail(label, JSON.stringify(got));
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
