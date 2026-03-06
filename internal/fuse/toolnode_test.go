package fuse

import (
	"encoding/json"
	"errors"
	"testing"
)

type mockToolCaller struct {
	results map[string]string // toolName -> JSON response
	calls   []string          // record of calls made
}

func (m *mockToolCaller) CallTool(name string, args map[string]interface{}) (json.RawMessage, error) {
	m.calls = append(m.calls, name)
	if result, ok := m.results[name]; ok {
		return json.RawMessage(result), nil
	}
	return nil, errors.New("tool not found: " + name)
}

func TestReadDataPrettyPrintsJSON(t *testing.T) {
	mock := &mockToolCaller{
		results: map[string]string{
			"dashboards-get-all": `[{"id":1,"name":"Main"}]`,
		},
	}
	fsys := &mcpFS{toolCaller: mock}
	node := &toolFileNode{fsys: fsys, toolName: "dashboards-get-all", args: nil}

	data, err := node.readData()
	if err != nil {
		t.Fatal(err)
	}

	// Should be pretty-printed
	want := "[\n  {\n    \"id\": 1,\n    \"name\": \"Main\"\n  }\n]\n"
	if string(data) != want {
		t.Errorf("readData() = %q, want %q", string(data), want)
	}

	// Should end with newline
	if data[len(data)-1] != '\n' {
		t.Error("readData should end with newline")
	}
}

func TestReadDataNonJSON(t *testing.T) {
	mock := &mockToolCaller{
		results: map[string]string{
			"get-readme": `This is plain text, not JSON`,
		},
	}
	fsys := &mcpFS{toolCaller: mock}
	node := &toolFileNode{fsys: fsys, toolName: "get-readme", args: nil}

	data, err := node.readData()
	if err != nil {
		t.Fatal(err)
	}

	// Non-JSON should be returned as-is with trailing newline
	want := "This is plain text, not JSON\n"
	if string(data) != want {
		t.Errorf("readData() = %q, want %q", string(data), want)
	}
}

func TestReadDataPassesArgs(t *testing.T) {
	var capturedArgs map[string]interface{}
	mock := &mockToolCaller{
		results: map[string]string{
			"dashboard-get": `{"id":"abc"}`,
		},
	}
	// Wrap to capture args
	origCall := mock.CallTool
	_ = origCall
	wrapper := &argCapturingCaller{inner: mock, captured: &capturedArgs}

	fsys := &mcpFS{toolCaller: wrapper}
	args := map[string]interface{}{"dashboardId": "abc-123"}
	node := &toolFileNode{fsys: fsys, toolName: "dashboard-get", args: args}

	_, err := node.readData()
	if err != nil {
		t.Fatal(err)
	}

	if capturedArgs == nil {
		t.Fatal("args not captured")
	}
	if capturedArgs["dashboardId"] != "abc-123" {
		t.Errorf("dashboardId = %v, want abc-123", capturedArgs["dashboardId"])
	}
}

type argCapturingCaller struct {
	inner    *mockToolCaller
	captured *map[string]interface{}
}

func (a *argCapturingCaller) CallTool(name string, args map[string]interface{}) (json.RawMessage, error) {
	*a.captured = args
	return a.inner.CallTool(name, args)
}

func TestReadDataMultipleReadsConsistent(t *testing.T) {
	callCount := 0
	mock := &countingCaller{
		result: `{"count":42}`,
		count:  &callCount,
	}
	fsys := &mcpFS{toolCaller: mock}
	node := &toolFileNode{fsys: fsys, toolName: "test-tool", args: nil}

	data1, err := node.readData()
	if err != nil {
		t.Fatal(err)
	}
	data2, err := node.readData()
	if err != nil {
		t.Fatal(err)
	}

	if string(data1) != string(data2) {
		t.Errorf("inconsistent reads: %q vs %q", data1, data2)
	}
	// Each call should invoke the tool (no caching in toolFileNode)
	if callCount != 2 {
		t.Errorf("call count = %d, want 2", callCount)
	}
}

type countingCaller struct {
	result string
	count  *int
}

func (c *countingCaller) CallTool(name string, args map[string]interface{}) (json.RawMessage, error) {
	*c.count++
	return json.RawMessage(c.result), nil
}

func TestReadDataToolError(t *testing.T) {
	mock := &mockToolCaller{results: map[string]string{}}
	fsys := &mcpFS{toolCaller: mock}
	node := &toolFileNode{fsys: fsys, toolName: "nonexistent", args: nil}

	_, err := node.readData()
	if err == nil {
		t.Fatal("expected error for missing tool")
	}
}

func TestReadDataEmptyJSONObject(t *testing.T) {
	mock := &mockToolCaller{
		results: map[string]string{
			"empty-tool": `{}`,
		},
	}
	fsys := &mcpFS{toolCaller: mock}
	node := &toolFileNode{fsys: fsys, toolName: "empty-tool", args: nil}

	data, err := node.readData()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{}\n" {
		t.Errorf("readData() = %q, want %q", string(data), "{}\n")
	}
}

func TestReadDataNilArgs(t *testing.T) {
	mock := &mockToolCaller{
		results: map[string]string{
			"list-all": `[]`,
		},
	}
	fsys := &mcpFS{toolCaller: mock}
	node := &toolFileNode{fsys: fsys, toolName: "list-all", args: nil}

	data, err := node.readData()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "[]\n" {
		t.Errorf("readData() = %q, want %q", string(data), "[]\n")
	}
}
