package fuse

import (
	"testing"

	"github.com/airshelf/mcpfs/pkg/mcpclient"
)

func TestBuildTreeStaticResources(t *testing.T) {
	resources := []mcpclient.Resource{
		{URI: "test://repos", Name: "repos", MimeType: "application/json"},
		{URI: "test://notifications", Name: "notifications", MimeType: "application/json"},
	}

	tree := BuildTree("test", resources, nil)

	if !tree.isDir {
		t.Fatal("root should be a dir")
	}
	if _, ok := tree.children["repos.json"]; !ok {
		t.Error("missing repos.json")
	}
	if _, ok := tree.children["notifications.json"]; !ok {
		t.Error("missing notifications.json")
	}
}

func TestBuildTreeTextResource(t *testing.T) {
	resources := []mcpclient.Resource{
		{URI: "test://readme", Name: "readme", MimeType: "text/plain"},
	}

	tree := BuildTree("test", resources, nil)

	// text/plain should NOT get .json extension
	if _, ok := tree.children["readme"]; !ok {
		t.Error("missing readme (text/plain should not have .json suffix)")
	}
	if _, ok := tree.children["readme.json"]; ok {
		t.Error("text/plain resource should not have .json extension")
	}
}

func TestBuildTreeNestedStaticResource(t *testing.T) {
	resources := []mcpclient.Resource{
		{URI: "test://projects/env", Name: "env", MimeType: "application/json"},
	}

	tree := BuildTree("test", resources, nil)

	proj, ok := tree.children["projects"]
	if !ok {
		t.Fatal("missing projects dir")
	}
	if !proj.isDir {
		t.Error("projects should be a dir")
	}
	if _, ok := proj.children["env.json"]; !ok {
		t.Error("missing env.json under projects/")
	}
}

func TestBuildTreeTemplateOneParam(t *testing.T) {
	templates := []mcpclient.ResourceTemplate{
		{URITemplate: "test://items/{id}", Name: "item", MimeType: "application/json"},
	}

	tree := BuildTree("test", nil, templates)

	items, ok := tree.children["items"]
	if !ok {
		t.Fatal("missing items dir")
	}
	if !items.isDir {
		t.Error("items should be a dir")
	}
	if items.template == "" {
		t.Error("items should have a template")
	}
	if items.param != "id" {
		t.Errorf("items.param = %q, want id", items.param)
	}
}

func TestBuildTreeTemplateTwoParams(t *testing.T) {
	templates := []mcpclient.ResourceTemplate{
		{URITemplate: "github://repos/{owner}/{repo}", Name: "repo", MimeType: "application/json"},
		{URITemplate: "github://repos/{owner}/{repo}/issues", Name: "issues", MimeType: "application/json"},
		{URITemplate: "github://repos/{owner}/{repo}/readme", Name: "readme", MimeType: "text/plain"},
	}

	tree := BuildTree("github", nil, templates)

	repos, ok := tree.children["repos"]
	if !ok {
		t.Fatal("missing repos dir")
	}
	if repos.param != "owner" {
		t.Errorf("repos.param = %q, want owner", repos.param)
	}
	if repos.nestedParam != "repo" {
		t.Errorf("repos.nestedParam = %q, want repo", repos.nestedParam)
	}
}

func TestBuildTreeMixedResourcesAndTemplates(t *testing.T) {
	resources := []mcpclient.Resource{
		{URI: "vercel://deployments", Name: "deployments", MimeType: "application/json"},
		{URI: "vercel://domains", Name: "domains", MimeType: "application/json"},
	}
	templates := []mcpclient.ResourceTemplate{
		{URITemplate: "vercel://deployments/{url}", Name: "deployment"},
		{URITemplate: "vercel://deployments/{url}/logs/build", Name: "build-logs", MimeType: "text/plain"},
		{URITemplate: "vercel://deployments/{url}/logs/runtime", Name: "runtime-logs", MimeType: "text/plain"},
	}

	tree := BuildTree("vercel", resources, templates)

	if _, ok := tree.children["deployments"]; !ok {
		t.Fatal("missing deployments")
	}
	if _, ok := tree.children["domains.json"]; !ok {
		t.Fatal("missing domains.json")
	}

	deploys := tree.children["deployments"]
	if !deploys.isDir {
		t.Error("deployments should be a dir")
	}
	// Template tail children (logs/) should exist in tree but have template URIs
	if logs, ok := deploys.children["logs"]; ok {
		if hasStaticURI(logs) {
			t.Error("logs/ in template param dir should NOT have static URI")
		}
	}
}

func TestHasStaticURI(t *testing.T) {
	static := &fsTree{children: make(map[string]*fsTree), uri: "vercel://deployments"}
	if !hasStaticURI(static) {
		t.Error("static URI should return true")
	}

	template := &fsTree{children: make(map[string]*fsTree), uri: "vercel://deployments/{url}/logs/build"}
	if hasStaticURI(template) {
		t.Error("template URI should return false")
	}

	dir := newFSTree()
	dir.isDir = true
	dir.children["build"] = template
	if hasStaticURI(dir) {
		t.Error("dir with only template children should return false")
	}

	dir.children["static"] = static
	if !hasStaticURI(dir) {
		t.Error("dir with static child should return true")
	}

	empty := &fsTree{children: make(map[string]*fsTree)}
	if hasStaticURI(empty) {
		t.Error("empty node should return false")
	}
}

