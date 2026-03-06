package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	mcpfuse "github.com/airshelf/mcpfs/internal/fuse"
	"github.com/airshelf/mcpfs/pkg/mcpclient"
)

func usage() {
	fmt.Fprintln(os.Stderr, `mcpfs — mount MCP resources as a filesystem

Usage:
  mcpfs <mountpoint> -- <command> [args...]
  mcpfs -u <mountpoint>
  mcpfs migrate [--apply|--undo|--json]

Examples:
  mcpfs /mnt/vercel -- mcpfs-vercel
  mcpfs /mnt/github -- mcpfs-github
  mcpfs -u /mnt/vercel
  mcpfs migrate          # preview plugin migration
  mcpfs migrate --apply  # disable MCP plugins, use mcpfs instead

Flags:
  -u          unmount
  --debug     enable FUSE debug logging`)
	os.Exit(2)
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
	}

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

	debug := false
	mountpoint := ""
	var cmdArgs []string

	dashDash := -1
	for i, a := range args {
		if a == "--" {
			dashDash = i
			break
		}
	}

	if dashDash < 1 {
		fmt.Fprintln(os.Stderr, "mcpfs: missing -- separator between mountpoint and command")
		usage()
	}

	preArgs := args[:dashDash]
	cmdArgs = args[dashDash+1:]

	if len(cmdArgs) == 0 {
		fmt.Fprintln(os.Stderr, "mcpfs: missing command after --")
		usage()
	}

	for _, a := range preArgs {
		if a == "--debug" {
			debug = true
		} else if mountpoint == "" {
			mountpoint = a
		} else {
			fmt.Fprintf(os.Stderr, "mcpfs: unexpected argument: %s\n", a)
			usage()
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
