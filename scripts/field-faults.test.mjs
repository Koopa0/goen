import assert from 'node:assert/strict';
import test from 'node:test';
import { fieldFaults } from './field-faults.mjs';

const rect = (left, top, width, height) => ({ left, top, width, height, right: left + width, bottom: top + height });
const el = (tagName, className, r, children = []) => ({
  tagName,
  className,
  children,
  classList: { contains: (c) => className.split(' ').includes(c) },
  getBoundingClientRect: () => r,
});

test('fieldFaults passes a clipped goen-sr-only label', () => {
  const label = el('LABEL', 'goen-sr-only', rect(0, 0, 1, 1));
  const input = el('INPUT', 'ui-input', rect(0, 0, 295, 44));
  const field = el('DIV', 'goen-admin__field', rect(0, 0, 295, 44), [label, input]);
  assert.deepEqual(fieldFaults([field]), []);
});

test('fieldFaults fails a squeezed visible label', () => {
  const label = el('LABEL', 'ui-label', rect(0, 0, 1, 20));
  const input = el('INPUT', 'ui-input', rect(0, 24, 295, 44));
  const field = el('DIV', 'goen-admin__field', rect(0, 0, 295, 68), [label, input]);
  assert.deepEqual(fieldFaults([field]), ['label 1.0px of a 295.0px field']);
});

test('fieldFaults still checks the left edge and overlap of visible children', () => {
  const label = el('LABEL', 'ui-label', rect(0, 0, 295, 20));
  const input = el('INPUT', 'ui-input', rect(10, 10, 285, 44));
  const field = el('DIV', 'goen-admin__field', rect(0, 0, 295, 54), [label, input]);
  assert.deepEqual(fieldFaults([field]), [
    'label.ui-label overlaps input.ui-input',
    'input.ui-input starts 10.0px from the field left edge',
  ]);
});
