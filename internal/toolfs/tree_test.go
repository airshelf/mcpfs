package toolfs

import (
	"testing"
)

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
		Name   string
		Schema map[string]string
		Req    []string
	}{
		{"dashboards-get-all", nil, nil},
		{"dashboard-get", map[string]string{"dashboardId": "string"}, []string{"dashboardId"}},
		{"dashboard-create", map[string]string{"name": "string"}, []string{"name"}},
	}

	var entries []ToolEntry
	for _, r := range raw {
		s := schema(r.Schema, r.Req)
		class := ClassifyTool(r.Name, s)
		if class == ToolWrite || class == ToolQuery {
			continue
		}
		entries = append(entries, ToolEntry{
			Name:           r.Name,
			Class:          class,
			Filename:       ToolToFilename(r.Name, class),
			ToolName:       r.Name,
			RequiredParams: RequiredParams(s),
		})
	}

	tree := BuildToolTree(entries)

	if _, ok := tree["dashboards.json"]; !ok {
		t.Error("missing dashboards.json (list tool)")
	}
	if _, ok := tree["dashboards"]; !ok {
		t.Error("missing dashboards/ dir (get tool)")
	}
	if len(tree) != 2 {
		t.Errorf("tree has %d entries, want 2", len(tree))
	}
}

func TestBuildToolTreeEmpty(t *testing.T) {
	tree := BuildToolTree(nil)
	if len(tree) != 0 {
		t.Errorf("empty entries should produce empty tree, got %d", len(tree))
	}
	tree2 := BuildToolTree([]ToolEntry{})
	if len(tree2) != 0 {
		t.Errorf("zero-length entries should produce empty tree, got %d", len(tree2))
	}
}

func TestBuildToolTreeDuplicateFilenames(t *testing.T) {
	// Both list and get for same resource produce different filenames:
	// list → "dashboards.json", get → "dashboards" (dir)
	tools := []ToolEntry{
		{Name: "dashboards-get-all", Class: ToolList, Filename: "dashboards.json", ToolName: "dashboards-get-all"},
		{Name: "dashboard-get", Class: ToolGet, Filename: "dashboards", ToolName: "dashboard-get",
			RequiredParams: []string{"dashboardId"}},
	}
	tree := BuildToolTree(tools)
	if len(tree) != 2 {
		t.Errorf("tree has %d entries, want 2", len(tree))
	}
	if _, ok := tree["dashboards.json"]; !ok {
		t.Error("missing dashboards.json")
	}
	if _, ok := tree["dashboards"]; !ok {
		t.Error("missing dashboards dir")
	}
}

func TestBuildToolTreeLargeEntrySet(t *testing.T) {
	var entries []ToolEntry
	// 10 list tools + 10 get tools = 20 entries
	for i := 0; i < 10; i++ {
		name := "resource" + string(rune('a'+i))
		entries = append(entries, ToolEntry{
			Name:     name + "-list",
			Class:    ToolList,
			Filename: name + "s.json",
			ToolName: name + "-list",
		})
		entries = append(entries, ToolEntry{
			Name:           name + "-get",
			Class:          ToolGet,
			Filename:       name + "s",
			ToolName:       name + "-get",
			RequiredParams: []string{"id"},
		})
	}
	tree := BuildToolTree(entries)
	if len(tree) != 20 {
		t.Errorf("tree has %d entries, want 20", len(tree))
	}
	// Verify all list tools are files, all get tools are dirs
	for _, e := range entries {
		node, ok := tree[e.Filename]
		if !ok {
			t.Errorf("missing %s", e.Filename)
			continue
		}
		if e.Class == ToolList && node.IsDir {
			t.Errorf("%s should be a file, not dir", e.Filename)
		}
		if e.Class == ToolGet && !node.IsDir {
			t.Errorf("%s should be a dir", e.Filename)
		}
	}
}

func TestBuildToolTreeWriteAndQueryExcluded(t *testing.T) {
	// Write and Query tools should be silently skipped (they produce no tree entries)
	tools := []ToolEntry{
		{Name: "create-item", Class: ToolWrite, Filename: "items.json", ToolName: "create-item"},
		{Name: "search-items", Class: ToolQuery, Filename: "items.json", ToolName: "search-items"},
	}
	tree := BuildToolTree(tools)
	if len(tree) != 0 {
		t.Errorf("write/query tools should not appear in tree, got %d entries", len(tree))
	}
}

func TestBuildToolTreeGetWithMultipleParams(t *testing.T) {
	tools := []ToolEntry{
		{Name: "get-commit", Class: ToolGet, Filename: "commits", ToolName: "get-commit",
			RequiredParams: []string{"owner", "repo", "sha"}},
	}
	tree := BuildToolTree(tools)
	node := tree["commits"]
	if node == nil {
		t.Fatal("missing commits dir")
	}
	if !node.IsDir {
		t.Error("should be dir")
	}
	if len(node.RequiredParams) != 3 {
		t.Errorf("RequiredParams len = %d, want 3", len(node.RequiredParams))
	}
}
