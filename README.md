# mcpfs

Mount any MCP server as a filesystem. Plan 9 for the agent era.

<!-- TODO: asciinema demo GIF -->

## The problem

The MCP ecosystem has 18,000+ servers. They inject 30,000–125,000 tokens of tool schemas before your agent asks a single question. mcpfs takes the opposite approach: MCP servers expose **resources** (not tools), and mcpfs mounts them as **files**. Agents read files. No schemas, no function calls, no token bloat.

```
cat /mnt/github/repos          # your repos, one per line
cat /mnt/vercel/deployments    # latest deploys
cat /mnt/docker/containers     # running containers
grep error /mnt/*/             # search across everything
```

## Quick start

```bash
go install github.com/airshelf/mcpfs/cmd/mcpfs@latest
go install github.com/airshelf/mcpfs/servers/github@latest

mkdir -p /tmp/mnt/github
mcpfs /tmp/mnt/github -- mcpfs-github
cat /tmp/mnt/github/repos
```

## Why files, not tools?

| | MCP Tools | MCP Resources | CLI | **Filesystem** |
|---|---|---|---|---|
| Discovery cost | 30K–125K tokens (schemas) | ~500 tokens (URI list) | ~200 tokens (--help) | **0 tokens** (`ls`) |
| How agents call it | function_call JSON | resources/read | subprocess + parse | **`cat` / `read`** |
| Cross-service query | N tool calls, N parsers | N reads, N parsers | N commands, N flags | **`grep -r pattern /mnt/`** |
| Composability | None | None | Pipes, but per-tool flags | **Full Unix: grep, awk, jq, diff** |
| Auth surface | Per-tool permissions | Per-server token | Per-CLI login | **Per-mount env var** |

## Available servers

11 servers, each 250–380 lines of Go. Servers with captured tool schemas also support CLI writes.

| Server | Auth | Resources | Install |
|--------|------|-----------|---------|
| **mcpfs-github** | `GITHUB_TOKEN` | repos, issues, PRs, readme, actions, releases, notifications, gists + **4 CLI tools** | `go install .../servers/github@latest` |
| **mcpfs-vercel** | `VERCEL_TOKEN` | deployments, projects, env vars, domains, build/runtime logs + **4 CLI tools** | `go install .../servers/vercel@latest` |
| **mcpfs-docker** | Docker socket | containers, images, networks, volumes, logs, inspect + **3 CLI tools** | `go install .../servers/docker@latest` |
| **mcpfs-k8s** | `KUBECONFIG` | namespaces, pods, services, deployments, nodes, logs + **3 CLI tools** | `go install .../servers/k8s@latest` |
| **mcpfs-postgres** | `DATABASE_URL` | tables, schema, row counts, sample data, extensions, connections | `go install .../servers/postgres@latest` |
| **mcpfs-npm** | (none) | package info, versions, dependencies, maintainers, search | `go install .../servers/npm@latest` |
| **mcpfs-slack** | `SLACK_TOKEN` | channels, messages, threads, users, search | `go install .../servers/slack@latest` |
| **mcpfs-linear** | `LINEAR_API_KEY` | issues, projects, cycles, teams + **7 CLI tools** | `go install .../servers/linear@latest` |
| **mcpfs-posthog** | `POSTHOG_API_KEY` | dashboards, insights, events, feature flags + **67 CLI tools** | `go install .../servers/posthog@latest` |
| **mcpfs-stripe** | `STRIPE_API_KEY` | balance, charges, customers, products, subscriptions | `go install .../servers/stripe@latest` |
| **mcpfs-notion** | `NOTION_API_KEY` | databases, pages, search | `go install .../servers/notion@latest` |

### Filesystem tree (GitHub example)

```
/mnt/github/
├── repos                          # all repos (name, stars, language)
├── notifications                  # unread notifications
├── gists                          # your gists
├── repos/owner/repo/issues        # issues for a repo
├── repos/owner/repo/pulls         # pull requests
├── repos/owner/repo/readme        # README content
├── repos/owner/repo/actions       # workflow runs
└── repos/owner/repo/releases      # releases
```

## Cross-service examples

**What's broken?** — Cross-reference GitHub issues with Vercel deploy errors:
```bash
# Mount both services
mcpfs /tmp/mnt/github -- mcpfs-github
mcpfs /tmp/mnt/vercel -- mcpfs-vercel

# Find failing deploys and related issues
grep ERROR /tmp/mnt/vercel/deployments
grep -i deploy /tmp/mnt/github/repos/myorg/myapp/issues
```

**Grep everything** — Search across all mounted services:
```bash
grep -r "database" /tmp/mnt/
```

**Project health dashboard** — Combine signals from multiple sources:
```bash
echo "=== Deploys ===" && cat /tmp/mnt/vercel/deployments | head -5
echo "=== Containers ===" && cat /tmp/mnt/docker/containers
echo "=== Open Issues ===" && cat /tmp/mnt/github/repos/myorg/myapp/issues | wc -l
```

See [examples/](examples/) for complete scripts.

## Benchmarks

| Metric | Filesystem | CLI | Raw MCP |
|--------|-----------|-----|---------|
| Discovery tokens | ~0 (ls) | ~200 (--help) | ~500 (resources/list) |
| Read tokens (repos) | ~500 | ~5000 | ~500 + framing |
| Composability | grep, awk, jq, diff | per-tool flags | custom JSON-RPC |
| Cross-service search | `grep -r` | N scripts | N clients |

