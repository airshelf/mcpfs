package fuse

import (
	"context"
	"encoding/json"
	"log"
	"syscall"

	gofuse "github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// ToolCaller executes an MCP tools/call.
type ToolCaller interface {
	CallTool(name string, args map[string]interface{}) (json.RawMessage, error)
}

// toolFileNode is a FUSE file backed by a tools/call invocation.
type toolFileNode struct {
	gofuse.Inode
	fsys     *mcpFS
	toolName string
	args     map[string]interface{}
}

var _ = (gofuse.NodeOpener)((*toolFileNode)(nil))
var _ = (gofuse.NodeGetattrer)((*toolFileNode)(nil))
var _ = (gofuse.NodeReader)((*toolFileNode)(nil))

func (f *toolFileNode) readData() ([]byte, error) {
	result, err := f.fsys.toolCaller.CallTool(f.toolName, f.args)
	if err != nil {
		return nil, err
	}
	// Pretty-print JSON
	var v interface{}
	if json.Unmarshal(result, &v) == nil {
		out, _ := json.MarshalIndent(v, "", "  ")
		return append(out, '\n'), nil
	}
	return append([]byte(result), '\n'), nil
}

func (f *toolFileNode) Getattr(ctx context.Context, fh gofuse.FileHandle, out *fuse.AttrOut) syscall.Errno {
	data, err := f.readData()
	if err != nil {
		log.Printf("mcpfs: tool getattr %s: %v", f.toolName, err)
		out.Size = 0
	} else {
		out.Size = uint64(len(data))
	}
	out.Mode = syscall.S_IFREG | 0444
	return 0
}

func (f *toolFileNode) Open(ctx context.Context, flags uint32) (gofuse.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

func (f *toolFileNode) Read(ctx context.Context, fh gofuse.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data, err := f.readData()
	if err != nil {
		log.Printf("mcpfs: tool read %s: %v", f.toolName, err)
		return nil, syscall.EIO
	}
	if off >= int64(len(data)) {
		return fuse.ReadResultData(nil), 0
	}
	end := off + int64(len(dest))
	if end > int64(len(data)) {
		end = int64(len(data))
	}
	return fuse.ReadResultData(data[off:end]), 0
}
