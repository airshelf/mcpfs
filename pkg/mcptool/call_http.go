package mcptool

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// HTTPCaller sends tools/call to an HTTP MCP server.
// Handles both plain JSON and Streamable HTTP (SSE) responses.
type HTTPCaller struct {
	URL        string
	AuthHeader string // e.g. "Bearer phx_..."

	mu        sync.Mutex
	sessionID string
}

// Call sends a JSON-RPC tools/call request and returns the result content.
func (c *HTTPCaller) Call(toolName string, args map[string]interface{}) (json.RawMessage, error) {
	// Initialize session if needed.
	if err := c.ensureSession(); err != nil {
		return nil, fmt.Errorf("session init: %w", err)
	}

	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": toolName, "arguments": args},
	}

	respBody, err := c.post(body)
	if err != nil {
		return nil, err
	}

	// MCP tools/call result has content[] array with text blocks.
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse result: %w", err)
	}

	if len(result.Content) > 0 {
		return json.RawMessage(result.Content[0].Text), nil
	}
	return json.RawMessage("{}"), nil
}

func (c *HTTPCaller) ensureSession() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessionID != "" {
		return nil
	}

	body := map[string]interface{}{
		"jsonrpc": "2.0", "id": 0,
		"method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]string{"name": "mcpfs", "version": "0.1.0"},
		},
	}

	_, err := c.postRaw(body)
	return err
}

// post sends a JSON-RPC request and returns the result field.
func (c *HTTPCaller) post(body interface{}) (json.RawMessage, error) {
	respData, err := c.postRaw(body)
	if err != nil {
		return nil, err
	}

	var rpc struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respData, &rpc); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if rpc.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", rpc.Error.Code, rpc.Error.Message)
	}
	return rpc.Result, nil
}

// postRaw sends a JSON-RPC request and returns the raw response JSON.
// Handles both plain JSON and SSE responses.
func (c *HTTPCaller) postRaw(body interface{}) (json.RawMessage, error) {
	data, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", c.URL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.AuthHeader != "" {
		req.Header.Set("Authorization", c.AuthHeader)
	}
	c.mu.Lock()
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	c.mu.Unlock()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	// Capture session ID.
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.mu.Lock()
		c.sessionID = sid
		c.mu.Unlock()
	}

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(b), 200))
	}

	ct := resp.Header.Get("Content-Type")

	// SSE: extract first "data:" line that parses as JSON-RPC.
	if strings.Contains(ct, "text/event-stream") {
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB for large responses
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			return json.RawMessage(strings.TrimPrefix(line, "data: ")), nil
		}
		return nil, fmt.Errorf("no data in SSE response")
	}

	// Plain JSON.
	b, _ := io.ReadAll(resp.Body)
	return json.RawMessage(b), nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
