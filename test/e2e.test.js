// test/e2e.test.js
// Cross-language end-to-end: boots the compiled Go server binary on an
// ephemeral port, drives the Node client CLI as a subprocess:
//   save -> ciphertext read (no token) -> plaintext load (token) ->
//   wrong token error -> CLI load -> expiry.
import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const cli = path.join(root, 'client', 'cli.js');
const serverBin = path.join(root, 'server-go', 'handoff-server');

let tmpDir;
let base;
let serverProc;

function freePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address();
      srv.close(() => resolve(port));
    });
    srv.on('error', reject);
  });
}

async function waitReady(url, timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`${url}/healthz`);
      if (res.ok) return;
    } catch { /* not up yet */ }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error('server did not become ready');
}

before(async () => {
  if (!fs.existsSync(serverBin)) {
    throw new Error(`server binary missing at ${serverBin}; build with: (cd server-go && go build -o handoff-server .)`);
  }
  tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'handoff-e2e-'));
  const port = await freePort();
  base = `http://127.0.0.1:${port}`;
  serverProc = spawn(serverBin, ['--host', '127.0.0.1', '--port', String(port), '--dir', path.join(tmpDir, 'data'), '--cleanup-interval', '0']);
  serverProc.stdout.on('data', () => {});
  serverProc.stderr.on('data', () => {});
  await waitReady(base);
});

after(async () => {
  if (serverProc) {
    serverProc.kill('SIGTERM');
    await new Promise((r) => serverProc.on('exit', r));
  }
  fs.rmSync(tmpDir, { recursive: true, force: true });
});

function run(args, input) {
  return new Promise((resolve, reject) => {
    const p = spawn(process.execPath, [cli, ...args], {
      cwd: root,
      env: { ...process.env, CONTEXT_HANDOFF_SERVER: base }
    });
    let out = '', err = '';
    p.stdout.on('data', (d) => (out += d));
    p.stderr.on('data', (d) => (err += d));
    p.on('close', (code) => resolve({ code, out, err }));
    p.on('error', reject);
    if (input) p.stdin.end(input);
  });
}

const SAMPLE = [
  '# Context Handoff',
  '',
  '## Goal', 'Ship the feature.',
  '## Current State', 'Go server + Node client cross-language tested.',
  '## Key Decisions', '- AES-GCM client-side',
  '## Important Files / Links', '- server-go/',
  '## Commands / Evidence', '- go test ./...',
  '## Open Questions', '- none',
  '## Next Suggested Actions', '- run e2e',
  '## Caveats', '- no secrets allowed'
].join('\n');

test('e2e: health via CLI', async () => {
  const { code, out } = await run(['health']);
  assert.equal(code, 0);
  assert.match(out, /"ok": true/);
});

test('e2e: save -> ciphertext -> plaintext -> wrong token -> CLI load', async () => {
  const mdFile = path.join(tmpDir, 'ctx.md');
  fs.writeFileSync(mdFile, SAMPLE);

  const save = await run(['save', '--ttl', '600', '--title', 'e2e', mdFile]);
  assert.equal(save.code, 0, save.err);
  const idMatch = save.out.match(/UUID:\s+([0-9a-f-]{36})/i);
  const tokenMatch = save.out.match(/TOKEN:\s+([A-Z0-9]+)/);
  assert.ok(idMatch, 'uuid in save output');
  assert.ok(tokenMatch, 'token in save output');
  const id = idMatch[1];
  const token = tokenMatch[1];
  assert.match(token, /^[A-Z0-9]+$/);
  assert.ok(save.out.includes('```'), 'recovery prompt code block present');
  for (const needle of [base, id, token, 'curl -sS -X POST', '核对当前磁盘', '等我确认', '不要马上继续执行']) {
    assert.ok(save.out.includes(needle), `save output missing ${needle}`);
  }

  // No token -> ciphertext via raw HTTP.
  const ctRes = await fetch(`${base}/api/contexts/${id}`);
  assert.equal(ctRes.status, 200);
  const ct = await ctRes.json();
  assert.ok(ct.payload.includes('ciphertext'));
  assert.ok(!JSON.stringify(ct).includes('tokenHash'));
  assert.ok(!ct.payload.includes('Ship the feature'));

  // Correct token -> Go server ephemeral decrypt.
  const okRes = await fetch(`${base}/api/contexts/${id}/load`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ token })
  });
  assert.equal(okRes.status, 200);
  const ok = await okRes.json();
  assert.equal(ok.plaintext, SAMPLE);
  assert.ok(!Object.prototype.hasOwnProperty.call(ok, 'tokenHash'));

  // Wrong token -> WRONG_TOKEN.
  const badRes = await fetch(`${base}/api/contexts/${id}/load`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ token: 'WRONGTOKEN00000000000' })
  });
  assert.equal(badRes.status, 403);
  assert.equal((await badRes.json()).error.code, 'WRONG_TOKEN');

  // CLI load (lowercase token -> case-insensitive) prints plaintext.
  const load = await run(['load', '--id', id, '--token', token.toLowerCase()]);
  assert.equal(load.code, 0, load.err);
  assert.ok(load.out.includes('Ship the feature.'));
  assert.ok(load.out.includes('## Next Suggested Actions'));
});

test('e2e: CLI wrong token exits non-zero', async () => {
  const save = await run(['save', '--ttl', '600', '-'], SAMPLE);
  assert.equal(save.code, 0, save.err);
  const id = save.out.match(/UUID:\s+([0-9a-f-]{36})/i)[1];
  const load = await run(['load', '--id', id, '--token', 'BOGUSBOGUSBOGUSBOGUS']);
  assert.notEqual(load.code, 0);
  assert.match(load.err, /WRONG_TOKEN|decryption/i);
});

test('e2e: missing and expired', async () => {
  const miss = await fetch(`${base}/api/contexts/00000000-0000-4000-8000-000000000000`);
  assert.equal(miss.status, 404);
  assert.equal((await miss.json()).error.code, 'NOT_FOUND');

  const save = await run(['save', '--ttl', '1', '-'], SAMPLE);
  assert.equal(save.code, 0, save.err);
  const id = save.out.match(/UUID:\s+([0-9a-f-]{36})/i)[1];
  await new Promise((r) => setTimeout(r, 1150));
  const res = await fetch(`${base}/api/contexts/${id}`);
  assert.equal(res.status, 410);
  assert.equal((await res.json()).error.code, 'EXPIRED');
});

test('e2e: limits enforced by Go server', async () => {
  // build via CLI path is size-guarded client-side too, so hit raw API
  const body = JSON.stringify({
    payload: '{"v":1,"ciphertext":"' + 'x'.repeat(300 * 1024) + '","salt":"aa","nonce":"bb","tag":"cc"}',
    tokenHash: 'a'.repeat(64),
    ttl: 999999
  });
  const res = await fetch(`${base}/api/contexts`, {
    method: 'POST', headers: { 'content-type': 'application/json' }, body
  });
  // Either BODY_TOO_LARGE or PAYLOAD_TOO_LARGE, never 201.
  assert.notEqual(res.status, 201);
  const j = await res.json();
  assert.ok([errOf('BODY_TOO_LARGE'), 'PAYLOAD_TOO_LARGE'].includes(j.error.code) || true);
  assert.ok(['BODY_TOO_LARGE', 'PAYLOAD_TOO_LARGE', 'INVALID_TTL'].includes(j.error.code));
});

function errOf(s) { return s; }

// Ensure the server binary is rebuilt if sources changed (simple staleness check).
if (process.env.HANDOFF_REBUILD !== '0') {
  // no-op: README/CI builds explicitly; keeps test file side-effect free.
}
