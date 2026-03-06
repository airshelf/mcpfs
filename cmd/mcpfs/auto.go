package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// discoverClaudePlugins reads Claude Code's installed plugins and their MCP
// server configs, merging OAuth tokens where available. Returns a servers.json
// compatible config.
func discoverClaudePlugins() (map[string]*serverEntry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	claudeDir := filepath.Join(home, ".claude")

	// Read global MCP servers from ~/.claude.json
	globalServers, _ := readGlobalMCPServers(filepath.Join(home, ".claude.json"))

	// Read installed plugins
	installed, err := readInstalledPlugins(filepath.Join(claudeDir, "plugins", "installed_plugins.json"))
	if err != nil {
		installed = make(map[string][]installedPlugin) // non-fatal
	}

	// Read OAuth credentials
	creds, _ := readCredentials(filepath.Join(claudeDir, ".credentials.json"))

	// Read env file for additional tokens
	envPath := filepath.Join(home, ".config", "mcpfs", "env")
	loadEnvFile(envPath)

	// GitHub token fallback from gh CLI
	if os.Getenv("GITHUB_TOKEN") == "" && os.Getenv("GITHUB_PERSONAL_ACCESS_TOKEN") == "" {
		if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
			token := strings.TrimSpace(string(out))
			if token != "" {
				os.Setenv("GITHUB_TOKEN", token)
			}
		}
	}

	servers := make(map[string]*serverEntry)

	// Load user's servers.json as base (if exists)
	userConfig := filepath.Join(home, ".config", "mcpfs", "servers.json")
	if data, err := os.ReadFile(userConfig); err == nil {
		var userServers map[string]*serverEntry
		if json.Unmarshal(data, &userServers) == nil {
			for name, srv := range userServers {
				// Interpolate env vars in user config
				if srv.Headers != nil {
					for k, v := range srv.Headers {
						srv.Headers[k] = interpolateEnvVars(v)
					}
				}
				if srv.Env != nil {
					for k, v := range srv.Env {
						srv.Env[k] = interpolateEnvVars(v)
					}
				}
				servers[name] = srv
			}
		}
	}

	// Add global MCP servers from ~/.claude.json (user-configured, highest priority)
	for name, srv := range globalServers {
		if shouldSkip(name, "") {
			continue
		}
		entry := &serverEntry{}
		if srv.Type == "http" || (srv.URL != "" && srv.Command == "") {
			entry.Type = "http"
			entry.URL = srv.URL
			if len(srv.Headers) > 0 {
				entry.Headers = srv.Headers
			}
		} else if srv.Command != "" {
			entry.Command = srv.Command
			entry.Args = srv.Args
			if len(srv.Env) > 0 {
				entry.Env = srv.Env
			}
		} else {
			continue
		}
		servers[name] = entry
	}

	// Also scan enabled plugins from settings that may not be in installed_plugins
	enabledPlugins, _ := readEnabledPlugins(filepath.Join(claudeDir, "settings.json"))
	cacheDir := filepath.Join(claudeDir, "plugins", "cache")
	for pluginKey := range enabledPlugins {
		if _, exists := installed[pluginKey]; exists {
			continue // already in installed list
		}
		// Extract plugin name and marketplace from "name@marketplace"
		parts := strings.SplitN(pluginKey, "@", 2)
		if len(parts) != 2 {
			continue
		}
		name, marketplace := parts[0], parts[1]
		// Look for cached .mcp.json
		matches, _ := filepath.Glob(filepath.Join(cacheDir, marketplace, name, "*", ".mcp.json"))
		if len(matches) > 0 {
			installed[pluginKey] = []installedPlugin{{InstallPath: filepath.Dir(matches[0])}}
		}
	}

	for pluginKey, installs := range installed {
		if len(installs) == 0 {
			continue
		}
		install := installs[0]

		// Read .mcp.json from install path
		mcpJSON := filepath.Join(install.InstallPath, ".mcp.json")
		mcpCfg, err := readMCPJSON(mcpJSON)
		if err != nil {
			continue
		}

		for name, srv := range mcpCfg {
			// Skip non-mountable servers (playwright, serena, etc.)
			if shouldSkip(name, pluginKey) {
				continue
			}

			entry := &serverEntry{}

			if srv.Type == "http" || srv.URL != "" {
				entry.Type = "http"
				entry.URL = srv.URL

				// Try OAuth token first
				token := findOAuthToken(creds, name, pluginKey, srv.URL)
				if token != "" {
					entry.Headers = map[string]string{"Authorization": "Bearer " + token}
				} else if len(srv.Headers) > 0 {
					// Use headers from .mcp.json, interpolating env vars
					entry.Headers = make(map[string]string)
					for k, v := range srv.Headers {
						entry.Headers[k] = interpolateEnvVars(v)
					}
				} else {
					// Try well-known env var patterns
					token := findEnvToken(name)
					if token != "" {
						entry.Headers = map[string]string{"Authorization": "Bearer " + token}
					}
				}

				// Skip if no auth available for servers that need it
				hasAuth := false
				if entry.Headers != nil {
					for _, v := range entry.Headers {
						if v != "" && v != "Bearer " && v != "Bearer" {
							hasAuth = true
							break
						}
					}
				}
				if !hasAuth && needsAuth(name) {
					fmt.Fprintf(os.Stderr, "mcpfs auto: skip %s (no auth token)\n", name)
					continue
				}
			} else if srv.Command != "" {
				entry.Command = srv.Command
				entry.Args = srv.Args
				if len(srv.Env) > 0 {
					entry.Env = make(map[string]string)
					for k, v := range srv.Env {
						entry.Env[k] = interpolateEnvVars(v)
					}
				}
			} else {
				continue
			}

			servers[name] = entry
		}
	}

	return servers, nil
}

