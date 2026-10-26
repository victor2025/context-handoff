# context-handoff（中文版）

[English](README.md) | 简体中文

在 Agent 会话之间安全、临时地**交接任务上下文**。当前会话保存一份结构化 Markdown 摘要；接手会话用 **UUID + 短 TOKEN** 恢复。

- 🔐 **客户端加密** — AES-256-GCM，密钥由 TOKEN 经 PBKDF2-HMAC-SHA256 派生（200 000 次迭代，每次消息随机 salt + nonce）。
- 🟢 **Go 零知识中继** — 只持久化密文、元数据、过期时间和 `SHA-256(upper(trim(TOKEN)))`。绝不存储 TOKEN 明文，绝不存储上下文明文。仅用标准库（PBKDF2 为仓库内自实现），单一静态二进制。
- 🙈 **静态零明文** — 服务端只在处理 `/load` 请求期间临时解密，明文绝不写回存储。
- ⏳ **临时性** — 默认 TTL 30 分钟，最大 1 天；原子文件写入（`0600`，tmp+fsync+rename）并定期清理过期记录。
- 🪶 **自包含 Node skill** — 客户端/skill 只需 Node.js ≥ 20 内置模块，无需 `npm install`。
- 🔗 **跨语言已验证** — 端到端测试中由 Go 服务端解密 Node 客户端生成的信封。
- 🌐 **开箱即用** — 默认端点 `https://ctx.victor2022.dpdns.org`（公共中继，零知识）；处理敏感内容时**推荐自建**。

```
会话 A ──save──▶ [ Go 中继：密文 + tokenHash + TTL ] ──load(UUID,TOKEN)──▶ 会话 B
   ▲ TOKEN 只留在用户/提示词里                                    （明文恢复）
```

## 仓库结构

```
context-handoff/
├── server-go/              # Go 中继（仅标准库）
│   ├── main.go             # CLI 入口 + flags/env + 清理循环 + 优雅关停
│   ├── server.go           # HTTP handler 与公开记录投影
│   ├── store.go            # FileStore：原子写、0600、UUIDv4、过期清理
│   ├── crypto.go           # PBKDF2-HMAC-SHA256 + AES-256-GCM（互通信封）
│   ├── limits.go           # 默认值、限制、错误码
│   └── *_test.go           # Go 服务端/加密测试
├── client/                 # Node 客户端（同时复制进 skill 包）
│   ├── cli.js              # handoff health|save|load
│   ├── crypto.js           # TOKEN 生成、PBKDF2 + AES-256-GCM 信封
│   └── handoff.js          # HTTP 客户端
├── skill/context-handoff/  # 自包含、可发布的 Agent skill
│   ├── SKILL.md
│   ├── bin/handoff
│   ├── lib/…               # 由 scripts/package-skill.sh 从 client/ 复制
│   └── templates/context-template.md
├── test/
│   ├── client.test.js      # Node 加密/CLI 单测
│   └── e2e.test.js         # 启动真实 Go 二进制，经 HTTP 驱动 Node CLI
├── scripts/package-skill.sh
├── scripts/verify.sh       # 构建 + 格式 + 测试完整门禁
└── package.json
```

## 1. 构建并部署服务端（Go）

**服务端部署与 skill 分发是两件独立的事。** 多个 Agent/skill 可共用一个中继；中继永远看不到明文。

要求：Go ≥ 1.19（仅标准库）。

```bash
cd server-go
GO111MODULE=on CGO_ENABLED=0 go build -o handoff-server .
./handoff-server --host 127.0.0.1 --port 8765 --dir ./.handoff-data
```

参数（flags 优先；括号内为环境变量）：

| Flag | 环境变量 | 默认值 |
| --- | --- | --- |
| `--host` | `HANDOFF_HOST` | `127.0.0.1` |
| `--port` | `HANDOFF_PORT` | `8765` |
| `--dir` | `HANDOFF_DATA_DIR` | `./.handoff-data` |
| `--cleanup-interval` | — | `300` 秒（0 表示禁用） |

运行服务端测试：

```bash
cd server-go
GO111MODULE=on go test -v -count=1 ./...
```

### systemd 服务（示例）

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

本地使用请只绑定 loopback。若要暴露到网络，必须自行配置 **TLS 与鉴权边界**；本协议只凭「知道 TOKEN」做认证。

## 2. 安装 / 更新 / 验证 skill

skill 包自包含、可任意迁移——不含绝对路径、缓存或密钥。修改 `client/` 后重新构建：

```bash
./scripts/package-skill.sh
```

安装：

