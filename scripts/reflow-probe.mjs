export const reflowProbe = (width) => `(async () => {
  await document.fonts.ready;
  await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
  const width = ${width};
  const scrolls = (o) => o === 'auto' || o === 'scroll';
  const clips = (o) => o === 'hidden' || o === 'clip';
  const cuts = (lo, hi, boxLo, boxHi) => lo < boxHi && hi > boxLo && (lo < boxLo - 0.5 || hi > boxHi + 0.5);
  const text = [];
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  const range = document.createRange();
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    if (!node.nodeValue.trim()) continue;
    const owner = node.parentElement;
    if (!owner || owner.closest('script, style, noscript, template, option')) continue;
    if (getComputedStyle(owner).visibility !== 'visible') continue;
    range.selectNodeContents(node);
    const rects = [...range.getClientRects()].filter((r) => r.width > 0 && r.height > 0);
    if (!rects.length) continue;
    const left = Math.min(...rects.map((r) => r.left));
    const right = Math.max(...rects.map((r) => r.right));
    const top = Math.min(...rects.map((r) => r.top));
    const bottom = Math.max(...rects.map((r) => r.bottom));
    let xFree = false;
    let yFree = false;
    let cause = '';
    for (let a = owner; a && a !== document.body && a !== document.documentElement && !cause; a = a.parentElement) {
      const cs = getComputedStyle(a);
      const box = a.getBoundingClientRect();
      if (!xFree) {
        if (scrolls(cs.overflowX)) xFree = true;
        else if (clips(cs.overflowX)) {
          if (box.width <= 1 || cs.textOverflow === 'ellipsis') xFree = true;
          else if (cuts(left, right, box.left, box.right)) cause = 'cut by the clip of';
        }
      }
      if (!yFree && !cause) {
        if (scrolls(cs.overflowY)) yFree = true;
        else if (clips(cs.overflowY)) {
          if (box.height <= 1 || (cs.webkitLineClamp && cs.webkitLineClamp !== 'none')) yFree = true;
          else if (cuts(top, bottom, box.top, box.bottom)) cause = 'cut by the clip of';
        }
      }
      if (cs.position === 'fixed') break;
    }
    if (!cause && !xFree && cuts(left, right, 0, width)) cause = 'runs past the viewport in';
    if (cause) {
      // The attribute, not className: on an SVG element className is an SVGAnimatedString.
      const where = owner.tagName.toLowerCase() + '.' + (owner.getAttribute('class') || '').split(' ')[0];
      text.push({ owner: where, detail: cause + ' ' + JSON.stringify(node.nodeValue.trim().slice(0, 24)) });
    }
  }
  const scrollWidth = document.body.scrollWidth;
  window.scrollTo(10000, 0);
  const scrollX = window.scrollX;
  window.scrollTo(0, 0);
  return { scrollWidth, scrollX, text };
})()`;
