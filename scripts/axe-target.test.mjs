import assert from 'node:assert/strict';
import test from 'node:test';
import { gatesAccessibility } from './axe-target.mjs';

test('new WCAG criteria fail at serious and critical impact', () => {
  assert.equal(gatesAccessibility({ impact: 'serious', tags: ['wcag22aa', 'wcag258'] }), true);
  assert.equal(gatesAccessibility({ impact: 'critical', tags: ['wcag21aa'] }), true);
});

test('best-practice findings remain advisory even at critical impact', () => {
  assert.equal(gatesAccessibility({ impact: 'critical', tags: ['best-practice'] }), false);
});

test('minor and moderate WCAG findings remain advisory', () => {
  for (const impact of ['minor', 'moderate']) {
    assert.equal(gatesAccessibility({ impact, tags: ['wcag2a'] }), false);
  }
});
