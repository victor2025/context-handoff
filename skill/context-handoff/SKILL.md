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
3. Default: `http://127.0.0.1:8765`

## Commands

Health check:

```bash
handoff health
handoff health --server http://127.0.0.1:8765
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
Server:     http://127.0.0.1:8765
UUID:       <uuid>
TTL:        1800s
Expires at: <ISO-8601>
TOKEN:      <UPPERCASE-TOKEN>

Recovery prompt — send this whole block to the receiving Agent session:
```
followed by a fenced code block containing exactly **one sentence** that includes:

- the server address, UUID, and TOKEN,
- the `handoff load` command (skill installed),
- a direct HTTP fallback: `POST <server>/api/contexts/<id>/load` with JSON body `{"token":"TOKEN"}` when the skill is not installed,
- an instruction to first verify current disk files, service state, and the latest user request,
- the precedence rule: latest user request and current file state win on conflict,
- an instruction to first give the user a short Chinese summary of what is known and what will be done,
- an instruction to wait for user confirmation before continuing.

`load` prints only the restored markdown plaintext on stdout (machine/human readable). Errors go to stderr with a non-zero exit code (`NOT_FOUND`, `EXPIRED`, `WRONG_TOKEN`, `DECRYPTION_FAILED`, …).

## Receiving-session workflow

1. Run the `handoff load` command (or the raw `POST …/load` if the skill is not installed).
2. Read the summary, then **verify against live reality**: inspect the referenced files, check service/process state, and read the latest user message.
3. If the summary conflicts with the live disk or the latest user request, **the latest user request and current file state win**.
4. Reply to the user in short Chinese: what you now know and what you plan to do.
5. **Wait for explicit user confirmation** before making changes or running mutating commands.

## Troubleshooting

| Symptom | Cause / fix |
| --- | --- |
| `fetch failed` / connection error | Server not running or wrong address. Check `handoff health`, `--server`, or `CONTEXT_HANDOFF_SERVER`. |
| `NOT_FOUND` | UUID wrong, never existed, or already cleaned up. Ask for a fresh handoff. |
| `EXPIRED` | TTL elapsed (default 30 min, max 1 day). Ask the sender to save again with a longer TTL. |
| `WRONG_TOKEN` | TOKEN does not match (case-insensitive; surrounding spaces are trimmed). Re-copy the full TOKEN. |
| `DECRYPTION_FAILED` | Token hash matched but envelope decryption failed — truncated/corrupted payload or mismatched envelope. Re-save. |
| `BODY_TOO_LARGE` / `METADATA_TOO_LARGE` | Summary > 256 KiB or metadata > 8 KiB. Trim the summary; link to files instead of embedding. |
| `INVALID_TTL` | TTL must be a positive integer ≤ 86400 seconds. |
| `node: command not found` | Install Node.js ≥ 20. |

## Security notes

- **Never write secrets into the summary**: no passwords, private keys, tokens, cookies, session IDs, `appSecret`, API keys, `.env` contents, or credential files. Encryption protects data in transit/at rest on the relay; it does not make a secret safe to paste into a chat prompt once decrypted.
- The TOKEN is the only decryption capability. It is generated locally, shown UPPERCASE, and never stored in plaintext by the server. Share the UUID over the normal channel and the TOKEN through a second channel when possible.
- The server's TOKEN hash (`SHA-256(upper(trim(token)))`) is used **only for verification**, never for key derivation or decryption; API responses never include it.
- Records expire (default 30 min, max 1 day) and are purged; expired IDs return `EXPIRED`.
- Run the server bound to `127.0.0.1` for local use. Exposing it requires TLS and an auth boundary you must operate yourself — the protocol alone authenticates only knowledge of the TOKEN.
- The server cannot read your context (zero-knowledge ciphertext), but a malicious server can still reject, delete, or withhold records; verify delivered work against the live disk.

## Files

- `bin/handoff` — POSIX launcher (resolves its own directory; relocatable)
- `lib/cli.js`, `lib/handoff.js`, `lib/crypto.js` — client (self-contained)
- `templates/context-template.md` — the summary skeleton
