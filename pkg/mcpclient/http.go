package mcpclient

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

// HTTPClient communicates with an MCP server over HTTP.
type HTTPClient struct {
	url     string
	headers map[string]string

	mu        sync.Mutex
	sessionID string
}

// NewHTTP creates an HTTP MCP client.
func NewHTTP(url string, headers map[string]string) (*HTTPClient, error) {
	c := &HTTPClient{url: url, headers: headers}
	if err := c.initialize(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *HTTPClient) initialize() error {
	body := map[string]interface{}{
		"jsonrpc": "2.0", "id": 0,
		"method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]string{"name": "mcpfs", "version": "0.1.0"},
		},
	}
	_, err := c.rpc(body)
	return err
}

func (c *HTTPClient) rpc(body interface{}) (json.RawMessage, error) {
	data, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", c.url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range c.headers {
		req.Header.Set(k, v)
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

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.mu.Lock()
		c.sessionID = sid
		c.mu.Unlock()
	}

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		msg := string(b)
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, msg)
	}

	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				var rpcResp struct {
					Result json.RawMessage `json:"result"`
					Error  *struct {
						Code    int    `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				payload := strings.TrimPrefix(line, "data: ")
				if json.Unmarshal([]byte(payload), &rpcResp) == nil {
					if rpcResp.Error != nil {
						return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
					}
					return rpcResp.Result, nil
				}
			}
		}
		return nil, fmt.Errorf("no data in SSE response")
	}

	b, _ := io.ReadAll(resp.Body)
	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &rpcResp); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}

// ListResources calls resources/list.
func (c *HTTPClient) ListResources() ([]Resource, error) {
	result, err := c.rpc(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "resources/list", "params": map[string]interface{}{},
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Resources []Resource `json:"resources"`
	}
	json.Unmarshal(result, &out)
	return out.Resources, nil
}

// ListResourceTemplates calls resources/templates/list.
func (c *HTTPClient) ListResourceTemplates() ([]ResourceTemplate, error) {
	result, err := c.rpc(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "resources/templates/list", "params": map[string]interface{}{},
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		ResourceTemplates []ResourceTemplate `json:"resourceTemplates"`
	}
	json.Unmarshal(result, &out)
	return out.ResourceTemplates, nil
}

// ListTools calls tools/list.
func (c *HTTPClient) ListTools() (json.RawMessage, error) {
	result, err := c.rpc(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": map[string]interface{}{},
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools json.RawMessage `json:"tools"`
	}
	json.Unmarshal(result, &out)
	return out.Tools, nil
}

// CallTool calls tools/call.
func (c *HTTPClient) CallTool(name string, args map[string]interface{}) (json.RawMessage, error) {
	result, err := c.rpc(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]interface{}{"name": name, "arguments": args},
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(result, &out) == nil && len(out.Content) > 0 {
		return json.RawMessage(out.Content[0].Text), nil
	}
	return json.RawMessage("{}"), nil
}

// ReadResource calls resources/read.
func (c *HTTPClient) ReadResource(uri string) (string, string, error) {
	result, err := c.rpc(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "resources/read",
		"params": map[string]interface{}{"uri": uri},
	})
	if err != nil {
		return "", "", err
	}
	var out struct {
		Contents []struct {
			URI      string `json:"uri"`
			MimeType string `json:"mimeType"`
			Text     string `json:"text"`
		} `json:"contents"`
	}
	if json.Unmarshal(result, &out) == nil && len(out.Contents) > 0 {
		return out.Contents[0].Text, out.Contents[0].MimeType, nil
	}
	return "", "", fmt.Errorf("empty response for %s", uri)
}

// Close is a no-op for HTTP clients.
func (c *HTTPClient) Close() {}
