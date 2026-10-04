export function contrastRatio(a, b) {
  const luminance = (rgb) => rgb.reduce((sum, channel, i) => {
    const c = channel / 255;
    return sum + [0.2126, 0.7152, 0.0722][i]
      * (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
  }, 0);
  const first = luminance(a);
  const second = luminance(b);
  return (Math.max(first, second) + 0.05) / (Math.min(first, second) + 0.05);
}

// The canvas resolves computed CSS colours, including oklch, into sRGB bytes.
export function measureControlBoundary(selectors, contrast) {
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
  const surface = (el) => {
    const ancestors = [];
    for (let parent = el; parent; parent = parent.parentElement) ancestors.unshift(parent);
    return ancestors.reduce((background, parent) => {
      const style = getComputedStyle(parent);
      if (style.backgroundImage !== 'none') throw new Error('background image needs a separate visual check');
      if (style.opacity !== '1') throw new Error('opacity needs a separate visual check');
      return over(rgba(style.backgroundColor), background);
    }, [255, 255, 255]);
  };
  return selectors.map((selector) => {
    const el = document.querySelector(selector);
    if (!el || !el.getClientRects().length || el.disabled) {
      return { selector, error: 'enabled visible control missing' };
    }
    el.blur();
    try {
      const style = getComputedStyle(el);
      const surrounding = surface(el.parentElement);
      const fill = over(rgba(style.backgroundColor), surrounding);
      const inward = parseFloat(style.outlineOffset) < 0;
      const outlineContrast = style.outlineStyle !== 'none' && parseFloat(style.outlineWidth) > 0
        ? contrast(over(rgba(style.outlineColor), inward ? fill : surrounding), inward ? fill : surrounding)
        : 1;
      const borders = ['Top', 'Right', 'Bottom', 'Left'].map((side) => {
        if (style['border' + side + 'Style'] === 'none' || parseFloat(style['border' + side + 'Width']) === 0) return 1;
        return contrast(over(rgba(style['border' + side + 'Color']), surrounding), surrounding);
      });
      return {
        selector, outline: style.outline, offset: style.outlineOffset,
        fill, surrounding, outlineContrast, borderContrast: Math.min(...borders),
        fillContrast: contrast(fill, surrounding), shadow: style.boxShadow,
      };
    } catch (err) {
      return { selector, error: err.message };
    }
  });
}
