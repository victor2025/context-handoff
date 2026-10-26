---
name: context-handoff
description: Securely hand off structured task context between Agent sessions. Save a markdown summary locally encrypted (AES-256-GCM) with a short TOKEN to a handoff server, get a UUID+TOKEN recovery block; the receiving session restores full context. Use when switching sessions/agents/machines mid-task, handing work to another agent, or resuming after context expiry.
---

# context-handoff

Secure, ephemeral context hand-off between Agent sessions.

- The **current session** writes a structured markdown summary and runs `handoff save`.
- Encryption happens **on the client**: AES-256-GCM with a key derived from a locally generated short TOKEN via PBKDF2-HMAC-SHA256 (random salt + nonce every time).
- The server stores **only ciphertext, metadata, expiry, and a SHA-256 hash of `upper(trim(TOKEN))`** — never the TOKEN plaintext, never the context plaintext.
- The **receiving session** runs `handoff load --id UUID --token TOKEN` (or pastes the one-sentence recovery prompt) and gets the plaintext summary back.

Requirements: this skill needs only Node.js ≥ 20 (built-in `node:crypto` + global `fetch`; no npm install). The relay server is a separate, dependency-free **Go binary** (`server-go/`) — deploying the server and installing this skill are two independent steps.

## When to use

- You are about to run out of context, switch sessions, or hand a task to another agent / machine.
- A fresh session needs the decisions, file pointers, commands already run, and next steps — without re-exploring everything.
- The context is sensitive enough that the relay server must not be able to read it.

Do **not** use it as durable documentation or a secrets store. Records are short-lived (default 30 min, max 1 day) and deleted on expiry.

## Server address resolution (precedence)

1. `--server http://host:port` CLI flag
2. `CONTEXT_HANDOFF_SERVER` environment variable
3. Default: `https://ctx.victor2022.dpdns.org` (public relay; alternate `https://ctx.victor2022.cc.cd`)

The public relay works out of the box and is zero-knowledge by design, but **self-hosting is recommended**: deploy `server-go/` yourself and point `--server` / `CONTEXT_HANDOFF_SERVER` at your own instance (full control over retention, uptime and exposure).

## Commands

Health check:

```bash
handoff health
handoff health --server https://ctx.victor2022.dpdns.org
handoff health --server http://127.0.0.1:8765   # self-hosted
```

Save (file argument, or `-`/omitted for stdin):

```bash
handoff save context.md
cat context.md | handoff save --ttl 1800 --title "login refactor handoff" -
handoff save context.md --metadata-json '{"project":"api","owner":"me"}'
```

Load in the receiving session:

```bash
handoff load --id 11111111-2222-4333-8444-555555555555 --token ABCDEFGHJKMNPQRSTVWXYZ
```

`save` prints the UUID, expiry, TTL, server address, the TOKEN, and a **recovery prompt code block** you hand to the next session. The TOKEN is shown UPPERCASE; verification is case-insensitive, so lowercase input also works.

If the server is an older build that returns ciphertext only, the client automatically falls back to **local decryption**.

## Summary template

Copy `templates/context-template.md`. Every handoff summary SHOULD contain these sections:

- **Goal** — the task / desired outcome
- **Current State** — what exists now, what works / is broken, progress
- **Key Decisions** — decisions made + rationale
- **Important Files / Links** — repo-relative paths, URLs, endpoints
- **Commands / Evidence** — exact commands run and observed results / proof
- **Open Questions** — unknowns to resolve or ask
- **Next Suggested Actions** — concrete first steps, priority order
- **Caveats** — constraints, gotchas, assumptions, environment quirks

Keep it compact and factual; the next session will verify against the live disk anyway.

## Output format of `save`

```
✅ context saved (encrypted locally; server only stores ciphertext)
Server:     https://ctx.victor2022.dpdns.org
UUID:       <uuid>
TTL:        1800s
Expires at: <ISO-8601>
TOKEN:      <UPPERCASE-TOKEN>

Recovery prompt — send this whole block to the receiving Agent session:
```
followed by a fenced code block containing exactly **one sentence** that includes:

- the `handoff load` command with UUID, TOKEN and `--server` (skill installed),
- when the skill is **not** installed, a **ready-to-run `curl` command** with the token in the query string:
  `curl -sS "<server>/api/contexts/<id>/load?token=<TOKEN>"`,
- an instruction to verify current disk files, service state, and the latest user request,
- the precedence rule: latest user request and current file state win on conflict,
- an instruction to reply briefly **in the same language as the current session** about what is known and what will be done,
- an instruction to **wait for user confirmation and not immediately start acting**.

