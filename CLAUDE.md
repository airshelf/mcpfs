# mcpfs

Mount MCP resource servers as FUSE filesystems.

## Quick start

```bash
go build ./...              # build everything
go vet ./...                # lint
go build -o mcpfs ./cmd/mcpfs  # build CLI
go build -o mcpfs-github ./servers/github  # build a server
```

## Architecture

- `pkg/mcpserve/` — MCP resource server framework. Every server uses it.
- `pkg/mcpclient/` — MCP JSON-RPC client over stdio.
- `pkg/mcptool/` — Tool schema → CLI bridge. Parses JSON Schema into flags, dispatches calls.
- `internal/fuse/` — FUSE filesystem. Maps resources → dirs, templates → subdirs.
- `cmd/mcpfs/` — CLI entry point. Launches server, connects client, mounts FUSE.
- `cmd/capture-tools/` — Captures tool schemas from HTTP or stdio MCP servers.
- `servers/*/main.go` — Each server is 250–380 lines. Self-contained.

## Conventions

- Servers use `mcpserve.New()` → `AddResource()` / `AddTemplate()` → `Serve()`
- Auth from env vars (GITHUB_TOKEN, VERCEL_TOKEN, etc.)
- All output is pre-formatted text (not raw JSON) — optimized for cat/grep
- Reads via FUSE, writes via CLI tool proxy (same binary, different mode)
- `slimObjects()` helper extracts key fields from JSON arrays (reuse across servers)
- SQL injection prevented via `pg_catalog` validation (postgres server)
- GQL injection prevented via escaping (linear server)

## Server modes

Each server binary runs in one of two modes:
1. **No args** → MCP resource server (stdio, for FUSE reads)
2. **Subcommand** → CLI tool proxy (parses flags, proxies to upstream MCP)

```bash
mcpfs-linear                    # mode 1: MCP server (used by mcpfs)
mcpfs-linear --help             # list available tools
mcpfs-linear create_issue ...   # mode 2: execute a tool
```

## Write architecture

Two patterns for writes:
- **Proxy** (PostHog, GitHub, Stripe, Vercel, Linear): tool schemas captured from upstream HTTP/stdio MCP, embedded via `//go:embed tools.json`, proxied via `mcptool.HTTPCaller` or `mcptool.StdioCaller`.
- **Native CLI** (Docker, K8s): no upstream MCP exists. Use `docker` / `kubectl` directly. These servers are read-only.

All proxy servers follow the same pattern in main():
```go
//go:embed tools.json
var toolSchemas []byte

if len(os.Args) > 1 {
    var tools []mcptool.ToolDef
    json.Unmarshal(toolSchemas, &tools)
    caller := &mcptool.HTTPCaller{URL: mcpURL(), AuthHeader: "Bearer " + token}
    os.Exit(mcptool.Run("mcpfs-myservice", tools, caller, os.Args[1:]))
}
```

## Current tool counts

| Server | Tools | Transport |
|--------|-------|-----------|
| posthog | 67 | HTTP proxy |
| github | 43 | HTTP proxy |
| stripe | 28 | HTTP proxy |
| vercel | 12 | HTTP proxy |
| linear | 7 | stdio proxy |
| notion | 0 (needs OAuth) | HTTP proxy (wired) |
| docker | — | use `docker` CLI |
| k8s | — | use `kubectl` |

## Adding a new server

1. Create `servers/myservice/main.go`
2. Use `mcpserve.New("mcpfs-myservice", "0.1.0", readFunc)`
3. Add resources (static URIs) and templates (parameterized URIs)
4. `readFunc` switches on URI, calls API, returns formatted text
5. Build: `go build ./servers/myservice`
6. Test: `mcpfs /tmp/mnt -- ./mcpfs-myservice && cat /tmp/mnt/...`

## Adding CLI writes to a server

1. Capture tool schemas: `go run ./cmd/capture-tools -url <mcp-url> -auth "Bearer $KEY" -out servers/myservice/tools.json`
2. Add `//go:embed tools.json` + CLI dispatch block to main.go (see posthog/linear for pattern)
3. Test: `mcpfs-myservice tools` then `mcpfs-myservice <tool-name> --help`

## Testing

```bash
# Build all
go build ./...

# Mount and test (GitHub example)
mkdir -p /tmp/mnt/github
./mcpfs /tmp/mnt/github -- ./mcpfs-github
cat /tmp/mnt/github/repos
fusermount -u /tmp/mnt/github
```

## Migration from Claude Code plugins

`mcpfs migrate` scans `~/.claude/settings.json`, disables MCP plugins that mcpfs replaces.
- `bin/mcpfs-migrate` — shell script, dry-run by default, `--apply` to execute, `--undo` to revert
- Keeps plugins with skills (Notion, Stripe), disables pure-tool plugins (PostHog, GitHub, Linear, Vercel)

## Dependencies

- `github.com/hanwen/go-fuse/v2` — FUSE bindings
- `github.com/lib/pq` — PostgreSQL driver (postgres server only)
- No other external dependencies. HTTP clients use `net/http`.

