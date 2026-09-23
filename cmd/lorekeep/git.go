package main

import (
	"flag"
	"fmt"
	"io"
	"slices"

	"github.com/gmreyer/lorekeep/internal/scaffold"
)

func gitCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("git", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ci := fs.Bool("ci", false, "also add the GitHub Actions workflow")
	positional, ok := parseInterleaved(fs, args)
	if !ok {
		return exitUsage
	}
	if len(positional) > 1 {
		fmt.Fprintln(stderr, "lorekeep git: takes at most one world directory")
		return exitUsage
	}
	dir := "."
	if len(positional) == 1 {
		dir = positional[0]
	}
	// The files come from the template the project's own lorekeep embeds.
	if code, done := runPinned(dir, append([]string{"git"}, args...), stdout, stderr); done {
		return code
	}

	// Without a terminal, a file that differs is kept: there is nobody to ask.
	replace := func(p string) bool {
		return ask(stderr, fmt.Sprintf("%s exists and differs from lorekeep's. Replace it?", p), false)
	}
	res, err := scaffold.AddGit(dir, *ci, replace)
	if err != nil {
		fmt.Fprintf(stderr, "lorekeep git: %v\n", err)
		return exitUsage
	}

	for _, p := range res.Written {
		fmt.Fprintf(stdout, "  wrote %s\n", p)
	}
	for _, p := range res.Present {
		fmt.Fprintf(stdout, "  already present %s\n", p)
	}
	for _, p := range res.Kept {
		fmt.Fprintf(stdout, "  kept your %s\n", p)
	}
	if slices.Contains(res.Written, scaffold.CIFile) {
		fmt.Fprintln(stdout, "The workflow runs on GitHub after you push; it needs no secrets.")
	}
	return exitOK
}
