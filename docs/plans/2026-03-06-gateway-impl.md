# mcpfs Gateway Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make mcpfs mount ANY MCP server as a filesystem by auto-classifying tools into read-only files and write-only CLI commands.

**Architecture:** On mount, mcpfs calls `tools/list` alongside `resources/list`. Tools are classified by name pattern + required params: list tools → `.json` files, get-by-id tools → template directories, write tools → CLI only. Tool-backed files call `tools/call` lazily on `cat`. HTTP MCP servers are supported via a new client wrapper around the existing `HTTPCaller`.

**Tech Stack:** Go 1.24, go-fuse/v2, JSON-RPC (stdio + HTTP)

---

### Task 1: Tool Classification

**Files:**
- Create: `internal/toolfs/classify.go`
- Create: `internal/toolfs/classify_test.go`

**Step 1: Write the tests**

Create `internal/toolfs/classify_test.go`:

```go
package toolfs

import (
	"encoding/json"
	"testing"
)

func schema(props map[string]string, required []string) json.RawMessage {
	p := make(map[string]interface{})
	for k, v := range props {
		p[k] = map[string]string{"type": v}
	}
	s := map[string]interface{}{"type": "object", "properties": p}
	if len(required) > 0 {
		s["required"] = required
	}
	b, _ := json.Marshal(s)
	return b
}

func TestClassifyTool(t *testing.T) {
	cases := []struct {
		name     string
		schema   json.RawMessage
		want     ToolClass
	}{
		{"dashboards-get-all", schema(nil, nil), ToolList},
		{"list_customers", schema(nil, nil), ToolList},
		{"feature-flag-get-all", schema(nil, nil), ToolList},
		{"dashboard-get", schema(map[string]string{"dashboardId": "string"}, []string{"dashboardId"}), ToolGet},
		{"retrieve_customer", schema(map[string]string{"customer_id": "string"}, []string{"customer_id"}), ToolGet},
		{"dashboard-create", schema(map[string]string{"name": "string"}, []string{"name"}), ToolWrite},
		{"update-feature-flag", schema(nil, nil), ToolWrite},
		{"delete_customer", schema(nil, nil), ToolWrite},
		{"search_issues", schema(map[string]string{"query": "string"}, []string{"query"}), ToolQuery},
		{"query-run", schema(nil, nil), ToolQuery},
		{"retrieve_balance", schema(nil, nil), ToolList},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyTool(tc.name, tc.schema)
			if got != tc.want {
				t.Errorf("ClassifyTool(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestToolToFilename(t *testing.T) {
	cases := []struct {
		name  string
		class ToolClass
		want  string
	}{
		{"dashboards-get-all", ToolList, "dashboards.json"},
		{"list_customers", ToolList, "customers.json"},
		{"feature-flag-get-all", ToolList, "feature-flags.json"},
		{"actions-get-all", ToolList, "actions.json"},
		{"retrieve_balance", ToolList, "balance.json"},
		{"dashboard-get", ToolGet, "dashboards"},
		{"retrieve_customer", ToolGet, "customers"},
		{"get_issue", ToolGet, "issues"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ToolToFilename(tc.name, tc.class)
			if got != tc.want {
				t.Errorf("ToolToFilename(%q, %v) = %q, want %q", tc.name, tc.class, got, tc.want)
			}
		})
	}
}

func TestRequiredParams(t *testing.T) {
	s := schema(map[string]string{"id": "string", "name": "string"}, []string{"id"})
	got := RequiredParams(s)
	if len(got) != 1 || got[0] != "id" {
		t.Errorf("RequiredParams = %v, want [id]", got)
	}
}
```

**Step 2: Run tests — verify they fail**

