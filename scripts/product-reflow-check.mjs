import assert from 'node:assert/strict';
import { reflowProbe } from './reflow-probe.mjs';

// The product page's own reflow, on its real markup, CSS and goen.js, without a
// database: every product the fixture serves (the seed's longest names, in
// Chinese, English and both, with model numbers that do not break) at each
// width, in Chinese and English, at 100% and 200% text, read at the top of the
// page and again at its foot, where the buy bar and the related products are.
// Nothing may widen the page or run out of its box.
const [origin, port, ...products] = process.argv.slice(2);
assert.ok(products.length > 0, 'no products to measure');
const debugging = `http://127.0.0.1:${port}`;
const response = await fetch(`${debugging}/json/new?about:blank`, { method: 'PUT' });
assert.equal(response.ok, true, 'Chrome must create the product reflow page');
const target = await response.json();
const socket = new WebSocket(target.webSocketDebuggerUrl);
const problems = [];
let measured = 0;
try {
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let id = 0;
  const pending = new Map();
  socket.onmessage = ({ data }) => {
    const message = JSON.parse(data);
    const request = pending.get(message.id);
    if (!request) return;
    pending.delete(message.id);
    clearTimeout(request.timer);
    if (message.error) request.reject(new Error(JSON.stringify(message.error)));
    else request.resolve(message.result);
  };
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    const current = ++id;
    const timer = setTimeout(() => { pending.delete(current); reject(new Error(`${method} timed out`)); }, 20000);
    pending.set(current, { resolve, reject, timer });
    socket.send(JSON.stringify({ id: current, method, params }));
  });
  const evaluate = async (expression) => {
    const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
    return result.result.value;
  };
  // As reflow-check measures: no scrollbar takes width from the page.
  await send('Emulation.setScrollbarsHidden', { hidden: true });
  for (const product of products) {
    for (const width of [320, 375, 768, 1440]) {
      await send('Emulation.setDeviceMetricsOverride', { width, height: 800, deviceScaleFactor: 1, mobile: width < 600 });
      for (const lang of ['zh-Hant', 'en']) {
        for (const text of ['100%', '200%']) {
          const url = `${origin}/p/${product}?lang=${lang}&text=${text}&width=${width}`;
          await send('Page.navigate', { url });
          for (let attempt = 0; !await evaluate(`location.href === ${JSON.stringify(url)} && document.readyState === 'complete'`); attempt++) {
            if (attempt === 200) throw new Error(`${url} did not load`);
            await new Promise((resolve) => setTimeout(resolve, 25));
          }
          await evaluate(`document.documentElement.style.fontSize = ${JSON.stringify(text)}`);
          const title = await evaluate(`document.querySelector('h1.goen-pdp__title')?.textContent.trim() ?? ''`);
          assert.ok(title, `${product}: the fixture has no product name`);
          for (const at of ['top', 'foot']) {
            await evaluate(at === 'top' ? 'scrollTo(0, 0)' : 'scrollTo(0, document.documentElement.scrollHeight)');
            const got = await evaluate(reflowProbe(width));
            measured++;
            if (got.scrollWidth > width || got.text.length) {
              problems.push(`「${title}」 ${width} ${lang} ${text} ${at}: scrollWidth ${got.scrollWidth} > ${width} or ${JSON.stringify(got.text.slice(0, 3))}`);
            }
          }
        }
      }
    }
  }
} finally {
  socket.close();
  await fetch(`${debugging}/json/close/${target.id}`);
}
assert.deepEqual(problems, [], `the product page does not reflow:\n${problems.join('\n')}`);
console.log(`${products.length} products, ${measured} readings at 320 to 1440, zh-Hant and en, 100% and 200% text: no sideways scroll, nothing past its box`);
