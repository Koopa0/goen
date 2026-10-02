import { test } from 'node:test';
import assert from 'node:assert/strict';
import { contrastRatio } from './control-boundary.mjs';

test('contrast uses linear sRGB luminance and is symmetric', () => {
  assert.equal(contrastRatio([0, 0, 0], [255, 255, 255]), 21);
  assert.equal(contrastRatio([255, 255, 255], [0, 0, 0]), 21);
  assert.equal(contrastRatio([212, 212, 216], [212, 212, 216]), 1);
  assert.ok(Math.abs(contrastRatio([212, 212, 216], [255, 255, 255]) - 1.47800) < 0.001);
  assert.ok(contrastRatio([113, 113, 122], [255, 255, 255]) > 3);
});
