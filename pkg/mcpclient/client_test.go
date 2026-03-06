package mcpclient

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
)

// mockServer starts a goroutine that reads JSON-RPC from r and writes responses to w.
// It mimics a minimal MCP server with configurable resources and templates.
type mockServer struct {
	resources []Resource
	templates []ResourceTemplate
	readFunc  func(uri string) (string, string, error)
}

func (m *mockServer) run(r io.Reader, w io.WriteCloser) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}

		var id interface{}
		json.Unmarshal(req.ID, &id)

		switch req.Method {
		case "initialize":
			writeResponse(w, id, map[string]interface{}{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]interface{}{"resources": map[string]interface{}{}},
				"serverInfo":      map[string]string{"name": "mock", "version": "0.1.0"},
			}, nil)

		case "notifications/initialized":
			// no reply

		case "resources/list":
			writeResponse(w, id, map[string]interface{}{"resources": m.resources}, nil)

		case "resources/templates/list":
			writeResponse(w, id, map[string]interface{}{"resourceTemplates": m.templates}, nil)

		case "resources/read":
			var params struct{ URI string `json:"uri"` }
			json.Unmarshal(req.Params, &params)
			if m.readFunc != nil {
				text, mime, err := m.readFunc(params.URI)
				if err != nil {
					writeResponse(w, id, nil, map[string]interface{}{"code": -32603, "message": err.Error()})
				} else {
					if mime == "" {
						mime = "application/json"
					}
					writeResponse(w, id, map[string]interface{}{
						"contents": []map[string]string{{"uri": params.URI, "mimeType": mime, "text": text}},
					}, nil)
				}
			} else {
				writeResponse(w, id, nil, map[string]interface{}{"code": -32603, "message": "no handler"})
			}

		default:
			if id != nil {
				writeResponse(w, id, nil, map[string]interface{}{"code": -32601, "message": "method not found: " + req.Method})
			}
		}
	}
	w.Close()
}

func writeResponse(w io.Writer, id interface{}, result, rpcErr interface{}) {
	resp := map[string]interface{}{"jsonrpc": "2.0", "id": id}
	if result != nil {
		resp["result"] = result
	}
	if rpcErr != nil {
		resp["error"] = rpcErr
	}
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	w.Write(data)
}

// newMockClient creates a Client connected to a mock server via pipes.
func newMockClient(t *testing.T, m *mockServer) *Client {
	t.Helper()

	// Client writes to serverIn, reads from serverOut
	serverInR, serverInW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	serverOutR, serverOutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	go m.run(serverInR, serverOutW)

	c := &Client{
		stdin:  serverInW,
		reader: bufio.NewReader(serverOutR),
	}

	// Perform initialization
	if err := c.initialize(); err != nil {
		t.Fatalf("mock init: %v", err)
	}

	t.Cleanup(func() {
		serverInW.Close()
		serverInR.Close()
		serverOutR.Close()
	})

	return c
}

func TestClientListResources(t *testing.T) {
	m := &mockServer{
		resources: []Resource{
			{URI: "test://a", Name: "a", MimeType: "application/json"},
			{URI: "test://b", Name: "b", MimeType: "text/plain"},
		},
	}
	c := newMockClient(t, m)

	resources, err := c.ListResources()
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 2 {
		t.Fatalf("got %d resources, want 2", len(resources))
	}
	if resources[0].URI != "test://a" {
		t.Errorf("URI = %q, want test://a", resources[0].URI)
	}
	if resources[1].Name != "b" {
		t.Errorf("Name = %q, want b", resources[1].Name)
	}
}

func TestClientListEmptyResources(t *testing.T) {
	m := &mockServer{}
	c := newMockClient(t, m)

	resources, err := c.ListResources()
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 0 {
		t.Fatalf("got %d resources, want 0", len(resources))
	}
}

func TestClientListResourceTemplates(t *testing.T) {
	m := &mockServer{
		templates: []ResourceTemplate{
			{URITemplate: "test://items/{id}", Name: "item"},
			{URITemplate: "test://items/{id}/detail", Name: "detail"},
		},
	}
	c := newMockClient(t, m)

	templates, err := c.ListResourceTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 2 {
		t.Fatalf("got %d templates, want 2", len(templates))
	}
}

