// capture-tools connects to an HTTP MCP server and saves its tool definitions.
// Supports Streamable HTTP transport (SSE responses, session management).
//
// Usage:
//
//	go run ./cmd/capture-tools -url https://mcp.posthog.com/mcp -auth "Bearer $KEY" -out tools.json
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
)

var sessionID string

func main() {
	url := flag.String("url", "", "MCP server URL (required)")
	auth := flag.String("auth", "", "Authorization header value")
	out := flag.String("out", "tools.json", "Output file path")
	flag.Parse()

	if *url == "" {
		fmt.Fprintln(os.Stderr, "capture-tools: -url required")
		os.Exit(1)
	}

	// Initialize.
	_, err := rpc(*url, *auth, 1, "initialize", map[string]interface{}{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]string{"name": "capture-tools", "version": "0.1.0"},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "capture-tools: initialize: %v\n", err)
		os.Exit(1)
	}

	// List tools.
	result, err := rpc(*url, *auth, 2, "tools/list", map[string]interface{}{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "capture-tools: tools/list: %v\n", err)
		os.Exit(1)
	}

	var toolsResult struct {
		Tools json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(result, &toolsResult); err != nil {
		fmt.Fprintf(os.Stderr, "capture-tools: parse: %v\n", err)
		os.Exit(1)
	}

	// Pretty-print and write.
	var tools interface{}
	json.Unmarshal(toolsResult.Tools, &tools)
	pretty, _ := json.MarshalIndent(tools, "", "  ")

	if err := os.WriteFile(*out, pretty, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "capture-tools: write %s: %v\n", *out, err)
		os.Exit(1)
	}

	var list []interface{}
	json.Unmarshal(toolsResult.Tools, &list)
	fmt.Fprintf(os.Stderr, "capture-tools: wrote %d tools to %s\n", len(list), *out)
}

// rpc sends a JSON-RPC request and handles both SSE and plain JSON responses.
func rpc(url, auth string, id int, method string, params interface{}) (json.RawMessage, error) {
	body := map[string]interface{}{
		"jsonrpc": "2.0", "id": id,
		"method": method,
		"params": params,
	}
	data, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", url, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Capture session ID from response.
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		sessionID = sid
	}

	if resp.StatusCode >= 400 {
		scanner := bufio.NewScanner(resp.Body)
		var msg string
		if scanner.Scan() {
			msg = scanner.Text()
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}

	ct := resp.Header.Get("Content-Type")

	// SSE response: parse "data:" lines.
	if strings.Contains(ct, "text/event-stream") {
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB buffer for large tool lists
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			jsonData := strings.TrimPrefix(line, "data: ")
			var rpcResp struct {
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(jsonData), &rpcResp); err != nil {
				continue
			}
			if rpcResp.Error != nil {
				return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
			}
			return rpcResp.Result, nil
		}
		return nil, fmt.Errorf("no data in SSE response")
	}

	// Plain JSON response.
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &rpcResp); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}
