package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/airshelf/mcpfs/internal/config"
	mcpfuse "github.com/airshelf/mcpfs/internal/fuse"
	"github.com/airshelf/mcpfs/pkg/mcpclient"
)

func usage() {
	fmt.Fprintln(os.Stderr, `mcpfs — mount MCP servers as filesystems

Usage:
  mcpfs <mountpoint> -- <command> [args...]    mount stdio server
  mcpfs <mountpoint> --http <url> [--auth H]   mount HTTP server
  mcpfs --config <servers.json>                mount all from config
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

	// mcpfs --config servers.json
	if args[0] == "--config" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "mcpfs: --config requires a path")
			os.Exit(1)
		}
		runConfig(args[1])
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

func runConfig(configPath string) {
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs: config: %v\n", err)
		os.Exit(1)
	}

	mountRoot := "/mnt/mcpfs"
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
