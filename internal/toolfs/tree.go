package toolfs

// ToolEntry is a classified tool ready for filesystem mounting.
type ToolEntry struct {
	Name           string
	Class          ToolClass
	Filename       string   // e.g. "dashboards.json" or "dashboards"
	ToolName       string   // original MCP tool name for tools/call
	RequiredParams []string // param names for get-by-id tools
}

// ToolTreeNode represents a tool-backed entry in the filesystem.
type ToolTreeNode struct {
	IsDir          bool
	ToolName       string   // MCP tool name
	RequiredParams []string // for get-by-id template dirs
}

// BuildToolTree creates a flat map of filename → ToolTreeNode.
func BuildToolTree(entries []ToolEntry) map[string]*ToolTreeNode {
	tree := make(map[string]*ToolTreeNode)
	for _, e := range entries {
		switch e.Class {
		case ToolList:
			tree[e.Filename] = &ToolTreeNode{ToolName: e.ToolName}
		case ToolGet:
			tree[e.Filename] = &ToolTreeNode{
				IsDir:          true,
				ToolName:       e.ToolName,
				RequiredParams: e.RequiredParams,
			}
		}
	}
	return tree
}
