// A rejected staff cookie must fail rather than measure the guest home page.
// Run against the layout server and Chrome prepared by make check-layout.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import test from 'node:test';

test('staff reflow refuses an unauthenticated home page', () => {
  const result = spawnSync(process.execPath, [
    'scripts/reflow-check.mjs', '--staff', '/@320@en@text200',
  ], {
    env: { ...process.env, ADMIN_TOKEN: 'invalid-staff-session', REFLOW_EVIDENCE_DIR: '' },
    encoding: 'utf8',
    timeout: 120000,
  });
  assert.equal(result.status, 1, result.stdout + result.stderr);
  assert.match(result.stderr, /staff session was not accepted/);
});
