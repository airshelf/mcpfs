// Package mcptool bridges MCP tool schemas to CLI flags.
// It parses JSON Schema from tool definitions into flag definitions,
// dispatches CLI args to tool calls, and handles HTTP/stdio transports.
package mcptool

import "encoding/json"

// ToolDef is a tool definition from MCP tools/list.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ParamDef is a single CLI flag derived from a JSON Schema property.
type ParamDef struct {
	Name     string
	Type     string // string, integer, number, boolean, array, object
	Desc     string
	Required bool
}

type schemaObj struct {
	Properties map[string]struct {
		Type        string          `json:"type"`
		Description string          `json:"description"`
		Properties  json.RawMessage `json:"properties"` // for nested objects
		Required    []string        `json:"required"`
	} `json:"properties"`
	Required []string `json:"required"`
}

// ParseSchema extracts CLI-friendly parameter definitions from a JSON Schema.
// If the schema has a single "data" property of type object, it flattens
// the inner properties as top-level flags (common PostHog pattern).
func ParseSchema(schema json.RawMessage) []ParamDef {
	var s schemaObj
	if err := json.Unmarshal(schema, &s); err != nil {
		return nil
	}

	// Detect "data" wrapper: single required property of type object with sub-properties.
	if len(s.Properties) == 1 {
		if dataProp, ok := s.Properties["data"]; ok && dataProp.Type == "object" && dataProp.Properties != nil {
			var inner schemaObj
			// Re-parse inner schema.
			wrapped, _ := json.Marshal(map[string]interface{}{
				"properties": dataProp.Properties,
				"required":   dataProp.Required,
			})
			if json.Unmarshal(wrapped, &inner) == nil && len(inner.Properties) > 0 {
				return extractParams(inner)
			}
		}
	}

	return extractParams(s)
}

// IsDataWrapped returns true if the schema uses a single "data" object wrapper.
func IsDataWrapped(schema json.RawMessage) bool {
	var s schemaObj
	if err := json.Unmarshal(schema, &s); err != nil {
		return false
	}
	if len(s.Properties) == 1 {
		if dataProp, ok := s.Properties["data"]; ok && dataProp.Type == "object" && dataProp.Properties != nil {
			return true
		}
	}
	return false
}

// BuildSchema constructs a JSON Schema from parameter definitions.
// Used by servers that define tools in Go code (no tools.json).
func BuildSchema(params []ParamDef) json.RawMessage {
	props := make(map[string]interface{})
	var required []string
	for _, p := range params {
		prop := map[string]string{"type": p.Type}
		if p.Desc != "" {
			prop["description"] = p.Desc
		}
		props[p.Name] = prop
		if p.Required {
			required = append(required, p.Name)
		}
	}
	schema := map[string]interface{}{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	data, _ := json.Marshal(schema)
	return data
}

func extractParams(s schemaObj) []ParamDef {
	reqSet := make(map[string]bool, len(s.Required))
	for _, r := range s.Required {
		reqSet[r] = true
	}

	params := make([]ParamDef, 0, len(s.Properties))
	for name, prop := range s.Properties {
		params = append(params, ParamDef{
			Name:     name,
			Type:     prop.Type,
			Desc:     prop.Description,
			Required: reqSet[name],
		})
	}
	return params
}
