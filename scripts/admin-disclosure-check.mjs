import assert from 'node:assert/strict';

const [origin, port] = process.argv.slice(2);
const debugging = `http://127.0.0.1:${port}`;
const target = await (await fetch(`${debugging}/json/new?about:blank`, { method: 'PUT' })).json();
const socket = new WebSocket(target.webSocketDebuggerUrl);
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
  await send('Page.enable');
  await send('Network.enable');
  await send('Network.setBlockedURLs', { urls: ['*/static/js/*'] });
  assert.equal(await evaluate('CSS.supports("selector(::details-content)")'), true, 'Chrome must exercise the desktop rail rule');
  for (const locale of ['en', 'zh-Hant']) {
    for (const width of [375, 1024, 1440]) {
      for (const path of ['/health', '/twofactor']) {
        const label = `${path} ${locale} ${width}px`;
        const selector = path === '/health' ? '#system-status' : '.goen-twofa details';
        await send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: width < 768 });
        const url = `${origin}${path}?lang=${locale}`;
        await send('Page.navigate', { url });
        for (let attempt = 0; !await evaluate(`location.href === ${JSON.stringify(url)} && document.readyState === 'complete'`); attempt++) {
          if (attempt === 100) throw new Error(`${label}: disclosure fixture did not load`);
          await new Promise(resolve => setTimeout(resolve, 30));
        }
        await evaluate('document.fonts.ready.then(() => new Promise(resolve => requestAnimationFrame(resolve)))');
        const state = await evaluate(`(() => {
          const details = document.querySelector(${JSON.stringify(selector)});
          const summary = details?.querySelector('summary');
          if (!summary) throw new Error('Missing disclosure');
          const rect = summary.getBoundingClientRect();
          summary.focus();
          return { locale: document.documentElement.lang, open: details.open,
            visible: getComputedStyle(summary).display !== 'none' && rect.width > 0 && rect.height > 0,
            focused: document.activeElement === summary };
        })()`);
        assert.deepEqual(state, { locale, open: false, visible: true, focused: true }, label);
        await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13 });
        await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13 });
        assert.equal(await evaluate(`(() => {
          const details = document.querySelector(${JSON.stringify(selector)});
          const panel = details.querySelector('.goen-disclosure__panel');
          return details.open && panel.checkVisibility() && panel.getBoundingClientRect().height > 0;
        })()`), true, `${label}: Enter opens visible content`);
        await send('Input.dispatchKeyEvent', { type: 'keyDown', key: ' ', code: 'Space', windowsVirtualKeyCode: 32 });
        await send('Input.dispatchKeyEvent', { type: 'keyUp', key: ' ', code: 'Space', windowsVirtualKeyCode: 32 });
        assert.equal(await evaluate(`document.querySelector(${JSON.stringify(selector)}).open`), false, `${label}: Space closes content`);
        if (path === '/health') {
          const rail = await evaluate(`(() => {
            const nav = document.querySelector('.goen-admin__nav');
            return { hidden: getComputedStyle(nav.querySelector(':scope > summary')).display === 'none',
              visible: nav.querySelector('.goen-disclosure__panel').checkVisibility() };
          })()`);
          assert.deepEqual(rail, { hidden: width >= 1024, visible: width >= 1024 }, `${label}: rail retains responsive behavior`);
        }
        console.log(`${label}: content summary visible; Enter opens; Space closes`);
      }
    }
  }
} finally {
  socket.close();
  await fetch(`${debugging}/json/close/${target.id}`);
}
