// Command lore is the world-repository toolchain.
//
// It is a single static binary on purpose. The audience includes writers who
// are not developers, on Windows, and "download lore.exe and run it" is close
// to the whole reason the rest of this is written in Go.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/gmreyer/lore-core/internal/index"
	"github.com/gmreyer/lore-core/internal/world"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// Exit codes. A build that found a blocking error and a build that could not
// run at all are different failures, and CI should be able to tell them apart.
const (
	exitOK      = 0
	exitInvalid = 1 // the world has blocking errors
	exitUsage   = 2 // bad invocation, or the repository could not be read: no schema/ or world/
)

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "build":
		return build(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "lore: unknown command %q\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `lore — tooling for a graph-based lore repository

usage:
  lore build <world-dir> [-out <dir>]

A world directory holds a schema pack in schema/ and authored lore in world/.
build validates the whole repository and, if it is sound, writes the SQLite
index and the JSON game snapshot. Nothing is written when validation fails.

flags:
  -out <dir>   where to write the artefacts (default: <world-dir>/build)
`)
}

func build(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "where to write the artefacts (default: <world-dir>/build)")
	// Flags are accepted before or after the world directory. Go's flag
	// package stops at the first positional argument, and "lore build myworld
	// -out dist" is the order a person types.
	var positional []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			return exitUsage
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		rest = rest[1:]
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "lore build: needs exactly one world directory")
		usage(stderr)
		return exitUsage
	}

	repo := positional[0]
	dest := *out
	if dest == "" {
		dest = filepath.Join(repo, index.BuildDir)
	}

	res, err := index.Build(repo, dest)
	if err != nil {
		fmt.Fprintf(stderr, "lore build: %v\n", err)
		return exitUsage
	}

	report(stderr, res.Findings)

	if res.Findings.HasErrors() {
		fmt.Fprintf(stderr, "\n%s — nothing written\n", tally(res.Findings))
		return exitInvalid
	}

	fmt.Fprintf(stdout, "%s\n", summary(res))
	for _, path := range res.Written {
		fmt.Fprintf(stdout, "  wrote %s\n", path)
	}
	return exitOK
}

// report prints every finding, errors first.
//
// Paths are printed relative to the world directory rather than to the world
// content directory, because that is where a writer's editor is open.
func report(w io.Writer, findings world.Findings) {
	for _, f := range findings {
		located := f
		located.File = filepath.ToSlash(filepath.Join(index.WorldDir, f.File))
		fmt.Fprintf(w, "%-7s %s\n", f.Severity, located.String())
	}
}

func summary(res *index.Result) string {
	idx := res.Index
	var edges, beliefs, assertions int
	for _, e := range idx.Entities {
		edges += len(e.Edges)
		beliefs += len(e.Beliefs)
		assertions += len(e.Assertions)
	}

	line := fmt.Sprintf("built %s %s: %s, %s, %s, %s, %s",
		idx.Pack.Name, idx.Pack.Version,
		plural(len(idx.Entities), "entity", "entities"),
		plural(len(idx.Statements), "statement", "statements"),
		plural(edges, "edge", "edges"),
		plural(beliefs, "belief", "beliefs"),
		plural(assertions, "assertion", "assertions"))

	if n := len(res.Findings); n > 0 {
		line += fmt.Sprintf(" — %s", plural(n, "warning", "warnings"))
	}
	return line
}

func tally(findings world.Findings) string {
	errs := findings.Errors()
	warns := len(findings) - errs
	out := plural(errs, "error", "errors")
	if warns > 0 {
		out += ", " + plural(warns, "warning", "warnings")
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
