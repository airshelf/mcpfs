# mcpfs

Mount any MCP server as a filesystem. Reads via `cat`, writes via CLI. Plan 9 for the agent era.

<!-- TODO: asciinema demo GIF -->

## The problem

The MCP ecosystem has 18,000+ servers. They inject 30,000–125,000 tokens of tool schemas before your agent asks a single question. Native CLIs (`gh`, `stripe`, `kubectl`) are great for their own service but each speaks a different language — different flags, different auth, different output formats. And many services (PostHog, Linear, Notion) have no CLI at all.

mcpfs takes the opposite approach: **filesystem for reads, CLI for writes, pipes for composition**.

```bash
# Reads — just cat | jq
cat /mnt/mcpfs/posthog/dashboards.json | jq '.[].name'
cat /mnt/mcpfs/stripe/balance.json | jq '.available[].amount'
cat /mnt/mcpfs/linear/issues.json | jq '.[].title'

# Writes — CLI proxy to upstream MCP servers
mcpfs-posthog create-feature-flag --key my-flag --name "My Flag" --active --description "test" --filters '{}'
mcpfs-github create_pull_request --owner myorg --repo myapp --title "Fix" --head feature --base main
mcpfs-stripe create_customer --name "Acme Corp" --email acme@example.com

# Composition — the killer feature
comm -23 \
  <(cat /mnt/mcpfs/stripe/customers.json | jq -r '.[].email' | sort) \
  <(cat /mnt/mcpfs/posthog/events.json | jq -r '.[].distinct_id' | sort)
# → "paying customers with no PostHog activity"
```

## Why mcpfs?

Four ways to query a SaaS API. Here's how they compare:

| | **mcpfs** | MCP tools | curl / API | Native CLIs |
|---|---|---|---|---|
| **Discovery** | `ls` (0 tokens) | 100+ schemas (~20K tokens) | read API docs | `--help` per CLI |
| **Query language** | `jq` (one, universal) | per-tool params | per-API (REST, GraphQL) | different flags per CLI |
| **Output format** | JSON (always) | varies | varies | some `--json`, some don't |
| **Auth** | pre-configured env vars | pre-configured | remember per service | `login` per CLI |
| **Composition** | `|` pipes, `comm`, `diff`, `jq` | impossible (LLM joins) | possible but painful | possible but mixed formats |
| **Coverage** | all services | all services | all services | **PostHog, Linear, Notion have no CLI** |
| **Install** | one FUSE mount | comes with Claude | `curl` exists | 5+ separate installs |

**Where mcpfs wins outright:**
- Services with **no CLI** (PostHog, Linear, Notion) — mcpfs is the only option besides raw API calls
- **Cross-service composition** — `comm -23 <(stripe) <(posthog)` is impossible with any single CLI
- **Agent context cost** — filesystem `ls` costs 0 tokens vs 20,000 tokens for MCP tool schemas

**Where native CLIs are fine:**
- `gh` — excellent JSON support, mature, well-documented
- `kubectl` — battle-tested, `-o json` works great
- `docker` — native, good `--format` support

mcpfs doesn't replace `gh` or `kubectl`. It **normalizes everything into one interface** so pipes work across all services — and fills the CLI gap for services that don't have one.

## Quick start

```bash
# Build and install
go install github.com/airshelf/mcpfs/cmd/mcpfs@latest
go install github.com/airshelf/mcpfs/servers/github@latest

# Mount
mkdir -p /mnt/mcpfs/github
mcpfs /mnt/mcpfs/github -- mcpfs-github

# Read
cat /mnt/mcpfs/github/repos.json | jq '.[].full_name'

# Write
mcpfs-github create_pull_request --help
```

## Available servers

11 servers, each 200–400 lines of Go.

