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
