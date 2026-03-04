# Benchmarks

Compare mcpfs (filesystem) against CLI tools and raw MCP JSON-RPC.

## Run

```bash
# Mount some servers first
mkdir -p /tmp/mnt
mcpfs mount github /tmp/mnt/github -- mcpfs-github
mcpfs mount vercel /tmp/mnt/vercel -- mcpfs-vercel
mcpfs mount docker /tmp/mnt/docker -- mcpfs-docker

# Run benchmarks
./bench/bench_tokens.sh /tmp/mnt
./bench/bench_latency.sh /tmp/mnt 10
./bench/bench_compose.sh /tmp/mnt
```

## What's measured

| Benchmark | Measures | Key metric |
|-----------|----------|------------|
| `bench_tokens.sh` | Context cost (chars/tokens) per interface | Filesystem is 3-10x cheaper |
| `bench_latency.sh` | Read latency: cold, warm, CLI, MCP | API call dominates |
| `bench_compose.sh` | Lines of code for same task across interfaces | Unix pipes vs custom scripts |

## Expected results

**Tokens**: Filesystem reads return pre-formatted summaries. CLI returns full API JSON. For a typical "list repos" query, filesystem uses ~500 tokens vs ~5000 for raw API.

**Latency**: Dominated by upstream API calls (100-500ms). FUSE overhead adds <5ms. Cold MCP start (Go compile) adds ~2s but pre-compiled binary is <50ms.

**Composability**: Filesystem enables `grep`, `awk`, `comm`, `wc` across all services. Same cross-service queries require per-service CLI flags and custom scripting.
