export function controlLabelReflow(control) {
  const box = control.getBoundingClientRect();
  if (!box.width || !box.height) return [];
  const faults = [];
  if (box.width < 44 || box.height < 44) faults.push('touch target is below 44px');
  const walker = document.createTreeWalker(control, NodeFilter.SHOW_TEXT);
  const range = document.createRange();
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    range.selectNodeContents(node);
    const text = [...range.getClientRects()].filter(r => r.width > 0 && r.height > 0);
    if (text.some(r => r.left < box.left - 1 || r.right > box.right + 1 || r.top < box.top - 1 || r.bottom > box.bottom + 1)) {
      faults.push('label exceeds the control');
    }
    // Chinese has native character breaks; Latin words must stay on one line.
    for (const word of node.nodeValue.matchAll(/[\p{Script=Latin}\d]+(?:['’-][\p{Script=Latin}\d]+)*/gu)) {
      range.setStart(node, word.index);
      range.setEnd(node, word.index + word[0].length);
      const rects = [...range.getClientRects()].filter(r => r.width > 0 && r.height > 0);
      if (rects.some(r => Math.abs(r.top - rects[0].top) > 1)) faults.push(`word breaks inside ${JSON.stringify(word[0])}`);
    }
  }
  return faults;
}
