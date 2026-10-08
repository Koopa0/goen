// A visually hidden label (.goen-sr-only) is read by assistive technology, not
// laid out beside the control, so only visible children are held to the column.
export function fieldFaults(fields) {
  const faults = [];
  for (const field of fields) {
    const box = field.getBoundingClientRect();
    if (box.width === 0) continue;
    const kids = [...field.children]
      .filter((el) => !el.classList.contains('goen-sr-only'))
      .map((el) => ({ el, r: el.getBoundingClientRect() }))
      .filter((k) => k.r.width > 0 && k.r.height > 0);
    const name = (el) => el.tagName.toLowerCase() + '.' + String(el.className || '').split(' ')[0];
    for (const [i, k] of kids.entries()) {
      if (Math.abs(k.r.left - box.left) > 1) faults.push(name(k.el) + ' starts ' + (k.r.left - box.left).toFixed(1) + 'px from the field left edge');
      if (k.el.tagName === 'LABEL' && k.r.width < box.width / 2) faults.push('label ' + k.r.width.toFixed(1) + 'px of a ' + box.width.toFixed(1) + 'px field');
      for (const o of kids.slice(i + 1)) {
        if (k.r.left < o.r.right - 1 && o.r.left < k.r.right - 1 && k.r.top < o.r.bottom - 1 && o.r.top < k.r.bottom - 1) faults.push(name(k.el) + ' overlaps ' + name(o.el));
      }
    }
  }
  return faults;
}