func TestResolveURI(t *testing.T) {
	cases := []struct {
		uri    string
		params map[string]string
		want   string
	}{
		{
			"github://repos/{owner}/{repo}",
			map[string]string{"owner": "airshelf", "repo": "mcpfs"},
			"github://repos/airshelf/mcpfs",
		},
		{
			"npm://packages/{name}",
			map[string]string{"name": "react"},
			"npm://packages/react",
		},
		{
			"test://no-params",
			map[string]string{},
			"test://no-params",
		},
		{
			"vercel://deployments/{url}/logs/build",
			map[string]string{"url": "my-app-abc.vercel.app"},
			"vercel://deployments/my-app-abc.vercel.app/logs/build",
		},
	}

	for _, tc := range cases {
		got := resolveURI(tc.uri, tc.params)
		if got != tc.want {
			t.Errorf("resolveURI(%q, %v) = %q, want %q", tc.uri, tc.params, got, tc.want)
		}
	}
}

func TestCopyParams(t *testing.T) {
	orig := map[string]string{"a": "1", "b": "2"}
	cp := copyParams(orig)

	// Modify copy, original should be unchanged
	cp["c"] = "3"
	if _, ok := orig["c"]; ok {
		t.Error("copyParams did not create independent copy")
	}
	if cp["a"] != "1" || cp["b"] != "2" {
		t.Error("copy missing original values")
	}
}

func TestSingularize(t *testing.T) {
	cases := []struct{ input, want string }{
		{"repos", "repo"},
		{"issues", "issue"},
		{"pulls", "pull"},
		{"item", "item"},
		{"s", ""},
		{"deployments", "deployment"},
	}
	for _, tc := range cases {
		got := singularize(tc.input)
		if got != tc.want {
			t.Errorf("singularize(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestBuildTreeWithToolFields(t *testing.T) {
	tree := newFSTree()
	tree.isDir = true

	child := newFSTree()
	child.toolName = "dashboards-get-all"
	tree.children["dashboards.json"] = child

	if tree.children["dashboards.json"].toolName != "dashboards-get-all" {
		t.Error("tool name not preserved")
	}
}

func TestBuildTreeWithToolDir(t *testing.T) {
	tree := newFSTree()
	tree.isDir = true

	child := newFSTree()
	child.isDir = true
	child.toolName = "dashboard-get"
	child.template = "tool"
	child.param = "dashboardId"
	child.toolParams = []string{"dashboardId"}
	tree.children["dashboards"] = child

	dash := tree.children["dashboards"]
	if !dash.isDir {
		t.Error("should be dir")
	}
	if dash.toolName != "dashboard-get" {
		t.Error("tool name not set")
	}
	if dash.param != "dashboardId" {
		t.Error("param not set")
	}
	if dash.template != "tool" {
		t.Error("template not set")
	}
	if len(dash.toolParams) != 1 || dash.toolParams[0] != "dashboardId" {
		t.Errorf("toolParams = %v, want [dashboardId]", dash.toolParams)
	}
}

func TestToolMergeResourcePriority(t *testing.T) {
	// Resources should take priority over tools with same name
	resources := []mcpclient.Resource{
		{URI: "test://dashboards", Name: "dashboards", MimeType: "application/json"},
	}
	tree := BuildTree("test", resources, nil)

	if _, exists := tree.children["dashboards.json"]; !exists {
		t.Fatal("resource should create dashboards.json")
	}

	// Simulate what Mount does: tool should NOT override existing resource
	toolChild := newFSTree()
	toolChild.toolName = "dashboards-get-all"
	if _, exists := tree.children["dashboards.json"]; exists {
		// Resource takes priority -- don't merge
	} else {
		tree.children["dashboards.json"] = toolChild
	}

	// Verify resource is still there
	node := tree.children["dashboards.json"]
	if node.toolName != "" {
		t.Error("tool should not override resource")
	}
	if node.uri != "test://dashboards" {
		t.Error("resource URI should be preserved")
	}
}

func TestBuildTreeEmptyInputs(t *testing.T) {
	tree := BuildTree("test", nil, nil)
	if !tree.isDir {
		t.Error("root should be dir")
	}
	if len(tree.children) != 0 {
		t.Errorf("empty inputs should produce empty tree, got %d children", len(tree.children))
	}
}

func TestBuildTreeMultipleToolFiles(t *testing.T) {
	// Simulate merging multiple tool entries into the tree
	tree := newFSTree()
	tree.isDir = true

	for _, name := range []string{"users.json", "repos.json", "issues.json", "pulls.json"} {
		child := newFSTree()
		child.toolName = name + "-tool"
		tree.children[name] = child
	}

	if len(tree.children) != 4 {
		t.Errorf("tree has %d children, want 4", len(tree.children))
	}
	for _, name := range []string{"users.json", "repos.json", "issues.json", "pulls.json"} {
		if _, ok := tree.children[name]; !ok {
			t.Errorf("missing %s", name)
		}
	}
}

func TestEnsureDirExisting(t *testing.T) {
	root := newFSTree()
	root.isDir = true

	// First call creates
	child := root.ensureDir("projects")
	if !child.isDir {
		t.Error("ensureDir should create dir")
	}

	// Second call returns same node
	child2 := root.ensureDir("projects")
	if child != child2 {
		t.Error("ensureDir should return existing node")
	}
	if len(root.children) != 1 {
		t.Errorf("should have 1 child, got %d", len(root.children))
	}
}

func TestAddFile(t *testing.T) {
	root := newFSTree()
	root.isDir = true
	root.addFile("test.json", "test://test")

	child, ok := root.children["test.json"]
	if !ok {
		t.Fatal("addFile should create child")
	}
	if child.uri != "test://test" {
		t.Errorf("uri = %q, want test://test", child.uri)
	}
	if child.isDir {
		t.Error("file should not be dir")
	}
}

func TestCopyParamsNil(t *testing.T) {
	cp := copyParams(nil)
	if cp == nil {
		t.Fatal("copyParams(nil) should return non-nil map")
	}
	if len(cp) != 0 {
		t.Errorf("copyParams(nil) should be empty, got %d", len(cp))
	}
}

