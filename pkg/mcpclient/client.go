// Package mcpclient is a JSON-RPC client for MCP servers over stdio.
package mcpclient

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
)

// Client communicates with an MCP server subprocess over stdin/stdout.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	mu     sync.Mutex
	nextID atomic.Int64
}

// New starts an MCP server subprocess and performs the initialize handshake.
func New(command string, args []string) (*Client, error) {
	cmd := exec.Command(command, args...)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", command, err)
	}

	c := &Client{
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReader(stdout),
	}

	if err := c.initialize(); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}

	return c, nil
}

func (c *Client) call(method string, params interface{}) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := c.nextID.Add(1)
	req := jsonRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')

	if _, err := c.stdin.Write(data); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	for {
		line, err := c.reader.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("read: %w", err)
		}

		var resp jsonRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}
		if resp.ID != id {
			continue
		}

		if resp.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *Client) initialize() error {
	params := map[string]interface{}{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]string{"name": "mcpfs", "version": "0.1.0"},
	}
	_, err := c.call("initialize", params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	notif, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})
	notif = append(notif, '\n')
	c.stdin.Write(notif)
	c.mu.Unlock()
	return nil
}

// ListResources calls resources/list.
func (c *Client) ListResources() ([]Resource, error) {
	result, err := c.call("resources/list", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Resources []Resource `json:"resources"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		return nil, err
	}
	return out.Resources, nil
}

// ListResourceTemplates calls resources/templates/list.
func (c *Client) ListResourceTemplates() ([]ResourceTemplate, error) {
	result, err := c.call("resources/templates/list", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var out struct {
		ResourceTemplates []ResourceTemplate `json:"resourceTemplates"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		return nil, err
	}
	return out.ResourceTemplates, nil
}

// ReadResource calls resources/read and returns the text content and MIME type.
func (c *Client) ReadResource(uri string) (string, string, error) {
	result, err := c.call("resources/read", map[string]interface{}{"uri": uri})
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
	if err := json.Unmarshal(result, &out); err != nil {
		return "", "", err
	}
	if len(out.Contents) == 0 {
		return "", "", fmt.Errorf("empty response for %s", uri)
	}
	return out.Contents[0].Text, out.Contents[0].MimeType, nil
}

// Close kills the server subprocess.
func (c *Client) Close() {
	c.stdin.Close()
	c.cmd.Process.Kill()
	c.cmd.Wait()
}