| Server | Auth | Reads (filesystem) | Writes (CLI) | Upstream |
|--------|------|--------------------|--------------|----------|
| **mcpfs-posthog** | `POSTHOG_API_KEY` | dashboards, insights, events, feature flags, experiments, surveys, cohorts, errors | 67 tools (proxy) | mcp.posthog.com |
| **mcpfs-github** | `GITHUB_TOKEN` | repos, issues, PRs, readme, actions, releases, notifications, gists | 43 tools (proxy) | api.githubcopilot.com |
| **mcpfs-stripe** | `STRIPE_API_KEY` | balance, customers, products, prices, subscriptions, invoices, charges, events | 28 tools (proxy) | mcp.stripe.com |
| **mcpfs-vercel** | `VERCEL_TOKEN` | deployments, projects, env vars, domains, build/runtime logs | 12 tools (proxy) | mcp.vercel.com |
| **mcpfs-linear** | `LINEAR_API_KEY` | issues, projects, cycles, teams | 7 tools (proxy) | stdio: @mseep/linear-mcp |
| **mcpfs-notion** | `NOTION_TOKEN` | databases, pages, search, users | proxy ready (needs OAuth) | mcp.notion.com |
| **mcpfs-docker** | Docker socket | containers, images, networks, volumes, logs, stats | use `docker` CLI | — |
| **mcpfs-k8s** | `KUBECONFIG` | namespaces, pods, services, deployments, nodes, logs | use `kubectl` | — |
| **mcpfs-postgres** | `DATABASE_URL` | tables, schema, row counts, sample data, extensions | — | — |
| **mcpfs-npm** | (none) | package info, versions, dependencies, downloads, search | — | — |
| **mcpfs-slack** | `SLACK_TOKEN` | channels, messages, threads, users, search | — | — |

**Architecture:**
- **Reads**: custom-built resource servers. Direct API calls, slim JSON, mounted as FUSE files.
- **Writes (proxy)**: tool schemas captured from upstream MCP servers, embedded via `//go:embed`, proxied through CLI. Agent sees `--help` (~50 tokens) instead of full schemas (~20,000 tokens).
- **Writes (native)**: Docker and K8s have no upstream MCP — use their native CLIs (`docker`, `kubectl`).

## Cross-service composition

**Business dashboard in one command:**
```bash
printf "%-20s %s\n" "Stripe balance" "$(cat /mnt/mcpfs/stripe/balance.json | jq -r '.available[] | "\(.currency) $\(.amount / 100)"')"
printf "%-20s %s\n" "Active subs" "$(cat /mnt/mcpfs/stripe/subscriptions.json | jq '[.[] | select(.status=="active")] | length')"
printf "%-20s %s\n" "MRR" "\$$(cat /mnt/mcpfs/stripe/subscriptions.json | jq '[.[] | select(.status=="active") | .items.data[0].price.unit_amount] | add / 100')/mo"
printf "%-20s %s\n" "PH events tracked" "$(cat /mnt/mcpfs/posthog/events.json | jq length)"
printf "%-20s %s\n" "Linear issues" "$(cat /mnt/mcpfs/linear/issues.json | jq length)"
printf "%-20s %s\n" "Linear urgent" "$(cat /mnt/mcpfs/linear/issues.json | jq '[.[] | select(.priorityLabel=="Urgent")] | length')"
```

**Find paying customers with no analytics activity:**
```bash
comm -23 \
  <(cat /mnt/mcpfs/stripe/customers.json | jq -r '.[].email' | sort) \
  <(cat /mnt/mcpfs/posthog/events.json | jq -r '.[].distinct_id' | sort)
```

**Linear issues vs PostHog feature flags alignment:**
```bash
echo "Issues in progress:"
cat /mnt/mcpfs/linear/issues.json | jq -r '.[] | select(.state.name == "In Progress") | .title'
echo "Active feature flags:"
cat /mnt/mcpfs/posthog/feature-flags.json | jq -r '.[] | select(.active) | .key'
```

**Compose with native CLIs** — pipes don't care where data comes from:
```bash
# gh CLI output + mcpfs in one pipeline
gh pr list --repo myorg/myapp --json title --jq '.[].title' > /tmp/prs.txt
cat /mnt/mcpfs/linear/issues.json | jq -r '.[].title' > /tmp/issues.txt
comm -23 <(sort /tmp/prs.txt) <(sort /tmp/issues.txt)
# → "PRs with no matching Linear issue"
```

## CLI writes

Each server binary doubles as a tool proxy. No args → MCP resource server (for FUSE reads). Subcommand → CLI (for writes).

