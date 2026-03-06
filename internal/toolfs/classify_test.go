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

func TestRequiredParamsNilSchema(t *testing.T) {
	got := RequiredParams(nil)
	if got != nil {
		t.Errorf("RequiredParams(nil) = %v, want nil", got)
	}
}

func TestRequiredParamsEmptySchema(t *testing.T) {
	got := RequiredParams(json.RawMessage(`{}`))
	if len(got) != 0 {
		t.Errorf("RequiredParams({}) = %v, want empty", got)
	}
}

func TestRequiredParamsMalformedRequiredNoProperties(t *testing.T) {
	// Schema with required but no properties — malformed but should not panic
	s := json.RawMessage(`{"required":["id","name"]}`)
	got := RequiredParams(s)
	if len(got) != 2 {
		t.Errorf("RequiredParams = %v, want [id name]", got)
	}
}

func TestClassifyToolAmbiguousNoParams(t *testing.T) {
	// "get-settings" contains "set" which is a write verb — classified as ToolWrite.
	// This is a known limitation of substring matching.
	got := ClassifyTool("get-settings", schema(nil, nil))
	if got != ToolWrite {
		t.Errorf("ClassifyTool(get-settings, no required) = %v, want ToolWrite (\"set\" substring match)", got)
	}

	// "show-config" with no required params — should be ToolList
	got2 := ClassifyTool("show-config", schema(nil, nil))
	if got2 != ToolList {
		t.Errorf("ClassifyTool(show-config, no required) = %v, want ToolList", got2)
	}
}

func TestClassifyToolMultipleRequiredParams(t *testing.T) {
	// "get_commit" with owner, repo, sha — should be ToolGet
	s := schema(map[string]string{"owner": "string", "repo": "string", "sha": "string"}, []string{"owner", "repo", "sha"})
	got := ClassifyTool("get_commit", s)
	if got != ToolGet {
		t.Errorf("ClassifyTool(get_commit, 3 required) = %v, want ToolGet", got)
	}
}

func TestClassifyToolWriteVerbInResourceName(t *testing.T) {
	// "list_updates" — "update" substring is in the name, but "list" is not a write verb
	// BUG: matchesAny checks write verbs first and "update" is a substring match,
	// so "list_updates" is classified as ToolWrite even though the intent is list.
	got := ClassifyTool("list_updates", schema(nil, nil))
	// Current behavior: ToolWrite because "update" substring matches write verbs first.
	// This is arguably a bug — the "list" prefix should win.
	if got != ToolWrite {
		t.Errorf("ClassifyTool(list_updates) = %v, want ToolWrite (known bug: write verb substring in resource name)", got)
	}
}

func TestClassifyToolNilSchema(t *testing.T) {
	got := ClassifyTool("some-list", nil)
	if got != ToolList {
		t.Errorf("ClassifyTool(some-list, nil schema) = %v, want ToolList", got)
	}
}

func TestClassifyToolUnknownVerbWithRequiredParams(t *testing.T) {
	// "frobulate-widget" contains "get" inside "widget" — classified as ToolGet.
	// This demonstrates that substring matching can produce surprising results.
	s := schema(map[string]string{"id": "string"}, []string{"id"})
	got := ClassifyTool("frobulate-widget", s)
	if got != ToolGet {
		t.Errorf("ClassifyTool(frobulate-widget, required) = %v, want ToolGet (\"get\" in \"widget\")", got)
	}

	// Use a name that truly doesn't contain any verb substrings
	s2 := schema(map[string]string{"id": "string"}, []string{"id"})
	got2 := ClassifyTool("frobnicator", s2)
	if got2 != ToolWrite {
		t.Errorf("ClassifyTool(frobnicator, required) = %v, want ToolWrite (safe default)", got2)
	}
}

func TestToolToFilenameUnderscoresVsHyphens(t *testing.T) {
	// stripVerb normalizes underscores to hyphens
	cases := []struct {
		name  string
		class ToolClass
		want  string
	}{
		{"list_customers", ToolList, "customers.json"},
		{"list-customers", ToolList, "customers.json"},
		{"get_customer", ToolGet, "customers"},
		{"get-customer", ToolGet, "customers"},
	}
	for _, tc := range cases {
		got := ToolToFilename(tc.name, tc.class)
		if got != tc.want {
			t.Errorf("ToolToFilename(%q, %v) = %q, want %q", tc.name, tc.class, got, tc.want)
		}
	}
}

func TestStripVerbNoMatchingVerb(t *testing.T) {
	// When no verb prefix/suffix matches, name is returned as-is (with _ → -)
	got := stripVerb("frobulate_widget")
	if got != "frobulate-widget" {
		t.Errorf("stripVerb(frobulate_widget) = %q, want frobulate-widget", got)
	}
}

func TestStripVerbSuffixes(t *testing.T) {
	cases := []struct{ input, want string }{
		{"dashboard-get-all", "dashboards"},
		{"dashboard-list", "dashboards"},
		{"dashboard-get", "dashboard"},
		{"dashboard-retrieve", "dashboard"},
	}
	for _, tc := range cases {
		got := stripVerb(tc.input)
		if got != tc.want {
			t.Errorf("stripVerb(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestStripVerbPrefixes(t *testing.T) {
	cases := []struct{ input, want string }{
		{"list-dashboards", "dashboards"},
		{"get-all-dashboards", "dashboards"},
		{"retrieve-all-dashboards", "dashboards"},
		{"get-dashboard", "dashboard"},
		{"retrieve-dashboard", "dashboard"},
	}
	for _, tc := range cases {
		got := stripVerb(tc.input)
		if got != tc.want {
			t.Errorf("stripVerb(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestPluralizeEdgeCases(t *testing.T) {
	cases := []struct{ input, want string }{
		{"", ""},
		{"items", "items"},       // already plural (ends in s)
		{"status", "status"},     // ends in s
		{"dashboard", "dashboards"},
		{"s", "s"},               // single char ending in s
	}
	for _, tc := range cases {
		got := pluralize(tc.input)
		if got != tc.want {
			t.Errorf("pluralize(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestToolClassString(t *testing.T) {
	cases := []struct {
		c    ToolClass
		want string
	}{
		{ToolList, "list"},
		{ToolGet, "get"},
		{ToolWrite, "write"},
		{ToolQuery, "query"},
		{ToolClass(99), "unknown"},
	}
	for _, tc := range cases {
		got := tc.c.String()
		if got != tc.want {
			t.Errorf("ToolClass(%d).String() = %q, want %q", tc.c, got, tc.want)
		}
	}
}
