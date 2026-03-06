package mcpclient

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// mockMCPHandler handles JSON-RPC requests for testing the HTTP client.
type mockMCPHandler struct {
	mu              sync.Mutex
	sessionID       string
	receivedHeaders map[string]string
	tools           []map[string]interface{}
	resources       []Resource
	templates       []ResourceTemplate
	callToolFunc    func(name string, args map[string]interface{}) interface{}
	readFunc        func(uri string) (string, string)
	failStatus      int // if > 0, return this HTTP status
	useSSE          bool
}

func (m *mockMCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	// Capture headers
	if m.receivedHeaders == nil {
		m.receivedHeaders = make(map[string]string)
	}
	for k := range r.Header {
		m.receivedHeaders[k] = r.Header.Get(k)
	}
	m.mu.Unlock()

	if m.failStatus > 0 {
		http.Error(w, "server error", m.failStatus)
		return
	}

	var req struct {
		JSONRPC string                 `json:"jsonrpc"`
		ID      interface{}            `json:"id"`
		Method  string                 `json:"method"`
		Params  map[string]interface{} `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}

	if m.sessionID != "" {
		w.Header().Set("Mcp-Session-Id", m.sessionID)
	}

	var result interface{}
	var rpcErr interface{}

	switch req.Method {
	case "initialize":
		result = map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"serverInfo":      map[string]string{"name": "test"},
			"capabilities":    map[string]interface{}{},
		}
	case "tools/list":
		tools := m.tools
		if tools == nil {
			tools = []map[string]interface{}{}
		}
		result = map[string]interface{}{"tools": tools}
	case "resources/list":
		res := m.resources
		if res == nil {
			res = []Resource{}
		}
		result = map[string]interface{}{"resources": res}
	case "resources/templates/list":
		tpl := m.templates
		if tpl == nil {
			tpl = []ResourceTemplate{}
		}
		result = map[string]interface{}{"resourceTemplates": tpl}
	case "tools/call":
		params := req.Params
		name, _ := params["name"].(string)
		args, _ := params["arguments"].(map[string]interface{})
		if m.callToolFunc != nil {
			text := m.callToolFunc(name, args)
			textStr, _ := json.Marshal(text)
			result = map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": string(textStr)},
				},
			}
		} else {
			result = map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": "{}"},
				},
			}
		}
	case "resources/read":
		params := req.Params
		uri, _ := params["uri"].(string)
		if m.readFunc != nil {
			text, mime := m.readFunc(uri)
			result = map[string]interface{}{
				"contents": []map[string]string{
					{"uri": uri, "mimeType": mime, "text": text},
				},
			}
		} else {
			rpcErr = map[string]interface{}{"code": -32603, "message": "no handler"}
		}
	default:
		rpcErr = map[string]interface{}{"code": -32601, "message": "method not found"}
	}

	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      req.ID,
	}
	if rpcErr != nil {
		resp["error"] = rpcErr
	} else {
		resp["result"] = result
	}

	if m.useSSE {
		w.Header().Set("Content-Type", "text/event-stream")
		data, _ := json.Marshal(resp)
		fmt.Fprintf(w, "data: %s\n\n", data)
	} else {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

func newTestHTTPClient(t *testing.T, handler *mockMCPHandler) (*HTTPClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := NewHTTP(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	return client, srv
}

func TestHTTPClientListTools(t *testing.T) {
	handler := &mockMCPHandler{
		tools: []map[string]interface{}{
			{"name": "test-tool", "description": "A test tool"},
			{"name": "other-tool", "description": "Another tool"},
		},
	}
	client, _ := newTestHTTPClient(t, handler)

	tools, err := client.ListTools()
	if err != nil {
		t.Fatal(err)
	}

	var toolList []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(tools, &toolList); err != nil {
		t.Fatal(err)
	}
	if len(toolList) != 2 {
		t.Fatalf("got %d tools, want 2", len(toolList))
	}
	if toolList[0].Name != "test-tool" {
		t.Errorf("tool[0].Name = %q", toolList[0].Name)
	}
}

func TestHTTPClientListResources(t *testing.T) {
	handler := &mockMCPHandler{
		resources: []Resource{
			{URI: "test://a", Name: "a", MimeType: "application/json"},
			{URI: "test://b", Name: "b", MimeType: "text/plain"},
		},
	}
	client, _ := newTestHTTPClient(t, handler)

	resources, err := client.ListResources()
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 2 {
		t.Fatalf("got %d resources, want 2", len(resources))
	}
	if resources[0].URI != "test://a" {
		t.Errorf("URI = %q", resources[0].URI)
	}
}

func TestHTTPClientListResourceTemplates(t *testing.T) {
	handler := &mockMCPHandler{
		templates: []ResourceTemplate{
			{URITemplate: "test://items/{id}", Name: "item"},
		},
	}
	client, _ := newTestHTTPClient(t, handler)

	templates, err := client.ListResourceTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 1 {
		t.Fatalf("got %d templates, want 1", len(templates))
	}
	if templates[0].URITemplate != "test://items/{id}" {
		t.Errorf("URITemplate = %q", templates[0].URITemplate)
	}
}

func TestHTTPClientReadResource(t *testing.T) {
	handler := &mockMCPHandler{
		readFunc: func(uri string) (string, string) {
			return `{"data": "hello"}`, "application/json"
		},
	}
	client, _ := newTestHTTPClient(t, handler)

	text, mime, err := client.ReadResource("test://hello")
	if err != nil {
		t.Fatal(err)
	}
	if text != `{"data": "hello"}` {
		t.Errorf("text = %q", text)
	}
	if mime != "application/json" {
		t.Errorf("mime = %q", mime)
	}
}

func TestHTTPClientCallTool(t *testing.T) {
	handler := &mockMCPHandler{
		callToolFunc: func(name string, args map[string]interface{}) interface{} {
			return map[string]interface{}{"tool": name, "args": args}
		},
	}
	client, _ := newTestHTTPClient(t, handler)

	result, err := client.CallTool("my-tool", map[string]interface{}{"key": "value"})
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["tool"] != "my-tool" {
		t.Errorf("tool = %v", parsed["tool"])
	}
}

func TestHTTPClientSSEResponse(t *testing.T) {
	handler := &mockMCPHandler{
		useSSE: true,
		tools: []map[string]interface{}{
			{"name": "sse-tool", "description": "SSE test"},
		},
	}
	// Must create manually since initialize also uses SSE
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := NewHTTP(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}

	tools, err := client.ListTools()
	if err != nil {
		t.Fatal(err)
	}

	var toolList []struct {
		Name string `json:"name"`
	}
	json.Unmarshal(tools, &toolList)
	if len(toolList) != 1 || toolList[0].Name != "sse-tool" {
		t.Errorf("SSE tools = %s", tools)
	}
}

func TestHTTPClientErrorResponse(t *testing.T) {
	handler := &mockMCPHandler{failStatus: 500}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	_, err := NewHTTP(srv.URL, nil)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention 500: %v", err)
	}
}

func TestHTTPClientSessionIDPropagation(t *testing.T) {
	handler := &mockMCPHandler{
		sessionID: "test-session-42",
	}
	client, _ := newTestHTTPClient(t, handler)

	// After initialize, session ID should be stored
	client.mu.Lock()
	sid := client.sessionID
	client.mu.Unlock()

	if sid != "test-session-42" {
		t.Errorf("sessionID = %q, want test-session-42", sid)
	}

	// Subsequent call should send the session ID
	handler.mu.Lock()
	handler.receivedHeaders = nil // reset
	handler.mu.Unlock()

	_, _ = client.ListTools()

	handler.mu.Lock()
	gotSID := handler.receivedHeaders["Mcp-Session-Id"]
	handler.mu.Unlock()

	if gotSID != "test-session-42" {
		t.Errorf("Mcp-Session-Id header = %q, want test-session-42", gotSID)
	}
}

func TestHTTPClientCustomHeaders(t *testing.T) {
	handler := &mockMCPHandler{}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	headers := map[string]string{
		"Authorization": "Bearer my-token",
		"X-Custom":      "custom-value",
	}
	client, err := NewHTTP(srv.URL, headers)
	if err != nil {
		t.Fatal(err)
	}

	// Make a call so headers are sent
	handler.mu.Lock()
	handler.receivedHeaders = nil
	handler.mu.Unlock()

	_, _ = client.ListTools()

	handler.mu.Lock()
	gotAuth := handler.receivedHeaders["Authorization"]
	gotCustom := handler.receivedHeaders["X-Custom"]
	handler.mu.Unlock()

	if gotAuth != "Bearer my-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotCustom != "custom-value" {
		t.Errorf("X-Custom = %q", gotCustom)
	}
}

func TestHTTPClientRPCError(t *testing.T) {
	handler := &mockMCPHandler{}
	client, _ := newTestHTTPClient(t, handler)

	// ReadResource with no handler returns an RPC error
	_, _, err := client.ReadResource("test://missing")
	if err == nil {
		t.Fatal("expected RPC error")
	}
	if !strings.Contains(err.Error(), "rpc error") {
		t.Errorf("error = %v, want rpc error", err)
	}
}

func TestHTTPClientClose(t *testing.T) {
	handler := &mockMCPHandler{}
	client, _ := newTestHTTPClient(t, handler)
	// Close should not panic
	client.Close()
}

func TestHTTPClientCallToolDefaultResponse(t *testing.T) {
	// When callToolFunc is nil, the handler returns "{}"
	handler := &mockMCPHandler{}
	client, _ := newTestHTTPClient(t, handler)

	result, err := client.CallTool("any-tool", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != "{}" {
		t.Errorf("result = %q, want {}", string(result))
	}
}

func TestHTTPClientEmptyToolsList(t *testing.T) {
	handler := &mockMCPHandler{tools: nil}
	client, _ := newTestHTTPClient(t, handler)

	tools, err := client.ListTools()
	if err != nil {
		t.Fatal(err)
	}

	var toolList []interface{}
	json.Unmarshal(tools, &toolList)
	if len(toolList) != 0 {
		t.Errorf("expected empty tools, got %d", len(toolList))
	}
}
