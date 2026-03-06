// Package fuse implements the FUSE filesystem that maps MCP resources to files.
package fuse

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

// MCPClient abstracts stdio and HTTP MCP clients.
type MCPClient interface {
	ListResources() ([]mcpclient.Resource, error)
	ListResourceTemplates() ([]mcpclient.ResourceTemplate, error)
	ListTools() (json.RawMessage, error)
	CallTool(name string, args map[string]interface{}) (json.RawMessage, error)
	ReadResource(uri string) (string, string, error)
}

// mcpFS is the root FUSE inode.
type mcpFS struct {
	gofuse.Inode
	client     MCPClient
	toolCaller ToolCaller
	scheme     string
	tree       *fsTree
}

// fsTree represents the filesystem structure built from MCP resources.
type fsTree struct {
	children map[string]*fsTree
	isDir    bool
	uri      string
	template string
	param    string
	leafName string
	nestedParam    string
	nestedChildren map[string]*fsTree
	nestedLeaf     *fsTree
	// Tool-backed fields (gateway)
	toolName   string   // MCP tool name for tools/call
	toolParams []string // required params for get-by-id
}

func newFSTree() *fsTree {
	return &fsTree{children: make(map[string]*fsTree)}
}

func (t *fsTree) ensureDir(name string) *fsTree {
	if child, ok := t.children[name]; ok {
		child.isDir = true
		return child
	}
	child := newFSTree()
	child.isDir = true
	t.children[name] = child
	return child
}

func (t *fsTree) addFile(name string, uri string) {
	t.children[name] = &fsTree{
		children: make(map[string]*fsTree),
		uri:      uri,
	}
}

// BuildTree constructs the filesystem tree from MCP resource listings.
func BuildTree(scheme string, resources []mcpclient.Resource, templates []mcpclient.ResourceTemplate) *fsTree {
	root := newFSTree()
	root.isDir = true
	prefix := scheme + "://"

	for _, r := range resources {
		path := strings.TrimPrefix(r.URI, prefix)
		parts := strings.Split(path, "/")

		node := root
		for _, p := range parts[:len(parts)-1] {
			node = node.ensureDir(p)
		}

		name := parts[len(parts)-1]
		ext := ".json"
		if r.MimeType == "text/plain" {
			ext = ""
		}
		node.addFile(name+ext, r.URI)
	}

	for _, t := range templates {
		path := strings.TrimPrefix(t.URITemplate, prefix)
		parts := strings.Split(path, "/")

		node := root
		for i, p := range parts {
			if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
				param := p[1 : len(p)-1]
				if node.template == "" {
					node.template = t.URITemplate
					node.param = param
				}
				node.isDir = true
				if i+1 < len(parts) {
					remaining := parts[i+1:]
					registerTemplateTail(node, remaining, t.URITemplate, scheme)
				} else {
					leafName := singularize(parts[i-1])
					node.children["_template_leaf"] = &fsTree{
						children: make(map[string]*fsTree),
						uri:      t.URITemplate,
						leafName: leafName,
					}
				}
				break
			}
			node = node.ensureDir(p)
		}
	}

	return root
}

func singularize(s string) string {
	if strings.HasSuffix(s, "s") {
		return s[:len(s)-1]
	}
	return s
}

func registerTemplateTail(paramDir *fsTree, remaining []string, uriTemplate string, scheme string) {
	for i, p := range remaining {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			nestedParam := p[1 : len(p)-1]
			paramDir.nestedParam = nestedParam
			if paramDir.nestedChildren == nil {
				paramDir.nestedChildren = make(map[string]*fsTree)
			}
			if i+1 < len(remaining) {
				registerNestedTail(paramDir, remaining[i+1:], uriTemplate)
			} else {
				paramDir.nestedLeaf = &fsTree{
					children: make(map[string]*fsTree),
					uri:      uriTemplate,
					leafName: nestedParam,
				}
			}
			return
		}
		if i == len(remaining)-1 {
			paramDir.addFile(p, uriTemplate)
		} else {
			paramDir = paramDir.ensureDir(p)
		}
	}
}

func registerNestedTail(paramDir *fsTree, remaining []string, uriTemplate string) {
	node := paramDir
	for i, p := range remaining {
		if i == len(remaining)-1 {
			node.nestedChildren[p] = &fsTree{
				children: make(map[string]*fsTree),
				uri:      uriTemplate,
			}
		} else {
			if child, ok := node.nestedChildren[p]; ok {
				child.isDir = true
				node = &fsTree{children: child.children, isDir: true}
			} else {
				child := newFSTree()
				child.isDir = true
				node.nestedChildren[p] = child
				node = child
			}
		}
	}
}

// dirNode represents a directory in the FUSE tree.
type dirNode struct {
	gofuse.Inode
	fsys        *mcpFS
	tree        *fsTree
	paramValues map[string]string
}

