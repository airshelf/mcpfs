package mcptool

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type mockCaller struct {
	calls  []mockCall
	result json.RawMessage
	err    error
}

type mockCall struct {
	Name string
	Args map[string]interface{}
}

func (m *mockCaller) Call(toolName string, args map[string]interface{}) (json.RawMessage, error) {
	m.calls = append(m.calls, mockCall{Name: toolName, Args: args})
	return m.result, m.err
}

func simpleSchema(props map[string]string, required []string) json.RawMessage {
	if props == nil {
		props = map[string]string{}
	}
	p := make(map[string]interface{})
	for k, v := range props {
		p[k] = map[string]interface{}{"type": v, "description": k}
	}
	s := map[string]interface{}{"type": "object", "properties": p}
	if len(required) > 0 {
		s["required"] = required
	}
	b, _ := json.Marshal(s)
	return b
}

var testTools = []ToolDef{
	{Name: "list-items", Description: "List all items", InputSchema: simpleSchema(nil, nil)},
	{Name: "get-item", Description: "Get an item by ID", InputSchema: simpleSchema(map[string]string{"id": "string"}, []string{"id"})},
	{Name: "create-item", Description: "Create an item", InputSchema: simpleSchema(map[string]string{"name": "string", "count": "integer"}, []string{"name"})},
	{Name: "search-items", Description: "Search items", InputSchema: simpleSchema(map[string]string{"query": "string", "limit": "integer"}, []string{"query"})},
	{Name: "toggle-flag", Description: "Toggle a flag", InputSchema: simpleSchema(map[string]string{"enabled": "boolean"}, nil)},
	{Name: "bulk-update", Description: "Bulk update", InputSchema: simpleSchema(map[string]string{"ids": "array", "data": "object"}, nil)},
}

func TestRunNoArgs(t *testing.T) {
	caller := &mockCaller{}
	// Capture stderr
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	code := Run("test", testTools, caller, nil)

	w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	buf.ReadFrom(r)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	output := buf.String()
	if !strings.Contains(output, "6 tools available") {
		t.Errorf("should list tools, got: %s", output)
	}
}