// serverEntry is the output format for discovered servers.
type serverEntry struct {
	Type    string            `json:"type,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type installedPlugin struct {
	InstallPath string `json:"installPath"`
}

func readEnabledPlugins(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	// Only return enabled ones
	result := make(map[string]bool)
	for k, v := range file.EnabledPlugins {
		if v {
			result[k] = true
		}
	}
	return result, nil
}

func readGlobalMCPServers(path string) (map[string]mcpServerDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file struct {
		MCPServers map[string]mcpServerDef `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return file.MCPServers, nil
}

func readInstalledPlugins(path string) (map[string][]installedPlugin, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file struct {
		Plugins map[string][]installedPlugin `json:"plugins"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return file.Plugins, nil
}

type mcpServerDef struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

func readMCPJSON(path string) (map[string]mcpServerDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Try two formats:
	// 1. {"mcpServers": {"name": {...}}}
	// 2. {"name": {...}}
	var wrapped struct {
		MCPServers map[string]mcpServerDef `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && len(wrapped.MCPServers) > 0 {
		return wrapped.MCPServers, nil
	}

	var flat map[string]mcpServerDef
	if err := json.Unmarshal(data, &flat); err != nil {
		return nil, err
	}
	return flat, nil
}

type oauthCred struct {
	AccessToken string `json:"accessToken"`
	ServerURL   string `json:"serverUrl"`
	ServerName  string `json:"serverName"`
}

func readCredentials(path string) (map[string]oauthCred, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file struct {
		MCPOAuth map[string]oauthCred `json:"mcpOAuth"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return file.MCPOAuth, nil
}

func findOAuthToken(creds map[string]oauthCred, name, pluginKey, url string) string {
	if creds == nil {
		return ""
	}

	// Match by server URL or plugin name
	for _, cred := range creds {
		if cred.ServerURL == url && cred.AccessToken != "" {
			return cred.AccessToken
		}
		if strings.Contains(cred.ServerName, name) && cred.AccessToken != "" {
			return cred.AccessToken
		}
	}
	return ""
}

func interpolateEnvVars(s string) string {
	for {
		start := strings.Index(s, "${")
		if start < 0 {
			return s
		}
		end := strings.Index(s[start:], "}")
		if end < 0 {
			return s
		}
		end += start
		varName := s[start+2 : end]
		s = s[:start] + os.Getenv(varName) + s[end+1:]
	}
}

func shouldSkip(name, pluginKey string) bool {
	// Skip non-data servers that don't make sense as filesystems
	skip := []string{"playwright", "serena", "context7", "gopls", "typescript", "mcp-search"}
	for _, s := range skip {
		if strings.Contains(name, s) || strings.Contains(pluginKey, s) {
			return true
		}
	}
	// Skip plugins that are tools/skills, not data servers
	skipPlugins := []string{"commit-commands", "code-review", "feature-dev", "frontend-design",
		"ralph", "security-guidance", "superpowers", "claude-md", "claude-mem"}
	for _, s := range skipPlugins {
		if strings.Contains(pluginKey, s) {
			return true
		}
	}
	return false
}

func needsAuth(name string) bool {
	return name != "context7"
}

// findEnvToken tries well-known env var patterns for a service name.
func findEnvToken(name string) string {
	upper := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	patterns := []string{
		upper + "_API_KEY",
		upper + "_TOKEN",
		upper + "_SECRET_KEY",
		upper + "_PERSONAL_ACCESS_TOKEN",
	}
	for _, p := range patterns {
		if v := os.Getenv(p); v != "" {
			return v
		}
	}
	// Special cases
	switch name {
	case "github":
		if v := os.Getenv("GITHUB_TOKEN"); v != "" {
			return v
		}
		if v := os.Getenv("GITHUB_PERSONAL_ACCESS_TOKEN"); v != "" {
			return v
		}
	case "stripe":
		if v := os.Getenv("STRIPE_API_KEY"); v != "" {
			return v
		}
		if v := os.Getenv("STRIPE_SECRET_KEY"); v != "" {
			return v
		}
	case "posthog":
		if v := os.Getenv("POSTHOG_API_KEY"); v != "" {
			return v
		}
	case "linear":
		if v := os.Getenv("LINEAR_API_KEY"); v != "" {
			return v
		}
	}
	return ""
}

func runAuto(args []string) {
	jsonOutput := false
	mountRoot := ".mcpfs"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOutput = true
		case "--mount":
			if i+1 < len(args) {
				mountRoot = args[i+1]
				i++
			}
		}
	}

	// Load project-local env files (local credentials override global)
	loadEnvFile(".env.local")
	loadEnvFile(".env")

	servers, err := discoverClaudePlugins()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs auto: %v\n", err)
		os.Exit(1)
	}

	if len(servers) == 0 {
		fmt.Fprintln(os.Stderr, "mcpfs auto: no mountable MCP servers found in Claude Code plugins")
		os.Exit(1)
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(servers)
		return
	}

	// Print what we found
	fmt.Fprintf(os.Stderr, "mcpfs auto: discovered %d servers from Claude Code plugins:\n", len(servers))
	for name, srv := range servers {
		if srv.Type == "http" {
			auth := "oauth"
			if srv.Headers != nil && strings.Contains(srv.Headers["Authorization"], "${") {
				auth = "env"
			}
			fmt.Fprintf(os.Stderr, "  %s (http: %s, auth: %s)\n", name, srv.URL, auth)
		} else {
			fmt.Fprintf(os.Stderr, "  %s (stdio: %s)\n", name, srv.Command)
		}
	}

	// Write to temp config and mount
	tmpFile, err := os.CreateTemp("", "mcpfs-auto-*.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs auto: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(tmpFile.Name())

	enc := json.NewEncoder(tmpFile)
	enc.SetIndent("", "  ")
	enc.Encode(servers)
	tmpFile.Close()

	fmt.Fprintf(os.Stderr, "mcpfs auto: mounting to %s/\n", mountRoot)
	runConfig(tmpFile.Name(), mountRoot)
}