### Recovery prompt language

The recovery prompt **must be written in the same language as the current session**.

- `handoff save` emits Chinese by default.
- For an English session: `handoff save --lang en context.md` (or `CONTEXT_HANDOFF_LANG=en`).
- For any other session language: translate the prompt yourself and keep **every** required element — the skill command with UUID/TOKEN/`--server`, the ready-to-run `curl`, the verification step, and the confirmation requirement.
Example (one sentence, one line, zh):

```
请恢复这份上下文交接：已安装 context-handoff skill 时执行 handoff load --id {{UUID}} --token {{TOKEN}} --server {{SERVER}}； 没有安装 skill 时用 curl -sS "{{SERVER}}/api/contexts/{{UUID}}/load?token={{TOKEN}}" 取回 plaintext； 拿到内容后先核对当前磁盘文件、服务状态和最新用户要求（如有冲突，以最新用户要求和当前文件状态为准）， 再用与本会话相同的语言简要说明你已知道什么、准备做什么，并等我确认，不要马上继续执行任何动作。
```

`load` prints only the restored markdown plaintext on stdout (machine/human readable). Errors go to stderr with a non-zero exit code (`NOT_FOUND`, `EXPIRED`, `WRONG_TOKEN`, `DECRYPTION_FAILED`, …).

## Receiving-session workflow

1. Run the `handoff load` command (or the raw `GET …/load?token=…` if the skill is not installed).
2. Read the summary, then **verify against live reality**: inspect the referenced files, check service/process state, and read the latest user message.
3. If the summary conflicts with the live disk or the latest user request, **the latest user request and current file state win**.
4. Reply to the user in short Chinese: what you now know and what you plan to do.
5. **Wait for explicit user confirmation** before making changes or running mutating commands.

## Troubleshooting

| Symptom | Cause / fix |
| --- | --- |
| `fetch failed` / connection error | Server unreachable or wrong address. Check `handoff health`; if the public relay is down, retry later or use the alternate `--server https://ctx.victor2022.cc.cd`. For guaranteed availability, self-host `server-go/`. |
| `NOT_FOUND` | UUID wrong, never existed, or already cleaned up. Ask for a fresh handoff. |
| `EXPIRED` | TTL elapsed (default 30 min, max 1 day). Ask the sender to save again with a longer TTL. |
| `WRONG_TOKEN` | TOKEN does not match (case-insensitive; surrounding spaces are trimmed). Re-copy the full TOKEN. |
| `DECRYPTION_FAILED` | Token hash matched but envelope decryption failed — truncated/corrupted payload or mismatched envelope. Re-save. |
| `BODY_TOO_LARGE` / `METADATA_TOO_LARGE` | Summary > 256 KiB or metadata > 8 KiB. Trim the summary; link to files instead of embedding. |
| `INVALID_TTL` | TTL must be a positive integer ≤ 86400 seconds. |
| `node: command not found` | Install Node.js ≥ 20. |

## Security notes

- **Never write secrets into the summary**: no passwords, private keys, tokens, cookies, session IDs, `appSecret`, API keys, `.env` contents, or credential files. Encryption protects data in transit/at rest on the relay; it does not make a secret safe to paste into a chat prompt once decrypted.
- The TOKEN travels in the URL query string for `load`; URLs may be captured by proxy/access logs. Use HTTPS (the public endpoint is HTTPS), keep TTL short, and self-host when that matters.
- The TOKEN is the only decryption capability. It is generated locally, shown UPPERCASE, and never stored in plaintext by the server. Share the UUID over the normal channel and the TOKEN through a second channel when possible.
- The server's TOKEN hash (`SHA-256(upper(trim(token)))`) is used **only for verification**, never for key derivation or decryption; API responses never include it.
- Records expire (default 30 min, max 1 day) and are purged; expired IDs return `EXPIRED`.
- Default endpoint is the public relay `https://ctx.victor2022.dpdns.org`; self-hosting is recommended for sensitive or large contexts. No relay — public or self-hosted — ever sees your plaintext or TOKEN.
- Run a self-hosted server bound to `127.0.0.1` for local use. Exposing it requires TLS and an auth boundary you must operate yourself — the protocol alone authenticates only knowledge of the TOKEN.
- The server cannot read your context (zero-knowledge ciphertext), but a malicious server can still reject, delete, or withhold records; verify delivered work against the live disk.

## Files

- `bin/handoff` — POSIX launcher (resolves its own directory; relocatable)
- `lib/cli.js`, `lib/handoff.js`, `lib/crypto.js` — client (self-contained)
- `templates/context-template.md` — the summary skeleton
