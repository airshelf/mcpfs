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
		name   string
		schema json.RawMessage
		want   ToolClass
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
