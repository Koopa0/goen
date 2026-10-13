import assert from 'node:assert/strict';
import test from 'node:test';
import { AXE_OPTIONS, WCAG_TAGS, gatesAccessibility, wcagRuleExclusion } from './wcag-gate.mjs';
import './layout-accessibility.test.mjs';

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

test('the AA policy and axe run select every WCAG version through 2.2', () => {
  const tags = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'];
  assert.deepEqual(WCAG_TAGS, tags);
  assert.equal(AXE_OPTIONS.runOnly.type, 'tag');
  assert.deepEqual(AXE_OPTIONS.runOnly.values, [...tags, 'best-practice']);
});

test('WCAG tag selection includes target-size but excludes experimental and deprecated rules', () => {
  assert.equal(wcagRuleExclusion({ tags: ['wcag22aa', 'wcag258'], enabled: false }), null);
  for (const tag of ['experimental', 'deprecated']) {
    assert.equal(wcagRuleExclusion({ tags: ['wcag21aa', tag], enabled: true }), tag);
  }
});
