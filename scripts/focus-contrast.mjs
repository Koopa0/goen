export function measureFocus(selector, cardSelector = '') {
  const input = document.querySelector(selector);
  if (!input) return { error: 'focus target missing: ' + selector };
  const target = cardSelector ? input.closest(cardSelector) : input;
  if (!target || !target.getClientRects().length) return { error: 'visible focus surface missing' };
  const canvas = document.createElement('canvas');
  canvas.width = canvas.height = 1;
  const ctx = canvas.getContext('2d', { willReadFrequently: true });
  const rgba = (colour) => {
    ctx.clearRect(0, 0, 1, 1);
    ctx.fillStyle = colour;
    ctx.fillRect(0, 0, 1, 1);
    return [...ctx.getImageData(0, 0, 1, 1).data];
  };
  const over = (colour, background) => colour.slice(0, 3)
    .map((c, i) => c * colour[3] / 255 + background[i] * (1 - colour[3] / 255));
  const ancestors = [];
  for (let parent = target.parentElement; parent; parent = parent.parentElement) ancestors.unshift(parent);
  let surrounding = [255, 255, 255];
  for (const parent of ancestors) {
    const style = getComputedStyle(parent);
    if (style.backgroundImage !== 'none' || style.opacity !== '1') {
      return { error: 'focus background needs a separate visual check' };
    }
    surrounding = over(rgba(style.backgroundColor), surrounding);
  }
  const selection = () => {
    const style = getComputedStyle(target);
    return { border: style.borderTop, shadow: style.boxShadow };
  };
  document.activeElement?.blur();
  const before = selection();
  target.scrollIntoView({ block: 'center', behavior: 'instant' });
  input.focus({ preventScroll: true });
  const style = getComputedStyle(target);
  const adjacent = parseFloat(style.outlineOffset) < 0
    ? over(rgba(style.backgroundColor), surrounding) : surrounding;
  const outline = over(rgba(style.outlineColor), adjacent);
  const luminance = (rgb) => rgb.reduce((sum, channel, i) => {
    const c = channel / 255;
    return sum + [0.2126, 0.7152, 0.0722][i]
      * (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
  }, 0);
  const first = luminance(outline);
  const second = luminance(adjacent);
  return { selector, outline: style.outline, offset: style.outlineOffset, adjacent,
    contrast: (Math.max(first, second) + 0.05) / (Math.min(first, second) + 0.05),
    width: parseFloat(style.outlineWidth), outlineStyle: style.outlineStyle,
    active: document.activeElement === input, focusVisible: input.matches(':focus-visible'),
    checked: !!input.checked, before, after: selection() };
}
