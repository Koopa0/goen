import assert from 'node:assert/strict';

// A valid query can have no word boundaries. Measure its actual text runs,
// including text clipped by an ancestor, rather than accepting a hidden scroll.
export async function checkSearchReflow({ origin, send, evaluate, navigate }) {
  const query = 'x'.repeat(100);
  const { cookies } = await send('Network.getCookies', { urls: [origin] });
  const saved = cookies.filter(({ name }) => name === 'goen_locale' || name === 'goen_session');
  const failures = [];
  try {
    await send('Network.setCacheDisabled', { cacheDisabled: true });
    for (const cookie of saved) await send('Network.deleteCookies', { name: cookie.name, domain: cookie.domain, path: cookie.path });
    await send('Emulation.setDeviceMetricsOverride', { width: 320, height: 800, deviceScaleFactor: 1, mobile: true });
    for (const disabled of [false, true]) {
      await send('Emulation.setScriptExecutionDisabled', { value: disabled });
      for (const locale of ['zh-Hant', 'en']) {
        await send('Network.setCookie', { name: 'goen_locale', value: locale, url: origin + '/' });
        await navigate('/search?q=' + query);
        const deadline = Date.now() + 5000;
        while (!await evaluate("document.fonts.status === 'loaded'")) {
          assert.ok(Date.now() < deadline, 'search fonts did not load');
          await new Promise((resolve) => setTimeout(resolve, 50));
        }
        for (const scale of [100, 200]) {
          await evaluate(`document.documentElement.style.fontSize = '${scale}%'`);
          const got = await evaluate(`(() => {
            const title = document.querySelector('h1.goen-pagehead__title');
            const empty = document.querySelector('p.goen-empty__title');
            const rendered = [title, empty].every((element) => element
              && element.getBoundingClientRect().width > 0 && element.getBoundingClientRect().height > 0
              && getComputedStyle(element).visibility === 'visible');
            const outside = [];
            for (const element of [title, empty]) {
              if (!element) continue;
              const range = document.createRange();
              range.selectNodeContents(element);
              for (const rect of range.getClientRects()) {
                if (rect.left < -1 || rect.right > document.documentElement.clientWidth + 1) {
                  outside.push({ element: element.tagName, left: rect.left, right: rect.right });
                }
              }
            }
            return { lang: document.documentElement.lang, title: title?.textContent,
              empty: empty?.textContent, rendered, width: document.documentElement.clientWidth, scroll: document.body.scrollWidth, outside };
          })()`);
          const label = `${locale} ${scale}% scripting=${!disabled}`;
          assert.equal(got.lang, locale, label + ': wrong locale');
          assert.ok(got.title?.includes(query), label + ': full query missing from title');
          assert.ok(got.empty?.includes(query), label + ': no-results feedback missing');
          assert.ok(got.rendered, label + ': search text is hidden or missing');
          assert.equal(got.width, 320, label + ': wrong viewport');
          if (got.scroll > 321 || got.outside.length) {
            failures.push(label + ': ' + JSON.stringify({ scroll: got.scroll, outside: got.outside }));
          }
        }
      }
    }
    assert.deepEqual(failures, [], 'search text must remain readable inside 320px');
  } finally {
    await send('Network.setCacheDisabled', { cacheDisabled: false });
    await send('Emulation.setScriptExecutionDisabled', { value: false });
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
