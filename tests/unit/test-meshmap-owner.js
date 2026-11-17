/* Fork-local (meshmap owner): unit tests for public/meshmap-owner.js, loaded
 * into a vm sandbox with the real escapeHtml from app.js. */
'use strict';
const vm = require('vm');
const fs = require('fs');
const path = require('path');
const assert = require('assert');

const ROOT = path.resolve(__dirname, '..', '..');
let passed = 0, failed = 0;
const pending = [];
function test(name, fn) {
  const p = Promise.resolve().then(fn).then(
    () => { passed++; console.log('  ok   ' + name); },
    (e) => { failed++; console.log('  FAIL ' + name + ': ' + e.message); });
  pending.push(p);
  return p;
}
const src = (f) => fs.readFileSync(path.join(ROOT, f), 'utf8');
const PK = 'ef'.repeat(32);
const SOURCE = 'https://meshmap.radio-actief.be';

function loadEscapeHtml() {
  const m = src('public/app.js').match(/function escapeHtml\(s\) \{[\s\S]*?\n\}/);
  assert(m, 'escapeHtml not found in app.js');
  return vm.runInNewContext('(' + m[0].replace(/^function escapeHtml/, 'function') + ')');
}

function load(answer) {
  const calls = [];
  const CSApi = {
    fetch(p) {
      calls.push(p);
      if (answer instanceof Error) return Promise.reject(answer);
      return Promise.resolve({ ok: answer.ok !== false, json: () => Promise.resolve(answer.body) });
    },
  };
  const ctx = { CSApi, console, escapeHtml: loadEscapeHtml() };
  ctx.window = ctx;
  vm.createContext(ctx);
  vm.runInContext(src('public/meshmap-owner.js'), ctx);
  return { M: ctx.CSMeshmapOwner, calls };
}
const owners = (list) => ({ body: { owners: list, fetchedAt: '2026-11-17T10:00:00Z', source: SOURCE } });

test('row for a known pubkey: label, @name and a safe meshmap link', async () => {
  const l = load(owners([{ pubkey: PK, discordName: 'erwinfiten' }]));
  await l.M.ready();
  const h = l.M.rowHtml({ public_key: PK, lat: 51.1378, lon: 5.5902 });
  assert(h.includes('Owner (Discord)'), h);
  assert(h.includes('@erwinfiten'), h);
  assert(h.includes('view on meshmap'), h);
  assert(h.includes('target="_blank"') && h.includes('rel="noopener noreferrer"'), h);
  assert(h.includes('<dt>') && h.includes('<dd>'), 'row must fit the repeater-info <dl>: ' + h);
});

test('owner name is escaped', async () => {
  const l = load(owners([{ pubkey: PK, discordName: '<img src=x onerror=1>' }]));
  await l.M.ready();
  const h = l.M.rowHtml({ public_key: PK });
  assert(!h.includes('<img') && h.includes('&lt;img'), h);
});

test('unknown pubkey gives no row; an uppercase node key still matches', async () => {
  const l = load(owners([{ pubkey: PK, discordName: 'erwinfiten' }]));
  await l.M.ready();
  assert.strictEqual(l.M.rowHtml({ public_key: 'cd'.repeat(32) }), '');
  assert(l.M.rowHtml({ public_key: PK.toUpperCase() }).includes('@erwinfiten'));
});

test('link uses the source from the API, with and without a position', () => {
  const T = load(owners([])).M._test;
  assert.strictEqual(T.linkFor(SOURCE, { public_key: PK, lat: 51.1378, lon: 5.5902 }),
    SOURCE + '/?zoom=15&lat=51.1378&lon=5.5902&node=' + PK);
  assert.strictEqual(T.linkFor(SOURCE, { public_key: PK }), SOURCE + '/?node=' + PK);
  assert.strictEqual(T.linkFor(SOURCE, { public_key: PK, lat: 0, lon: 0 }), SOURCE + '/?node=' + PK,
    '0,0 is "no position" in CoreScope');
});

test('a failed fetch resolves and shows no row; ready() fetches once', async () => {
  const l = load(new Error('offline'));
  await l.M.ready();
  await l.M.ready();
  assert.strictEqual(l.calls.length, 1);
  assert.strictEqual(l.calls[0], '/api/meshmap/owners');
  assert.strictEqual(l.M.rowHtml({ public_key: PK }), '');
  const off = load({ ok: false, body: {} });
  await off.M.ready();
  assert.strictEqual(off.M.rowHtml({ public_key: PK }), '');
});

Promise.all(pending).then(() => {
  console.log(passed + ' passed, ' + failed + ' failed');
  if (failed) process.exit(1);
});