```bash
cd ~/src/mcpfs && go test ./internal/toolfs/ -v
```
Expected: compilation error (package doesn't exist yet)

**Step 3: Implement classification**

Create `internal/toolfs/classify.go`:

```go
// Package toolfs classifies MCP tools into filesystem-mountable categories.
package toolfs

import (
	"encoding/json"
	"strings"
)

type ToolClass int

const (
	ToolList  ToolClass = iota // No required params, read-only → static file
	ToolGet                    // Has required params, read-only → template dir
	ToolWrite                  // Mutating → CLI only
	ToolQuery                  // Search/query → CLI only
)

func (c ToolClass) String() string {
	switch c {
	case ToolList:
		return "list"
	case ToolGet:
		return "get"
	case ToolWrite:
		return "write"
	case ToolQuery:
		return "query"
	}
	return "unknown"
}

// ClassifyTool determines how a tool should be exposed in the filesystem.
func ClassifyTool(name string, inputSchema json.RawMessage) ToolClass {
	lower := strings.ToLower(name)

	if matchesAny(lower, "create", "update", "delete", "remove", "add", "set", "patch", "put", "post") {
		return ToolWrite
	}
	if matchesAny(lower, "search", "query", "find", "run", "execute") {
		return ToolQuery
	}

	required := RequiredParams(inputSchema)
	if len(required) == 0 {
		return ToolList
	}

	if matchesAny(lower, "get", "retrieve", "read", "show", "describe") {
		return ToolGet
	}

	return ToolWrite // safe default
}

// ToolToFilename converts a tool name to a filesystem name.
// List tools → "resource.json", Get tools → "resources" (directory).
func ToolToFilename(name string, class ToolClass) string {
	resource := stripVerb(name)
	if class == ToolList {
		return resource + ".json"
	}
	// Get tools: pluralize for directory name
	return pluralize(resource)
}

// RequiredParams extracts required parameter names from a JSON Schema.
func RequiredParams(schema json.RawMessage) []string {
	if schema == nil {
		return nil
	}
	var s struct {
		Required []string `json:"required"`
	}
	json.Unmarshal(schema, &s)
	return s.Required
}

func matchesAny(name string, verbs ...string) bool {
	for _, v := range verbs {
		if strings.Contains(name, v) {
			return true
		}
	}
	return false
}

func stripVerb(name string) string {
	// Normalize separator to hyphen
	n := strings.ReplaceAll(name, "_", "-")

	// Strip suffixes: -get-all, -list, -get, -retrieve
	for _, suffix := range []string{"-get-all", "-list", "-retrieve", "-get"} {
		if strings.HasSuffix(n, suffix) {
			return strings.TrimSuffix(n, suffix)
		}
	}

	// Strip prefixes: list-, get-all-, get-, retrieve-
	for _, prefix := range []string{"list-", "get-all-", "retrieve-all-", "get-", "retrieve-"} {
		if strings.HasPrefix(n, prefix) {
			return strings.TrimPrefix(n, prefix)
		}
	}

	return n
}

func pluralize(s string) string {
	if s == "" {
		return s
	}
	if strings.HasSuffix(s, "s") {
		return s
	}
	return s + "s"
}
```

**Step 4: Run tests — verify they pass**

```bash
cd ~/src/mcpfs && go test ./internal/toolfs/ -v
```
Expected: all PASS

**Step 5: Commit**

```bash
cd ~/src/mcpfs && git add internal/toolfs/ && git commit -m "feat: tool classification for gateway (list/get/write/query)"
```

---

### Task 2: Tool-backed FUSE Nodes

**Files:**
- Create: `internal/fuse/toolnode.go`
- Modify: `internal/fuse/fs.go:18-23` (add `ToolCaller` interface to `mcpFS`)

**Step 1: Write toolnode.go**

This file adds `toolFileNode` — a FUSE file that calls `tools/call` on read instead of `resources/read`.

```go
package fuse

import (
	"context"
	"encoding/json"
	"log"
	"syscall"

	gofuse "github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// ToolCaller executes an MCP tools/call.
type ToolCaller interface {
	CallTool(name string, args map[string]interface{}) (json.RawMessage, error)
}

// toolFileNode is a FUSE file backed by a tools/call invocation.
type toolFileNode struct {
	gofuse.Inode
	fsys     *mcpFS
	toolName string
	args     map[string]interface{}
}

var _ = (gofuse.NodeOpener)((*toolFileNode)(nil))
var _ = (gofuse.NodeGetattrer)((*toolFileNode)(nil))
var _ = (gofuse.NodeReader)((*toolFileNode)(nil))

func (f *toolFileNode) readData() ([]byte, error) {
	result, err := f.fsys.toolCaller.CallTool(f.toolName, f.args)
	if err != nil {
		return nil, err
	}
	// Pretty-print JSON
	var v interface{}
	if json.Unmarshal(result, &v) == nil {
		out, _ := json.MarshalIndent(v, "", "  ")
		return append(out, '\n'), nil
	}
	return append([]byte(result), '\n'), nil
}

func (f *toolFileNode) Getattr(ctx context.Context, fh gofuse.FileHandle, out *fuse.AttrOut) syscall.Errno {
	data, err := f.readData()
	if err != nil {
		log.Printf("mcpfs: tool getattr %s: %v", f.toolName, err)
		out.Size = 0
	} else {
		out.Size = uint64(len(data))
	}
	out.Mode = syscall.S_IFREG | 0444
	return 0
}

func (f *toolFileNode) Open(ctx context.Context, flags uint32) (gofuse.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

func (f *toolFileNode) Read(ctx context.Context, fh gofuse.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data, err := f.readData()
	if err != nil {
		log.Printf("mcpfs: tool read %s: %v", f.toolName, err)
		return nil, syscall.EIO
	}
	if off >= int64(len(data)) {
		return fuse.ReadResultData(nil), 0
	}
	end := off + int64(len(dest))
	if end > int64(len(data)) {
		end = int64(len(data))
	}
	return fuse.ReadResultData(data[off:end]), 0
}
```

**Step 2: Add `toolCaller` field to `mcpFS`**

In `internal/fuse/fs.go:18-23`, add `toolCaller ToolCaller` to the `mcpFS` struct:

```go
type mcpFS struct {
	gofuse.Inode
	client     *mcpclient.Client
	toolCaller ToolCaller
	scheme     string
	tree       *fsTree
}
```

**Step 3: Run existing tests — verify nothing broke**

```bash
cd ~/src/mcpfs && go test ./internal/fuse/ -v
```
Expected: all existing tests PASS

**Step 4: Commit**

```bash
cd ~/src/mcpfs && git add internal/fuse/toolnode.go internal/fuse/fs.go && git commit -m "feat: tool-backed FUSE file nodes (tools/call on cat)"
```

---

### Task 3: Build Tool Tree and Merge into Resource Tree

**Files:**
- Create: `internal/toolfs/tree.go`
- Create: `internal/toolfs/tree_test.go`
- Modify: `internal/fuse/fs.go:26-36` (add tool fields to `fsTree`)
- Modify: `internal/fuse/fs.go:246-263` (handle tool-backed files in `buildInode`)
- Modify: `internal/fuse/fs.go:370-425` (`Mount` function — call tools/list, merge trees)

**Step 1: Write tree_test.go**

```go
package toolfs

import (
	"encoding/json"
	"testing"
)

type testTool struct {
	Name        string
	InputSchema json.RawMessage
}

func TestBuildToolTree(t *testing.T) {
	tools := []ToolEntry{
		{Name: "dashboards-get-all", Class: ToolList, Filename: "dashboards.json", ToolName: "dashboards-get-all"},
		{Name: "dashboard-get", Class: ToolGet, Filename: "dashboards", ToolName: "dashboard-get",
			RequiredParams: []string{"dashboardId"}},
		{Name: "actions-get-all", Class: ToolList, Filename: "actions.json", ToolName: "actions-get-all"},
	}

	tree := BuildToolTree(tools)

	if _, ok := tree["dashboards.json"]; !ok {
		t.Error("missing dashboards.json")
	}
	if _, ok := tree["actions.json"]; !ok {
		t.Error("missing actions.json")
	}
	dash, ok := tree["dashboards"]
	if !ok {
		t.Fatal("missing dashboards dir")
	}
	if !dash.IsDir {
		t.Error("dashboards should be a dir for get-by-id")
	}
	if dash.ToolName != "dashboard-get" {
		t.Errorf("dashboards.ToolName = %q, want dashboard-get", dash.ToolName)
	}
}

func TestClassifyAndBuild(t *testing.T) {
	raw := []struct {
		Name        string          `json:"name"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}{
		{"dashboards-get-all", schema(nil, nil)},
		{"dashboard-get", schema(map[string]string{"dashboardId": "string"}, []string{"dashboardId"})},
		{"dashboard-create", schema(map[string]string{"name": "string"}, []string{"name"})},
	}

	var entries []ToolEntry
	for _, r := range raw {
		class := ClassifyTool(r.Name, r.InputSchema)
		if class == ToolWrite || class == ToolQuery {
			continue
		}
		entries = append(entries, ToolEntry{
			Name:           r.Name,
			Class:          class,
			Filename:       ToolToFilename(r.Name, class),
			ToolName:       r.Name,
			RequiredParams: RequiredParams(r.InputSchema),
		})
	}

	tree := BuildToolTree(entries)

	if _, ok := tree["dashboards.json"]; !ok {
		t.Error("missing dashboards.json (list tool)")
	}
	if _, ok := tree["dashboards"]; !ok {
		t.Error("missing dashboards/ dir (get tool)")
	}
	// create tool should not be in tree
	if len(tree) != 2 {
		t.Errorf("tree has %d entries, want 2", len(tree))
	}
}
```

**Step 2: Run tests — verify they fail**

```bash
cd ~/src/mcpfs && go test ./internal/toolfs/ -v -run TestBuildToolTree
```
Expected: compilation error (ToolEntry, BuildToolTree don't exist)

**Step 3: Implement tree.go**

Create `internal/toolfs/tree.go`:

```go
package toolfs

