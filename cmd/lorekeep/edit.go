package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	osexec "os/exec"
	"os/signal"
	"runtime"
	"strings"

	"github.com/gmreyer/lorekeep/internal/editor"
)

// editCmd runs lorekeep edit: the web editor on 127.0.0.1, opened in the
// default browser, until Ctrl+C.
func editCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	noBrowser := fs.Bool("no-browser", false, "print the address without opening the browser")
	positional, ok := parseInterleaved(fs, args)
	if !ok {
		return exitUsage
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "lorekeep edit: needs exactly one world directory")
		return exitUsage
	}
	dir := positional[0]
	if code, done := runPinned(dir, append([]string{"edit"}, args...), stdout, stderr); done {
		return code
	}

	ed, err := editor.New(editor.Config{Repo: dir})
	if err != nil {
		fmt.Fprintf(stderr, "lorekeep edit: %v\n", err)
		return exitUsage
	}
	// A free port each run (Step 6 plan Q4), so two projects can be open at
	// once.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(stderr, "lorekeep edit: %v\n", err)
		return exitUsage
	}
	url := ed.OpenURL(ln)
	fmt.Fprintf(stdout, "Editing %s at\n  %s\nPress Ctrl+C here to stop the editor.\n", dir, url)
	if sys.openBrowser != nil && !*noBrowser {
		if err := sys.openBrowser(url); err != nil {
			fmt.Fprintf(stderr, "lorekeep edit: could not open the browser (%v); open the address above\n", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	go func() {
		warned := false
		for range sig {
			// Unsaved edits live only in the editor: the first Ctrl+C names
			// them, and only a second one stops and loses them.
			if unsaved := ed.Unsaved(); len(unsaved) > 0 && !warned {
				warned = true
				fmt.Fprintf(stdout, "Unsaved changes in %s.\nSave them in the editor, or press Ctrl+C again to stop and lose them.\n",
					strings.Join(unsaved, ", "))
				continue
			}
			cancel()
			return
		}
	}()
	if err := ed.Serve(ctx, ln); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "lorekeep edit: %v\n", err)
		return exitUsage
	}
	fmt.Fprintln(stdout, "Editor stopped.")
	return exitOK
}

// openBrowser opens url in the default browser.
func openBrowser(url string) error {
	var cmd *osexec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = osexec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = osexec.Command("open", url)
	default:
		cmd = osexec.Command("xdg-open", url)
	}
	return cmd.Start()
}
