// test/client.test.js
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  generateToken,
  normalizeToken,
  hashToken,
  encryptPayload,
  encryptPlaintext,
  decryptPayload,
  TOKEN_LENGTH
} from '../client/crypto.js';
import { buildRecoveryPrompt } from '../client/cli.js';

test('generated token has expected length and alphabet', async () => {
  const t = generateToken();
  assert.equal(t.length, TOKEN_LENGTH);
  assert.match(t, /^[A-HJ-NP-TV-Z2-9]+$/);
  assert.equal(t, t.toUpperCase());
});

test('token is shown uppercase; normalize trims/uppercases', () => {
  const t = generateToken();
  assert.equal(normalizeToken(' ' + t.toLowerCase() + '\t'), t);
  assert.throws(() => normalizeToken('   '));
});

test('token hash is case-insensitive on trimmed input', () => {
  const t = generateToken();
  assert.equal(hashToken(t), hashToken(t.toLowerCase()));
  assert.equal(hashToken(t), hashToken(`  ${t}  `));
  assert.match(hashToken(t), /^[0-9a-f]{64}$/);
});

test('ciphertext envelope contains every required field', () => {
  const env = encryptPlaintext('hello', generateToken());
  assert.equal(env.v, 1);
  assert.equal(env.alg, 'AES-256-GCM');
  assert.equal(env.kdf, 'PBKDF2-HMAC-SHA256');
  assert.ok(env.kdfParams.iterations >= 100_000);
  assert.equal(env.kdfParams.keyLength, 32);
  assert.ok(env.salt);
  assert.ok(env.nonce);
  assert.ok(env.ciphertext);
  assert.ok(env.tag);
});

test('ciphertext does not contain plaintext; random salt/nonce per message', () => {
  const token = generateToken();
  const secret = 'UNIQUE-SECRET-MARKER-彩虹猫-9f3a';
  const p1 = encryptPayload(secret, token);
  const p2 = encryptPayload(secret, token);
  assert.ok(!p1.includes(secret));
  assert.notEqual(p1, p2, 'salt/nonce must randomize envelopes');
});

test('correct token decrypts; wrong token does not', () => {
  const token = generateToken();
  const secret = '# Goal\n\ndo things';
  const payload = encryptPayload(secret, token);
  assert.equal(decryptPayload(payload, token), secret);
  assert.equal(decryptPayload(payload, token.toLowerCase()), secret);
  assert.throws(() => decryptPayload(payload, generateToken()));
  assert.throws(() => decryptPayload(payload, token.slice(0, -1) + 'X'));
});

test('tampered ciphertext fails authentication', () => {
  const token = generateToken();
  const env = encryptPlaintext('secret', token);
  env.ciphertext = env.ciphertext.slice(0, -4) + 'AAAA';
  assert.throws(() => decryptPayload(env, token));
});

test('recovery prompt is one sentence in a code block with server/uuid/token', () => {
  const block = buildRecoveryPrompt({
    server: 'http://127.0.0.1:8765',
    id: '11111111-2222-4333-8444-555555555555',
    token: 'ABCDEFGHJKMNPQRSTVWXYZ',
    ttlSeconds: 1800
  });
  assert.ok(block.startsWith('```\n') && block.endsWith('\n```'));
  const inner = block.split('\n').slice(1, -1).join('\n');
  assert.ok(!inner.includes('\n')); // single sentence / single line
  for (const needle of [
    'http://127.0.0.1:8765',
    '11111111-2222-4333-8444-555555555555',
    'ABCDEFGHJKMNPQRSTVWXYZ',
    'curl -sS -X POST',
    '/api/contexts/11111111-2222-4333-8444-555555555555/load',
    '-H "content-type: application/json"',
    '\'{"token":"ABCDEFGHJKMNPQRSTVWXYZ"}\'',
    '核对当前磁盘',
    '最新用户要求',
    '简短中文',
    '等我确认',
    '不要马上继续执行'
  ]) {
    assert.ok(inner.includes(needle), `missing: ${needle}`);
  }
});