func TestRunToolsCommand(t *testing.T) {
	caller := &mockCaller{}
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	code := Run("test", testTools, caller, []string{"tools"})

	w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	buf.ReadFrom(r)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestRunHelpFlag(t *testing.T) {
	caller := &mockCaller{}
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	code := Run("test", testTools, caller, []string{"--help"})

	w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	buf.ReadFrom(r)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestRunUnknownTool(t *testing.T) {
	caller := &mockCaller{}
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	code := Run("test", testTools, caller, []string{"nonexistent"})

	w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	buf.ReadFrom(r)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(buf.String(), "unknown command") {
		t.Errorf("should say unknown command, got: %s", buf.String())
	}
}

func TestRunToolHelp(t *testing.T) {
	caller := &mockCaller{}
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	code := Run("test", testTools, caller, []string{"get-item", "--help"})

	w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	buf.ReadFrom(r)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	output := buf.String()
	if !strings.Contains(output, "get-item") {
		t.Errorf("should show tool name, got: %s", output)
	}
	if !strings.Contains(output, "--id") {
		t.Errorf("should show flags, got: %s", output)
	}
}

// Helper to create a ToolDef inline — avoids index issues from sort.Slice mutation.
func toolDef(name string, props map[string]string, required []string) ToolDef {
	return ToolDef{Name: name, Description: name, InputSchema: simpleSchema(props, required)}
}

func TestParseFlagsString(t *testing.T) {
	tool := toolDef("get-item", map[string]string{"id": "string"}, []string{"id"})
	params, err := parseFlags(&tool, []string{"--id", "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if params["id"] != "abc" {
		t.Errorf("id = %v, want abc", params["id"])
	}
}

func TestParseFlagsStringEquals(t *testing.T) {
	tool := toolDef("get-item", map[string]string{"id": "string"}, []string{"id"})
	params, err := parseFlags(&tool, []string{"--id=abc"})
	if err != nil {
		t.Fatal(err)
	}
	if params["id"] != "abc" {
		t.Errorf("id = %v, want abc", params["id"])
	}
}

func TestParseFlagsInteger(t *testing.T) {
	tool := toolDef("search", map[string]string{"query": "string", "limit": "integer"}, []string{"query"})
	params, err := parseFlags(&tool, []string{"--query", "test", "--limit", "42"})
	if err != nil {
		t.Fatal(err)
	}
	if params["limit"] != int64(42) {
		t.Errorf("limit = %v (%T), want 42", params["limit"], params["limit"])
	}
}

func TestParseFlagsBoolean(t *testing.T) {
	tool := toolDef("toggle", map[string]string{"enabled": "boolean"}, nil)
	params, err := parseFlags(&tool, []string{"--enabled"})
	if err != nil {
		t.Fatal(err)
	}
	if params["enabled"] != true {
		t.Errorf("enabled = %v, want true", params["enabled"])
	}
}

func TestParseFlagsBooleanExplicit(t *testing.T) {
	tool := toolDef("toggle", map[string]string{"enabled": "boolean"}, nil)
	params, err := parseFlags(&tool, []string{"--enabled=false"})
	if err != nil {
		t.Fatal(err)
	}
	if params["enabled"] != false {
		t.Errorf("enabled = %v, want false", params["enabled"])
	}
}

func TestParseFlagsArray(t *testing.T) {
	tool := toolDef("bulk", map[string]string{"ids": "array"}, nil)
	params, err := parseFlags(&tool, []string{"--ids", "a,b,c"})
	if err != nil {
		t.Fatal(err)
	}
	ids, ok := params["ids"].([]string)
	if !ok {
		t.Fatalf("ids type = %T, want []string", params["ids"])
	}
	if len(ids) != 3 || ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Errorf("ids = %v", ids)
	}
}

func TestParseFlagsObject(t *testing.T) {
	tool := toolDef("bulk", map[string]string{"data": "object"}, nil)
	params, err := parseFlags(&tool, []string{"--data", `{"key":"val"}`})
	if err != nil {
		t.Fatal(err)
	}
	data, ok := params["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data type = %T, want map", params["data"])
	}
	if data["key"] != "val" {
		t.Errorf("data.key = %v", data["key"])
	}
}

func TestParseFlagsRequiredMissing(t *testing.T) {
	tool := toolDef("get-item", map[string]string{"id": "string"}, []string{"id"})
	_, err := parseFlags(&tool, []string{})
	if err == nil {
		t.Fatal("expected error for missing required flag")
	}
	if !strings.Contains(err.Error(), "--id") {
		t.Errorf("error = %v, should mention --id", err)
	}
}

func TestParseFlagsUnknownFlag(t *testing.T) {
	tool := toolDef("list", nil, nil)
	_, err := parseFlags(&tool, []string{"--nonexistent", "val"})
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestParseFlagsInvalidInteger(t *testing.T) {
	tool := toolDef("search", map[string]string{"query": "string", "limit": "integer"}, []string{"query"})
	_, err := parseFlags(&tool, []string{"--query", "q", "--limit", "not-a-number"})
	if err == nil {
		t.Fatal("expected error for invalid integer")
	}
}

func TestParseFlagsInvalidBoolean(t *testing.T) {
	tool := toolDef("toggle", map[string]string{"enabled": "boolean"}, nil)
	_, err := parseFlags(&tool, []string{"--enabled=maybe"})
	if err == nil {
		t.Fatal("expected error for invalid boolean")
	}
}

func TestParseFlagsInvalidJSON(t *testing.T) {
	tool := toolDef("bulk", map[string]string{"data": "object"}, nil)
	_, err := parseFlags(&tool, []string{"--data", "{invalid"})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseFlagsMissingValue(t *testing.T) {
	tool := toolDef("get-item", map[string]string{"id": "string"}, []string{"id"})
	_, err := parseFlags(&tool, []string{"--id"})
	if err == nil {
		t.Fatal("expected error for missing value")
	}
}

func TestParseFlagsUnexpectedArg(t *testing.T) {
	tool := toolDef("list", nil, nil)
	_, err := parseFlags(&tool, []string{"positional"})
	if err == nil {
		t.Fatal("expected error for positional arg")
	}
}

func TestFindToolExact(t *testing.T) {
	tool := findTool(testTools, "get-item")
	if tool == nil {
		t.Fatal("should find get-item")
	}
	if tool.Name != "get-item" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestFindToolMissing(t *testing.T) {
	tool := findTool(testTools, "nonexistent")
	if tool != nil {
		t.Error("should not find nonexistent tool")
	}
}

func TestIsDataWrapped(t *testing.T) {
	wrapped := json.RawMessage(`{
		"type": "object",
		"properties": {
			"data": {
				"type": "object",
				"properties": {"name": {"type": "string"}}
			}
		},
		"required": ["data"]
	}`)
	if !IsDataWrapped(wrapped) {
		t.Error("should detect data-wrapped schema")
	}

	notWrapped := simpleSchema(map[string]string{"name": "string"}, nil)
	if IsDataWrapped(notWrapped) {
		t.Error("regular schema should not be data-wrapped")
	}

	twoProps := json.RawMessage(`{
		"type": "object",
		"properties": {
			"data": {"type": "object", "properties": {"x": {"type": "string"}}},
			"other": {"type": "string"}
		}
	}`)
	if IsDataWrapped(twoProps) {
		t.Error("schema with multiple props should not be data-wrapped")
	}

	dataNotObject := json.RawMessage(`{
		"type": "object",
		"properties": {
			"data": {"type": "string"}
		}
	}`)
	if IsDataWrapped(dataNotObject) {
		t.Error("data property that is not object should not be data-wrapped")
	}
}

func TestRunCallsToolCorrectly(t *testing.T) {
	caller := &mockCaller{result: json.RawMessage(`{"ok":true}`)}

	// Redirect stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := Run("test", testTools, caller, []string{"get-item", "--id", "123"})

	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	buf.ReadFrom(r)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if len(caller.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(caller.calls))
	}
	if caller.calls[0].Name != "get-item" {
		t.Errorf("tool name = %q", caller.calls[0].Name)
	}
	if caller.calls[0].Args["id"] != "123" {
		t.Errorf("args = %v", caller.calls[0].Args)
	}
	// Output should be pretty-printed
	if !strings.Contains(buf.String(), "\"ok\": true") {
		t.Errorf("output = %q, want pretty-printed JSON", buf.String())
	}
}
