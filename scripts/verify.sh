#!/usr/bin/env bash
# scripts/verify.sh — full quality gate: syntax, format, vet, build, all tests.
set -euo pipefail

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

export GO111MODULE=on
export GOPATH="${GOPATH:-$HOME/go}"
export GOMODCACHE="${GOMODCACHE:-$GOPATH/pkg/mod}"
export CGO_ENABLED=0

echo "==> [1/7] Node syntax check"
for f in client/cli.js client/crypto.js client/handoff.js; do node --check "$f"; done

echo "==> [2/7] gofmt"
FMT=$(cd server-go && gofmt -l .)
if [ -n "$FMT" ]; then echo "gofmt needs to format: $FMT"; exit 1; fi

echo "==> [3/7] go vet"
(cd server-go && go vet ./...)

echo "==> [4/7] go build"
(cd server-go && go build -o handoff-server .)
test -x server-go/handoff-server

echo "==> [5/7] Go server tests"
(cd server-go && go test -count=1 ./...)

echo "==> [6/7] Node client unit tests"
node --test test/client.test.js

echo "==> [7/7] package skill + cross-language e2e (Go server vs Node CLI)"
./scripts/package-skill.sh >/dev/null
node --test test/e2e.test.js

echo ""
echo "✅ ALL CHECKS PASSED"
