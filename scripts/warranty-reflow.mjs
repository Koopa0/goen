import assert from 'node:assert/strict';

// The customer token is essential: a staff member's empty list does not prove
// the returned customer's long status can be read.
export async function checkWarrantyReflow({ origin, token, send, evaluate, navigate }) {
  assert.ok(token, 'CUST_TOKEN is required to measure customer warranty states');
  const { cookies } = await send('Network.getCookies', { urls: [origin] });
  const saved = cookies.filter(({ name }) => name === 'goen_locale' || name === 'goen_session');
  const failures = [];
  try {
    await send('Network.setCacheDisabled', { cacheDisabled: true });
    await send('Network.setCookie', { name: 'goen_session', value: token, url: origin + '/' });
    await send('Emulation.setDeviceMetricsOverride', { width: 320, height: 800, deviceScaleFactor: 1, mobile: true });
    for (const disabled of [false, true]) {
      await send('Emulation.setScriptExecutionDisabled', { value: disabled });
      for (const locale of ['zh-Hant', 'en']) {
        await send('Network.setCookie', { name: 'goen_locale', value: locale, url: origin + '/' });
        await navigate('/account/warranty');
        const deadline = Date.now() + 5000;
        while (!await evaluate("document.fonts.status === 'loaded'")) {
          assert.ok(Date.now() < deadline, 'warranty fonts did not load');
          await new Promise((resolve) => setTimeout(resolve, 50));
        }
        for (const scale of [100, 200]) {
          await evaluate(`document.documentElement.style.fontSize = '${scale}%'`);
          const got = await evaluate(`(() => {
            const badges = [...document.querySelectorAll('.goen-warranty__head .goen-badge')];
            const heading = document.querySelector('main h1');
            const metadata = [...document.querySelectorAll('.goen-warranty__meta')];
            const text = [heading, ...metadata, ...badges].filter(Boolean);
            const rendered = !!heading && metadata.length > 0 && text.every((element) =>
              element.getBoundingClientRect().width > 0 && element.getBoundingClientRect().height > 0
              && getComputedStyle(element).visibility === 'visible');
            const outside = [];
            for (const element of text) {
              const range = document.createRange();
              range.selectNodeContents(element);
              for (const r of range.getClientRects()) {
                if (r.left < -1 || r.right > document.documentElement.clientWidth + 1) {
                  outside.push({ left: r.left, right: r.right });
                }
              }
            }
            return { lang: document.documentElement.lang, width: document.documentElement.clientWidth,
              scroll: document.body.scrollWidth, heading: heading?.textContent.trim(), rendered,
              badges: badges.map(b => b.textContent.trim()), outside };
          })()`);
          const label = `${locale} ${scale}% scripting=${!disabled}`;
          assert.equal(got.lang, locale, label + ': wrong locale');
          assert.equal(got.width, 320, label + ': wrong viewport');
          assert.ok(got.badges.length >= 2, label + ': customer warranty fixtures missing');
          assert.ok(got.rendered, label + ': warranty text is hidden or missing');
          assert.equal(got.heading, locale === 'en' ? 'Warranty registration' : '保固登錄', label + ': warranty heading missing');
          // These are existing i18n messages, checked so an empty list or a
          // shorter, different state cannot satisfy the geometry assertion.
          const returned = locale === 'en'
            ? 'This was returned, so there is no cover to register.'
            : '這項商品已辦理退貨，沒有可登錄的保固。';
          assert.ok(got.badges.includes(returned), label + ': returned state missing');
          if (got.scroll > 321 || got.outside.length) failures.push(label + ': ' + JSON.stringify({ scroll: got.scroll, outside: got.outside }));
        }
      }
    }
    assert.deepEqual(failures, [], 'warranty status text must remain readable inside 320px');
  } finally {
    await send('Emulation.setScriptExecutionDisabled', { value: false });
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
