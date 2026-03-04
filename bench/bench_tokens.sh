#!/bin/bash
set -euo pipefail

# bench_tokens.sh — Compare context cost (tokens ≈ chars/4) across interfaces
#
# Usage: ./bench/bench_tokens.sh [mountpoint]
#   mountpoint: path where mcpfs servers are mounted (default: /tmp/mnt)
#
# Compares discovery cost across three interfaces:
# 1. Filesystem (ls + cat)
# 2. CLI (vx --json, gh api, etc.)
# 3. MCP JSON-RPC (resources/list, resources/read)

MOUNT="${1:-/tmp/mnt}"

count_tokens() {
  local label="$1" chars words
  chars=$(wc -c | tr -d ' ')
  words=$(echo "$chars / 4" | bc)  # rough token estimate
  printf "%-45s %8s chars  ~%s tokens\n" "$label" "$chars" "$words"
}

echo "=== Token Cost Benchmark ==="
echo "Mount: $MOUNT"
echo "Date: $(date -Iseconds)"
echo ""

# --- Discovery cost ---
echo "## Discovery (what's available?)"
echo ""

# Filesystem: just ls
if [ -d "$MOUNT" ]; then
  ls -R "$MOUNT" 2>/dev/null | count_tokens "fs: ls -R mountpoint"
else
  echo "  (mount not available, skipping filesystem tests)"
fi

# CLI: vx help output
if command -v vx &>/dev/null; then
  vx --help 2>&1 | count_tokens "cli: vx --help"
fi

# MCP: resources/list response (simulated via server)
if [ -d "$MOUNT/github" ]; then
  echo '{"jsonrpc":"2.0","id":1,"method":"resources/list"}' | \
    timeout 5 go run ./servers/github 2>/dev/null | \
    head -1 | count_tokens "mcp: github resources/list"
fi

echo ""

# --- Single resource read cost ---
echo "## Single Resource Read"
echo ""

# GitHub repos via filesystem
if [ -f "$MOUNT/github/repos" ]; then
  cat "$MOUNT/github/repos" 2>/dev/null | count_tokens "fs: cat github/repos"
fi

# GitHub repos via CLI
if command -v gh &>/dev/null; then
  gh api /user/repos --paginate -q '.[].full_name' 2>/dev/null | \
    count_tokens "cli: gh api /user/repos (names only)"
  gh api /user/repos 2>/dev/null | \
    count_tokens "cli: gh api /user/repos (full JSON)"
fi

# GitHub repos via MCP read
if [ -d "$MOUNT/github" ]; then
  echo '{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"github://repos"}}' | \
    timeout 5 go run ./servers/github 2>/dev/null | \
    head -1 | count_tokens "mcp: github://repos (JSON-RPC response)"
fi

echo ""

# --- Vercel deployments ---
echo "## Vercel Deployments"
echo ""

if [ -f "$MOUNT/vercel/deployments" ]; then
  cat "$MOUNT/vercel/deployments" 2>/dev/null | count_tokens "fs: cat vercel/deployments"
fi

if command -v vx &>/dev/null; then
  vx ls --json 2>/dev/null | count_tokens "cli: vx ls --json"
fi

echo ""

# --- Docker containers ---
echo "## Docker Containers"
echo ""

if [ -f "$MOUNT/docker/containers" ]; then
  cat "$MOUNT/docker/containers" 2>/dev/null | count_tokens "fs: cat docker/containers"
fi

if command -v docker &>/dev/null; then
  docker ps --format json 2>/dev/null | count_tokens "cli: docker ps --format json"
  docker ps -a --format json 2>/dev/null | count_tokens "cli: docker ps -a --format json"
fi

echo ""
echo "## Summary"
echo ""
echo "Filesystem reads return pre-formatted summaries (name, key fields)."
echo "CLI returns full API responses (all fields, pagination metadata)."
echo "MCP JSON-RPC adds framing overhead but same content as filesystem."
echo ""
echo "Rule of thumb: filesystem interface is 3-10x fewer tokens than raw API."
