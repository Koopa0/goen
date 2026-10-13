export function deliveryRefusalTarget(path, placedOrder) {
  if (!placedOrder || !/^[A-Za-z0-9_-]+$/.test(placedOrder) || path !== `/admin/orders/${placedOrder}`) {
    throw new Error('delivery-refused needs the layout PLACED_ORDER staff page');
  }
  return `${path}/delivery`;
}

// Serialized into the existing browser; submission uses its production form.
export function deliveryRefusalForm(path, submit = false) {
  if (location.pathname !== path || location.search ||
      performance.getEntriesByType('navigation')[0]?.responseStatus !== 200) {
    throw new Error('delivery-refused needs the successful baseline order GET');
  }
  const target = path + '/delivery';
  const form = [...document.querySelectorAll('form')].find((f) =>
    f.method.toLowerCase() === 'post' && f.action === location.origin + target);
  const names = ['recipient', 'phone', 'email', 'postal_code', 'city', 'district', 'street'];
  if (!form || names.some((name) => !form.elements.namedItem(name)) ||
      form.elements.namedItem('pickup_store_code') || form.elements.namedItem('pickup_chain')) {
    throw new Error('the layout order has no correctable address delivery form');
  }
  const summary = [...document.querySelectorAll('dd.ui-dl__desc')].map((e) => e.textContent.trim());
  if (!summary.length) throw new Error('the order has no saved delivery summary');
  const savedDraft = names.map((name) => ({ name, value: form.elements.namedItem(name).value }));
  form.elements.namedItem('recipient').value = ' Proposed <recipient> ';
  if (!form.checkValidity()) throw new Error('the layout delivery form has another invalid control');
  const draft = names.map((name) => ({ name, value: form.elements.namedItem(name).value }));
  if (submit) {
    const field = document.createElement('input');
    field.type = 'hidden';
    field.name = 'pickup_store_code';
    field.value = String.fromCharCode(1);
    form.append(field);
    form.requestSubmit();
  }
  return { draft, savedDraft, summary };
}

export function deliveryRefusalPage() {
  const lead = document.querySelector('.goen-notice__lead');
  const alert = lead?.closest('[role="alert"]');
  const box = alert?.getBoundingClientRect();
  const names = ['recipient', 'phone', 'email', 'postal_code', 'city', 'district', 'street'];
  const form = [...document.querySelectorAll('form')].find((f) => f.action === location.href);
  return {
    origin: location.origin,
    finalPath: location.pathname + location.search,
    lang: document.documentElement.lang,
    lead: lead?.textContent.trim() || '',
    alert: alert?.textContent.trim() || '',
    visible: !!(box && box.width > 0 && box.height > 0 && getComputedStyle(alert).visibility === 'visible'),
    oppositeControl: !!document.querySelector('[name="pickup_store_code"]'),
    draft: names.map((name) => ({ name, value: form?.elements.namedItem(name)?.value ?? null })),
    summary: [...document.querySelectorAll('dd.ui-dl__desc')].map((e) => e.textContent.trim()),
  };
}

export function requireDeliveryRefusal(before, after, response, target, lang, origin) {
  if (response.method !== 'POST' || response.path !== target || response.status !== 422 ||
      after.origin !== origin || after.finalPath !== target) {
    throw new Error('delivery-refused did not return the same-order POST 422 document');
  }
  const lead = lang === 'en' ? 'Not saved' : '未儲存';
  const reason = lang === 'en' ? 'Contains characters that are not allowed' : '含有不允許的字元';
  if (after.lang !== lang || !after.visible || after.lead !== lead || !after.alert.includes(reason)) {
    throw new Error('delivery-refused has no visible localized refusal alert');
  }
  if (after.oppositeControl || JSON.stringify(after.draft) !== JSON.stringify(before.draft) ||
      JSON.stringify(after.summary) !== JSON.stringify(before.summary)) {
    throw new Error('delivery-refused changed the saved summary, lost the draft or introduced the absent control');
  }
}

export async function captureDeliveryRefusal({ entry, placedOrder, origin, ws, evaluate }) {
  const target = deliveryRefusalTarget(entry.path, placedOrder);
  const before = await evaluate(`(${deliveryRefusalForm.toString()})(${JSON.stringify(entry.path)})`);
  let requestID = '', loaded = false, response = null, observed = null, after = null, observe, timer;
  let resolve, reject;
  const completed = new Promise((yes, no) => { resolve = yes; reject = no; });
  // An evaluation can fail while the navigation deadline is still pending.
  completed.catch(() => {});
  const finish = () => { if (response && loaded) resolve(response); };
  observe = (event) => {
    const { method, params = {} } = JSON.parse(event.data);
    if (method === 'Network.requestWillBeSent' && params.type === 'Document') {
      const matches = params.request?.method === 'POST' && params.request.url === origin + target;
      if (!requestID && matches) requestID = params.requestId;
      // Chrome keeps the request ID when a POST redirects to a different request.
      if (requestID && params.requestId === requestID && (params.redirectResponse || !matches)) {
        reject(new Error('delivery-refused POST redirected or changed request'));
        return;
      }
    }
    if (method === 'Network.responseReceived' && requestID && params.requestId === requestID && params.type === 'Document') {
      response = { method: 'POST', path: params.response.url === origin + target ? target : null, status: params.response.status };
      finish();
    }
    if (method === 'Page.loadEventFired' && requestID) { loaded = true; finish(); }
  };
  ws.addEventListener('message', observe);
  timer = setTimeout(() => reject(new Error('delivery-refused POST did not finish')), 15000);
  try {
    await evaluate(`(${deliveryRefusalForm.toString()})(${JSON.stringify(entry.path)}, true)`);
    observed = await completed;
    after = await evaluate(`(${deliveryRefusalPage.toString()})()`);
    requireDeliveryRefusal(before, after, observed, target, entry.lang, origin);
    return { state: 'delivery-refused', ...observed, lead: after.lead, alert: after.alert, baseline: before };
  } catch (error) {
    if (observed) error.capture = {
      status: observed.status, finalPath: after?.finalPath || observed.path,
      refusal: { state: 'delivery-refused', ...observed },
    };
    throw error;
  } finally {
    clearTimeout(timer);
    ws.removeEventListener('message', observe);
  }
}

export function deliveryRecoveryPage() {
  const form = [...document.querySelectorAll('form')].find((f) =>
    f.method.toLowerCase() === 'post' && f.action === location.href + '/delivery');
  const names = ['recipient', 'phone', 'email', 'postal_code', 'city', 'district', 'street'];
  return {
    origin: location.origin,
    path: location.pathname + location.search,
    status: performance.getEntriesByType('navigation')[0]?.responseStatus || 0,
    draft: names.map((name) => ({ name, value: form?.elements.namedItem(name)?.value ?? null })),
    summary: [...document.querySelectorAll('dd.ui-dl__desc')].map((e) => e.textContent.trim()),
  };
}

export async function recoverDeliveryBaseline({ entry, origin, baseline, navigate, evaluate }) {
  await navigate(origin + entry.path);
  const after = await evaluate(`(${deliveryRecoveryPage.toString()})()`);
  if (after.origin !== origin || after.path !== entry.path || after.status !== 200 ||
      JSON.stringify(after.draft) !== JSON.stringify(baseline.savedDraft) ||
      JSON.stringify(after.summary) !== JSON.stringify(baseline.summary)) {
    throw new Error('delivery-refused recovery did not restore the unchanged saved delivery');
  }
  return { method: 'GET', path: after.path, status: after.status, savedDeliveryUnchanged: true };
}
