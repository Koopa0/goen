import { deliveryRefusalTarget } from './screenshot-delivery.mjs';

// The names scripts/check-layout.sql writes that are not session tokens, so an
// entry cannot copy a token into the artifact.
const fixtureName = /^(\w+_SLUG|\w+_ORDER|PICKUP_SHIP|CUSTOMER_ID|LAYOUT_SERIAL)$/;

export function parseEntry(text, env) {
  const [path, width, ...flags] = text.split('@');
  const entry = { text, path, width: Number(width), lang: 'zh-Hant', text200: false, forced: false, member: false, deliveryRefused: false };
  if (!path.startsWith('/') || !(entry.width >= 200 && entry.width <= 4000)) {
    throw new Error('want path@width, e.g. /deals@375');
  }
  for (const flag of flags) {
    if (flag === 'en') entry.lang = 'en';
    else if (flag === 'zh') entry.lang = 'zh-Hant';
    else if (flag === 'text200') entry.text200 = true;
    else if (flag === 'forced') entry.forced = true;
    else if (flag === 'member') entry.member = true;
    else if (flag === 'delivery-refused') entry.deliveryRefused = true;
    else throw new Error(`unknown flag ${JSON.stringify(flag)}`);
  }
  entry.path = path.replace(/\{(\w+)\}/g, (_, name) => {
    if (!fixtureName.test(name)) throw new Error(`{${name}} is not a fixture name`);
    if (!env[name]) throw new Error(`${name} is not set`);
    return encodeURIComponent(env[name]);
  });
  if (entry.deliveryRefused) {
    if (entry.member) throw new Error('delivery-refused requires the staff visitor');
    deliveryRefusalTarget(entry.path, env.PLACED_ORDER);
  }
  return entry;
}

// The cookie each kind of visitor carries, and the token it needs.
const visitors = [
  { prefix: '/admin', cookie: 'goen_session', token: 'ADMIN_TOKEN' },
  { prefix: '/account', cookie: 'goen_session', token: 'CUST_TOKEN' },
  { prefix: '/cart', cookie: 'goen_cart', token: 'CART_TOKEN' },
  { prefix: '/checkout', cookie: 'goen_cart', token: 'CART_TOKEN' },
  { prefix: '/orders', cookie: 'goen_placed', token: 'PLACED_TOKEN' },
];

// member makes the visitor the signed-in customer on any path; the capture must
// then stay on the requested path itself.
export function screenshotVisitor(entry) {
  const byPath = visitors.find((v) => entry.path === v.prefix || entry.path.startsWith(v.prefix + '/') || entry.path.startsWith(v.prefix + '?'));
  if (!entry.member || byPath?.prefix === '/account') return byPath;
  return { prefix: entry.path.split(/[?#]/)[0], cookie: 'goen_session', token: 'CUST_TOKEN' };
}