```bash
# List all available tools
mcpfs-posthog --help                                    # 67 tools
mcpfs-github --help                                     # 43 tools
mcpfs-stripe --help                                     # 28 tools

# Per-tool help
mcpfs-posthog create-feature-flag --help

# Execute a write
mcpfs-posthog create-feature-flag --key my-flag --name "My Flag" --active --description "test" --filters '{}'
mcpfs-github create_pull_request --owner myorg --repo myapp --title "Fix bug" --head feature --base main
mcpfs-stripe create_customer --name "Acme Corp" --email acme@example.com
mcpfs-linear create_issue --teamId abc --title "Fix login bug"
```

### Capture tools for a new server

```bash
# HTTP MCP server
go run ./cmd/capture-tools -url https://mcp.posthog.com/mcp \
  -auth "Bearer $POSTHOG_API_KEY" -out servers/posthog/tools.json

# Stdio MCP server
go run ./cmd/capture-tools -cmd npx -args "@mseep/linear-mcp" \
  -out servers/linear/tools.json
```

### Mounting all servers

```bash
source bin/mcpfs-env    # load auth tokens
bin/mcpfs-mount         # mount all servers at /mnt/mcpfs/

ls /mnt/mcpfs/
# github/  linear/  npm/  posthog/  stripe/  ...
```

### Migrating from Claude Code MCP plugins

If you're using Claude Code with MCP plugins (PostHog, GitHub, Linear, etc.), mcpfs can replace them:

```bash
mcpfs migrate          # preview: shows which plugins would be disabled
mcpfs migrate --apply  # disable plugins, save ~25,000 tokens/conversation
mcpfs migrate --undo   # restore from backup
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
            return mcpserve.ReadResult{Text: `{"status": "ok"}`, MimeType: "application/json"}, nil
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

Mount it: `mcpfs /mnt/mcpfs/myservice -- my-server`

## How it works

```
 Agent / Shell                    mcpfs (FUSE)                MCP Server
┌─────────────┐              ┌──────────────────┐         ┌─────────────┐
│ cat repos   │─── read() ──▶│ FUSE → mcpclient │── RPC ─▶│ mcpserve    │
│ jq '.name'  │              │ stdio JSON-RPC   │         │ resources/  │
│ ls /mnt/    │◀── bytes ───│ cache + format   │◀─ JSON ─│ read        │──▶ API
└─────────────┘              └──────────────────┘         └─────────────┘

 Agent / Shell                    Server binary
┌─────────────┐              ┌──────────────────┐         ┌─────────────┐
│ mcpfs-posthog│── flags ──▶│ parse CLI flags  │── RPC ─▶│ upstream    │
│ create-flag │              │ tools.json embed │         │ MCP server  │
│ --key x     │◀── JSON ───│ HTTPCaller proxy │◀─ JSON ─│ tools/call  │
└─────────────┘              └──────────────────┘         └─────────────┘
```

1. **Reads**: `mcpfs` launches the server as a child process (stdio). On mount, calls `resources/list` to build the directory tree. File reads trigger `resources/read` — responses become file content.
2. **Writes**: Server binary parses CLI flags against embedded tool schemas. Proxies `tools/call` to upstream MCP server (HTTP or stdio). Response printed as JSON.

## Project structure

```
cmd/mcpfs/          # FUSE mount CLI
cmd/capture-tools/  # Capture MCP tool schemas (HTTP + stdio)
pkg/mcpserve/       # MCP resource server framework (shared by all servers)
pkg/mcpclient/      # MCP client (JSON-RPC over stdio)
pkg/mcptool/        # Tool schema → CLI bridge (dispatch, HTTP/stdio callers)
internal/fuse/      # FUSE filesystem implementation
servers/            # 11 MCP resource servers
bin/mcpfs-mount     # Mount all servers at /mnt/mcpfs/
bin/mcpfs-env       # Auth token loader
bin/mcpfs-migrate   # Migrate from Claude Code MCP plugins to mcpfs
```

## Requirements

- Go 1.22+
- FUSE 3 (`libfuse3-dev` on Debian/Ubuntu, `macfuse` on macOS)
- Auth tokens for the services you want to mount (see table above)

## License

MIT
