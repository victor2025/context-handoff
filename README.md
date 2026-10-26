# context-handoff

English | [简体中文](README.zh-CN.md)

Secure, ephemeral **context hand-off between Agent sessions**. The current session saves a structured Markdown summary; the receiving session restores it with a **UUID + short TOKEN**.

- 🔐 **Client-side encryption** — AES-256-GCM, key derived from the TOKEN with PBKDF2-HMAC-SHA256 (200 000 iterations, random salt + nonce per message).
- 🟢 **Go zero-knowledge relay** — stores only ciphertext, metadata, expiry, and `SHA-256(upper(trim(TOKEN)))`. Never the TOKEN, never the plaintext. Standard library only (PBKDF2 implemented in-repo), single static binary.
- 🙈 **Zero plaintext at rest** — server-side decryption on `/load` is ephemeral per request and never written back.
- ⏳ **Ephemeral** — default TTL 30 minutes, maximum 1 day, atomic file storage (`0600`, tmp+fsync+rename) with expiry purge.
- 🪶 **Self-contained Node skill** — client/skill needs only Node.js ≥ 20 built-ins, no `npm install`.
- 🔗 **Cross-language verified** — the Node client envelope is decrypted by the Go server in the end-to-end tests.
- 🌐 **Works out of the box** — default endpoint `https://ctx.victor2022.dpdns.org` (public, zero-knowledge); **self-hosting is recommended** for sensitive work.

```
session A ──save──▶ [ Go relay: ciphertext + tokenHash + TTL ] ──load(UUID,TOKEN)──▶ session B
   ▲ token stays with the user / prompt                                  (plaintext restored)
```

## Repository layout

```
context-handoff/
├── server-go/              # Go relay (standard library only)
│   ├── main.go             # CLI entry + flags/env + purge loop + graceful shutdown
│   ├── server.go           # HTTP handlers and public record projection
│   ├── store.go            # FileStore: atomic writes, 0600, UUIDv4, purge
│   ├── crypto.go           # PBKDF2-HMAC-SHA256 + AES-256-GCM (interoperable envelope)
│   ├── limits.go           # defaults, limits, error codes
│   └── *_test.go           # Go server/crypto tests
├── client/                 # Node client (also copied into the skill bundle)
│   ├── cli.js              # handoff health|save|load
│   ├── crypto.js           # token gen, PBKDF2 + AES-256-GCM envelope
│   └── handoff.js          # HTTP client
├── skill/context-handoff/  # SELF-CONTAINED publishable Agent skill
│   ├── SKILL.md
│   ├── bin/handoff
│   ├── lib/…               # copied from client/ by scripts/package-skill.sh
│   └── templates/context-template.md
├── test/
│   ├── client.test.js      # Node crypto/CLI unit tests
│   └── e2e.test.js         # boots the Go binary, drives the Node CLI over HTTP
├── scripts/package-skill.sh
├── scripts/verify.sh       # full build + format + test gate
└── package.json
```

## 1. Build & deploy the server (Go)

**Server deployment and skill distribution are two separate things.** Any number of agents/skills can share one relay; the relay never sees plaintext.

Requirements: Go ≥ 1.19 (uses only the standard library).

```bash
cd server-go
GO111MODULE=on CGO_ENABLED=0 go build -o handoff-server .
./handoff-server --host 127.0.0.1 --port 8765 --dir ./.handoff-data
```

Flags (flags take precedence; env in parentheses):

| Flag | Env var | Default |
| --- | --- | --- |
| `--host` | `HANDOFF_HOST` | `127.0.0.1` |
| `--port` | `HANDOFF_PORT` | `8765` |
| `--dir` | `HANDOFF_DATA_DIR` | `./.handoff-data` |
| `--cleanup-interval` | — | `300` seconds (0 disables) |

Run server tests:

```bash
cd server-go
GO111MODULE=on go test -v -count=1 ./...
```

### systemd service (example)

```bash
sudo install -m 0755 server-go/handoff-server /usr/local/bin/handoff-server
sudo mkdir -p /var/lib/context-handoff
cat > /tmp/context-handoff.service <<'EOF'
[Unit]
Description=context-handoff relay
After=network.target

[Service]
ExecStart=/usr/local/bin/handoff-server --host 127.0.0.1 --port 8765 --dir /var/lib/context-handoff
Restart=on-failure
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/context-handoff
StateDirectory=context-handoff

[Install]
WantedBy=multi-user.target
EOF
sudo mv /tmp/context-handoff.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now context-handoff
curl -s http://127.0.0.1:8765/healthz
```

Keep it on loopback for local use. Putting it on a network requires **TLS and your own auth boundary**; the protocol authenticates only knowledge of the TOKEN.

## 2. Install / update / verify the skill