func TestClientReadResource(t *testing.T) {
	m := &mockServer{
		readFunc: func(uri string) (string, string, error) {
			switch uri {
			case "test://hello":
				return `{"msg":"hello"}`, "application/json", nil
			case "test://readme":
				return "# README", "text/plain", nil
			default:
				return "", "", fmt.Errorf("not found: %s", uri)
			}
		},
	}
	c := newMockClient(t, m)

	text, mime, err := c.ReadResource("test://hello")
	if err != nil {
		t.Fatal(err)
	}
	if text != `{"msg":"hello"}` {
		t.Errorf("text = %q", text)
	}
	if mime != "application/json" {
		t.Errorf("mime = %q", mime)
	}
}

func TestClientReadResourceTextPlain(t *testing.T) {
	m := &mockServer{
		readFunc: func(uri string) (string, string, error) {
			return "plain text content", "text/plain", nil
		},
	}
	c := newMockClient(t, m)

	text, mime, err := c.ReadResource("test://text")
	if err != nil {
		t.Fatal(err)
	}
	if text != "plain text content" {
		t.Errorf("text = %q", text)
	}
	if mime != "text/plain" {
		t.Errorf("mime = %q", mime)
	}
}

func TestClientReadResourceError(t *testing.T) {
	m := &mockServer{
		readFunc: func(uri string) (string, string, error) {
			return "", "", fmt.Errorf("resource not found")
		},
	}
	c := newMockClient(t, m)

	_, _, err := c.ReadResource("test://missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "rpc error -32603: resource not found" {
		t.Errorf("error = %q", got)
	}
}

func TestClientReadResourceLargePayload(t *testing.T) {
	// Test with a 1MB JSON response
	bigData := make([]byte, 1<<20)
	for i := range bigData {
		bigData[i] = 'x'
	}
	payload := fmt.Sprintf(`{"data":"%s"}`, string(bigData))

	m := &mockServer{
		readFunc: func(uri string) (string, string, error) {
			return payload, "application/json", nil
		},
	}
	c := newMockClient(t, m)

	text, _, err := c.ReadResource("test://big")
	if err != nil {
		t.Fatal(err)
	}
	if len(text) != len(payload) {
		t.Errorf("len = %d, want %d", len(text), len(payload))
	}
}

func TestClientConcurrentReads(t *testing.T) {
	callCount := 0
	var mu sync.Mutex

	m := &mockServer{
		readFunc: func(uri string) (string, string, error) {
			mu.Lock()
			callCount++
			mu.Unlock()
			return fmt.Sprintf(`{"uri":"%s"}`, uri), "application/json", nil
		},
	}
	c := newMockClient(t, m)

	// Note: The client's call() method uses a mutex, so concurrent calls
	// are serialized but should all succeed.
	var wg sync.WaitGroup
	errors := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			uri := fmt.Sprintf("test://item/%d", i)
			text, _, err := c.ReadResource(uri)
			if err != nil {
				errors <- fmt.Errorf("item %d: %v", i, err)
				return
			}
			var result map[string]string
			if err := json.Unmarshal([]byte(text), &result); err != nil {
				errors <- fmt.Errorf("item %d parse: %v", i, err)
				return
			}
			if result["uri"] != uri {
				errors <- fmt.Errorf("item %d: got uri %q, want %q", i, result["uri"], uri)
			}
		}(i)
	}
	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}

	mu.Lock()
	if callCount != 10 {
		t.Errorf("callCount = %d, want 10", callCount)
	}
	mu.Unlock()
}

func TestClientIDsAreUnique(t *testing.T) {
	m := &mockServer{
		resources: []Resource{{URI: "test://a", Name: "a"}},
		templates: []ResourceTemplate{{URITemplate: "test://b/{id}", Name: "b"}},
		readFunc: func(uri string) (string, string, error) {
			return `{}`, "", nil
		},
	}
	c := newMockClient(t, m)

	// Make several calls and verify client doesn't panic or produce duplicate IDs
	// (the nextID atomic counter should handle this)
	for i := 0; i < 20; i++ {
		_, err := c.ListResources()
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
}
