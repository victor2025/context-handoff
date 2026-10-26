#!/usr/bin/env node
// client/cli.js — context-handoff client
//
// Commands:
//   handoff health  [--server URL]
//   handoff save    [--server URL] [--ttl 1800] [--title T] [--metadata-json '{...}'] [FILE | -]
//   handoff load    --id UUID --token TOKEN [--server URL]
//
// Precedence for server URL: CLI flag > $CONTEXT_HANDOFF_SERVER > default.
import fs from 'node:fs';
import {
  generateToken,
  hashToken,
  encryptPayload,
  decryptPayload,
  TOKEN_LENGTH
} from './crypto.js';
import {
  DEFAULT_SERVER,
  resolveServer,
  health as apiHealth,
  saveContext,
  readCiphertext,
  readPlaintext
} from './handoff.js';

function parseArgs(argv) {
  const args = { _: [], flags: {} };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a.startsWith('--')) {
      const key = a.slice(2);
      const next = argv[i + 1];
      if (next === undefined || next.startsWith('--')) {
        args.flags[key] = true;
      } else {
        args.flags[key] = next;
        i += 1;
      }
    } else {
      args._.push(a);
    }
  }
  return args;
}

function readStdin() {
  return new Promise((resolve, reject) => {
    const chunks = [];
    process.stdin.on('data', (c) => chunks.push(c));
    process.stdin.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    process.stdin.on('error', reject);
  });
}

async function readMarkdown(fileArg) {
  if (!fileArg || fileArg === '-') return readStdin();
  return fs.readFileSync(fileArg, 'utf8');
}

// One-sentence recovery prompt, always inside a fenced code block.
export function buildRecoveryPrompt({ server, id, token }) {
  // Deliberately terse: no "server/UUID" preamble, just the two recovery paths.
  const sentence =
    `装了 context-handoff skill 就执行 handoff load --id ${id} --token ${token}；` +
    `没装就执行 curl -sS "${server}/api/contexts/${id}/load?token=${token}"；` +
    `恢复后先核对磁盘文件、服务状态和最新用户要求（有冲突以最新要求和当前文件为准），` +
    `再用简短中文说明你已知道什么、准备做什么，等我确认后再动手，不要马上执行动作。`;
  return '```\n' + sentence + '\n```';
}

export async function runSave(flags, positionals) {
  const server = resolveServer(flags.server);
  const ttl = flags.ttl !== undefined ? Number(flags.ttl) : undefined;
  const title = typeof flags.title === 'string' ? flags.title : 'handoff context';
  let metadata = {};
  if (typeof flags['metadata-json'] === 'string') {
    try { metadata = JSON.parse(flags['metadata-json']); }
    catch { throw new Error('--metadata-json must be valid JSON'); }
  }

  const markdown = await readMarkdown(positionals[0]);
  if (!markdown.trim()) throw new Error('context markdown is empty');

  const token = generateToken();              // local, uppercase
  const tokenHash = hashToken(token);         // upper(trim(token))
  const payload = encryptPayload(markdown, token); // local AES-256-GCM

  const rec = await saveContext(server, {
    payload, tokenHash, ttl, title, format: 'markdown', metadata
  });

  const lines = [];
  lines.push('✅ context saved (encrypted locally; server only stores ciphertext)');
  lines.push(`Server:     ${server}`);
  lines.push(`UUID:       ${rec.id}`);
  lines.push(`TTL:        ${rec.ttl}s`);
  lines.push(`Expires at: ${new Date(rec.expiresAt).toISOString()}`);
  lines.push(`TOKEN:      ${token}   (show UPPERCASE; verification is case-insensitive)`);
  lines.push('');
  lines.push('Recovery prompt — send this whole block to the receiving Agent session:');
  lines.push(buildRecoveryPrompt({
    server, id: rec.id, token, ttlSeconds: rec.ttl, expiresAt: rec.expiresAt
  }));
  return lines.join('\n');
}

export async function runLoad(flags) {
  const server = resolveServer(flags.server);
  const id = flags.id;
  const token = flags.token;
  if (!id || !/^[0-9a-f-]{36}$/i.test(String(id))) throw new Error('--id must be a UUID');
  if (!token || typeof token !== 'string' || !token.trim()) throw new Error('--token is required');

  // Preferred path: server verifies hash + decrypts ephemerally.
  try {
    const rec = await readPlaintext(server, id, token);
    if (rec.plaintext) return rec.plaintext;
  } catch (err) {
    // Legacy/incompatible server fallback: fetch ciphertext and decrypt locally.
    if (err.status !== 404 && err.code !== 'NOT_FOUND_ROUTE') throw err;
  }

  // Fallback: ciphertext endpoint + local decryption (compat with old servers).
  const rec = await readCiphertext(server, id);
  if (!rec.payload) throw new Error('server returned no ciphertext payload');
  return decryptPayload(rec.payload, token);
}

async function main() {
  const { flags, _ } = parseArgs(process.argv.slice(2));
  const command = _[0];
  if (!command || flags.help || flags.h) {
    process.stderr.write(
      'usage: handoff <health|save|load> [options]\n' +
      `default server: ${DEFAULT_SERVER} (override: --server or $CONTEXT_HANDOFF_SERVER)\n` +
      '  health\n' +
      '  save [--ttl SECONDS] [--title T] [--metadata-json JSON] [FILE|-]\n' +
      '  load --id UUID --token TOKEN\n'
    );
    process.exit(0);
  }

  try {
    if (command === 'health') {
      const h = await apiHealth(resolveServer(flags.server));
      process.stdout.write(JSON.stringify(h, null, 2) + '\n');
    } else if (command === 'save') {
      process.stdout.write((await runSave(flags, _.slice(1))) + '\n');
    } else if (command === 'load') {
      process.stdout.write((await runLoad(flags)) + '\n');
    } else {
      throw new Error(`unknown command: ${command}`);
    }
  } catch (err) {
    process.stderr.write(`error: ${err.message}\n`);
    process.exit(1);
  }
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main();
}

export { TOKEN_LENGTH };
