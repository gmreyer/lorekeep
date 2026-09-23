// Command lorekeep is the world-repository toolchain.
//
// It is a single static binary on purpose. The audience includes writers who
// are not developers, on Windows, and "download lorekeep.exe and run it" is close
// to the whole reason the rest of this is written in Go.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/gmreyer/lorekeep/internal/world"
)

func main() {
	sys = newSystem()
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
		// A double-click opens a console: that is a writer, so show the menu.
		if sys.interactive {
			return menu(stdout, stderr)
		}
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "build":
		return build(args[1:], stdout, stderr)
	case "setup":
		return setup(args[1:], stdout, stderr)
	case "update":
		return update(args[1:], stdout, stderr)
	case "git":
		return gitCmd(args[1:], stdout, stderr)
	case "version", "--version", "-version":
		fmt.Fprintf(stdout, "lorekeep %s\n", version())
		return exitOK
	case "-h", "--help", "help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "lorekeep: unknown command %q\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `lorekeep — tooling for a graph-based lore repository

usage:
  lorekeep                 (at a console: the menu)
  lorekeep build <world-dir> [-out <dir>]
  lorekeep setup <dir> -name <name> [-example] [-git] [-ci] [-version <vX.Y.Z>]
  lorekeep update [<world-dir>] [-version <vX.Y.Z>]
  lorekeep git [<world-dir>] [-ci]
  lorekeep version

A world directory holds a schema pack in schema/ and authored lore in world/.
build validates the whole repository and, if it is sound, writes the SQLite
index and the JSON game snapshot. Nothing is written when validation fails.

setup creates a new project in an empty or new directory, pinned to this
lorekeep's version. Git is optional: -git adds .gitattributes, .gitignore and
world/.gitkeep, and -ci adds a GitHub Actions workflow on top of them. git
adds the same files to an existing project; it never runs git, and never
replaces a file of yours without asking.

A project pins its lorekeep version in lorekeep-version, and build runs that
version, downloading it on request. update moves a project to the latest
release, or to -version, and re-pins only if the project builds cleanly with it.

build flags:
  -out <dir>        where to write the artefacts (default: <world-dir>/build)

setup flags:
  -name <name>      the project name: lowercase letters, digits, underscores
  -example          add a five-entity example world
  -git              add the files git needs
  -ci               add a GitHub Actions workflow (needs -git)
  -version <v>      pin this release instead of the running one

git flags:
  -ci               also add the GitHub Actions workflow

update flags:
  -version <v>      the release to move to (default: the latest)
`)
}

// version is the module version the binary was built from.
//
// It comes from the build info rather than from a linker flag, so no build
// needs special flags: a release build from a tagged checkout reports the tag,
// go install at a tag reports that tag, a local go build reports a
// pseudo-version, marked +dirty when the tree was, and go run reports
// "(devel)". It is a variable so tests can pose as a release.
var version = buildVersion

func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "(unknown)"
	}
	return info.Main.Version
}

// parseInterleaved parses flags given before or after positional arguments and
// returns the positional ones. Go's flag package stops at the first positional
// argument, and "lorekeep build myworld -out dist" is the order a person types.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, bool) {
	var positional []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			return nil, false
		}
		rest = fs.Args()
		if len(rest) == 0 {
			return positional, true
		}
		positional = append(positional, rest[0])
		rest = rest[1:]
	}
}

func build(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "where to write the artefacts (default: <world-dir>/build)")
	positional, ok := parseInterleaved(fs, args)
	if !ok {
		return exitUsage
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "lorekeep build: needs exactly one world directory")
		usage(stderr)
		return exitUsage
	}

	repo := positional[0]
	if code, done := runPinned(repo, append([]string{"build"}, args...), stdout, stderr); done {
		return code
	}

	dest := *out
	if dest == "" {
		dest = filepath.Join(repo, index.BuildDir)
	}

	res, err := index.Build(repo, dest)
	if err != nil {
		fmt.Fprintf(stderr, "lorekeep build: %v\n", err)
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
