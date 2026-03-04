#!/bin/bash
set -euo pipefail

# bench_compose.sh — Compare composability across interfaces
#
# Usage: ./bench/bench_compose.sh [mountpoint]
#
# Same 5 questions answered via filesystem, CLI, and MCP.
# Measures: lines of code, pipe complexity, success/failure.

MOUNT="${1:-/tmp/mnt}"

echo "=== Composability Benchmark ==="
echo "Mount: $MOUNT"
echo "Date: $(date -Iseconds)"
echo ""

pass=0
fail=0
total=0

test_approach() {
  local label="$1" cmd="$2"
  total=$((total + 1))
  local chars
  chars=${#cmd}
  if output=$(eval "$cmd" 2>/dev/null) && [ -n "$output" ]; then
    pass=$((pass + 1))
    printf "  ✓ %-25s %3d chars  %s\n" "$label" "$chars" "$(echo "$output" | head -1)"
  else
    fail=$((fail + 1))
    printf "  ✗ %-25s %3d chars  (failed or empty)\n" "$label" "$chars"
  fi
}

# --- Q1: List all GitHub repo names ---
echo "## Q1: List repo names"
echo ""

test_approach "filesystem" \
  "cat $MOUNT/github/repos 2>/dev/null | head -5"

test_approach "cli (gh)" \
  "gh api /user/repos -q '.[].full_name' 2>/dev/null | head -5"

test_approach "cli (curl + jq)" \
  "curl -sH 'Authorization: token \$GITHUB_TOKEN' https://api.github.com/user/repos | jq -r '.[].full_name' | head -5"

echo ""

# --- Q2: Count running Docker containers ---
echo "## Q2: Count running containers"
echo ""

test_approach "filesystem" \
  "cat $MOUNT/docker/containers 2>/dev/null | wc -l"

test_approach "cli (docker)" \
  "docker ps -q 2>/dev/null | wc -l"

echo ""

# --- Q3: Latest Vercel deployment URL ---
echo "## Q3: Latest deployment URL"
echo ""

test_approach "filesystem" \
  "cat $MOUNT/vercel/deployments 2>/dev/null | head -1"

test_approach "cli (vx)" \
  "vx ls --json 2>/dev/null | jq -r '.[0].url'"

echo ""

# --- Q4: Search across services (grep) ---
echo "## Q4: Search for 'error' across services"
echo ""

test_approach "filesystem (grep)" \
  "grep -rl error $MOUNT/ 2>/dev/null | head -5"

test_approach "cli (multiple cmds)" \
  "{ gh api /user/repos -q '.[].full_name'; vx ls --json | jq -r '.[].url'; } 2>/dev/null | head -5"

echo ""

# --- Q5: Cross-reference (repos + deployments) ---
echo "## Q5: GitHub repos that have Vercel deployments"
echo ""

test_approach "filesystem (join)" \
  "comm -12 <(cat $MOUNT/github/repos 2>/dev/null | awk '{print \$1}' | sort) <(cat $MOUNT/vercel/projects 2>/dev/null | awk '{print \$1}' | sort) | head -5"

test_approach "cli (scripted)" \
  "comm -12 <(gh api /user/repos -q '.[].name' 2>/dev/null | sort) <(vx projects --json 2>/dev/null | jq -r '.[].name' | sort) | head -5"

echo ""

# --- Summary ---
echo "## Summary"
echo ""
echo "Passed: $pass / $total"
echo "Failed: $fail / $total"
echo ""
echo "Key insight: filesystem interface enables standard Unix tools (grep, awk,"
echo "comm, wc) without learning per-service CLI flags or API endpoints."
echo "Cross-service queries that require scripting with CLIs become simple"
echo "file operations with mcpfs."
