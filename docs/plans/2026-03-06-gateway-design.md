# mcpfs gateway: any MCP server → filesystem

## Insight

Most MCP servers expose tools, not resources. But read-only tools (list, get) are functionally identical to resources — they return data with no side effects. mcpfs auto-promotes these tools to filesystem files.

`list_customers()` IS `customers.json`. The server just doesn't know it.

## Architecture

```
mcpfs --config servers.json
  │
  ├── /mnt/mcpfs/posthog/
  │     ├── dashboards.json        ← tools/call dashboards-get-all {}
  │     ├── dashboards/{id}.json   ← tools/call dashboard-get {dashboardId: id}
  │     ├── feature-flags.json     ← tools/call feature-flag-get-all {}
  │     └── ...
  │
  ├── /mnt/mcpfs/stripe/
  │     ├── customers.json         ← tools/call list_customers {}
  │     ├── customers/{id}.json    ← tools/call retrieve_customer {customer_id: id}
  │     └── ...
  │
  └── mcpfs tool posthog create-dashboard --name X   ← write proxy
```

## Config format

`~/.config/mcpfs/servers.json` — same shape as Claude Desktop `mcpServers`:

```json
{
  "posthog": {
    "type": "http",
    "url": "https://mcp.posthog.com/mcp",
    "headers": { "Authorization": "Bearer ${POSTHOG_API_KEY}" }
  },
  "github": {
    "command": "npx",
    "args": ["-y", "@modelcontextprotocol/server-github"],
    "env": { "GITHUB_TOKEN": "${GITHUB_TOKEN}" }
  },
  "stripe": {
    "type": "http",
    "url": "https://mcp.stripe.com/mcp",
    "headers": { "Authorization": "Bearer ${STRIPE_API_KEY}" }
  }
}
```

Env var interpolation: `${VAR}` in values gets replaced from environment.

## Tool classification

On mount, mcpfs calls `tools/list` and classifies each tool:

1. **Write** — name contains `create|update|delete|remove|add|set|patch|put` → CLI only
2. **Query** — name contains `search|query|run|execute` → CLI only
3. **List** — no required params → static file (called on `cat`)
4. **Get** — has required params → template directory

## Tool-to-filename mapping

Strip verb prefixes, normalize to resource names:

```
list_customers       → customers.json
dashboards-get-all   → dashboards.json
retrieve_balance     → balance.json
actions-get-all      → actions.json
dashboard-get        → dashboards/{dashboardId}.json
retrieve_customer    → customers/{customer_id}.json
get_issue            → issues/{owner}/{repo}/{issue_number}.json
```

Rules:
1. Strip: `list_`, `get_`, `retrieve_`, `*-get-all`, `*-list`, `*-retrieve`, `*-get`
2. Pluralize list tools, keep get tools singular → nested under plural dir
3. `.json` extension (default mime type for tools)
4. Required params become path segments in order

## Merging resources + tools

If a server exposes both resources AND tools:
1. Build tree from resources first (existing behavior)
2. Add tool-backed files for tools that don't overlap with existing resources
3. Overlap detection: if a resource `customers.json` exists, skip `list_customers` tool

## File reads (lazy tool calls)

Tool-backed files call `tools/call` lazily on `cat`:

```go
type toolFileNode struct {
    gofuse.Inode
    fsys     *mcpFS
    toolName string
    args     map[string]interface{}
}

func (f *toolFileNode) readData() ([]byte, error) {
    result, err := f.fsys.client.CallTool(f.toolName, f.args)
    // result is JSON text from MCP content[0].text
    return result, err
}
```

For template dirs (get-by-id), the param value from the path segment populates the args map.

## HTTP client integration

Extend `mcpclient.Client` with an HTTP mode. The `HTTPCaller` in `call_http.go` already handles:
- Session initialization
- SSE streaming responses
- Auth headers

New: `mcpclient.NewHTTP(url, headers)` returns a `*Client` that speaks HTTP instead of stdio. Same interface (`ListTools`, `CallTool`, `ListResources`, `ReadResource`).

## CLI subcommands

```bash
mcpfs --config servers.json              # mount all from config
mcpfs /mnt/x -- command [args]           # mount one (stdio, existing)
mcpfs /mnt/x --http URL --auth "Bearer"  # mount one (HTTP, new)
mcpfs tool <name> <tool> [--flags]       # write proxy
mcpfs -u /mnt/x                          # unmount (existing)
```

## New files

| File | Lines | Purpose |
|------|-------|---------|
| `internal/toolfs/classify.go` | ~80 | Tool classification + naming |
| `internal/toolfs/classify_test.go` | ~100 | Tests for classification |
| `internal/fuse/toolnode.go` | ~60 | Tool-backed file/dir FUSE nodes |
| `pkg/mcpclient/http.go` | ~120 | HTTP MCP client (wraps HTTPCaller) |
| `internal/config/config.go` | ~80 | Config file parsing + env interpolation |
| Changes to `internal/fuse/fs.go` | ~30 | Merge tool tree into resource tree |
| Changes to `cmd/mcpfs/main.go` | ~50 | --config, --http, tool subcommand |

**Total: ~520 new lines.** Replaces 3357 lines of custom servers.

## What survives

- FUSE layer (extended, not replaced)
- `mcpclient` stdio (unchanged, HTTP added alongside)
- `mcptool` dispatch (reused for `mcpfs tool` subcommand)
- Existing `mcpfs /mnt/x -- command` syntax (backward compat)
- Custom server binaries (still work, just unnecessary for new servers)

## What dies

- Per-server Go code (for new servers — existing ones keep working)
- `mcpfs-mount` bash script → replaced by `mcpfs --config`
- `mcpfs-env` → replaced by config file env interpolation
- `capture-tools` → tools discovered at mount time

## Outcome

```bash
# Before: build a Go server per service
go build ./servers/jira/...   # 300 lines of custom Go

# After: add 3 lines to config
{ "jira": { "command": "npx", "args": ["-y", "@jira/mcp"] } }
mcpfs --config servers.json
cat /mnt/mcpfs/jira/issues.json | jq '.[].summary'
```
