#!/usr/bin/env bash
# scripts/package-skill.sh — build a self-contained, publishable Node skill.
# The server is Go and deployed separately; the skill bundles ONLY the client.
set -euo pipefail

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SKILL="$ROOT/skill/context-handoff"
LIB="$SKILL/lib"

echo "==> staging skill bundle at $SKILL"
rm -rf "$LIB"
mkdir -p "$LIB" "$SKILL/bin" "$SKILL/templates"
cp "$ROOT/client/"*.js "$LIB/"
chmod 0755 "$SKILL/bin/handoff"

# Never ship caches, data, secrets, or temp files.
find "$SKILL" -type f \( \
  -name '*.log' -o -name '.DS_Store' -o -name '*.tmp' -o -name '*.swp' \
  -o -name '.env*' -o -name '*secret*' -o -name '*credential*' \
  \) -delete
find "$SKILL" -type d -name node_modules -prune -exec rm -rf {} + 2>/dev/null || true

# Smoke: launcher resolves from any CWD.
"$SKILL/bin/handoff" --help >/dev/null

# Guard: no machine-local absolute paths baked into shipped JS/docs.
if grep -RInE "/home/|/Users/|/root/" "$SKILL" --include='*.js' --include='*.md' --include='handoff'; then
  echo "ERROR: absolute local path found in skill bundle" >&2
  exit 1
fi

echo "==> bundle contents:"
find "$SKILL" -type f | sort
echo "==> OK"
