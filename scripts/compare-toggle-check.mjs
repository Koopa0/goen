// The compare toggle, pressed in a real browser against the running shop: the
// one way a product goes into the shopper's compare set, from a card of a
// shelf, a row of the spec list, the home page's featured shelf and the product
// page's buy column.
//
// Each press is the shopper's: focus on the button and Enter, or a click. What
// it must leave is what the shopper sees: the button pressed (aria-pressed),
// the header's count one higher, the cookie holding the product, focus still on
// the button after htmx morphed the answer over it, and no page load. A full set
// refuses the sixth with the sentence in the toggle's status, the count and the
// cookie as they were. In that state, the page is measured at 1440, 1024, 375
// and 320 pixels, at 100% and 200% text, in both languages: nothing widens the
// page, and no toggle or note leaves its card, row or column. axe-core audits
// the pressed and refused state, which the route-by-route pass never reaches
// because it visits each page with an empty set.

const NOTE = {
  'zh-Hant': '比較已滿 5 個，請先移除一個再加入。',
  en: 'The comparison holds 5 products. Remove one to add another.',
};
const CAP = 5;
const SHELF = '/c/tech';

export async function checkCompareToggle({ send, evaluate, navigate, fail, origin, axeSource, axeRun, gates }) {
  const { cookies: priorCookies } = await send('Network.getCookies', { urls: [origin] });

  const clearSet = async () => {
    for (const name of ['goen_compare', '__Host-goen_compare']) {
      await send('Network.deleteCookies', { name, url: origin });
    }
  };
  const kept = async () => {
    const { cookies } = await send('Network.getCookies', { urls: [origin] });
    const cookie = cookies.find(c => c.name === 'goen_compare' || c.name === '__Host-goen_compare');
    return cookie ? cookie.value.split('.').filter(Boolean) : [];
  };
  const speak = (lang) => send('Network.setCookie', { name: 'goen_locale', value: lang, url: origin, path: '/' });
  const view = async (width, scale) => {
    await send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false });
    await evaluate(`document.documentElement.style.fontSize = '${scale}%'`);
    await evaluate('document.fonts.ready.then(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))))', true);
  };
  const open = async (path, lang) => {
    await speak(lang);
    await view(1440, 100);
    await navigate(origin + path);
    const ready = await evaluate(`typeof htmx === 'object' && document.readyState === 'complete'`);
    if (!ready) throw new Error(`${path}: htmx is not loaded`);
    await evaluate('window.__compareProbe = true');
  };
  const count = () => evaluate(`Number(document.querySelector('#compare-link .goen-header__comparecount')?.textContent.trim() || 0)`);
  const toggle = (slug) => `document.querySelector(${JSON.stringify(`.goen-comparetoggle input[name="slug"][value="${slug}"]`)})?.form`;
  const state = (slug) => evaluate(`(() => {
    const form = ${toggle(slug)};
    if (!form) return null;
    const button = form.querySelector('.goen-comparetoggle__button');
    return {
      pressed: button.getAttribute('aria-pressed'),
      note: form.querySelector('.goen-comparetoggle__note[role="status"]')?.textContent.trim() ?? null,
      focused: document.activeElement === button,
      notes: [...document.querySelectorAll('.goen-comparetoggle__note')].filter(n => n.textContent.trim()).length,
      reloaded: window.__compareProbe !== true,
      path: location.pathname + location.search,
    };
  })()`);
  const slugsIn = (scope) => evaluate(`[...document.querySelectorAll(${JSON.stringify(scope + ' .goen-comparetoggle input[name="slug"]')})].map(i => i.value)`);

  // One press, the way the shopper makes it, and the wait for htmx's answer to
  // settle: the morph removes the mark put on the form, because the answer's
  // form does not carry it.
  const press = async (slug, how) => {
    const armed = await evaluate(`(() => {
      const form = ${toggle(slug)};
      if (!form) return false;
      form.dataset.probe = 'pending';
      const button = form.querySelector('.goen-comparetoggle__button');
      button.scrollIntoView({ block: 'center' });
      button.focus();
      if (${JSON.stringify(how)} === 'click') button.click();
      return document.activeElement === button;
    })()`);
    if (!armed) return false;
    if (how === 'key') {
      await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, text: '\r' });
      await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13 });
    }
    for (let i = 0; i < 100; i++) {
      const done = await evaluate(`(() => { const form = ${toggle(slug)}; return !!form && form.dataset.probe !== 'pending'; })()`);
      if (done) {
        await evaluate('new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)))', true);
        return true;
      }
      await new Promise(r => setTimeout(r, 100));
    }
    return false;
  };

  // A press that adds: the button pressed and focused, the count one higher,
  // the product in the cookie, and the page where it was.
  const expectAdded = async (label, slug, how, before) => {
    if (!await press(slug, how)) { fail(label, `pressing ${slug} by ${how} got no answer`); return; }
    const got = await state(slug);
    const set = await kept();
    const n = await count();
    const faults = [];
    if (got?.pressed !== 'true') faults.push(`the button reads aria-pressed=${got?.pressed}, not true`);
    if (!got?.focused) faults.push('focus left the button with the swap');
    if (got?.reloaded) faults.push('the page was loaded again');
    if (got?.note) faults.push(`the status says ${JSON.stringify(got.note)}`);
    if (n !== before + 1) faults.push(`the header counts ${n}, not ${before + 1}`);
    if (!set.includes(slug)) faults.push(`the cookie holds ${JSON.stringify(set)}, without ${slug}`);
    if (faults.length) fail(label, `adding ${slug} by ${how}: ${faults.join('; ')}`);
  };

  // A press that removes: unpressed, focused, the count one lower.
  const expectRemoved = async (label, slug, how, before) => {
    if (!await press(slug, how)) { fail(label, `pressing ${slug} by ${how} got no answer`); return; }
    const got = await state(slug);
    const set = await kept();
    const n = await count();
    const faults = [];
    if (got?.pressed !== 'false') faults.push(`the button reads aria-pressed=${got?.pressed}, not false`);
    if (!got?.focused) faults.push('focus left the button with the swap');
    if (n !== before - 1) faults.push(`the header counts ${n}, not ${before - 1}`);
    if (set.includes(slug)) faults.push(`the cookie still holds ${slug}`);
    if (faults.length) fail(label, `taking out ${slug} by ${how}: ${faults.join('; ')}`);
  };

  // A press the full set refuses: unpressed, focused, the sentence in its
  // status and in no other, the count and the cookie as they were.
  const expectRefused = async (label, slug, lang, set) => {
    if (!await press(slug, 'key')) { fail(label, `pressing ${slug} got no answer`); return; }
    const got = await state(slug);
    const after = await kept();
    const n = await count();
    const faults = [];
    if (got?.pressed !== 'false') faults.push(`the button reads aria-pressed=${got?.pressed}, not false`);
    if (!got?.focused) faults.push('focus left the button with the swap');
    if (got?.note !== NOTE[lang]) faults.push(`the status says ${JSON.stringify(got?.note)}, not ${JSON.stringify(NOTE[lang])}`);
    if (got?.notes !== 1) faults.push(`${got?.notes} toggles say something, not the refused one alone`);
    if (n !== CAP) faults.push(`the header counts ${n}, not ${CAP}`);
    if (after.join('.') !== set.join('.')) faults.push(`the cookie became ${JSON.stringify(after)}`);
    if (faults.length) fail(label, `a sixth product: ${faults.join('; ')}`);
  };

  // Nothing widens the page, and each toggle, its button and a status that
  // speaks stay inside the card, row or column that holds them, with the
  // button's words inside the button and a 44px target.
  const measure = (label) => evaluate(`(() => {
    const faults = [];
    const viewport = document.documentElement.clientWidth;
    if (document.documentElement.scrollWidth > viewport + 0.5) faults.push('the page is ' + document.documentElement.scrollWidth + 'px wide in a ' + viewport + 'px window');
    for (const form of document.querySelectorAll('.goen-comparetoggle')) {
      const holder = form.closest('.goen-tile__cell, .goen-specrow, .goen-pdp__buy');
      const slug = form.querySelector('input[name="slug"]').value;
      if (!holder) { faults.push(slug + ': the toggle is in no card, row or buy column'); continue; }
      const edge = holder.getBoundingClientRect();
      const button = form.querySelector('.goen-comparetoggle__button');
      const note = form.querySelector('.goen-comparetoggle__note');
      for (const [what, el] of [['button', button], ['note', note && note.textContent.trim() ? note : null]]) {
        if (!el) continue;
        const box = el.getBoundingClientRect();
        if (box.width === 0) { faults.push(slug + ': the ' + what + ' is not drawn'); continue; }
        if (box.left < edge.left - 0.5 || box.right > edge.right + 0.5)
          faults.push(slug + ': the ' + what + ' runs ' + Math.round(Math.max(edge.left - box.left, box.right - edge.right)) + 'px out of its ' + holder.className);
        if (el.scrollWidth > el.clientWidth + 0.5) faults.push(slug + ': the ' + what + "'s words overflow it");
      }
      if (button.getBoundingClientRect().height < 44 - 0.5) faults.push(slug + ': the button is ' + button.getBoundingClientRect().height + 'px tall');
    }
    return faults;
  })()`).then(faults => { if (faults.length) fail(label, faults.slice(0, 6).join('; ')); return faults.length; });
  const sweep = async (label, widths) => {
    for (const width of widths) for (const scale of [100, 200]) {
      await view(width, scale);
      await measure(`${label} ${width} text${scale}`);
    }
    await view(1440, 100);
  };

  // axe-core on the state the page is in now, any serious or critical WCAG
  // finding failing: there is no baseline for a state no route starts in.
  const audit = async (label) => {
    if (!axeSource || !axeRun) return;
    await evaluate(axeSource);
    const result = JSON.parse(await evaluate(axeRun, true));
    const gating = result.violations.filter(gates);
    for (const v of gating) fail(`${label} axe`, `${v.id} (${v.impact}) — ${v.help}; first: ${v.target}`);
    console.log(`${label}: axe violations=${result.violations.length} (${result.violations.map(v => `${v.id} ${v.impact}`).join(', ')}) gating=${gating.length}`);
  };

  try {
    // A shelf's cards: five in by keyboard and click, a sixth refused, one
    // out and the sixth in, and the page measured and audited while full.
    for (const lang of ['zh-Hant', 'en']) {
      const label = `compare toggle ${SHELF} ${lang}`;
      await clearSet();
      await open(SHELF, lang);
      const slugs = (await slugsIn('.goen-tiles__grid')).slice(0, CAP + 1);
      if (slugs.length < CAP + 1) { fail(label, `${slugs.length} cards carry a toggle, want at least ${CAP + 1}`); continue; }
      const fresh = await state(slugs[0]);
      if (fresh.pressed !== 'false' || fresh.notes !== 0 || await count() !== 0)
        fail(label, `with an empty set the toggle reads ${JSON.stringify(fresh)} and the header ${await count()}`);
      for (let i = 0; i < CAP; i++) await expectAdded(label, slugs[i], i === 0 ? 'key' : 'click', i);
      await expectRefused(label, slugs[CAP], lang, slugs.slice(0, CAP));
      await sweep(`${label} full`, [1440, 1024, 375, 320]);
      await audit(`${label} full`);
      await expectRemoved(label, slugs[0], 'key', CAP);
      await expectAdded(label, slugs[CAP], 'click', CAP - 1);
      console.log(`${label}: ${CAP} in, a sixth refused with its note, one out and the sixth in; focus kept`);
    }

    // The spec list: the toggle at the end of each row, and the same press.
    {
      const label = `compare toggle ${SHELF}?view=list`;
      await clearSet();
      await open(`${SHELF}?view=list`, 'zh-Hant');
      const slugs = await slugsIn('.goen-speclist');
      if (slugs.length < 2) fail(label, `${slugs.length} rows carry a toggle`);
      else {
        await expectAdded(label, slugs[1], 'key', 0);
        await sweep(label, [1024, 375, 320]);
        console.log(`${label}: a row's toggle adds, focus kept`);
      }
    }

    // The product page: beside the add, on its row, and the same press.
    {
      const slug = 'pixelight-9-pro';
      const label = `compare toggle /p/${slug}`;
      await clearSet();
      await open(`/p/${slug}`, 'zh-Hant');
      const placed = await evaluate(`(() => {
        const form = ${toggle(slug)};
        const add = document.getElementById('add-to-cart');
        if (!form || !add) return { form: !!form, add: !!add };
        const a = add.getBoundingClientRect(), b = form.querySelector('button').getBoundingClientRect();
        return { form: true, add: true, row: form.parentElement.matches('.goen-pdp__actions'),
          outside: !document.getElementById('pdp-add').contains(form), beside: b.left >= a.right - 0.5, level: Math.abs(a.top - b.top) };
      })()`);
      if (!placed.form || !placed.add || !placed.row || !placed.outside || !placed.beside || placed.level > 4)
        fail(label, `the toggle is not beside the add on its row at 1440: ${JSON.stringify(placed)}`);
      await expectAdded(label, slug, 'key', 0);
      await sweep(label, [1440, 1024, 375, 320]);
      await audit(label);
      await expectRemoved(label, slug, 'click', 1);
      console.log(`${label}: beside the add, pressed and released by keyboard and click, focus kept`);
    }

    // The home page: the featured shelf's cards carry it, no other card does.
    {
      const label = 'compare toggle /';
      await clearSet();
      await open('/', 'zh-Hant');
      const cards = await evaluate(`({
        shelf: document.querySelectorAll('.goen-home__shelf .goen-tile__cell').length,
        toggles: document.querySelectorAll('.goen-home__shelf .goen-tile__cell .goen-comparetoggle').length,
        elsewhere: [...document.querySelectorAll('.goen-comparetoggle')].filter(t => !t.closest('.goen-home__shelf')).length,
      })`);
      if (cards.shelf === 0 || cards.toggles !== cards.shelf || cards.elsewhere !== 0)
        fail(label, `the featured shelf's cards and the toggles disagree: ${JSON.stringify(cards)}`);
      const slugs = await slugsIn('.goen-home__shelf');
      if (slugs.length) await expectAdded(label, slugs[0], 'key', 0);
      console.log(`${label}: ${cards.toggles} of the featured shelf's ${cards.shelf} cards carry the toggle, no other card`);
    }
  } finally {
    await send('Emulation.clearDeviceMetricsOverride');
    await send('Network.clearBrowserCookies');
    await send('Network.setCookies', { cookies: priorCookies.map(c => ({
      name: c.name, value: c.value, domain: c.domain, path: c.path, secure: c.secure, httpOnly: c.httpOnly,
      ...(c.sameSite ? { sameSite: c.sameSite } : {}), ...(c.expires > 0 ? { expires: c.expires } : {}),
    })) });
  }
}
