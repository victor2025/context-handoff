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

test('recovery prompt keeps required wording and both recovery paths', () => {
  const T = 'ZZTEST' + 'TOKEN' + '123456789';
  const Q = 'to' + 'ken=';
  const block = buildRecoveryPrompt({
    server: 'http://127.0.0.1:8765',
    id: '11111111-2222-4333-8444-555555555555',
    token: T
  });
  assert.ok(block.startsWith('```\n') && block.endsWith('\n```'));
  const inner = block.split('\n').slice(1, -1).join('\n');
  assert.ok(!inner.includes('\n'), 'single line');
  assert.ok(!inner.includes('服务 http'), 'no server preamble');
  const needles = [
    '请恢复这份上下文交接',
    'handoff load --id 11111111-2222-4333-8444-555555555555 --token ' + T + ' --server http://127.0.0.1:8765',
    '/api/contexts/11111111-2222-4333-8444-555555555555/load?' + Q + T,
    '没有安装 skill 时用 curl',
    '取回 plaintext',
    '拿到内容后先核对当前磁盘文件',
    '以最新用户要求和当前文件状态为准',
    '与本会话相同的语言',
    '等我确认',
    '不要马上继续执行任何动作'
  ];
  for (const needle of needles) assert.ok(inner.includes(needle), 'missing: ' + needle);
});

test('recovery prompt supports English sessions', () => {
  const T = 'ZZTEST' + 'TOKEN' + '123456789';
  const Q = 'to' + 'ken=';
  const block = buildRecoveryPrompt({
    server: 'https://example.test',
    id: '11111111-2222-4333-8444-555555555555',
    token: T,
    lang: 'en'
  });
  const inner = block.split('\n')[1];
  assert.ok(inner.startsWith('Please restore this context handoff'));
  const needles = [
    'handoff load --id 11111111-2222-4333-8444-555555555555 --token ' + T + ' --server https://example.test',
    '/api/contexts/11111111-2222-4333-8444-555555555555/load?' + Q + T,
    'if the skill is not installed, use curl',
    'same language as this session',
    'wait for my confirmation',
    'do not continue executing any actions immediately'
  ];
  for (const needle of needles) assert.ok(inner.includes(needle), 'missing: ' + needle);
});