See [bench/](bench/) for runnable benchmarks.

## CLI writes (tool proxy)

Reads via filesystem, writes via CLI. Each server binary doubles as a tool proxy — MCP tool schemas are embedded and exposed as CLI flags.

```bash
# List available write operations (~50 tokens vs 20,000 for raw MCP schemas)
mcpfs-posthog tools
mcpfs-linear tools

# Execute a write
mcpfs-linear create_issue --teamId abc --title "Fix login bug"
mcpfs-posthog create-feature-flag --key my-flag --name "My Flag"
mcpfs-github create-issue --owner myorg --repo myapp --title "Bug report"
mcpfs-docker restart --id my-container
mcpfs-k8s scale --deployment api --replicas 3
mcpfs-vercel set-env --project myapp --key API_URL --value https://api.example.com

# Per-tool help
mcpfs-linear create_issue --help
```

How it works: tool schemas are captured once from upstream MCP servers (`cmd/capture-tools`), embedded via `//go:embed`, and exposed as CLI flags. Calls are proxied to the original MCP server (HTTP or stdio).

| Server | Transport | Tools | Status |
|--------|-----------|-------|--------|
| **mcpfs-posthog** | HTTP (mcp.posthog.com) | 67 | Captured |
| **mcpfs-linear** | stdio (@mseep/linear-mcp) | 7 | Captured |
| **mcpfs-stripe** | HTTP (mcp.stripe.com) | — | Wired, needs auth |
| **mcpfs-notion** | HTTP (mcp.notion.com) | — | Wired, needs auth |
| **mcpfs-github** | Direct REST API | 4 | Hand-written |
| **mcpfs-vercel** | Direct REST API | 4 | Hand-written |
| **mcpfs-docker** | Docker socket | 3 | Hand-written |
| **mcpfs-k8s** | kubectl | 3 | Hand-written |

### Capture tools for a new server

```bash
# HTTP MCP server
go run ./cmd/capture-tools -url https://mcp.posthog.com/mcp \
  -auth "Bearer $POSTHOG_API_KEY" -out servers/posthog/tools.json

# Stdio MCP server
go run ./cmd/capture-tools -cmd npx -args "@mseep/linear-mcp" \
  -out servers/linear/tools.json
```

## Write your own server

Each server is a Go program using the `mcpserve` framework:

```go
package main

import "github.com/airshelf/mcpfs/pkg/mcpserve"

func main() {
    s := mcpserve.New("my-server", "0.1.0", func(uri string) (mcpserve.ReadResult, error) {
        switch uri {
        case "myservice://status":
            return mcpserve.ReadResult{Text: "all good"}, nil
        default:
            return mcpserve.ReadResult{}, fmt.Errorf("unknown: %s", uri)
        }
    })
    s.AddResource(mcpserve.Resource{
        URI:  "myservice://status",
        Name: "status",
    })
    s.Serve()
}
```

Mount it: `mcpfs /tmp/mnt/myservice -- my-server`

## How it works

```
 Agent / Shell                    mcpfs (FUSE)                MCP Server
┌─────────────┐              ┌──────────────────┐         ┌─────────────┐
│ cat repos   │─── read() ──▶│ FUSE → mcpclient │── RPC ─▶│ mcpserve    │
│ grep error  │              │ stdio JSON-RPC   │         │ resources/  │
│ ls /mnt/    │◀── bytes ───│ cache + format   │◀─ JSON ─│ read        │──▶ API
└─────────────┘              └──────────────────┘         └─────────────┘
```

1. `mcpfs` launches the MCP server as a child process (stdio transport)
2. On mount, it calls `resources/list` and `resources/templates/list` to build the directory tree
3. File reads trigger `resources/read` calls — responses become file content
4. Standard FUSE: works with any program that reads files

## Requirements

- Go 1.22+
- FUSE 3 (`libfuse3-dev` on Debian/Ubuntu, `macfuse` on macOS)
- Auth tokens for the services you want to mount (see table above)

## Project structure

```
cmd/mcpfs/          # FUSE mount CLI
cmd/capture-tools/  # Capture MCP tool schemas (HTTP + stdio)
pkg/mcpserve/       # MCP resource server framework (shared by all servers)
pkg/mcpclient/      # MCP client (JSON-RPC over stdio)
pkg/mcptool/        # Tool schema → CLI bridge (dispatch, HTTP/stdio callers)
internal/fuse/      # FUSE filesystem implementation
servers/            # 11 MCP resource servers (read via FUSE, write via CLI)
  github/           # GitHub REST API
  vercel/           # Vercel REST API
  docker/           # Docker Engine API (unix socket)
  k8s/              # Kubernetes via kubectl
  postgres/         # PostgreSQL via database/sql
  npm/              # NPM registry API
  slack/            # Slack Web API
  linear/           # Linear GraphQL API
  posthog/          # PostHog HTTP MCP proxy
  stripe/           # Stripe HTTP MCP proxy
  notion/           # Notion HTTP MCP proxy
examples/           # Cross-service shell scripts
bench/              # Benchmarks (tokens, latency, composability)
```

## License

MIT