```bash
# 安装
cp -r skill/context-handoff ~/.agents/skills/context-handoff

# 更新
rm -rf ~/.agents/skills/context-handoff && cp -r skill/context-handoff ~/.agents/skills/context-handoff

# 验证安装
~/.agents/skills/context-handoff/bin/handoff --help
CONTEXT_HANDOFF_SERVER=http://127.0.0.1:8765 ~/.agents/skills/context-handoff/bin/handoff health
```

可选的全局命令：

```bash
sudo ln -sf "$PWD/skill/context-handoff/bin/handoff" /usr/local/bin/handoff
handoff health
```

## 3. 使用

服务地址优先级：`--server` 参数 → `CONTEXT_HANDOFF_SERVER` 环境变量 → `https://ctx.victor2022.dpdns.org`（公共中继）。

> **推荐自建服务。** 公共中继开箱即用且在设计上零知识，但自建 `server-go/` 才能完全掌控保留策略、可用性和暴露面。备用公共端点：`https://ctx.victor2022.cc.cd`。
>
> ```bash
> export CONTEXT_HANDOFF_SERVER=http://127.0.0.1:8765   # 指向自建实例
> ```

从模板写一份摘要，然后保存：

```bash
cp skill/context-handoff/templates/context-template.md context.md
# 编辑 context.md…
handoff save context.md
cat context.md | handoff save --ttl 1800 --title "登录重构交接" -
```

接手会话：

```bash
handoff load --id 11111111-2222-4333-8444-555555555555 --token ***
```

没装 skill？直接请求 API：

```bash
curl -sS -X POST "http://127.0.0.1:8765/api/contexts/11111111-2222-4333-8444-555555555555/load" \
  -H 'content-type: application/json' \
  -d '{"token":"***"}'
```

## HTTP API

| 方法 | 路径 | 请求体 | 说明 |
| --- | --- | --- | --- |
| `GET` | `/healthz` | — | 健康检查 + 限制 |
| `POST` | `/api/contexts` | `{payload, tokenHash, ttl?, title?, format?, metadata?}` | 201 + `{id, ttl, expiresAt,…}`（不含哈希） |
| `GET` | `/api/contexts/:id` | — | 返回**密文** + 元数据 |
| `POST` | `/api/contexts/:id/load` | `{token}` | 校验哈希、临时解密 → `plaintext` |

信封（`payload`）：

```json
{
  "v": 1,
  "alg": "AES-256-GCM",
  "kdf": "PBKDF2-HMAC-SHA256",
  "kdfParams": {"iterations": 200000, "keyLength": 32, "saltLength": 16, "nonceLength": 12, "digest": "sha256"},
  "salt": "<base64>", "nonce": "<base64>", "ciphertext": "<base64>", "tag": "<base64>"
}
```

限制：payload ≤ **256 KiB**，metadata JSON ≤ **8 KiB**，TTL 为正整数 ≤ **86400 秒**（默认 **1800 秒**）。

### 错误码

`INVALID_JSON`、`EMPTY_BODY`、`BODY_TOO_LARGE`、`METADATA_TOO_LARGE`、`INVALID_TTL`、`INVALID_REQUEST`、`INVALID_TOKEN`、`WRONG_TOKEN`、`DECRYPTION_FAILED`、`NOT_FOUND`、`EXPIRED`、`STORAGE_UNAVAILABLE`、`NOT_FOUND_ROUTE`、`PAYLOAD_TOO_LARGE`。

响应永远不包含 `tokenHash`。不带 TOKEN 的 GET 返回密文；TOKEN 错误 → `403 WRONG_TOKEN`；不存在/已过期 → `404 NOT_FOUND` / `410 EXPIRED`。

## 安全模型

- TOKEN（约 100 bit，无歧义字母表）在**客户端本地生成**，展示为**大写**，校验时先 `trim().toUpperCase()`，大小写不敏感。
- 加密密钥 = `PBKDF2(TOKEN, 随机 salt)`（20 万次迭代）。服务端的 `SHA-256(upper(trim(TOKEN)))` 哈希**仅用于校验**——绝不参与密钥派生——且以常数时间比较。
- AES-256-GCM 认证加密使「TOKEN 错误」与「密文被篡改」不可区分（统一报 `DECRYPTION_FAILED`）。
- Go 中继只在处理 `/load` 期间解密；明文不保留、不落盘。
- 文件原子写入（临时文件 + fsync + rename），权限 `0600`，目录 `0700`。
- **绝不要把密钥写进摘要**——密码、私钥、token、cookie、`appSecret`、API key 一概不要。建议 UUID 与 TOKEN 通过不同渠道分别传递。

## 完整验证

```bash
./scripts/verify.sh
```

依次执行：所有客户端文件 `node --check`、`gofmt -l`、`go vet`、`go build`、`go test ./...`、Node 客户端单测、skill 打包、跨语言端到端测试。

## 许可证

MIT
