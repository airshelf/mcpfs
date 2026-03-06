# mcpfs gateway: mount ANY MCP server as a filesystem

## Vision

```bash
# Today: custom Go server per service
mcpfs /mnt/mcpfs/posthog -- mcpfs-posthog     # 400 lines of Go

# Gateway: zero custom code
mcpfs /mnt/mcpfs/posthog -- npx @posthog/mcp   # generic adapter
mcpfs /mnt/mcpfs/stripe  -- npx @stripe/mcp    # same binary
mcpfs /mnt/mcpfs/linear  -- npx @linear/mcp    # same binary
```

One binary mounts any MCP server. No per-server Go code. 18,000+ servers become filesystems.

## How it works

mcpfs connects to the child MCP server and calls both `resources/list` AND `tools/list`.

### Resources (already works)
Servers that expose resources get mounted as-is. This is the current behavior.

### Tools → Files (new: auto-mapping)

Tools are classified by name pattern and required params:

| Classification | Pattern | Filesystem | On `cat` |
|---|---|---|---|
| **List** (no required params) | `list_customers`, `dashboards-get-all` | `customers.json` | `tools/call list_customers {}` |
| **Get** (1 required param) | `dashboard-get(dashboardId)` | `dashboards/{id}.json` | `tools/call dashboard-get {dashboardId: id}` |
| **Get** (2+ required params) | `get_commit(owner, repo, sha)` | `commits/{owner}/{repo}/{sha}.json` | nested template dirs |
| **Write** | `create_*`, `update_*`, `delete_*` | not mounted | CLI proxy only |
| **Search/Query** | `search_*`, `query_*` | not mounted | CLI proxy (needs input) |

### Name normalization

Tool names vary wildly across servers:
- PostHog: `dashboards-get-all`, `dashboard-get`, `dashboard-create`
- Stripe: `list_customers`, `retrieve_customer`, `create_customer`
- GitHub: `list_issues`, `get_issue`, `create_issue`

Normalize to filesystem names:
```
dashboards-get-all  → dashboards.json
list_customers      → customers.json
actions-get-all     → actions.json
dashboard-get       → dashboards/{dashboardId}.json
retrieve_customer   → customers/{customer_id}.json
```

Rules:
1. Strip prefix: `list_`, `get_all_`, `*-get-all`, `*-list` → resource name
2. Pluralize if needed (singular tool name → plural file name)
3. `.json` extension for application/json (default)
4. For get-by-id: singularize the list name → directory, param becomes path segment

### Classification heuristics

```go
func classifyTool(t Tool) ToolClass {
    name := strings.ToLower(t.Name)
    required := t.InputSchema.Required

    // Writes: always CLI-only
    if matchesAny(name, "create", "update", "delete", "remove", "add", "set", "patch", "put", "post") {
        return ToolWrite
    }

    // Search/query: needs input, CLI-only
    if matchesAny(name, "search", "query", "find", "run") {
        return ToolWrite
    }

    // List: no required params → static file
    if len(required) == 0 && matchesAny(name, "list", "get-all", "get_all", "retrieve_all", "balance") {
        return ToolList
    }

    // No required params but doesn't match list pattern → still try as static
    if len(required) == 0 {
        return ToolList
    }

    // Get by ID: has required params, matches get pattern → template dir
    if matchesAny(name, "get", "retrieve", "read", "show", "describe") {
        return ToolGet
    }

    // Unknown: CLI-only (safe default)
    return ToolWrite
}
```

### Fallback: resources + tools merge

If a server exposes BOTH resources and tools:
1. Resources take priority (they're designed for reading)
2. Tools fill gaps (e.g., server has `list_customers` resource but no `get_customer` resource — tool provides the template dir)
3. Write tools always go to CLI proxy

## Architecture

```
                    mcpfs (single binary)
                    ┌─────────────────────┐
   cat file.json ──▶│ 1. resources/list   │──▶ mount as dirs/files (existing)
                    │ 2. tools/list       │──▶ classify → mount reads as files
                    │ 3. tool reads: lazy │──▶ tools/call on cat (new)
                    │ 4. tool writes: CLI │──▶ --help / execute (existing)
                    └─────────────────────┘
                              │
                         stdio/HTTP
                              │
                    ┌─────────────────────┐
                    │ ANY MCP server      │
                    │ (npx, binary, HTTP) │
                    └─────────────────────┘
```

Key: `mcpfs` itself becomes the gateway. No separate mcpfs-* server binaries needed for new services.

## CLI proxy (already generic)

The `mcptool` package already handles this:
```bash
mcpfs /mnt/mcpfs/posthog -- npx @posthog/mcp
# Reads: cat /mnt/mcpfs/posthog/dashboards.json
# Writes: mcpfs tool posthog create-dashboard --name "My Dashboard"
```

New subcommand `mcpfs tool <mount> <tool-name> [--flags]` proxies writes.

## Mount command changes

```bash
# Current
mcpfs /mnt/mcpfs/github -- mcpfs-github          # custom binary

# Gateway mode (new)
mcpfs /mnt/mcpfs/github -- npx @github/mcp       # any server
mcpfs /mnt/mcpfs/slack  -- npx @slack/mcp         # any server
mcpfs /mnt/mcpfs/jira   -- npx @jira/mcp          # any server

# HTTP servers
mcpfs /mnt/mcpfs/posthog --http https://mcp.posthog.com/mcp --auth "Bearer $KEY"

# Config file (mount all)
mcpfs --config ~/.config/mcpfs/servers.json
```

### servers.json

```json
{
  "posthog": {
    "type": "http",
    "url": "https://mcp.posthog.com/mcp",
    "auth": "Bearer ${POSTHOG_API_KEY}"
  },
  "github": {
    "command": "npx",
    "args": ["-y", "@modelcontextprotocol/server-github"],
    "env": {"GITHUB_TOKEN": "${GITHUB_TOKEN}"}
  },
  "stripe": {
    "type": "http",
    "url": "https://mcp.stripe.com/mcp",
    "auth": "Bearer ${STRIPE_API_KEY}"
  }
}
```

This is exactly Claude Desktop's `mcpServers` format — mcpfs reads the same config.

## Implementation plan

1. **Tool classification** — `internal/toolfs/classify.go` (~100 lines)
2. **Tool→file mapping** — `internal/toolfs/tree.go` (~150 lines)
3. **Lazy tool calls on read** — modify `fileNode.readData()` to call `tools/call` for tool-backed files
4. **HTTP MCP client** — `pkg/mcpclient/http.go` (~100 lines, for servers like PostHog)
5. **Config file** — `mcpfs --config` reads servers.json, mounts all
6. **`mcpfs tool` subcommand** — proxy writes through mounted servers

Total: ~500 lines of new Go code. Eliminates ~4000 lines of per-server code.

## What this kills

- 11 custom server binaries (mcpfs-github, mcpfs-posthog, etc.)
- capture-tools workflow (tools discovered at mount time)
- mcpfs-env (auth in servers.json)
- mcpfs-mount (replaced by `mcpfs --config`)

## What survives

- FUSE layer (unchanged)
- mcpclient (stdio, extended with HTTP)
- Tree building (extended with tool classification)
- The principle: reads = files, writes = CLI, pipes = composition
