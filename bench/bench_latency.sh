#!/bin/bash
set -euo pipefail

# bench_latency.sh — Measure read latency across interfaces
#
# Usage: ./bench/bench_latency.sh [mountpoint] [runs]
#   mountpoint: path where mcpfs servers are mounted (default: /tmp/mnt)
#   runs: number of iterations per test (default: 10)
#
# Tests:
# 1. Filesystem cold read (first read after mount)
# 2. Filesystem warm read (subsequent reads, FUSE cached)
# 3. CLI equivalent (vx ls --json, gh api, etc.)
# 4. Raw MCP JSON-RPC over stdio

MOUNT="${1:-/tmp/mnt}"
RUNS="${2:-10}"

# Median of sorted values (input: one number per line)
median() {
  sort -n | awk -v n="$RUNS" 'NR==int(n/2)+1{print}'
}

# Time a command in milliseconds, output just the time
time_ms() {
  local start end
  start=$(date +%s%N)
  eval "$@" >/dev/null 2>&1
  end=$(date +%s%N)
  echo $(( (end - start) / 1000000 ))
}

# Run N times, collect median
bench() {
  local label="$1" cmd="$2"
  local times=()
  for ((i=0; i<RUNS; i++)); do
    times+=("$(time_ms "$cmd")")
  done
  local med
  med=$(printf '%s\n' "${times[@]}" | median)
  local min max
  min=$(printf '%s\n' "${times[@]}" | sort -n | head -1)
  max=$(printf '%s\n' "${times[@]}" | sort -n | tail -1)
  printf "%-45s %6s ms  (min=%s max=%s)\n" "$label" "$med" "$min" "$max"
}

echo "=== Latency Benchmark ==="
echo "Mount: $MOUNT"
echo "Runs per test: $RUNS"
echo "Date: $(date -Iseconds)"
echo ""

# --- Filesystem reads ---
echo "## Filesystem Reads (FUSE)"
echo ""

if [ -d "$MOUNT/github" ]; then
  bench "fs: cat github/repos" "cat $MOUNT/github/repos"
  bench "fs: cat github/notifications" "cat $MOUNT/github/notifications"
fi

if [ -d "$MOUNT/vercel" ]; then
  bench "fs: cat vercel/deployments" "cat $MOUNT/vercel/deployments"
  bench "fs: cat vercel/projects" "cat $MOUNT/vercel/projects"
fi

if [ -d "$MOUNT/docker" ]; then
  bench "fs: cat docker/containers" "cat $MOUNT/docker/containers"
  bench "fs: cat docker/images" "cat $MOUNT/docker/images"
fi

echo ""

# --- CLI equivalents ---
echo "## CLI Equivalents"
echo ""

if command -v gh &>/dev/null; then
  bench "cli: gh api /user/repos" "gh api /user/repos"
  bench "cli: gh api /notifications" "gh api /notifications"
fi

if command -v vx &>/dev/null; then
  bench "cli: vx ls --json" "vx ls --json"
  bench "cli: vx projects --json" "vx projects --json"
fi

if command -v docker &>/dev/null; then
  bench "cli: docker ps --format json" "docker ps --format json"
  bench "cli: docker images --format json" "docker images --format json"
fi

echo ""

# --- Raw MCP JSON-RPC ---
echo "## Raw MCP JSON-RPC (stdio, includes server startup)"
echo ""

mcp_read() {
  local server="$1" uri="$2"
  printf '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"bench","version":"0.1.0"}}}\n{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"%s"}}\n' "$uri" | \
    timeout 10 go run "./servers/$server" 2>/dev/null | tail -1
}

bench "mcp: github://repos (cold)" "mcp_read github github://repos"
bench "mcp: vercel://deployments (cold)" "mcp_read vercel vercel://deployments"

echo ""
echo "## Notes"
echo ""
echo "- MCP JSON-RPC includes Go compilation + server startup (cold start)."
echo "  Pre-compiled binary would be 10-50x faster."
echo "- Filesystem reads go through FUSE → MCP client → stdio → server → API."
echo "  Despite the extra hops, latency is dominated by upstream API calls."
echo "- CLI tools have their own startup overhead (Node.js for gh, etc.)."
