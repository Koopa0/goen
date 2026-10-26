import assert from 'node:assert/strict';

// At 1024px the footer has five columns. At 200% text the contact address, one
// unbreakable word, used to run out of its column into the department links
// beside it. The shared reflow probe misses that: nothing is clipped and the
// page does not scroll, so the text rectangles themselves are compared.
export async function checkFooterReflow({ origin, send, evaluate, navigate }) {
  const { cookies } = await send('Network.getCookies', { urls: [origin] });
  const saved = cookies.filter(({ name }) => name === 'goen_locale' || name === 'goen_session');
  const failures = [];
  try {
    await send('Network.setCacheDisabled', { cacheDisabled: true });
    for (const cookie of saved) await send('Network.deleteCookies', { name: cookie.name, domain: cookie.domain, path: cookie.path });
    await send('Emulation.setDeviceMetricsOverride', { width: 1024, height: 800, deviceScaleFactor: 1, mobile: false });
    for (const locale of ['zh-Hant', 'en']) {
      await send('Network.setCookie', { name: 'goen_locale', value: locale, url: origin + '/' });
      await navigate('/');
      const deadline = Date.now() + 5000;
      while (!await evaluate("document.fonts.status === 'loaded'")) {
        assert.ok(Date.now() < deadline, 'footer fonts did not load');
        await new Promise((resolve) => setTimeout(resolve, 50));
      }
      await evaluate("document.documentElement.style.fontSize = '200%'");
      const got = await evaluate(`(() => {
        const rects = (element) => {
          const range = document.createRange();
          range.selectNodeContents(element);
          return [...range.getClientRects()].filter((r) => r.width > 0 && r.height > 0);
        };
        const email = document.querySelector('.goen-footer__contact a');
        const brand = document.querySelector('.goen-footer__brand');
        const links = [...document.querySelectorAll('.goen-footer__links a')];
        const mine = email ? rects(email) : [];
        const hits = [];
        for (const link of links) {
          for (const b of rects(link)) {
            for (const a of mine) {
              const w = Math.min(a.right, b.right) - Math.max(a.left, b.left);
              const h = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
              if (w > 0 && h > 0) hits.push({ link: link.textContent.trim(), width: w, height: h });
            }
          }
        }
        return { lang: document.documentElement.lang, width: window.innerWidth,
          email: email?.textContent, links: links.length,
          emailRight: Math.max(0, ...mine.map((r) => r.right)),
          brandRight: brand ? brand.getBoundingClientRect().right : 0, hits };
      })()`);
      assert.equal(got.lang, locale, locale + ': wrong locale');
      assert.equal(got.width, 1024, locale + ': wrong viewport');
      assert.ok(got.email && got.links > 0, locale + ': footer contact or department links missing');
      if (got.hits.length) failures.push(locale + ': the address overlaps ' + JSON.stringify(got.hits));
      if (got.emailRight > got.brandRight + 1) {
        failures.push(locale + ': the address ends at ' + got.emailRight + ', past its column at ' + got.brandRight);
      }
    }
    assert.deepEqual(failures, [], 'the footer address must stay inside its column at 200% text');
  } finally {
    await send('Network.setCacheDisabled', { cacheDisabled: false });
    const current = await send('Network.getCookies', { urls: [origin] });
    for (const cookie of current.cookies.filter(({ name }) => name === 'goen_locale' || name === 'goen_session')) {
      await send('Network.deleteCookies', { name: cookie.name, domain: cookie.domain, path: cookie.path });
    }
    for (const cookie of saved) await send('Network.setCookie', {
      name: cookie.name, value: cookie.value, domain: cookie.domain, path: cookie.path,
      secure: cookie.secure, httpOnly: cookie.httpOnly, sameSite: cookie.sameSite,
    });
  }
}
