package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseEmptyConfig(t *testing.T) {
	cfg, err := Parse([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg) != 0 {
		t.Errorf("empty config should produce empty map, got %d", len(cfg))
	}
}

func TestParseUnknownFields(t *testing.T) {
	data := []byte(`{
		"server1": {
			"type": "http",
			"url": "https://example.com",
			"unknown_field": "should be ignored",
			"nested": {"deep": true}
		}
	}`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["server1"].URL != "https://example.com" {
		t.Errorf("URL = %q", cfg["server1"].URL)
	}
}

func TestInterpolateNestedEnvVar(t *testing.T) {
	// ${${VAR}} — inner ${VAR} resolves first, but the result won't
	// form a valid ${...} unless the env var value happens to match.
	// This should not infinite loop.
	os.Setenv("INNER", "OUTER")
	os.Setenv("OUTER", "final")
	defer os.Unsetenv("INNER")
	defer os.Unsetenv("OUTER")

	// ${${INNER}} → first pass finds ${, then finds } at position of INNER}
	// The varName extracted is "${INNER" which won't match any env var.
	got := interpolateEnv("${${INNER}}")
	// The implementation finds first "${" and first "}" after it.
	// start=0, end=index of first "}" in "${${INNER}}" which is at position 9 relative to start.
	// Actually: s[start:] = "${${INNER}}" → Index(s[0:], "}") finds "}" at index 9
	// wait, let me re-check: s = "${${INNER}}"
	// start = 0 (first "${"), s[start:] = "${${INNER}}"
	// end = strings.Index("${${INNER}}", "}") = 9 (the first "}")
	// varName = s[0+2 : 0+9] = "${INNER"
	// os.Getenv("${INNER") = ""
	// s becomes "" + "" + "}" = "}"
	// Next iteration: no "${" found, return "}"
	if got == "" || len(got) > 100 {
		t.Errorf("nested env var should not infinite loop, got %q", got)
	}
}

func TestInterpolateMissingCloseBrace(t *testing.T) {
	// "${VAR" without closing brace should not infinite loop
	got := interpolateEnv("${VAR")
	if got != "${VAR" {
		t.Errorf("missing close brace: got %q, want ${VAR", got)
	}
}

func TestInterpolateEmptyVarName(t *testing.T) {
	got := interpolateEnv("${}")
	// varName = "", os.Getenv("") = ""
	if got != "" {
		t.Errorf("empty var name: got %q, want empty", got)
	}
}

func TestParseHTTPOnlyConfig(t *testing.T) {
	data := []byte(`{
		"api1": {"type": "http", "url": "https://api1.example.com"},
		"api2": {"type": "http", "url": "https://api2.example.com"}
	}`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg) != 2 {
		t.Fatalf("got %d servers, want 2", len(cfg))
	}
	for _, name := range []string{"api1", "api2"} {
		if cfg[name].Type != "http" {
			t.Errorf("%s type = %q", name, cfg[name].Type)
		}
		if cfg[name].Command != "" {
			t.Errorf("%s should have no command", name)
		}
	}
}

func TestParseStdioOnlyConfig(t *testing.T) {
	data := []byte(`{
		"local": {
			"command": "/usr/bin/mcp-server",
			"args": ["--port", "8080"],
			"env": {"DEBUG": "true"}
		}
	}`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	srv := cfg["local"]
	if srv.Type != "" {
		t.Errorf("type should be empty for stdio, got %q", srv.Type)
	}
	if srv.Command != "/usr/bin/mcp-server" {
		t.Errorf("command = %q", srv.Command)
	}
	if len(srv.Args) != 2 {
		t.Errorf("args len = %d, want 2", len(srv.Args))
	}
	if srv.Env["DEBUG"] != "true" {
		t.Errorf("env DEBUG = %q", srv.Env["DEBUG"])
	}
}

func TestLoadNonexistentFile(t *testing.T) {
	_, err := Load("/nonexistent/path/to/servers.json")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestLoadValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	content := []byte(`{"test": {"type": "http", "url": "https://test.com"}}`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["test"].URL != "https://test.com" {
		t.Errorf("URL = %q", cfg["test"].URL)
	}
}

func TestParseInvalidJSON(t *testing.T) {
	_, err := Parse([]byte(`{invalid json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseMultipleEnvVarsInOneField(t *testing.T) {
	os.Setenv("HOST", "example.com")
	os.Setenv("PORT", "8080")
	defer os.Unsetenv("HOST")
	defer os.Unsetenv("PORT")

	data := []byte(`{"srv": {"type": "http", "url": "https://${HOST}:${PORT}/api"}}`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["srv"].URL != "https://example.com:8080/api" {
		t.Errorf("URL = %q", cfg["srv"].URL)
	}
}

func TestParseNilHeaders(t *testing.T) {
	data := []byte(`{"srv": {"type": "http", "url": "https://test.com"}}`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	// Headers should be nil, not panic
	if cfg["srv"].Headers != nil {
		t.Errorf("headers should be nil, got %v", cfg["srv"].Headers)
	}
}
