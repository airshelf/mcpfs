// Package toolfs classifies MCP tools into filesystem-mountable categories.
package toolfs

import (
	"encoding/json"
	"strings"
)

type ToolClass int

const (
	ToolList  ToolClass = iota // No required params, read-only → static file
	ToolGet                    // Has required params, read-only → template dir
	ToolWrite                  // Mutating → CLI only
	ToolQuery                  // Search/query → CLI only
)

func (c ToolClass) String() string {
	switch c {
	case ToolList:
		return "list"
	case ToolGet:
		return "get"
	case ToolWrite:
		return "write"
	case ToolQuery:
		return "query"
	}
	return "unknown"
}

// ClassifyTool determines how a tool should be exposed in the filesystem.
func ClassifyTool(name string, inputSchema json.RawMessage) ToolClass {
	lower := strings.ToLower(name)

	if matchesAny(lower, "create", "update", "delete", "remove", "add", "set", "patch", "put", "post") {
		return ToolWrite
	}
	if matchesAny(lower, "search", "query", "find", "run", "execute") {
		return ToolQuery
	}

	required := RequiredParams(inputSchema)
	if len(required) == 0 {
		return ToolList
	}

	if matchesAny(lower, "get", "retrieve", "read", "show", "describe") {
		return ToolGet
	}

	return ToolWrite // safe default
}

// ToolToFilename converts a tool name to a filesystem name.
// List tools → "resource.json", Get tools → "resources" (directory).
func ToolToFilename(name string, class ToolClass) string {
	resource := stripVerb(name)
	if class == ToolList {
		return resource + ".json"
	}
	// Get tools: pluralize for directory name
	return pluralize(resource)
}

// RequiredParams extracts required parameter names from a JSON Schema.
func RequiredParams(schema json.RawMessage) []string {
	if schema == nil {
		return nil
	}
	var s struct {
		Required []string `json:"required"`
	}
	json.Unmarshal(schema, &s)
	return s.Required
}

func matchesAny(name string, verbs ...string) bool {
	for _, v := range verbs {
		if strings.Contains(name, v) {
			return true
		}
	}
	return false
}

func stripVerb(name string) string {
	// Normalize separator to hyphen
	n := strings.ReplaceAll(name, "_", "-")

	// Strip suffixes: -get-all, -list, -get, -retrieve
	// -get-all implies plural (e.g. feature-flag-get-all → feature-flags)
	for _, suffix := range []string{"-get-all", "-list"} {
		if strings.HasSuffix(n, suffix) {
			return pluralize(strings.TrimSuffix(n, suffix))
		}
	}
	for _, suffix := range []string{"-retrieve", "-get"} {
		if strings.HasSuffix(n, suffix) {
			return strings.TrimSuffix(n, suffix)
		}
	}

	// Strip prefixes: list-, get-all-, get-, retrieve-
	for _, prefix := range []string{"list-", "get-all-", "retrieve-all-", "get-", "retrieve-"} {
		if strings.HasPrefix(n, prefix) {
			return strings.TrimPrefix(n, prefix)
		}
	}

	return n
}

func pluralize(s string) string {
	if s == "" {
		return s
	}
	if strings.HasSuffix(s, "s") {
		return s
	}
	return s + "s"
}
