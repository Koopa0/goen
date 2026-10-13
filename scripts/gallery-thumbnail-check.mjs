import assert from 'node:assert/strict';

const [origin, port] = process.argv.slice(2);
const debugging = `http://127.0.0.1:${port}`;
const response = await fetch(`${debugging}/json/new?about:blank`, { method: 'PUT' });
assert.equal(response.ok, true, 'Chrome must create the gallery test page');
const target = await response.json();
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
  const evaluate = async expression => {
    const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
    return result.result.value;
  };
  const expectedRows = new Map([[0, []], [1, []], [5, [5]], [6, [5, 1]], [10, [5, 5]]]);
  await send('Page.enable');
  await send('Network.enable');
  await send('Network.setBlockedURLs', { urls: ['*/static/js/*'] });
  for (const locale of ['en', 'zh-Hant']) for (const width of [320, 375, 1440]) {
    for (const [count, rows] of expectedRows) {
      const label = `${count} images ${locale} ${width}px`;
      await send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: width < 768 });
      const url = `${origin}/gallery/${count}?lang=${locale}`;
      await send('Page.navigate', { url });
      for (let attempt = 0; !await evaluate(`location.href === ${JSON.stringify(url)} && document.readyState === 'complete'`); attempt++) {
        if (attempt === 100) throw new Error(`${label}: gallery fixture did not load`);
        await new Promise(resolve => setTimeout(resolve, 30));
      }
      await evaluate('document.fonts.ready.then(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))');
      const geometry = await evaluate(`(() => {
        const gallery = document.querySelector('#gallery');
        if (!gallery) throw new Error('Gallery missing');
        const box = gallery.getBoundingClientRect();
        const thumbs = [...gallery.querySelectorAll('.goen-pdp__thumb')];
        const rows = [];
        const faults = [];
        for (const thumb of thumbs) {
          const rect = thumb.getBoundingClientRect();
          let row = rows.find(row => Math.abs(row.top - rect.top) < 1);
          if (!row) { row = { top: rect.top, count: 0 }; rows.push(row); }
          row.count++;
          if (rect.left < box.left - 1 || rect.right > box.right + 1 || rect.width <= 0 || rect.height <= 0)
            faults.push('thumbnail exceeds the gallery or has no area');
        }
        const shots = [...gallery.querySelectorAll('.goen-pdp__shot')];
        for (const shot of shots) {
          const rect = shot.getBoundingClientRect();
          if (Math.abs(rect.left - box.left) > 1 || Math.abs(rect.right - box.right) > 1)
            faults.push('stage does not span the gallery');
        }
        if (document.documentElement.scrollWidth > innerWidth) faults.push('page scrolls sideways');
        return { locale: document.documentElement.lang,
          columns: getComputedStyle(gallery).gridTemplateColumns.trim().split(' ').filter(Boolean).length,
          rows: rows.map(row => row.count), images: gallery.querySelectorAll('.goen-pdp__shotimg').length,
          radios: gallery.querySelectorAll('input[type="radio"][name="shot"]').length,
          placeholder: gallery.querySelectorAll('.goen-pdp__shot--empty').length,
          loaded: [...gallery.querySelectorAll('img')].every(img => img.complete && img.naturalWidth > 0), faults };
      })()`);
      assert.deepEqual(geometry, { locale, columns: 5, rows, images: count,
        radios: count > 1 ? count : 0, placeholder: count ? 0 : 1, loaded: true, faults: [] }, label);
      if (count > 1) {
        for (let index = 0; index < count; index++) {
          const point = await evaluate(`(() => {
            const thumb = document.querySelector('label[for="shot-${index}"]');
            if (!thumb) throw new Error('Thumbnail label missing');
            thumb.scrollIntoView({ block: 'center' });
            const box = thumb.getBoundingClientRect();
            return { x: box.left + box.width / 2, y: box.top + box.height / 2 };
          })()`);
          await send('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, ...point });
          await send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...point });
          const selection = await evaluate(`(() => {
            const checked = document.querySelector('.goen-pdp__pick:checked');
            const visible = [...document.querySelectorAll('.goen-pdp__shot')].filter(shot => getComputedStyle(shot).visibility === 'visible');
            return { checked: checked?.id, visible: visible.map(shot => shot.querySelector('img').alt) };
          })()`);
          assert.deepEqual(selection, { checked: `shot-${index}`, visible: [`Photo ${index + 1}`] }, `${label}: native label ${index}`);
        }
        await evaluate('document.querySelector(".goen-pdp__pick:checked").focus()');
        await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'ArrowLeft', code: 'ArrowLeft', windowsVirtualKeyCode: 37 });
        await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'ArrowLeft', code: 'ArrowLeft', windowsVirtualKeyCode: 37 });
        assert.deepEqual(await evaluate(`(() => {
          const checked = document.querySelector('.goen-pdp__pick:checked');
          const visible = [...document.querySelectorAll('.goen-pdp__shot')].filter(shot => getComputedStyle(shot).visibility === 'visible');
          return { checked: checked?.id, focused: document.activeElement === checked, visible: visible.map(shot => shot.querySelector('img').alt) };
        })()`), { checked: `shot-${count - 2}`, focused: true, visible: [`Photo ${count - 1}`] }, `${label}: native ArrowLeft`);
      }
      console.log(`${label}: five columns, rows ${JSON.stringify(rows)}, native selection preserved`);
    }
  }
} finally {
  socket.close();
  await fetch(`${debugging}/json/close/${target.id}`);
}
