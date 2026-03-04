package mcptool

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// ToolCaller executes an MCP tools/call and returns the result.
type ToolCaller interface {
	Call(toolName string, args map[string]interface{}) (json.RawMessage, error)
}

// Run dispatches CLI args to the appropriate tool.
// Returns an exit code (0 = success, 1 = error).
func Run(serverName string, tools []ToolDef, caller ToolCaller, args []string) int {
	if len(args) == 0 || args[0] == "tools" || args[0] == "--help" || args[0] == "-h" {
		printToolList(serverName, tools)
		return 0
	}

	toolName := args[0]
	tool := findTool(tools, toolName)
	if tool == nil {
		fmt.Fprintf(os.Stderr, "%s: unknown command %q\nRun: %s tools\n", serverName, toolName, serverName)
		return 1
	}

	if len(args) > 1 && (args[1] == "--help" || args[1] == "-h") {
		printToolHelp(serverName, tool)
		return 0
	}

	params, err := parseFlags(tool, args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %s: %v\n", serverName, toolName, err)
		return 1
	}

	// Re-wrap params in "data" object if the schema uses that pattern.
	callArgs := params
	if IsDataWrapped(tool.InputSchema) {
		callArgs = map[string]interface{}{"data": params}
	}

	result, err := caller.Call(tool.Name, callArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %s: %v\n", serverName, toolName, err)
		return 1
	}

	// Pretty-print JSON output.
	var v interface{}
	if json.Unmarshal(result, &v) == nil {
		out, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(out))
	} else {
		fmt.Println(string(result))
	}
	return 0
}

func findTool(tools []ToolDef, name string) *ToolDef {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

func printToolList(serverName string, tools []ToolDef) {
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	fmt.Fprintf(os.Stderr, "%s: %d tools available\n\n", serverName, len(tools))
	for _, t := range tools {
		desc := t.Description
		if len(desc) > 80 {
			desc = desc[:77] + "..."
		}
		fmt.Fprintf(os.Stderr, "  %-40s %s\n", t.Name, desc)
	}
	fmt.Fprintf(os.Stderr, "\nUsage: %s <tool-name> [--flag value ...]\n", serverName)
}

func printToolHelp(serverName string, tool *ToolDef) {
	fmt.Fprintf(os.Stderr, "%s %s\n\n", serverName, tool.Name)
	fmt.Fprintf(os.Stderr, "%s\n\n", tool.Description)

	params := ParseSchema(tool.InputSchema)
	if len(params) == 0 {
		fmt.Fprintln(os.Stderr, "No parameters.")
		return
	}

	sort.Slice(params, func(i, j int) bool { return params[i].Name < params[j].Name })
	fmt.Fprintln(os.Stderr, "Flags:")
	for _, p := range params {
		req := ""
		if p.Required {
			req = " (required)"
		}
		desc := p.Desc
		if desc == "" {
			desc = p.Type
		}
		fmt.Fprintf(os.Stderr, "  --%-30s %s%s\n", p.Name, desc, req)
	}
}

func parseFlags(tool *ToolDef, args []string) (map[string]interface{}, error) {
	params := ParseSchema(tool.InputSchema)
	paramMap := make(map[string]ParamDef, len(params))
	for _, p := range params {
		paramMap[p.Name] = p
	}

	result := make(map[string]interface{})

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			return nil, fmt.Errorf("unexpected argument: %s", arg)
		}
		name := strings.TrimPrefix(arg, "--")

		// Handle --flag=value
		var value string
		if idx := strings.IndexByte(name, '='); idx >= 0 {
			value = name[idx+1:]
			name = name[:idx]
		}

		p, ok := paramMap[name]
		if !ok {
			return nil, fmt.Errorf("unknown flag: --%s", name)
		}

		// Boolean flags don't require a value.
		if p.Type == "boolean" {
			if value == "" {
				result[name] = true
			} else {
				b, err := strconv.ParseBool(value)
				if err != nil {
					return nil, fmt.Errorf("--%s: invalid boolean: %s", name, value)
				}
				result[name] = b
			}
			continue
		}

		// Non-boolean flags require a value.
		if value == "" {
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("--%s requires a value", name)
			}
			value = args[i]
		}

		switch p.Type {
		case "integer":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("--%s: invalid integer: %s", name, value)
			}
			result[name] = n
		case "number":
			n, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return nil, fmt.Errorf("--%s: invalid number: %s", name, value)
			}
			result[name] = n
		case "array":
			// Comma-separated values.
			result[name] = strings.Split(value, ",")
		case "object":
			// Raw JSON string.
			var obj interface{}
			if err := json.Unmarshal([]byte(value), &obj); err != nil {
				return nil, fmt.Errorf("--%s: invalid JSON: %v", name, err)
			}
			result[name] = obj
		default:
			result[name] = value
		}
	}

	// Check required params.
	for _, p := range params {
		if p.Required {
			if _, ok := result[p.Name]; !ok {
				return nil, fmt.Errorf("required flag missing: --%s", p.Name)
			}
		}
	}

	return result, nil
}
