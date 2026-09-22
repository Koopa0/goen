// Drive real server responses in an owned browser; mutation orchestration is
// deliberately outside this oracle so the expected assertions stay unchanged.
import { createCipheriv, createHmac, randomBytes } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { measureAdminRows } from './admin-layout-rows.mjs';

const origin = new URL(process.env.GOEN_URL);
const database = new URL(process.env.GOEN_DATABASE_URL);
if (process.env.GITHUB_ACTIONS !== 'true' || process.env.GOEN_LAYOUT_DISPOSABLE !== '1' ||
    origin.hostname !== '127.0.0.1' || database.hostname !== '127.0.0.1') {
  throw new Error('focused layout requires owned CI and loopback server/database');
}
const emit = (event, target, reason = '') => console.log(JSON.stringify({ event, target, reason }));
class Assertion extends Error {
  constructor(target, reason) { super(reason); this.target = target; }
}
const requireThat = (condition, target, reason) => {
  if (!condition) throw new Assertion(target, reason);
};
const fixture = randomBytes(12).toString('hex');
const staff = `totp-${fixture}@goen.invalid`;
const enrol = `enrol-${fixture}@goen.invalid`;
const staffToken = randomBytes(32).toString('hex');
const enrolToken = randomBytes(32).toString('hex');
const secret = randomBytes(20);
const nonce = randomBytes(12);
const cipher = createCipheriv('aes-256-gcm', Buffer.from(process.env.GOEN_TOTP_KEY, 'hex'), nonce);
const encrypted = Buffer.concat([nonce, cipher.update(secret), cipher.final(), cipher.getAuthTag()]);
const sql = (statement) => execFileSync('psql', [process.env.GOEN_DATABASE_URL, '-X', '-q',
  '-v', 'ON_ERROR_STOP=1', '-v', `staff=${staff}`, '-v', `enrol=${enrol}`,
  '-v', `staff_token=${staffToken}`, '-v', `enrol_token=${enrolToken}`,
  '-v', `secret=${encrypted.toString('hex')}`], { input: statement, stdio: ['pipe', 'pipe', 'pipe'] });
let ws;
let nextID = 0;
const pending = new Map();
const send = (method, params = {}) => new Promise((resolve, reject) => {
  const id = ++nextID;
  const timer = setTimeout(() => { pending.delete(id); reject(new Error(`${method} timed out`)); }, 30000);
  pending.set(id, { resolve, reject, timer });
  ws.send(JSON.stringify({ id, method, params }));
});
const evaluate = async (expression) => {
  const reply = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
  if (reply.exceptionDetails) throw new Error(reply.exceptionDetails.text);
  return reply.result?.value;
};
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const settled = async (path) => {
  for (let i = 0; i < 100; i++) {
    const ready = await evaluate(`document.readyState === 'complete' && location.href === ${JSON.stringify(new URL(path, origin).href)}`);
    if (ready) {
      await evaluate('document.fonts.ready.then(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))))');
      return;
    }
    await delay(100);
  }
  throw new Error(`navigation did not settle: ${path}`);
};
const navigate = async (path, width, token) => {
  await send('Network.setCookie', { name: 'goen_session', value: token, domain: '127.0.0.1', path: '/' });
  await send('Emulation.setDeviceMetricsOverride', { width, height: width === 375 ? 812 : 900, deviceScaleFactor: 1, mobile: width < 768 });
  await send('Page.navigate', { url: new URL(path, origin).href });
  await settled(path);
};
const axeSource = readFileSync(process.env.AXE_SOURCE, 'utf8');
const measure = async (target, enrolling) => {
  emit('run', target);
  const action = enrolling ? '/admin/verify/confirm' : '/admin/verify';
  const got = await evaluate(`(() => {
    const input = document.querySelector('form[action="${action}"] input[name="code"]');
    const rect = input?.getBoundingClientRect();
    const qr = document.querySelector('.goen-twofa__qr');
    return { input: !!rect && rect.width > 0 && rect.height >= 44,
      secret: !!document.querySelector('.goen-twofa__secret code'),
      qr: !!qr && qr.complete && qr.naturalWidth > 0,
      overflow: document.body.scrollWidth > document.documentElement.clientWidth };
  })()`);
  requireThat(got.input, target, 'OTP input is absent or not visible');
  requireThat(!got.overflow, target, 'two-factor page scrolls horizontally');
  if (enrolling) {
    requireThat(got.secret, target, 'enrolment secret is absent');
    requireThat(got.qr, target, 'enrolment QR is absent or unloaded');
  }
  await evaluate(axeSource);
  const violations = await evaluate(`axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] } }).then(r => r.violations.filter(v => ['serious', 'critical'].includes(v.impact)).map(v => v.id))`);
  requireThat(Array.isArray(violations) && violations.length === 0, target, `axe serious/critical: ${JSON.stringify(violations)}`);
  emit('pass', target);
};

