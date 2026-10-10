import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { runInNewContext } from 'node:vm';

const source = readFileSync(new URL('./check-layout.mjs', import.meta.url), 'utf8');
const declaration = source.match(/^const ACCESSIBILITY = (`[\s\S]*?^`);/m);
assert.ok(declaration, 'the layout checker must declare its emitted accessibility fragment');
// Evaluate the authored template first, exactly as the checker does before CDP
// receives it; reading the regexp directly would miss template escape loss.
const emitted = runInNewContext(declaration[1]);
const inspect = new Function('document', `return ({${emitted}});`);

function unexplained(describedBy, targets, invalid = 'true') {
  const field = {
    id: 'points', tagName: 'INPUT',
    getAttribute: (name) => name === 'aria-describedby' ? describedBy : null,
  };
  const document = {
    documentElement: { getAttribute: () => 'zh-Hant' },
    querySelectorAll: (selector) => selector === '[aria-invalid="true"]' && invalid === 'true' ? [field] : [],
    getElementById: (id) => targets[id] ?? null,
  };
  return inspect(document).unexplainedInvalids;
}

function target(role = null, classes = []) {
  return {
    getAttribute: (name) => name === 'role' ? role : null,
    classList: { contains: (name) => classes.includes(name) },
  };
}

for (const [name, separator] of [
  ['space', ' '], ['multiple spaces', '   '], ['tab', '\t'],
  ['line feed', '\n'], ['carriage return', '\r'], ['form feed', '\f'],
]) {
  test(`emitted accessibility fragment accepts hint and alert IDs separated by ${name}`, () => {
    assert.deepEqual(unexplained(`points-rule${separator}points-error`, {
      'points-rule': target(), 'points-error': target('alert'),
    }), []);
  });
}

test('emitted accessibility fragment accepts leading whitespace and an error before its hint', () => {
  assert.deepEqual(unexplained(' \tpoints-error points-rule\n ', {
    'points-rule': target(), 'points-error': target('alert'),
  }), []);
});

for (const errorClass of ['ui-error-text', 'ui-alert--error']) {
  test(`emitted accessibility fragment accepts an attached ${errorClass} without an alert role`, () => {
    assert.deepEqual(unexplained('points-rule points-error', {
      'points-rule': target(), 'points-error': target(null, [errorClass]),
    }), []);
  });
}

for (const [name, describedBy, targets] of [
  ['missing error target', 'points-rule points-error', { 'points-rule': target() }],
  ['hint only', 'points-rule', { 'points-rule': target(), 'points-error': target('alert') }],
  ['ordinary text target', 'points-rule points-error', { 'points-rule': target(), 'points-error': target() }],
  ['no reference', null, { 'points-error': target('alert') }],
  ['empty reference', '', { 'points-error': target('alert') }],
  ['whitespace only', ' \t\n ', { 'points-error': target('alert') }],
]) {
  test(`emitted accessibility fragment refuses ${name}`, () => {
    assert.deepEqual(unexplained(describedBy, targets), ['input#points']);
  });
}

test('emitted accessibility fragment does not require an error on a valid field', () => {
  assert.deepEqual(unexplained(null, {}, 'false'), []);
});
