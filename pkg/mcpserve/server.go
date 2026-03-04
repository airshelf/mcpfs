// Package mcpserve provides a minimal MCP resource server framework over stdio.
// It handles JSON-RPC boilerplate (initialize, resources/list, resources/templates/list,
// resources/read) so server implementations only define resources and a ReadFunc.
package mcpserve

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// Resource is a static MCP resource.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// Template is an MCP resource template with URI parameters.
type Template struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ReadResult is the content returned by a ReadFunc.
type ReadResult struct {
	Text     string
	MimeType string
}

// ReadFunc handles resources/read requests. Given a URI, return content or error.
type ReadFunc func(uri string) (ReadResult, error)

// Server is a minimal MCP resource server over stdio.
type Server struct {
	Name      string
	Version   string
	resources []Resource
	templates []Template
	handler   ReadFunc
}

// New creates a new MCP resource server.
func New(name, version string, handler ReadFunc) *Server {
	return &Server{
		Name:    name,
		Version: version,
		handler: handler,
	}
}

// AddResource registers a static resource.
func (s *Server) AddResource(r Resource) {
	s.resources = append(s.resources, r)
}

// AddTemplate registers a resource template.
func (s *Server) AddTemplate(t Template) {
	s.templates = append(s.templates, t)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
}

func reply(w *bufio.Writer, id interface{}, result interface{}, rpcErr interface{}) {
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr}
	data, _ := json.Marshal(resp)
	w.Write(data)
	w.WriteByte('\n')
	w.Flush()
}

func rpcError(code int, msg string) interface{} {
	return map[string]interface{}{"code": code, "message": msg}
}

// Serve starts the JSON-RPC server on stdin/stdout. Blocks until stdin closes.
func (s *Server) Serve() error {
	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return nil // stdin closed
		}

		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}

		var id interface{}
		if req.ID != nil {
			json.Unmarshal(req.ID, &id)
		}

		switch req.Method {
		case "initialize":
			reply(writer, id, map[string]interface{}{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]interface{}{"resources": map[string]interface{}{}},
				"serverInfo":      map[string]string{"name": s.Name, "version": s.Version},
			}, nil)

		case "notifications/initialized":
			// No reply for notifications.

		case "resources/list":
			reply(writer, id, map[string]interface{}{"resources": s.resources}, nil)

		case "resources/templates/list":
			reply(writer, id, map[string]interface{}{"resourceTemplates": s.templates}, nil)

		case "resources/read":
			var params struct {
				URI string `json:"uri"`
			}
			json.Unmarshal(req.Params, &params)
			result, err := s.handler(params.URI)
			if err != nil {
				reply(writer, id, nil, rpcError(-32603, err.Error()))
			} else {
				mime := result.MimeType
				if mime == "" {
					mime = "application/json"
				}
				reply(writer, id, map[string]interface{}{
					"contents": []map[string]string{{"uri": params.URI, "mimeType": mime, "text": result.Text}},
				}, nil)
			}

		default:
			if id != nil {
				reply(writer, id, nil, rpcError(-32601, fmt.Sprintf("method not found: %s", req.Method)))
			}
		}
	}
}
