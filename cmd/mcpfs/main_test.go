package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFileValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	content := "KEY1=value1\nKEY2=value2\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Clear first
	os.Unsetenv("KEY1")
	os.Unsetenv("KEY2")

	loadEnvFile(path)

	if v := os.Getenv("KEY1"); v != "value1" {
		t.Errorf("KEY1 = %q, want value1", v)
	}
	if v := os.Getenv("KEY2"); v != "value2" {
		t.Errorf("KEY2 = %q, want value2", v)
	}

	os.Unsetenv("KEY1")
	os.Unsetenv("KEY2")
}

func TestLoadEnvFileNonexistent(t *testing.T) {
	// Should silently return without error
	loadEnvFile("/nonexistent/path/env")
}

func TestLoadEnvFileCommentsAndEmptyLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	content := `# This is a comment
KEY1=value1

# Another comment

KEY2=value2
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	os.Unsetenv("KEY1")
	os.Unsetenv("KEY2")

	loadEnvFile(path)

	if v := os.Getenv("KEY1"); v != "value1" {
		t.Errorf("KEY1 = %q, want value1", v)
	}
	if v := os.Getenv("KEY2"); v != "value2" {
		t.Errorf("KEY2 = %q, want value2", v)
	}

	os.Unsetenv("KEY1")
	os.Unsetenv("KEY2")
}

func TestLoadEnvFileNoEquals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	content := "NOEQUALS\nKEY=val\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	os.Unsetenv("NOEQUALS")
	os.Unsetenv("KEY")

	loadEnvFile(path)

	// Line without = should be skipped
	if v := os.Getenv("NOEQUALS"); v != "" {
		t.Errorf("NOEQUALS should not be set, got %q", v)
	}
	if v := os.Getenv("KEY"); v != "val" {
		t.Errorf("KEY = %q, want val", v)
	}

	os.Unsetenv("KEY")
}

func TestLoadEnvFileValueWithEquals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	content := "URL=https://example.com?a=1&b=2\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	os.Unsetenv("URL")
	loadEnvFile(path)

	if v := os.Getenv("URL"); v != "https://example.com?a=1&b=2" {
		t.Errorf("URL = %q", v)
	}

	os.Unsetenv("URL")
}

func TestToolCallerAdapterCall(t *testing.T) {
	called := false
	var capturedName string
	var capturedArgs map[string]interface{}

	adapter := &toolCallerAdapter{
		callTool: func(name string, args map[string]interface{}) (json.RawMessage, error) {
			called = true
			capturedName = name
			capturedArgs = args
			return json.RawMessage(`{"ok":true}`), nil
		},
	}

	args := map[string]interface{}{"key": "value"}
	result, err := adapter.Call("test-tool", args)
	if err != nil {
		t.Fatal(err)
	}

	if !called {
		t.Fatal("callTool should have been called")
	}
	if capturedName != "test-tool" {
		t.Errorf("name = %q", capturedName)
	}
	if capturedArgs["key"] != "value" {
		t.Errorf("args = %v", capturedArgs)
	}
	if string(result) != `{"ok":true}` {
		t.Errorf("result = %q", result)
	}
}
