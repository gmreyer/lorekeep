package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gmreyer/lorekeep/internal/mcp"
)

// desktopConfigPaths are the configs Claude Desktop may read; tests replace it.
var desktopConfigPaths = mcp.DesktopConfigPaths

// mcpCmd runs lorekeep mcp and lorekeep mcp install.
//
// When serving, stdout is the protocol and nothing else may write to it, so
// every message of lorekeep's own goes to stderr.
func mcpCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "install" {
		return mcpInstall(args[1:], stdout, stderr)
	}

	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	role := fs.String("role", string(mcp.RoleAuthor), "the session role; only author for now")
	positional, ok := parseInterleaved(fs, args)
	if !ok {
		return exitUsage
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "lorekeep mcp: needs exactly one world directory")
		return exitUsage
	}
	if mcp.Role(*role) != mcp.RoleAuthor {
		fmt.Fprintf(stderr, "lorekeep mcp: unknown role %q; only author for now\n", *role)
		return exitUsage
	}
	dir := positional[0]

	// MCP clients launch lorekeep without a console, so this never prompts or
	// downloads there; a pin that is missing is an error on stderr.
	if code, done := runPinned(dir, append([]string{"mcp"}, args...), stdout, stderr); done {
		return code
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// The raw os.Stdin, not sys.in: that reader may already hold protocol bytes.
	err := mcp.Serve(ctx, mcp.Config{Repo: dir, Role: mcp.Role(*role)}, os.Stdin, stdout)
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "lorekeep mcp: %v\n", err)
		return exitUsage
	}
	return exitOK
}

// mcpInstall registers the server with Claude Code (the project's .mcp.json,
// written outright) and Claude Desktop (a user-wide file, written only after
// asking at a console).
func mcpInstall(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mcp install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	positional, ok := parseInterleaved(fs, args)
	if !ok {
		return exitUsage
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "lorekeep mcp install: needs exactly one world directory")
		return exitUsage
	}
	dir := positional[0]

	// The entry records the running lorekeep's path, so it must be the
	// release the project pins.
	if code, done := runPinned(dir, append([]string{"mcp", "install"}, args...), stdout, stderr); done {
		return code
	}

	fail := func(err error) int {
		fmt.Fprintf(stderr, "lorekeep mcp install: %v\n", err)
		return exitUsage
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fail(err)
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return fail(fmt.Errorf("%s is not a directory", dir))
	}
	exe, err := sys.executable()
	if err != nil {
		return fail(err)
	}
	entry := mcp.NewEntry(exe, abs)

	// Claude Code: the project's own file, which lorekeep owns.
	projectFile := filepath.Join(abs, mcp.ProjectConfigFile)
	changed, err := mcp.WriteConfig(projectFile, mcp.ProjectServerName, entry, false)
	if err != nil {
		return fail(fmt.Errorf("%s: %w", projectFile, err))
	}
	if changed {
		fmt.Fprintf(stdout, "wrote %s\n", projectFile)
	} else {
		fmt.Fprintf(stdout, "%s already has the lorekeep entry\n", projectFile)
	}

	// Claude Desktop: one file for every project, so ask first.
	name, fromPack := mcp.ProjectName(abs)
	if !fromPack {
		fmt.Fprintf(stderr, "lorekeep mcp install: no project name in schema/pack.yaml; using the directory name %q\n", name)
	}
	server := mcp.DesktopServerName(name)
	paths, err := desktopConfigPaths()
	if err != nil {
		return fail(err)
	}
	path := paths[0]
	if len(paths) > 1 {
		fmt.Fprintln(stdout, "Claude Desktop is installed more than once; its configs are:")
		for i, p := range paths {
			fmt.Fprintf(stdout, "  %d. %s\n", i+1, p)
		}
		if !sys.interactive {
			fmt.Fprintln(stdout, "No console to ask which one, so Claude Desktop's config is unchanged.")
			return exitOK
		}
		i := choose(stderr, "Which one should lorekeep add the server to?", len(paths))
		if i < 0 {
			fmt.Fprintln(stdout, "Claude Desktop's config is unchanged.")
			return exitOK
		}
		path = paths[i]
	}

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fail(err)
	}
	existed := err == nil
	_, needed, err := mcp.Merge(existing, server, entry)
	if err != nil {
		return fail(fmt.Errorf("%s: %w", path, err))
	}
	if !needed {
		fmt.Fprintf(stdout, "%s already has %s\n", path, server)
		return exitOK
	}

	shown, _ := json.MarshalIndent(map[string]mcp.Entry{server: entry}, "  ", "  ")
	fmt.Fprintf(stdout, "Claude Desktop entry for %s:\n  %s\n", path, shown)
	if !sys.interactive {
		fmt.Fprintln(stdout, "No console to ask on, so Claude Desktop's config is unchanged.")
		return exitOK
	}
	fmt.Fprintln(stderr, "Quit Claude Desktop first (tray icon, then Quit): while it runs, it rewrites this file and drops the entry.")
	if !ask(stderr, "Add it to Claude Desktop's config?", false) {
		fmt.Fprintln(stdout, "Claude Desktop's config is unchanged.")
		return exitOK
	}
	if _, err := mcp.WriteConfig(path, server, entry, true); err != nil {
		return fail(fmt.Errorf("%s: %w", path, err))
	}
	if !existed {
		fmt.Fprintf(stdout, "created %s with %s\n", path, server)
	} else {
		fmt.Fprintf(stdout, "added %s to %s (the previous file is kept as %s.bak)\n", server, path, filepath.Base(path))
	}
	fmt.Fprintln(stdout, "Restart Claude Desktop to pick it up.")
	return exitOK
}

// choose asks for a number from 1 to n at the console and returns it from 0,
// or -1 for an empty or unreadable answer.
func choose(w io.Writer, question string, n int) int {
	fmt.Fprintf(w, "%s [1-%d] ", question, n)
	line, _ := sys.in.ReadString('\n')
	i, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || i < 1 || i > n {
		return -1
	}
	return i - 1
}
