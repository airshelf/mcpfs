package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/airshelf/mcpfs/internal/config"
	mcpfuse "github.com/airshelf/mcpfs/internal/fuse"
	"github.com/airshelf/mcpfs/pkg/mcpclient"
	"github.com/airshelf/mcpfs/pkg/mcptool"
)

func usage() {
	fmt.Fprintln(os.Stderr, `mcpfs — mount MCP servers as filesystems

Usage:
  mcpfs <mountpoint> -- <command> [args...]    mount stdio server
  mcpfs <mountpoint> --http <url> [--auth H]   mount HTTP server
  mcpfs --config <servers.json> [--mount dir]   mount all from config
  mcpfs auto [--mount dir] [--json]             discover and mount Claude Code plugins
  mcpfs tool <server> [tool] [--flags]          call a tool via CLI
  mcpfs -u <mountpoint>                        unmount
  mcpfs migrate [--apply|--undo|--json]

Flags:
  --debug     enable FUSE debug logging
  --config    path to servers.json (Claude Desktop format)
  --http      MCP server HTTP endpoint
  --auth      Authorization header value`)
	os.Exit(2)
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
	}

	// mcpfs --config servers.json [--mount dir]
	if args[0] == "--config" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "mcpfs: --config requires a path")
			os.Exit(1)
		}
		configPath := args[1]
		mountRoot := ".mcpfs"
		for i := 2; i < len(args); i++ {
			if args[i] == "--mount" && i+1 < len(args) {
				mountRoot = args[i+1]
				i++
			}
		}
		runConfig(configPath, mountRoot)
		return
	}

	// mcpfs auto [--json]
	if args[0] == "auto" {
		runAuto(args[1:])
		return
	}

	// mcpfs tool <server> [tool-name] [--flags]
	if args[0] == "tool" {
		runTool(args[1:])
		return
	}

	// mcpfs migrate ...
	if args[0] == "migrate" {
		migrateBin := "mcpfs-migrate"
		if bin, err := os.Executable(); err == nil {
			candidate := filepath.Join(filepath.Dir(bin), "mcpfs-migrate")
			if _, err := os.Stat(candidate); err == nil {
				migrateBin = candidate
			}
		}
		migrateCmd := exec.Command(migrateBin, args[1:]...)
		migrateCmd.Stdout = os.Stdout
		migrateCmd.Stderr = os.Stderr
		if err := migrateCmd.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			}
			fmt.Fprintf(os.Stderr, "mcpfs: migrate: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// mcpfs -u <mountpoint>
	if args[0] == "-u" {
		if len(args) < 2 {
			usage()
		}
		cmd := exec.Command("fusermount", "-u", args[1])
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "mcpfs: unmount failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Parse flags before --
	debug := false
	mountpoint := ""
	httpURL := ""
	authHeader := ""
	var cmdArgs []string

	dashDash := -1
	for i, a := range args {
		if a == "--" {
			dashDash = i
			break
		}
	}

	// HTTP mode: mcpfs <mountpoint> --http <url> [--auth <header>] [--debug]
	if dashDash < 0 {
		// No --, check for --http mode
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--http":
				if i+1 < len(args) {
					httpURL = args[i+1]
					i++
				}
			case "--auth":
				if i+1 < len(args) {
					authHeader = args[i+1]
					i++
				}
			case "--debug":
				debug = true
			default:
				if mountpoint == "" {
					mountpoint = args[i]
				}
			}
		}

		if httpURL == "" || mountpoint == "" {
			fmt.Fprintln(os.Stderr, "mcpfs: missing -- separator or --http flag")
			usage()
		}

		if err := os.MkdirAll(mountpoint, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "mcpfs: create mountpoint: %v\n", err)
			os.Exit(1)
		}

		headers := map[string]string{}
		if authHeader != "" {
			headers["Authorization"] = authHeader
		}
		client, err := mcpclient.NewHTTP(httpURL, headers)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mcpfs: %v\n", err)
			os.Exit(1)
		}

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sig
			fmt.Fprintln(os.Stderr, "\nmcpfs: unmounting...")
			exec.Command("fusermount", "-u", mountpoint).Run()
			os.Exit(0)
		}()

		if err := mcpfuse.Mount(mountpoint, client, debug); err != nil {
			fmt.Fprintf(os.Stderr, "mcpfs: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Stdio mode: mcpfs <mountpoint> [--debug] -- <command> [args...]
	preArgs := args[:dashDash]
	cmdArgs = args[dashDash+1:]

	if len(cmdArgs) == 0 {
		fmt.Fprintln(os.Stderr, "mcpfs: missing command after --")
		usage()
	}

	for _, a := range preArgs {
		switch a {
		case "--debug":
			debug = true
		default:
			if mountpoint == "" {
				mountpoint = a
			} else {
				fmt.Fprintf(os.Stderr, "mcpfs: unexpected argument: %s\n", a)
				usage()
			}
		}
	}

	if mountpoint == "" {
		fmt.Fprintln(os.Stderr, "mcpfs: missing mountpoint")
		usage()
	}

	if err := os.MkdirAll(mountpoint, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs: create mountpoint: %v\n", err)
		os.Exit(1)
	}

	client, err := mcpclient.New(cmdArgs[0], cmdArgs[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Fprintln(os.Stderr, "\nmcpfs: unmounting...")
		exec.Command("fusermount", "-u", mountpoint).Run()
		client.Close()
		os.Exit(0)
	}()

	if err := mcpfuse.Mount(mountpoint, client, debug); err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs: %v\n", err)
		os.Exit(1)
	}
}

func runTool(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "mcpfs tool: usage: mcpfs tool <server> [tool-name] [--flags]")
		os.Exit(1)
	}

	// Parse --config flag (can appear anywhere)
	configPath := filepath.Join(os.Getenv("HOME"), ".config", "mcpfs", "servers.json")
	serverName := ""
	var toolArgs []string

	for i := 0; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			configPath = args[i+1]
			i++
		} else if serverName == "" {
			serverName = args[i]
		} else {
			toolArgs = append(toolArgs, args[i:]...)
			break
		}
	}

	if serverName == "" {
		fmt.Fprintln(os.Stderr, "mcpfs tool: missing server name")
		os.Exit(1)
	}

	// Load env vars from ~/.config/mcpfs/env if it exists.
	envPath := filepath.Join(filepath.Dir(configPath), "env")
	loadEnvFile(envPath)

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs tool: config: %v\n", err)
		fmt.Fprintf(os.Stderr, "hint: create %s with server definitions\n", configPath)
		os.Exit(1)
	}

	srv, ok := cfg[serverName]
	if !ok {
		fmt.Fprintf(os.Stderr, "mcpfs tool: unknown server %q\n", serverName)
		fmt.Fprintf(os.Stderr, "available:")
		for name := range cfg {
			fmt.Fprintf(os.Stderr, " %s", name)
		}
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}

	// Connect to server
	var caller toolCallerAdapter
	if srv.Type == "http" {
		client, err := mcpclient.NewHTTP(srv.URL, srv.Headers)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mcpfs tool: %s: %v\n", serverName, err)
			os.Exit(1)
		}
		caller.callTool = client.CallTool
		caller.listTools = client.ListTools
	} else {
		for k, v := range srv.Env {
			os.Setenv(k, v)
		}
		client, err := mcpclient.New(srv.Command, srv.Args)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mcpfs tool: %s: %v\n", serverName, err)
			os.Exit(1)
		}
		defer client.Close()
		caller.callTool = client.CallTool
		caller.listTools = client.ListTools
	}

	// List tools
	toolsRaw, err := caller.listTools()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs tool: %s: list tools: %v\n", serverName, err)
		os.Exit(1)
	}

	var tools []mcptool.ToolDef
	if err := json.Unmarshal(toolsRaw, &tools); err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs tool: %s: parse tools: %v\n", serverName, err)
		os.Exit(1)
	}

	os.Exit(mcptool.Run(serverName, tools, &caller, toolArgs))
}

