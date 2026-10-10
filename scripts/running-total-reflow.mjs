export async function checkRunningTotalReflow({ send, evaluate, navigate, fail, origin, adminToken }) {
  if (!adminToken) { fail('running total reflow', 'staff fixture is missing'); return; }
  const { cookies: priorCookies } = await send('Network.getCookies', { urls: [origin] });
  try {
    for (const lang of ['zh-Hant', 'en']) for (const width of [320, 375, 1440]) for (const scale of [100, 200]) {
      for (const path of ['/admin/reports?days=7', '/admin/reports', '/admin/reports?days=90']) {
        const label = `running total ${lang} ${width} text${scale} ${path}`;
        await send('Network.clearBrowserCookies');
        for (const [name, value] of [['goen_session', adminToken], ['goen_locale', lang]])
          await send('Network.setCookie', { name, value, url: origin, path: '/' });
        await send('Emulation.setDeviceMetricsOverride', { width, height: 812, deviceScaleFactor: 1, mobile: width < 768 });
        await navigate(origin + path);
        await evaluate(`document.documentElement.style.fontSize = '${scale}%'`);
        await evaluate('document.fonts.ready.then(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))))', true);
        const got = await evaluate(`(() => {
          const faults = [];
          const fig = [...document.querySelectorAll('.goen-chart')].find(f => f.querySelector('.goen-chart__ends'));
          if (!fig) return { faults: ['running-total figure is missing'] };
          const frame = fig.querySelector('.goen-chart__frame'), plot = frame.querySelector('.goen-chart__plot');
          const box = plot.getBoundingClientRect(), viewport = document.documentElement.clientWidth;
          if (box.width < 80 || box.height < 100) faults.push('plot has no usable area');
          if (box.left < -1 || box.right > viewport + 1) faults.push('plot exceeds the viewport');
          const visible = [...fig.querySelectorAll('.goen-chart__ends text, .goen-chart__endlegend dt, .goen-chart__endlegend dd')]
            .filter(t => t.getBoundingClientRect().width > 0);
          if (visible.length !== 4) faults.push('both end names and values are not visible');
          const labels = visible.map(t => {
            const b = t.getBoundingClientRect();
            if (b.left < -1 || b.right > viewport + 1) faults.push(t.textContent.trim() + ' exceeds the viewport');
            return { text: t.textContent.trim(), left: b.left, right: b.right, top: b.top, bottom: b.bottom };
          });
          for (let i = 0; i < labels.length; i++) for (let j = i + 1; j < labels.length; j++) {
            const a = labels[i], b = labels[j];
            if (a.left < b.right - 1 && b.left < a.right - 1 && a.top < b.bottom - 1 && b.top < a.bottom - 1)
              faults.push(a.text + ' overlaps ' + b.text);
          }
          const ticks = [...plot.querySelectorAll(':scope > text')].filter(t => getComputedStyle(t).display !== 'none').map(t => {
            const b = t.getBoundingClientRect(); return { text: t.textContent, left: b.left, right: b.right };
          }).sort((a,b) => a.left - b.left);
          for (let i = 1; i < ticks.length; i++) if (ticks[i].left < ticks[i-1].right + 3)
            faults.push(ticks[i-1].text + ' overlaps ' + ticks[i].text);
          const table = fig.querySelector('.goen-chart__table');
          if (table && parseFloat(getComputedStyle(table).fontSize) < parseFloat(getComputedStyle(document.documentElement).fontSize) * .8)
            faults.push('table text did not enlarge');
          if (!fig.querySelector('figcaption')?.textContent.trim() || !table?.tHead || table.tBodies[0].rows.length !== plot.querySelectorAll('.goen-chart__hit').length)
            faults.push('caption or complete text alternative is missing');
          if (plot.getAttribute('aria-hidden') !== 'true') faults.push('decorative SVG is exposed');
          return { path: location.pathname + location.search, lang: document.documentElement.lang, viewport, root: getComputedStyle(document.documentElement).fontSize,
            columns: getComputedStyle(frame).gridTemplateColumns, plotWidth: box.width, labels, ticks, tableFont: table ? getComputedStyle(table).fontSize : null, tableRows: table?.tBodies[0].rows.length, faults };
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
