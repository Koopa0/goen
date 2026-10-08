export async function screenshotReflow() {
  const initialScrollX = window.scrollX;
  const initialScrollY = window.scrollY;
  const result = {
    bodyScrollWidth: document.body.scrollWidth,
    viewportWidth: document.documentElement.clientWidth,
    innerWidth: window.innerWidth,
    viewportHeight: window.innerHeight,
    rootFontSize: getComputedStyle(document.documentElement).fontSize,
    devicePixelRatio: window.devicePixelRatio,
    initialScrollX,
    initialScrollY,
  };
  const frame = () => new Promise((resolve) => {
    const timer = setTimeout(resolve, 100);
    requestAnimationFrame(() => { clearTimeout(timer); resolve(); });
  });
  const settle = async () => {
    let priorX = window.scrollX;
    let priorY = window.scrollY;
    let stable = 0;
    for (let i = 0; i < 6; i++) {
      await frame();
      const x = window.scrollX;
      const y = window.scrollY;
      stable = x === priorX && y === priorY ? stable + 1 : 0;
      if (stable === 2) return true;
      priorX = x;
      priorY = y;
    }
    return false;
  };
  try {
    // A descendant can inflate document width without moving the window.
    window.scrollTo({
      left: Math.max(document.documentElement.scrollWidth, result.bodyScrollWidth, window.innerWidth),
      top: initialScrollY,
      behavior: 'instant',
    });
    result.settled = await settle();
    result.scrollX = window.scrollX;
    result.scrollY = window.scrollY;
  } finally {
    window.scrollTo({ left: initialScrollX, top: initialScrollY, behavior: 'instant' });
    const settled = await settle();
    result.restoredScrollX = window.scrollX;
    result.restoredScrollY = window.scrollY;
    result.restored = settled && result.restoredScrollX === initialScrollX && result.restoredScrollY === initialScrollY;
  }
  return result;
}
