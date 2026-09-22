// An owned disposable database may seed an authenticator; authorization still
// comes from the application's ordinary POST /admin/verify.
import { createCipheriv, createHmac, randomBytes } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';

if (!process.env.GOEN_TOTP_KEY) process.exit(0);
const origin = new URL(process.env.GOEN_URL || 'http://127.0.0.1:9700');
const database = new URL(process.env.GOEN_DATABASE_URL);
if (process.env.GOEN_LAYOUT_DISPOSABLE !== '1' ||
    !['127.0.0.1', 'localhost', '[::1]'].includes(origin.hostname) ||
    !['127.0.0.1', 'localhost', '[::1]'].includes(database.hostname)) {
  throw new Error('TOTP layout fixtures require GOEN_LAYOUT_DISPOSABLE=1 and loopback server/database');
}
const encoded = process.env.GOEN_TOTP_KEY.trim();
const key = /^[0-9a-fA-F]{64}$/.test(encoded) ? Buffer.from(encoded, 'hex') : Buffer.from(encoded, 'base64');
if (key.length !== 32) throw new Error('layout fixture requires the server AES-256 TOTP key');
const secret = randomBytes(20);
const nonce = randomBytes(12);
const cipher = createCipheriv('aes-256-gcm', key, nonce);
const encrypted = Buffer.concat([nonce, cipher.update(secret), cipher.final(), cipher.getAuthTag()]);
const enrolToken = randomBytes(32).toString('hex');
const sql = `
INSERT INTO staff_totp_credentials (user_id, secret_encrypted, confirmed_at)
SELECT id, decode(:'secret', 'hex'), now() FROM users WHERE email = 'layout-check@goen.invalid'
ON CONFLICT (user_id) DO UPDATE SET secret_encrypted = EXCLUDED.secret_encrypted,
confirmed_at = EXCLUDED.confirmed_at, last_step = NULL;
INSERT INTO users (email, role) VALUES ('layout-enrol@goen.invalid', 'admin')
ON CONFLICT (lower(email)) DO NOTHING;
DELETE FROM staff_totp_credentials WHERE user_id = (SELECT id FROM users WHERE email = 'layout-enrol@goen.invalid');
INSERT INTO sessions (token_hash, user_id, expires_at)
SELECT sha256(:'token'::bytea), id, now() + interval '1 hour' FROM users WHERE email = 'layout-enrol@goen.invalid';
`;
execFileSync('psql', [process.env.GOEN_DATABASE_URL, '-X', '-q', '-v', 'ON_ERROR_STOP=1',
  '-v', `secret=${encrypted.toString('hex')}`, '-v', `token=${enrolToken}`], { input: sql, stdio: ['pipe', 'pipe', 'pipe'] });
writeFileSync('.layout-chrome/enrol-token', enrolToken, { mode: 0o600 });
const counter = Buffer.alloc(8);
counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)));
const digest = createHmac('sha1', secret).update(counter).digest();
const offset = digest[digest.length - 1] & 15;
const code = String((digest.readUInt32BE(offset) & 0x7fffffff) % 1000000).padStart(6, '0');
const token = readFileSync('.layout-chrome/admin-token', 'utf8').trim();
const response = await fetch(new URL('/admin/verify', origin), {
  method: 'POST', redirect: 'manual',
  headers: { Cookie: `goen_session=${token}`, 'Sec-Fetch-Site': 'same-origin' },
  body: new URLSearchParams({ code }),
});
if (response.status !== 303 || response.headers.get('location') !== '/admin') {
  throw new Error(`TOTP fixture verification failed: ${response.status} ${response.headers.get('location')}`);
}
const admin = await fetch(new URL('/admin', origin), {
  redirect: 'manual', headers: { Cookie: `goen_session=${token}` },
});
if (admin.status !== 200) throw new Error(`verified staff cannot open /admin: ${admin.status}`);
console.log('TOTP fixture: real verify POST established staff step-up');