var _ = (gofuse.NodeLookuper)((*dirNode)(nil))
var _ = (gofuse.NodeReaddirer)((*dirNode)(nil))
var _ = (gofuse.NodeGetattrer)((*dirNode)(nil))

func (d *dirNode) Getattr(ctx context.Context, fh gofuse.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0555
	out.Nlink = 2
	return 0
}

func (d *dirNode) Readdir(ctx context.Context) (gofuse.DirStream, syscall.Errno) {
	var entries []fuse.DirEntry

	isParamDir := d.tree.template != "" && d.tree.param != ""
	for name, child := range d.tree.children {
		if name == "_template_leaf" {
			continue
		}
		// Hide template tail children in param directories — they only
		// make sense inside resolved dynamic children, not at the param level.
		if isParamDir && !hasStaticURI(child) {
			continue
		}
		mode := uint32(syscall.S_IFREG | 0444)
		if child.isDir {
			mode = syscall.S_IFDIR | 0555
		}
		entries = append(entries, fuse.DirEntry{Name: name, Mode: mode})
	}

	return gofuse.NewListDirStream(entries), 0
}

// hasStaticURI returns true if the tree or any descendant has a non-template URI.
func hasStaticURI(t *fsTree) bool {
	if t.uri != "" && !strings.Contains(t.uri, "{") {
		return true
	}
	for _, c := range t.children {
		if hasStaticURI(c) {
			return true
		}
	}
	return false
}

func (d *dirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*gofuse.Inode, syscall.Errno) {
	isParamDir := d.tree.template != "" && d.tree.param != ""

	child, ok := d.tree.children[name]
	if ok {
		// In param directories, template tail children are not directly
		// accessible — treat the name as a param value instead.
		if isParamDir && !hasStaticURI(child) {
			return d.lookupTemplateChild(ctx, name, out)
		}
		return d.buildInode(ctx, name, child, out)
	}

	if isParamDir {
		return d.lookupTemplateChild(ctx, name, out)
	}

	return nil, syscall.ENOENT
}

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

	childTree := newFSTree()
	childTree.isDir = true

	if d.tree.nestedParam != "" {
		childTree.template = d.tree.template
		childTree.param = d.tree.nestedParam
		for k, v := range d.tree.nestedChildren {
			childTree.children[k] = v
		}
		if d.tree.nestedLeaf != nil {
			childTree.children["_template_leaf"] = d.tree.nestedLeaf
		}
	} else {
		for k, v := range d.tree.children {
			if k == "_template_leaf" {
				continue
			}
			childTree.children[k] = v
		}
		if leaf, ok := d.tree.children["_template_leaf"]; ok {
			childTree.addFile(leaf.leafName, leaf.uri)
		}
	}

	out.Mode = syscall.S_IFDIR | 0555
	out.Nlink = 2
	dn := &dirNode{
		fsys:        d.fsys,
		tree:        childTree,
		paramValues: params,
	}
	return d.NewInode(ctx, dn, gofuse.StableAttr{Mode: syscall.S_IFDIR}), 0
}

// fileNode represents a readable file backed by an MCP resource.
type fileNode struct {
	gofuse.Inode
	fsys *mcpFS
	uri  string
}

var _ = (gofuse.NodeOpener)((*fileNode)(nil))
var _ = (gofuse.NodeGetattrer)((*fileNode)(nil))
var _ = (gofuse.NodeReader)((*fileNode)(nil))

func (f *fileNode) Getattr(ctx context.Context, fh gofuse.FileHandle, out *fuse.AttrOut) syscall.Errno {
	data, err := f.readData()
	if err != nil {
		log.Printf("mcpfs: getattr %s: %v", f.uri, err)
		out.Size = 0
	} else {
		out.Size = uint64(len(data))
	}
	out.Mode = syscall.S_IFREG | 0444
	return 0
}

func (f *fileNode) Open(ctx context.Context, flags uint32) (gofuse.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

func (f *fileNode) Read(ctx context.Context, fh gofuse.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data, err := f.readData()
	if err != nil {
		log.Printf("mcpfs: read %s: %v", f.uri, err)
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

func (f *fileNode) readData() ([]byte, error) {
	text, _, err := f.fsys.client.ReadResource(f.uri)
	if err != nil {
		return nil, err
	}
	return []byte(text), nil
}

func resolveURI(uri string, params map[string]string) string {
	for k, v := range params {
		uri = strings.ReplaceAll(uri, "{"+k+"}", v)
	}
	return uri
}

func copyParams(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Mount creates the FUSE mount and blocks until unmounted.
func Mount(mountpoint string, client MCPClient, debug bool) error {
	resources, err := client.ListResources()
	if err != nil {
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
			child.template = "tool"
			if len(node.RequiredParams) > 0 {
				child.param = node.RequiredParams[0]
			}
			child.toolParams = node.RequiredParams
		}
		tree.children[name] = child
	}

	root := &dirNode{
		fsys: &mcpFS{
			client:     client,
			toolCaller: client,
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
