// Measure the real delivery and inspection forms, including their full labels.
export async function checkAdminActionReflow({ send, evaluate, navigate, fail, origin, adminToken, placedOrder }) {
  if (!adminToken || !placedOrder) {
    fail('admin action reflow', 'staff/order fixtures are missing; no action was measured');
    return;
  }
  const { cookies: priorCookies } = await send('Network.getCookies', { urls: [origin] });
  const pages = [
    [`/admin/orders/${placedOrder}`, 'form[action$="/delivery"] button[type="submit"]'],
    ['/admin/returns', 'form[action$="/inspect"] button[type="submit"]'],
  ];
  try {
    for (const lang of ['zh', 'en']) {
      for (const width of [320, 375]) {
        for (const scale of [100, 200]) {
          for (const [path, selector] of pages) {
            const label = `admin action ${path} ${lang} ${width} text${scale}`;
            await send('Network.clearBrowserCookies');
            for (const [name, value] of [['goen_session', adminToken], ['goen_locale', lang === 'zh' ? 'zh-Hant' : lang]]) {
              await send('Network.setCookie', { name, value, url: origin, path: '/' });
            }
            await send('Emulation.setDeviceMetricsOverride', { width, height: 812, deviceScaleFactor: 1, mobile: true });
            await navigate(origin + path);
            await evaluate(`document.documentElement.style.fontSize = '${scale}%'`);
            await evaluate('document.fonts.ready.then(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))))', true);
            const got = await evaluate(`(() => {
              const buttons = [...document.querySelectorAll(${JSON.stringify(selector)})];
              const viewport = document.documentElement.clientWidth;
              const faults = [];
              const measured = buttons.map(button => {
                const box = button.getBoundingClientRect();
                const form = button.closest('form').getBoundingClientRect();
                const range = document.createRange();
                range.selectNodeContents(button);
                const text = [...range.getClientRects()].filter(r => r.width > 0);
                if (box.left < form.left - 1 || box.right > form.right + 1 || box.right > viewport + 1 || box.left < -1)
                  faults.push('button exceeds its form or viewport');
                if (box.height < 44 || box.width < 44) faults.push('touch target is below 44px');
                if (!text.length || text.some(r => r.left < box.left - 1 || r.right > box.right + 1 || r.top < box.top - 1 || r.bottom > box.bottom + 1))
                  faults.push('label exceeds the button');
                if (button.form.method !== 'post') faults.push('action is not a plain POST form');
                return { text: button.textContent.trim(), width: box.width, height: box.height, right: box.right, formWidth: form.width };
              });
              return { path: location.pathname, lang: document.documentElement.lang, root: getComputedStyle(document.documentElement).fontSize,
                count: buttons.length, viewport, bodyWidth: document.body.scrollWidth, measured, faults };
            })()`);
            if (got.path !== path || !got.count || got.lang !== (lang === 'zh' ? 'zh-Hant' : 'en')) {
              fail(label, `requested action/locale was not rendered: ${JSON.stringify(got)}`);
            } else if (got.faults.length) {
              fail(label, `${got.faults.join('; ')}: ${JSON.stringify(got.measured)}`);
            }
            console.log(`${label}: ${JSON.stringify(got)}`);
          }
        }
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
      fail('admin action browser state', 'the original cookies were not restored for subsequent audits');
    }
  }
}