The skill bundle is self-contained and relocatable — no absolute paths, caches, or secrets. Rebuild after changing `client/`:

```bash
./scripts/package-skill.sh
```

Install:

```bash
# install
cp -r skill/context-handoff ~/.agents/skills/context-handoff

# update
rm -rf ~/.agents/skills/context-handoff && cp -r skill/context-handoff ~/.agents/skills/context-handoff

# verify installation
~/.agents/skills/context-handoff/bin/handoff --help
CONTEXT_HANDOFF_SERVER=http://127.0.0.1:8765 ~/.agents/skills/context-handoff/bin/handoff health
```

Optional global command:

```bash
sudo ln -sf "$PWD/skill/context-handoff/bin/handoff" /usr/local/bin/handoff
handoff health
```

## 3. Usage

Server URL precedence: `--server` flag → `CONTEXT_HANDOFF_SERVER` env → `https://ctx.victor2022.dpdns.org` (public relay).

> **Self-hosting is recommended.** The public relay is convenient and zero-knowledge by design, but running your own `server-go/` gives you full control over retention, uptime and exposure. Alternate public endpoint: `https://ctx.victor2022.cc.cd`.
>
> ```bash
> export CONTEXT_HANDOFF_SERVER=http://127.0.0.1:8765   # use your own instance
> ```

```bash
cp skill/context-handoff/templates/context-template.md context.md
# edit context.md…
handoff save context.md
cat context.md | handoff save --ttl 1800 --title "login refactor handoff" -
```

Receiving session:

```bash
handoff load --id 11111111-2222-4333-8444-555555555555 --token ABCDEFGHJKMNPQRSTVWXYZ
```

Without the skill, hit the Go API directly:

```bash
curl -sS "http://127.0.0.1:8765/api/contexts/11111111-2222-4333-8444-555555555555/load?token=ABCDEFGHJKMNPQRSTVWXYZ"
```

## HTTP API

| Method | Path | Body | Notes |
| --- | --- | --- | --- |
| `GET` | `/healthz` | — | health + limits |
| `POST` | `/api/contexts` | `{payload, tokenHash, ttl?, title?, format?, metadata?}` | 201 + `{id, ttl, expiresAt,…}` (no hash) |
| `GET` | `/api/contexts/:id` | — | **ciphertext** + metadata |
| `GET` | `/api/contexts/:id/load?token=…` | — | verifies hash, ephemeral decrypt → `plaintext` (legacy POST `{token}` accepted) |

Envelope (`payload`):

```json
{
  "v": 1,
  "alg": "AES-256-GCM",
  "kdf": "PBKDF2-HMAC-SHA256",
  "kdfParams": {"iterations": 200000, "keyLength": 32, "saltLength": 16, "nonceLength": 12, "digest": "sha256"},
  "salt": "<base64>", "nonce": "<base64>", "ciphertext": "<base64>", "tag": "<base64>"
}
```

Limits: payload ≤ **256 KiB**, metadata JSON ≤ **8 KiB**, TTL positive integer ≤ **86400 s** (default **1800 s**).

### Error codes

`INVALID_JSON`, `EMPTY_BODY`, `BODY_TOO_LARGE`, `METADATA_TOO_LARGE`, `INVALID_TTL`, `INVALID_REQUEST`, `INVALID_TOKEN`, `WRONG_TOKEN`, `DECRYPTION_FAILED`, `NOT_FOUND`, `EXPIRED`, `STORAGE_UNAVAILABLE`, `NOT_FOUND_ROUTE`, `PAYLOAD_TOO_LARGE`.

Responses never contain `tokenHash`. GET without token returns ciphertext; wrong token → `403 WRONG_TOKEN`; missing/expired → `404 NOT_FOUND` / `410 EXPIRED`.

## Security model

- TOKEN (~100-bit, ambiguity-free alphabet) is generated **client-side**, shown **UPPERCASE**, accepted case-insensitively after `trim().toUpperCase()`.
- Encryption key = `PBKDF2(TOKEN, random salt)` (200k iterations). The server-side `SHA-256(upper(trim(TOKEN)))` hash is **only** for verification — never key derivation — and is compared in constant time.
- AES-256-GCM authentication makes wrong-token/tampering indistinguishable (`DECRYPTION_FAILED`).
- Go relay decrypts only while handling `/load`; plaintext is not retained or persisted.
- Files are written atomically (temp + fsync + rename) at `0600`, directory `0700`.
- **Never put secrets in the summary** — no passwords, private keys, tokens, cookies, `appSecret`, API keys. Prefer delivering UUID and TOKEN over separate channels.

## Full verification

```bash
./scripts/verify.sh
```

Runs: `node --check` on all client files, `gofmt -l`, `go vet`, `go build`, `go test ./...`, Node client unit tests, packaging the skill, and the cross-language end-to-end suite.

## License

MIT
