export async function measureChooserStates(name) {
  const inputs = [...document.querySelectorAll('input[type=radio]')].filter((input) => input.name === name);
  if (inputs.length < 2) return { error: 'chooser group missing: ' + name };
  const previous = inputs.find((input) => input.checked);
  const signature = (card) => {
    const style = getComputedStyle(card);
    const properties = ['backgroundColor', 'color', 'borderTopColor', 'borderTopWidth',
      'borderTopStyle', 'outlineColor', 'outlineWidth', 'outlineStyle', 'outlineOffset',
      'boxShadow', 'fontWeight', 'textDecorationLine'];
    return Object.fromEntries(properties.map((key) => [key, style[key]]));
  };
  const settle = () => new Promise((resolve) => setTimeout(resolve, 350));
  const results = [];
  document.activeElement?.blur();
  for (const input of inputs) {
    const card = input.closest('.goen-checkout__ship');
    if (!card) return { error: 'chooser has no visible card' };
    inputs.find((other) => other !== input).checked = true;
    await settle();
    const unchecked = signature(card);
    input.checked = true;
    await settle();
    const checked = signature(card);
    const rect = input.getBoundingClientRect();
    const radioStyle = getComputedStyle(input);
    results.push({ value: input.value, checked, unchecked,
      radioVisible: rect.width >= 8 && rect.height >= 8 && radioStyle.clipPath === 'none' &&
        radioStyle.visibility === 'visible' && Number(radioStyle.opacity) === 1,
      distinct: JSON.stringify(checked) !== JSON.stringify(unchecked) });
  }
  for (const input of inputs) input.checked = input === previous;
  return { forced: matchMedia('(forced-colors: active)').matches, name, results };
}

export async function measureSwatchState(dot = false) {
  const selected = document.querySelector(dot ? '.goen-swatch--dot.goen-swatch--on' : '.goen-swatch--on:not(.goen-swatch--dot)');
  if (!selected) return { error: 'selected text swatch missing' };
  const properties = ['color', 'backgroundColor', 'borderTopColor', 'borderTopWidth',
    'outlineColor', 'outlineWidth', 'outlineStyle', 'fontWeight', 'textDecorationLine'];
  const signature = () => {
    const style = getComputedStyle(dot ? selected.querySelector('.goen-swatch__dot') : selected);
    return Object.fromEntries(properties.map((key) => [key, style[key]]));
  };
  document.activeElement?.blur();
  selected.classList.remove('goen-swatch--on');
  await new Promise((resolve) => setTimeout(resolve, 350));
  const unchecked = signature();
  selected.classList.add('goen-swatch--on');
  await new Promise((resolve) => setTimeout(resolve, 350));
  const checked = signature();
  return { forced: matchMedia('(forced-colors: active)').matches,
    kind: dot ? 'colour' : 'text', text: selected.textContent.trim(), checked, unchecked,
    distinct: JSON.stringify(checked) !== JSON.stringify(unchecked) };
}