// toolCallerAdapter bridges mcpclient's CallTool to mcptool's Call interface.
type toolCallerAdapter struct {
	callTool  func(name string, args map[string]interface{}) (json.RawMessage, error)
	listTools func() (json.RawMessage, error)
}

func (a *toolCallerAdapter) Call(toolName string, args map[string]interface{}) (json.RawMessage, error) {
	return a.callTool(toolName, args)
}

// loadEnvFile reads KEY=VALUE lines from a file and sets them as env vars.
func loadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			os.Setenv(k, v)
		}
	}
}

func runConfig(configPath, mountRoot string) {
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs: config: %v\n", err)
		os.Exit(1)
	}

	var wg sync.WaitGroup

	for name, srv := range cfg {
		mp := filepath.Join(mountRoot, name)
		os.MkdirAll(mp, 0755)

		wg.Add(1)
		go func(name string, srv *config.ServerConfig, mp string) {
			defer wg.Done()

			if srv.Type == "http" {
				client, err := mcpclient.NewHTTP(srv.URL, srv.Headers)
				if err != nil {
					fmt.Fprintf(os.Stderr, "mcpfs: %s: %v\n", name, err)
					return
				}
				if err := mcpfuse.Mount(mp, client, false); err != nil {
					fmt.Fprintf(os.Stderr, "mcpfs: %s: %v\n", name, err)
				}
			} else {
				for k, v := range srv.Env {
					os.Setenv(k, v)
				}
				client, err := mcpclient.New(srv.Command, srv.Args)
				if err != nil {
					fmt.Fprintf(os.Stderr, "mcpfs: %s: %v\n", name, err)
					return
				}
				defer client.Close()
				if err := mcpfuse.Mount(mp, client, false); err != nil {
					fmt.Fprintf(os.Stderr, "mcpfs: %s: %v\n", name, err)
				}
			}
		}(name, srv, mp)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	fmt.Fprintln(os.Stderr, "\nmcpfs: unmounting all...")
	for name := range cfg {
		exec.Command("fusermount", "-u", filepath.Join(mountRoot, name)).Run()
	}
}
