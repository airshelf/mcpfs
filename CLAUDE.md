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

Each server binary runs in one of three modes:
1. **No args** → MCP resource server (stdio, for FUSE reads)
2. **Tool name** → CLI tool proxy (parses flags, calls upstream MCP)
3. **`tools`** → List available write operations

```bash
mcpfs-linear                    # mode 1: MCP server (used by mcpfs)
mcpfs-linear tools              # mode 3: list available tools
mcpfs-linear create_issue ...   # mode 2: execute a tool
```

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

## Dependencies

- `github.com/hanwen/go-fuse/v2` — FUSE bindings
- `github.com/lib/pq` — PostgreSQL driver (postgres server only)
- No other external dependencies. HTTP clients use `net/http`.
