package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/gmreyer/lorekeep/internal/project"
	"github.com/gmreyer/lorekeep/internal/versions"
)

// system is everything outside the process that version handling touches,
// so tests can replace it.
type system struct {
	in          *bufio.Reader
	interactive bool // stdin is a terminal a person can answer prompts on
	versions    *versions.Manager
	statePath   string
	recentPath  string
	now         func() time.Time
	executable  func() (string, error)
}

// sys starts empty: non-interactive and without a cache, which is what tests
// get unless they set one up. main replaces it.
var sys = &system{in: bufio.NewReader(strings.NewReader("")), now: time.Now, executable: os.Executable}

func newSystem() *system {
	s := &system{
		in:          bufio.NewReader(os.Stdin),
		interactive: isTerminal(os.Stdin),
		now:         time.Now,
		executable:  os.Executable,
	}
	s.versions, _ = versions.New()
	s.statePath, _ = versions.StatePath()
	s.recentPath, _ = project.RecentPath()
	return s
}

// ask prints a yes/no question to w and reads the answer. An empty answer, or
// no terminal to answer on, takes the default.
func ask(w io.Writer, question string, def bool) bool {
	if !sys.interactive {
		return def
	}
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	fmt.Fprintf(w, "%s %s ", question, hint)
	line, _ := sys.in.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	}
	return def
}

// runPinned makes sure a command on the project in dir runs with the lorekeep
// version the project pins. When that is not this binary, it runs the pinned
// one with the same arguments and reports done, with its exit code; otherwise
// the caller carries on in-process.
//
// Before that, a writer at a terminal is offered a newer release, never
// silently: a new lorekeep can add blocking errors, so moving a project is the
// writer's decision, and it only happens once the project builds cleanly.
func runPinned(dir string, args []string, stdout, stderr io.Writer) (int, bool) {
	if os.Getenv(versions.NoDispatchEnv) != "" {
		return 0, false
	}
	pin, err := project.ReadPin(dir)
	if errors.Is(err, project.ErrNoPin) {
		return 0, false
	}
	if err != nil {
		fmt.Fprintf(stderr, "lorekeep: %v\n", err)
		return exitUsage, true
	}
	if project.Compare(pin, project.FirstPinVersion) < 0 {
		fmt.Fprintf(stderr, "lorekeep: %s pins %s, but the first lorekeep that reads it is %s\n",
			project.PinFile, pin, project.FirstPinVersion)
		return exitUsage, true
	}

	if sys.interactive {
		pin = offerUpdate(dir, pin, stdout, stderr)
	}

	self := version()
	switch {
	case self == pin:
		return 0, false
	case !project.IsRelease(self):
		fmt.Fprintf(stderr, "lorekeep: development build %s; ignoring the project's pin %s\n", self, pin)
		return 0, false
	}

	if sys.versions == nil || !sys.versions.Installed(pin) {
		if !sys.interactive {
			fmt.Fprintf(stderr, "lorekeep: this project uses lorekeep %s, which is not installed; "+
				"run lorekeep in a terminal to download it\n", pin)
			return exitUsage, true
		}
		if !ask(stderr, fmt.Sprintf("This project uses lorekeep %s, which is not installed. Download it?", pin), true) {
			fmt.Fprintf(stderr, "lorekeep: not building without lorekeep %s\n", pin)
			return exitUsage, true
		}
		if err := install(pin, stderr); err != nil {
			fmt.Fprintf(stderr, "lorekeep: %v\n", err)
			return exitUsage, true
		}
	}

	code, err := sys.versions.Run(pin, args, os.Stdin, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "lorekeep: %v\n", err)
		return exitUsage, true
	}
	return code, true
}

// install puts version v into the cache: this binary itself when it is v,
// otherwise a checked download.
func install(v string, w io.Writer) error {
	if sys.versions == nil {
		return errors.New("cannot find a cache directory for lorekeep versions")
	}
	if sys.versions.Installed(v) {
		return nil
	}
	if v == version() {
		exe, err := sys.executable()
		if err != nil {
			return err
		}
		return sys.versions.InstallFrom(v, exe)
	}
	if runtime.GOOS != "windows" && sys.versions.Downloads == versions.DefaultDownloads {
		return fmt.Errorf("lorekeep releases are built for Windows only; build %s from source", v)
	}
	fmt.Fprintf(w, "downloading lorekeep %s…\n", v)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return sys.versions.Install(ctx, v)
}