try {
  sql(`INSERT INTO users (email, role) VALUES (:'staff', 'admin'), (:'enrol', 'admin');
    INSERT INTO staff_totp_credentials (user_id, secret_encrypted, confirmed_at)
      SELECT id, decode(:'secret', 'hex'), now() FROM users WHERE email = :'staff';
    INSERT INTO sessions (token_hash, user_id, expires_at)
      SELECT sha256(:'staff_token'::bytea), id, now() + interval '1 hour' FROM users WHERE email = :'staff';
    INSERT INTO sessions (token_hash, user_id, expires_at)
      SELECT sha256(:'enrol_token'::bytea), id, now() + interval '1 hour' FROM users WHERE email = :'enrol';`);
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)));
  const digest = createHmac('sha1', secret).update(counter).digest();
  const code = String((digest.readUInt32BE(digest[digest.length - 1] & 15) & 0x7fffffff) % 1000000).padStart(6, '0');
  const verified = await fetch(new URL('/admin/verify', origin), { method: 'POST', redirect: 'manual',
    headers: { Cookie: `goen_session=${staffToken}`, 'Sec-Fetch-Site': 'same-origin' }, body: new URLSearchParams({ code }) });
  if (verified.status !== 303 || verified.headers.get('location') !== '/admin') throw new Error('fixture failed real verification POST');
  emit('pass', 'real-verification-post');
  const pages = await (await fetch(`http://127.0.0.1:${process.env.CDP_PORT}/json/list`)).json();
  ws = new WebSocket(pages.find(page => page.type === 'page').webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject; });
  ws.onmessage = event => {
    const reply = JSON.parse(event.data);
    const request = pending.get(reply.id);
    if (!request) return;
    pending.delete(reply.id); clearTimeout(request.timer);
    if (reply.error) request.reject(new Error(JSON.stringify(reply.error)));
    else request.resolve(reply.result);
  };
  await send('Page.enable'); await send('Network.enable');
  for (const width of [375, 1440]) {
    await navigate('/admin/verify', width, staffToken);
    await measure(`challenge-${width}`, false);
  }
  for (const width of [375, 1440]) {
    await navigate('/admin/verify', width, enrolToken);
    const submitted = await evaluate(`(() => { const form = document.querySelector('form[action="/admin/verify/enrol"]'); if (!form) return false; form.requestSubmit(); return true; })()`);
    if (!submitted) throw new Error('enrolment start form is absent');
    await settled('/admin/verify/enrol');
    await measure(`enrol-${width}`, true);
  }
  try {
    await measureAdminRows([375, 1440].map(width => ({ width, label: `admin-${width}` })), async row => {
      emit('run', row.label);
      await navigate('/admin', row.width, staffToken);
      const admin = await evaluate(`!!document.querySelector('.goen-admin') && location.pathname === '/admin'`);
      requireThat(admin, row.label, 'verified admin route did not render');
      emit('pass', row.label);
    });
  } catch (error) {
    if (error.message === 'admin coverage missing required rows') throw new Assertion('admin-coverage', error.message);
    throw error;
  }
  emit('pass', 'focused-layout');
} catch (error) {
  emit(error instanceof Assertion ? 'assert-fail' : 'error', error.target || 'setup', error.message);
  process.exitCode = error instanceof Assertion ? 1 : 2;
} finally {
  for (const request of pending.values()) clearTimeout(request.timer);
  ws?.close();
  try { sql(`DELETE FROM users WHERE email IN (:'staff', :'enrol');`); }
  catch { emit('error', 'cleanup', 'could not remove owned fixture users'); process.exitCode = 2; }
}