// ToolEntry is a classified tool ready for filesystem mounting.
type ToolEntry struct {
	Name           string
	Class          ToolClass
	Filename       string   // e.g. "dashboards.json" or "dashboards"
	ToolName       string   // original MCP tool name for tools/call
	RequiredParams []string // param names for get-by-id tools
}

// ToolTreeNode represents a tool-backed entry in the filesystem.
type ToolTreeNode struct {
	IsDir          bool
	ToolName       string   // MCP tool name
	RequiredParams []string // for get-by-id template dirs
}

// BuildToolTree creates a flat map of filename → ToolTreeNode.
// List tools become files, Get tools become template directories.
func BuildToolTree(entries []ToolEntry) map[string]*ToolTreeNode {
	tree := make(map[string]*ToolTreeNode)
	for _, e := range entries {
		switch e.Class {
		case ToolList:
			tree[e.Filename] = &ToolTreeNode{ToolName: e.ToolName}
		case ToolGet:
			tree[e.Filename] = &ToolTreeNode{
				IsDir:          true,
				ToolName:       e.ToolName,
				RequiredParams: e.RequiredParams,
			}
		}
	}
	return tree
}
```

**Step 4: Run tests — verify they pass**

```bash
cd ~/src/mcpfs && go test ./internal/toolfs/ -v
```
Expected: all PASS

**Step 5: Add tool fields to fsTree and wire into Mount**

In `internal/fuse/fs.go`, add fields to `fsTree`:

```go
type fsTree struct {
	children       map[string]*fsTree
	isDir          bool
	uri            string
	template       string
	param          string
	leafName       string
	nestedParam    string
	nestedChildren map[string]*fsTree
	nestedLeaf     *fsTree
	// Tool-backed fields (gateway)
	toolName       string   // MCP tool name for tools/call
	toolParams     []string // required params for get-by-id
}
```

In `buildInode`, after the `uri` check, add tool-backed file handling:

```go
func (d *dirNode) buildInode(ctx context.Context, name string, t *fsTree, out *fuse.EntryOut) (*gofuse.Inode, syscall.Errno) {
	if t.isDir {
		out.Mode = syscall.S_IFDIR | 0555
		out.Nlink = 2
		dn := &dirNode{
			fsys:        d.fsys,
			tree:        t,
			paramValues: copyParams(d.paramValues),
		}
		return d.NewInode(ctx, dn, gofuse.StableAttr{Mode: syscall.S_IFDIR}), 0
	}

	// Tool-backed file
	if t.toolName != "" && d.fsys.toolCaller != nil {
		args := make(map[string]interface{})
		for k, v := range d.paramValues {
			args[k] = v
		}
		out.Mode = syscall.S_IFREG | 0444
		fn := &toolFileNode{fsys: d.fsys, toolName: t.toolName, args: args}
		return d.NewInode(ctx, fn, gofuse.StableAttr{Mode: syscall.S_IFREG}), 0
	}

	// Resource-backed file
	uri := resolveURI(t.uri, d.paramValues)
	out.Mode = syscall.S_IFREG | 0444
	fn := &fileNode{fsys: d.fsys, uri: uri}
	return d.NewInode(ctx, fn, gofuse.StableAttr{Mode: syscall.S_IFREG}), 0
}
```

Modify `Mount` to call `tools/list`, classify, and merge:

```go
func Mount(mountpoint string, client *mcpclient.Client, debug bool) error {
	resources, err := client.ListResources()
	if err != nil {
		// Some servers only have tools, not resources — that's fine
		resources = nil
	}
	templates, err := client.ListResourceTemplates()
	if err != nil {
		templates = nil
	}

	// Discover tools
	var toolEntries []toolfs.ToolEntry
	toolsRaw, err := client.ListTools()
	if err == nil && toolsRaw != nil {
		var tools []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"inputSchema"`
		}
		if json.Unmarshal(toolsRaw, &tools) == nil {
			for _, t := range tools {
				class := toolfs.ClassifyTool(t.Name, t.InputSchema)
				if class == toolfs.ToolWrite || class == toolfs.ToolQuery {
					continue
				}
				toolEntries = append(toolEntries, toolfs.ToolEntry{
					Name:           t.Name,
					Class:          class,
					Filename:       toolfs.ToolToFilename(t.Name, class),
					ToolName:       t.Name,
					RequiredParams: toolfs.RequiredParams(t.InputSchema),
				})
			}
		}
	}

	if len(resources) == 0 && len(templates) == 0 && len(toolEntries) == 0 {
		return fmt.Errorf("server has no resources or tools")
	}

	scheme := "mcp"
	if len(resources) > 0 {
		if idx := strings.Index(resources[0].URI, "://"); idx > 0 {
			scheme = resources[0].URI[:idx]
		}
	} else if len(templates) > 0 {
		if idx := strings.Index(templates[0].URITemplate, "://"); idx > 0 {
			scheme = templates[0].URITemplate[:idx]
		}
	}

	tree := BuildTree(scheme, resources, templates)

	// Merge tool-backed entries (tools fill gaps — don't override resources)
	toolTree := toolfs.BuildToolTree(toolEntries)
	for name, node := range toolTree {
		if _, exists := tree.children[name]; exists {
			continue // resource takes priority
		}
		child := newFSTree()
		child.toolName = node.ToolName
		if node.IsDir {
			child.isDir = true
			child.template = "tool"  // marker
			child.param = node.RequiredParams[0]
			child.toolParams = node.RequiredParams
		}
		tree.children[name] = child
	}

	root := &dirNode{
		fsys: &mcpFS{
			client:     client,
			toolCaller: client, // Client implements ToolCaller via CallTool
			scheme:     scheme,
			tree:       tree,
		},
		tree:        tree,
		paramValues: make(map[string]string),
	}

	fmt.Fprintf(os.Stderr, "mcpfs: mounting %s:// at %s (%d resources, %d templates, %d tools)\n",
		scheme, mountpoint, len(resources), len(templates), len(toolEntries))

	opts := &gofuse.Options{
		MountOptions: fuse.MountOptions{
			FsName: "mcpfs",
			Name:   scheme,
			Debug:  debug,
		},
	}

	server, err := gofuse.Mount(mountpoint, root, opts)
	if err != nil {
		return fmt.Errorf("mount: %w", err)
	}

	server.Wait()
	return nil
}
```

Add import for `toolfs` and `encoding/json` at top of `fs.go`:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"syscall"

	"github.com/airshelf/mcpfs/internal/toolfs"
	"github.com/airshelf/mcpfs/pkg/mcpclient"
	gofuse "github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)
```

**Step 6: Handle tool-backed template directories in lookupTemplateChild**

When a tool-backed directory (ToolGet) receives a lookup for a param value, it should create a file node that calls the tool with that param:

In `lookupTemplateChild`, add handling for tool-backed templates after the existing logic. When `d.tree.toolName != ""` and there are no resource-based children:

```go
func (d *dirNode) lookupTemplateChild(ctx context.Context, name string, out *fuse.EntryOut) (*gofuse.Inode, syscall.Errno) {
	params := copyParams(d.paramValues)
	params[d.tree.param] = name

	// Tool-backed template: param value → file with tools/call
	if d.tree.toolName != "" && d.fsys.toolCaller != nil && len(d.tree.children) == 0 {
		args := make(map[string]interface{})
		for k, v := range params {
			args[k] = v
		}
		out.Mode = syscall.S_IFREG | 0444
		fn := &toolFileNode{
			fsys:     d.fsys,
			toolName: d.tree.toolName,
			args:     args,
		}
		return d.NewInode(ctx, fn, gofuse.StableAttr{Mode: syscall.S_IFREG}), 0
	}

	// Existing resource-backed template logic...
	childTree := newFSTree()
	childTree.isDir = true
	// ... (rest unchanged)
```

**Step 7: Verify Client implements ToolCaller**

The `mcpclient.Client` already has `CallTool(name string, args map[string]interface{}) (json.RawMessage, error)` at `client.go:183`. This matches the `ToolCaller` interface defined in `toolnode.go`. No changes needed.

**Step 8: Run all tests**

```bash
cd ~/src/mcpfs && go test ./... -v
```
Expected: all PASS

**Step 9: Commit**

```bash
cd ~/src/mcpfs && git add internal/toolfs/tree.go internal/toolfs/tree_test.go internal/fuse/fs.go && git commit -m "feat: merge tool tree into resource tree, tool-backed file reads"
```

---

### Task 4: Config File Parsing

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

**Step 1: Write config_test.go**

```go
package config

import (
	"os"
	"testing"
)

func TestParseConfig(t *testing.T) {
	data := `{
		"posthog": {
			"type": "http",
			"url": "https://mcp.posthog.com/mcp",
			"headers": {"Authorization": "Bearer ${POSTHOG_KEY}"}
		},
		"github": {
			"command": "npx",
			"args": ["-y", "@modelcontextprotocol/server-github"],
			"env": {"GITHUB_TOKEN": "${GITHUB_TOKEN}"}
		}
	}`

	os.Setenv("POSTHOG_KEY", "phx_test123")
	os.Setenv("GITHUB_TOKEN", "ghp_test456")
	defer os.Unsetenv("POSTHOG_KEY")
	defer os.Unsetenv("GITHUB_TOKEN")

	cfg, err := Parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg) != 2 {
		t.Fatalf("got %d servers, want 2", len(cfg))
	}

	ph := cfg["posthog"]
	if ph.Type != "http" {
		t.Errorf("posthog type = %q, want http", ph.Type)
	}
	if ph.URL != "https://mcp.posthog.com/mcp" {
		t.Errorf("posthog url = %q", ph.URL)
	}
	if ph.Headers["Authorization"] != "Bearer phx_test123" {
		t.Errorf("posthog auth = %q (env not interpolated)", ph.Headers["Authorization"])
	}

	gh := cfg["github"]
	if gh.Command != "npx" {
		t.Errorf("github command = %q", gh.Command)
	}
	if gh.Env["GITHUB_TOKEN"] != "ghp_test456" {
		t.Errorf("github env = %q (env not interpolated)", gh.Env["GITHUB_TOKEN"])
	}
}

func TestInterpolateEnv(t *testing.T) {
	os.Setenv("TEST_VAR", "hello")
	defer os.Unsetenv("TEST_VAR")

	cases := []struct{ input, want string }{
		{"Bearer ${TEST_VAR}", "Bearer hello"},
		{"no-vars", "no-vars"},
		{"${MISSING_VAR}", ""},
		{"${TEST_VAR} and ${TEST_VAR}", "hello and hello"},
	}
	for _, tc := range cases {
		got := interpolateEnv(tc.input)
		if got != tc.want {
			t.Errorf("interpolateEnv(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
```

**Step 2: Run tests — verify they fail**

```bash
cd ~/src/mcpfs && go test ./internal/config/ -v
```

**Step 3: Implement config.go**

```go
// Package config parses mcpfs server configuration files.
package config

import (
	"encoding/json"
	"os"
	"strings"
)

// ServerConfig describes how to connect to an MCP server.
type ServerConfig struct {
	Type    string            `json:"type"`    // "http" or "" (stdio)
	URL     string            `json:"url"`     // HTTP endpoint
	Headers map[string]string `json:"headers"` // HTTP headers
	Command string            `json:"command"` // stdio command
	Args    []string          `json:"args"`    // stdio args
	Env     map[string]string `json:"env"`     // env vars for stdio
}

// Parse reads a servers.json config and interpolates env vars.
func Parse(data []byte) (map[string]*ServerConfig, error) {
	var raw map[string]*ServerConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	for _, cfg := range raw {
		cfg.URL = interpolateEnv(cfg.URL)
		for k, v := range cfg.Headers {
			cfg.Headers[k] = interpolateEnv(v)
		}
		for k, v := range cfg.Env {
			cfg.Env[k] = interpolateEnv(v)
		}
	}
	return raw, nil
}

// Load reads and parses a config file.
func Load(path string) (map[string]*ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

func interpolateEnv(s string) string {
	for {
		start := strings.Index(s, "${")
		if start < 0 {
			return s
		}
		end := strings.Index(s[start:], "}")
		if end < 0 {
			return s
		}
		varName := s[start+2 : start+end]
		s = s[:start] + os.Getenv(varName) + s[start+end+1:]
	}
}
```

**Step 4: Run tests — verify they pass**

```bash
cd ~/src/mcpfs && go test ./internal/config/ -v
```

**Step 5: Commit**

```bash
cd ~/src/mcpfs && git add internal/config/ && git commit -m "feat: config file parsing with env interpolation"
```

---

### Task 5: HTTP MCP Client Wrapper

**Files:**
- Create: `pkg/mcpclient/http.go`

The existing `mcptool.HTTPCaller` handles HTTP transport but only exposes `Call()` for tools. We need a wrapper that presents the same `ToolCaller` interface (`CallTool`) and also supports `ListTools`.

**Step 1: Implement http.go**

```go
package mcpclient

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// HTTPClient communicates with an MCP server over HTTP.
// It implements the same methods as Client (ListResources, ListTools, CallTool, etc).
type HTTPClient struct {
	url     string
	headers map[string]string

	mu        sync.Mutex
	sessionID string
}

// NewHTTP creates an HTTP MCP client.
func NewHTTP(url string, headers map[string]string) (*HTTPClient, error) {
	c := &HTTPClient{url: url, headers: headers}
	if err := c.initialize(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *HTTPClient) initialize() error {
	body := map[string]interface{}{
		"jsonrpc": "2.0", "id": 0,
		"method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]string{"name": "mcpfs", "version": "0.1.0"},
		},
	}
	_, err := c.rpc(body)
	return err
}

func (c *HTTPClient) rpc(body interface{}) (json.RawMessage, error) {
	data, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", c.url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	c.mu.Lock()
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	c.mu.Unlock()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.mu.Lock()
		c.sessionID = sid
		c.mu.Unlock()
	}

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(b), 200))
	}

	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				var rpcResp struct {
					Result json.RawMessage `json:"result"`
					Error  *struct {
						Code    int    `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				payload := strings.TrimPrefix(line, "data: ")
				if json.Unmarshal([]byte(payload), &rpcResp) == nil {
					if rpcResp.Error != nil {
						return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
					}
					return rpcResp.Result, nil
				}
			}
		}
		return nil, fmt.Errorf("no data in SSE response")
	}

	b, _ := io.ReadAll(resp.Body)
	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &rpcResp); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}

// ListResources calls resources/list.
func (c *HTTPClient) ListResources() ([]Resource, error) {
	result, err := c.rpc(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "resources/list", "params": map[string]interface{}{},
	})
	if err != nil {
		return nil, err
	}
	var out struct{ Resources []Resource `json:"resources"` }
	json.Unmarshal(result, &out)
	return out.Resources, nil
}

// ListResourceTemplates calls resources/templates/list.
func (c *HTTPClient) ListResourceTemplates() ([]ResourceTemplate, error) {
	result, err := c.rpc(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "resources/templates/list", "params": map[string]interface{}{},
	})
	if err != nil {
		return nil, err
	}
	var out struct{ ResourceTemplates []ResourceTemplate `json:"resourceTemplates"` }
	json.Unmarshal(result, &out)
	return out.ResourceTemplates, nil
}

// ListTools calls tools/list.
func (c *HTTPClient) ListTools() (json.RawMessage, error) {
	result, err := c.rpc(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": map[string]interface{}{},
	})
	if err != nil {
		return nil, err
	}
	var out struct{ Tools json.RawMessage `json:"tools"` }
	json.Unmarshal(result, &out)
	return out.Tools, nil
}

// CallTool calls tools/call.
func (c *HTTPClient) CallTool(name string, args map[string]interface{}) (json.RawMessage, error) {
	result, err := c.rpc(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]interface{}{"name": name, "arguments": args},
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(result, &out) == nil && len(out.Content) > 0 {
		return json.RawMessage(out.Content[0].Text), nil
	}
	return json.RawMessage("{}"), nil
}

// ReadResource calls resources/read.
func (c *HTTPClient) ReadResource(uri string) (string, string, error) {
	result, err := c.rpc(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "resources/read",
		"params": map[string]interface{}{"uri": uri},
	})
	if err != nil {
		return "", "", err
	}
	var out struct {
		Contents []struct {
			URI      string `json:"uri"`
			MimeType string `json:"mimeType"`
			Text     string `json:"text"`
		} `json:"contents"`
	}
	if json.Unmarshal(result, &out) == nil && len(out.Contents) > 0 {
		return out.Contents[0].Text, out.Contents[0].MimeType, nil
	}
	return "", "", fmt.Errorf("empty response for %s", uri)
}

func (c *HTTPClient) Close() {}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
```

**Step 2: Run build to verify compilation**

```bash
cd ~/src/mcpfs && go build ./...
```

**Step 3: Commit**

```bash
cd ~/src/mcpfs && git add pkg/mcpclient/http.go && git commit -m "feat: HTTP MCP client (same interface as stdio client)"
```

---

### Task 6: Refactor Mount to Accept Interface Instead of Concrete Client

**Files:**
- Modify: `internal/fuse/fs.go`

The current `Mount()` takes `*mcpclient.Client`. For HTTP servers, we need it to accept either `Client` or `HTTPClient`. Extract an interface.

**Step 1: Define MCPClient interface in fs.go**

```go
// MCPClient abstracts stdio and HTTP MCP clients.
type MCPClient interface {
	ListResources() ([]mcpclient.Resource, error)
	ListResourceTemplates() ([]mcpclient.ResourceTemplate, error)
	ListTools() (json.RawMessage, error)
	CallTool(name string, args map[string]interface{}) (json.RawMessage, error)
	ReadResource(uri string) (string, string, error)
}
```

**Step 2: Change `mcpFS` and `Mount` signature**

```go
type mcpFS struct {
	gofuse.Inode
	client     MCPClient
	toolCaller ToolCaller
	scheme     string
	tree       *fsTree
}

func Mount(mountpoint string, client MCPClient, debug bool) error {
```

Both `*mcpclient.Client` and `*mcpclient.HTTPClient` satisfy this interface — no adapter needed.

**Step 3: Run tests**

```bash
cd ~/src/mcpfs && go test ./... -v
```

**Step 4: Commit**

```bash
cd ~/src/mcpfs && git add internal/fuse/fs.go && git commit -m "refactor: MCPClient interface for stdio/HTTP polymorphism"
```

---

### Task 7: Wire --config and --http into main.go

**Files:**
- Modify: `cmd/mcpfs/main.go`

**Step 1: Add --config mode**

```go
func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
	}

	// mcpfs --config servers.json
	if args[0] == "--config" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "mcpfs: --config requires a path")
			os.Exit(1)
		}
		runConfig(args[1])
		return
	}

	// mcpfs migrate ...
	if args[0] == "migrate" { /* existing code */ }

	// mcpfs -u <mountpoint>
	if args[0] == "-u" { /* existing code */ }

	// mcpfs <mountpoint> [--http URL --auth HEADER] -- <command>
	// ... parse --http and --auth flags ...
```

Add the `runConfig` function:

```go
func runConfig(configPath string) {
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs: config: %v\n", err)
		os.Exit(1)
	}

	mountRoot := "/mnt/mcpfs"
	var wg sync.WaitGroup

	for name, srv := range cfg {
		mp := filepath.Join(mountRoot, name)
		os.MkdirAll(mp, 0755)

		wg.Add(1)
		go func(name string, srv *config.ServerConfig, mp string) {
			defer wg.Done()
			var client mcpfuse.MCPClient
			var err error

			if srv.Type == "http" {
				client, err = mcpclient.NewHTTP(srv.URL, srv.Headers)
			} else {
				// Set env vars for subprocess
				for k, v := range srv.Env {
					os.Setenv(k, v)
				}
				var stdioClient *mcpclient.Client
				stdioClient, err = mcpclient.New(srv.Command, srv.Args)
				client = stdioClient
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "mcpfs: %s: %v\n", name, err)
				return
			}

			if err := mcpfuse.Mount(mp, client, false); err != nil {
				fmt.Fprintf(os.Stderr, "mcpfs: %s: %v\n", name, err)
			}
		}(name, srv, mp)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	fmt.Fprintln(os.Stderr, "\nmcpfs: unmounting all...")
	for name := range cfg {
		exec.Command("fusermount", "-u", filepath.Join(mountRoot, name)).Run()
	}
}
```

**Step 2: Add --http mode to existing single-mount path**

In the existing single-mount argument parsing, detect `--http` and `--auth` flags:

```go
	httpURL := ""
	authHeader := ""
	for _, a := range preArgs {
		if a == "--debug" {
			debug = true
		} else if strings.HasPrefix(a, "--http") {
			// handled below
		} else if mountpoint == "" {
			mountpoint = a
		}
	}

	// Check for --http URL --auth HEADER
	for i, a := range preArgs {
		if a == "--http" && i+1 < len(preArgs) {
			httpURL = preArgs[i+1]
		}
		if a == "--auth" && i+1 < len(preArgs) {
			authHeader = preArgs[i+1]
		}
	}

	if httpURL != "" {
		headers := map[string]string{}
		if authHeader != "" {
			headers["Authorization"] = authHeader
		}
		client, err := mcpclient.NewHTTP(httpURL, headers)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mcpfs: %v\n", err)
			os.Exit(1)
		}
		defer client.Close()
		if err := mcpfuse.Mount(mountpoint, client, debug); err != nil {
			fmt.Fprintf(os.Stderr, "mcpfs: %v\n", err)
			os.Exit(1)
		}
		return
	}
```

**Step 3: Update usage text**

```go
func usage() {
	fmt.Fprintln(os.Stderr, `mcpfs — mount MCP servers as filesystems

Usage:
  mcpfs <mountpoint> -- <command> [args...]    mount stdio server
  mcpfs <mountpoint> --http <url> [--auth H]   mount HTTP server
  mcpfs --config <servers.json>                mount all from config
  mcpfs -u <mountpoint>                        unmount
  mcpfs migrate [--apply|--undo|--json]

Flags:
  --debug     enable FUSE debug logging
  --config    path to servers.json (Claude Desktop format)
  --http      MCP server HTTP endpoint
  --auth      Authorization header value`)
	os.Exit(2)
}
```

**Step 4: Add imports**

```go
import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/airshelf/mcpfs/internal/config"
	mcpfuse "github.com/airshelf/mcpfs/internal/fuse"
	"github.com/airshelf/mcpfs/pkg/mcpclient"
)
```

**Step 5: Build and verify**

```bash
cd ~/src/mcpfs && go build -o bin/mcpfs ./cmd/mcpfs/ && bin/mcpfs --help
```

Expected: shows updated usage with `--config`, `--http`

**Step 6: Commit**

```bash
cd ~/src/mcpfs && git add cmd/mcpfs/main.go && git commit -m "feat: --config and --http modes for gateway"
```

---

### Task 8: End-to-End Test with Real Server

**Step 1: Test with stdio server (existing behavior still works)**

```bash
cd ~/src/mcpfs
bin/mcpfs-mount -u 2>/dev/null || true
bin/mcpfs /tmp/mcpfs-test -- bin/mcpfs-github &
sleep 2
ls /tmp/mcpfs-test/
cat /tmp/mcpfs-test/repos.json | head -c 200
fusermount -u /tmp/mcpfs-test
```

Expected: repos.json exists, contains JSON array

**Step 2: Test with a tool-only server**

Pick a server that exposes tools but not resources. Try PostHog MCP (HTTP):

```bash
source ~/.config/mcpfs/env
bin/mcpfs /tmp/mcpfs-posthog --http https://mcp.posthog.com/mcp --auth "Bearer $POSTHOG_API_KEY" &
sleep 3
ls /tmp/mcpfs-posthog/
cat /tmp/mcpfs-posthog/dashboards.json | jq '.[0].name' 2>/dev/null || cat /tmp/mcpfs-posthog/dashboards.json | head -c 200
fusermount -u /tmp/mcpfs-posthog
```

Expected: dashboards.json, feature-flags.json, actions.json appear as files; write tools (create/update/delete) do NOT appear

**Step 3: Test --config mode**

Create a test config:

```bash
cat > /tmp/mcpfs-test-config.json <<'EOF'
{
  "github": {
    "command": "mcpfs-github",
    "args": [],
    "env": {}
  }
}
EOF
PATH=$PATH:~/src/mcpfs/bin bin/mcpfs --config /tmp/mcpfs-test-config.json &
sleep 3
ls /mnt/mcpfs/github/
fusermount -u /mnt/mcpfs/github
kill %1 2>/dev/null
```

**Step 4: Commit (no code changes — just verification)**

This step is manual verification only.

---

### Task 9: Build and Install

**Step 1: Build the binary**

```bash
cd ~/src/mcpfs && go build -o bin/mcpfs ./cmd/mcpfs/
```

**Step 2: Verify the existing mcpfs-mount still works**

```bash
bin/mcpfs-mount -u 2>/dev/null; bin/mcpfs-mount github
ls /mnt/mcpfs/github/
bin/mcpfs-mount -u
```

**Step 3: Final commit with all changes**

```bash
cd ~/src/mcpfs && git add -A && git status
```

Review for any unstaged files, then:

```bash
git push
```

---

## Files Summary

| File | Action | Lines | Purpose |
|------|--------|-------|---------|
| `internal/toolfs/classify.go` | Create | ~80 | Tool classification (list/get/write/query) |
| `internal/toolfs/classify_test.go` | Create | ~80 | Classification + naming tests |
| `internal/toolfs/tree.go` | Create | ~35 | Build tool tree from classified entries |
| `internal/toolfs/tree_test.go` | Create | ~60 | Tool tree tests |
| `internal/fuse/toolnode.go` | Create | ~65 | Tool-backed FUSE file node |
| `internal/fuse/fs.go` | Modify | ~50 Δ | MCPClient interface, tool merge, toolCaller |
| `internal/config/config.go` | Create | ~55 | Config file parsing + env interpolation |
| `internal/config/config_test.go` | Create | ~50 | Config tests |
| `pkg/mcpclient/http.go` | Create | ~170 | HTTP MCP client (same API as stdio) |
| `cmd/mcpfs/main.go` | Modify | ~80 Δ | --config, --http modes |

**Total: ~725 new/changed lines.** Replaces 3357 lines of custom per-server code.
