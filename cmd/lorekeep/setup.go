package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/gmreyer/lorekeep/internal/project"
	"github.com/gmreyer/lorekeep/internal/scaffold"
)

func setup(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o scaffold.Options
	fs.StringVar(&o.Name, "name", "", "the project name")
	fs.BoolVar(&o.Example, "example", false, "add an example world")
	fs.BoolVar(&o.Git, "git", false, "add the files git needs")
	fs.BoolVar(&o.CI, "ci", false, "add a GitHub Actions workflow")
	fs.StringVar(&o.Version, "version", "", "pin this release instead of the running one")
	positional, ok := parseInterleaved(fs, args)
	if !ok {
		return exitUsage
	}
	if len(positional) != 1 || o.Name == "" {
		fmt.Fprintln(stderr, "lorekeep setup: needs one directory and -name")
		usage(stderr)
		return exitUsage
	}

	// A project pins the lorekeep that created it. A development build has no
	// release to pin, so it has to be told which one.
	if o.Version == "" {
		o.Version = version()
		if !project.IsRelease(o.Version) {
			fmt.Fprintf(stderr, "lorekeep setup: this is development build %s; pass -version vX.Y.Z to pin a release\n", o.Version)
			return exitUsage
		}
	}

	dir := positional[0]
	written, err := scaffold.Setup(dir, o)
	if err != nil {
		fmt.Fprintf(stderr, "lorekeep setup: %v\n", err)
		return exitUsage
	}
	fmt.Fprintf(stdout, "set up %s in %s, pinned to lorekeep %s\n", o.Name, dir, o.Version)
	for _, p := range written {
		fmt.Fprintf(stdout, "  wrote %s\n", p)
	}
	return exitOK
}