// offerUpdate offers a newer release than the project's pin, at most once per
// release, and returns the pin afterwards.
func offerUpdate(dir, pin string, stdout, stderr io.Writer) string {
	if sys.versions == nil {
		return pin
	}
	state := versions.LoadState(sys.statePath)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	latest := sys.versions.Refresh(ctx, &state, sys.now())
	cancel()
	// A closure, so the save sees the Decline below: Save has a value
	// receiver, and a plain defer would copy the state now.
	defer func() { state.Save(sys.statePath) }()

	if latest == nil || project.Compare(latest.Tag, pin) <= 0 || state.HasDeclined(latest.Tag) {
		return pin
	}
	fmt.Fprintf(stderr, "lorekeep %s is available; this project uses %s.\n", latest.Tag, pin)
	printNotes(stderr, latest.Notes)
	if !ask(stderr, "Update this project?", false) {
		state.Decline(latest.Tag)
		fmt.Fprintf(stderr, "Not asking about %s again; lorekeep update moves the project when you want.\n", latest.Tag)
		return pin
	}
	if moveProject(dir, pin, latest.Tag, stdout, stderr) != exitOK {
		return pin
	}
	return latest.Tag
}

// printNotes shows the head of a release's notes.
func printNotes(w io.Writer, notes string) {
	const maxLines = 20
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(notes, "\r\n", "\n")), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return
	}
	fmt.Fprintln(w, "Release notes:")
	for i, l := range lines {
		if i == maxLines {
			fmt.Fprintln(w, "  …")
			break
		}
		fmt.Fprintf(w, "  %s\n", l)
	}
}

// moveProject re-pins the project in dir from one version to another, but
// only if the new version builds it cleanly. Otherwise the pin stays.
func moveProject(dir, from, to string, stdout, stderr io.Writer) int {
	if err := install(to, stderr); err != nil {
		fmt.Fprintf(stderr, "lorekeep update: %v\n", err)
		return exitUsage
	}
	fmt.Fprintf(stdout, "checking the project with lorekeep %s…\n", to)
	code, err := sys.versions.Run(to, []string{"build", dir}, nil, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "lorekeep update: %v\n", err)
		return exitUsage
	}
	if code != exitOK {
		fmt.Fprintf(stderr, "lorekeep %s does not build this project cleanly; it stays on %s. "+
			"Fix the errors above, then run lorekeep update again.\n", to, from)
		return code
	}
	if err := project.WritePin(dir, to); err != nil {
		fmt.Fprintf(stderr, "lorekeep update: %v\n", err)
		return exitUsage
	}
	fmt.Fprintf(stdout, "pinned to lorekeep %s (was %s)\n", to, from)
	return exitOK
}

func update(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	target := fs.String("version", "", "the release to move to (default: the latest)")
	positional, ok := parseInterleaved(fs, args)
	if !ok {
		return exitUsage
	}
	if len(positional) > 1 {
		fmt.Fprintln(stderr, "lorekeep update: takes at most one world directory")
		return exitUsage
	}
	dir := "."
	if len(positional) == 1 {
		dir = positional[0]
	}

	pin, err := project.ReadPin(dir)
	if err != nil {
		fmt.Fprintf(stderr, "lorekeep update: %v\n", err)
		return exitUsage
	}

	to := *target
	switch {
	case to == "":
		if sys.versions == nil {
			fmt.Fprintln(stderr, "lorekeep update: cannot find a cache directory for lorekeep versions")
			return exitUsage
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		latest, err := sys.versions.Latest(ctx)
		cancel()
		if err != nil {
			fmt.Fprintf(stderr, "lorekeep update: cannot find the latest release: %v\n", err)
			return exitUsage
		}
		if project.Compare(latest.Tag, pin) <= 0 {
			// Never a downgrade by default; -version asks for one explicitly.
			to = pin
			break
		}
		to = latest.Tag
		fmt.Fprintf(stdout, "lorekeep %s is the latest release.\n", to)
		printNotes(stdout, latest.Notes)
	case !project.IsRelease(to):
		fmt.Fprintf(stderr, "lorekeep update: %q is not a release version like v1.2.3\n", to)
		return exitUsage
	case project.Compare(to, project.FirstPinVersion) < 0:
		fmt.Fprintf(stderr, "lorekeep update: cannot pin %s; the first lorekeep that reads %s is %s\n",
			to, project.PinFile, project.FirstPinVersion)
		return exitUsage
	}

	if to == pin {
		if err := install(to, stderr); err != nil {
			fmt.Fprintf(stderr, "lorekeep update: %v\n", err)
			return exitUsage
		}
		fmt.Fprintf(stdout, "already on lorekeep %s\n", pin)
		return exitOK
	}
	return moveProject(dir, pin, to, stdout, stderr)
}
