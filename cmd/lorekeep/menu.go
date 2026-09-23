package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gmreyer/lorekeep/internal/project"
	"github.com/gmreyer/lorekeep/internal/scaffold"
)

// The menu is what a writer sees on double-clicking lorekeep.exe. It is kept
// thin on purpose: every item runs the same subcommand a script would, so the
// menu adds no behaviour of its own, and the web editor replaces it later.

type menuItem struct {
	label string
	run   func() bool // reports whether to pause so the output can be read
}

// menu runs the interactive menu until the writer quits or input ends.
func menu(stdout, stderr io.Writer) int {
	m := &menuState{out: stdout, errw: stderr}
	m.current = startProject()
	for {
		items := m.items()
		m.header()
		for i, it := range items {
			fmt.Fprintf(stdout, "  %d) %s\n", i+1, it.label)
		}
		fmt.Fprintln(stdout, "  q) Quit")
		fmt.Fprint(stdout, "> ")

		choice, ok := readLine()
		if !ok || strings.EqualFold(choice, "q") {
			return exitOK
		}
		n, err := strconv.Atoi(choice)
		if err != nil || n < 1 || n > len(items) {
			fmt.Fprintf(stdout, "No item %q.\n\n", choice)
			continue
		}
		fmt.Fprintln(stdout)
		if items[n-1].run() {
			fmt.Fprint(stdout, "\nPress Enter to return to the menu.")
			if _, ok := readLine(); !ok {
				return exitOK
			}
		}
		fmt.Fprintln(stdout)
	}
}

type menuState struct {
	current string // absolute path of the open project, or empty
	out     io.Writer
	errw    io.Writer
}

// startProject is the project the menu opens with: the current directory if
// it is one, since that is where the writer started lorekeep, otherwise the
// last project opened.
func startProject() string {
	if project.IsProject(".") {
		abs, err := filepath.Abs(".")
		if err == nil {
			remember(abs)
			return abs
		}
	}
	if recent := project.LoadRecent(sys.recentPath); len(recent) > 0 {
		return recent[0]
	}
	return ""
}

func (m *menuState) header() {
	fmt.Fprintf(m.out, "lorekeep %s\n", version())
	if m.current == "" {
		fmt.Fprintln(m.out, "No project open.")
		return
	}
	fmt.Fprintf(m.out, "Project: %s", m.current)
	if pin, err := project.ReadPin(m.current); err == nil {
		fmt.Fprintf(m.out, " (lorekeep %s)", pin)
	}
	fmt.Fprintln(m.out)
}

func (m *menuState) items() []menuItem {
	if m.current == "" {
		return []menuItem{
			{"Set up a new project", m.setup},
			{"Open a project", m.open},
		}
	}
	items := []menuItem{
		{"Build", func() bool { run([]string{"build", m.current}, m.out, m.errw); return true }},
		{"Update lorekeep for this project", func() bool { run([]string{"update", m.current}, m.out, m.errw); return true }},
	}
	switch {
	case !exists(filepath.Join(m.current, ".gitattributes")):
		items = append(items, menuItem{"Prepare for git", m.prepareGit})
	case !exists(filepath.Join(m.current, filepath.FromSlash(scaffold.CIFile))):
		items = append(items, menuItem{"Add a GitHub CI workflow", func() bool {
			run([]string{"git", m.current, "-ci"}, m.out, m.errw)
			return true
		}})
	}
	return append(items,
		menuItem{"Open another project", m.open},
		menuItem{"Set up a new project", m.setup},
	)
}

func (m *menuState) open() bool {
	recent := project.LoadRecent(sys.recentPath)
	for i, dir := range recent {
		fmt.Fprintf(m.out, "  %d) %s\n", i+1, dir)
	}
	q := "Folder of the project (Enter to cancel): "
	if len(recent) > 0 {
		q = "Number, or folder of the project (Enter to cancel): "
	}
	answer := prompt(m.out, q)
	if answer == "" {
		return false
	}
	dir := answer
	if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(recent) {
		dir = recent[n-1]
	}
	abs, err := filepath.Abs(dir)
	if err != nil || !project.IsProject(abs) {
		fmt.Fprintf(m.out, "%s is not a lorekeep project: it needs schema/ and world/.\n", dir)
		return true
	}
	m.current = abs
	remember(abs)
	return false
}

func (m *menuState) setup() bool {
	dir := prompt(m.out, "Folder for the new project; it must be empty or new (Enter to cancel): ")
	if dir == "" {
		return false
	}
	name := suggestName(dir)
	if answer := prompt(m.out, fmt.Sprintf("Project name [%s]: ", name)); answer != "" {
		name = answer
	}
	args := []string{"setup", dir, "-name", name}
	if ask(m.out, "Add a small example world to learn the format from?", false) {
		args = append(args, "-example")
	}
	if ask(m.out, "Prepare the project for git? A project works without it.", false) {
		args = append(args, "-git")
		if ask(m.out, "Also add a GitHub CI workflow that builds on every push?", false) {
			args = append(args, "-ci")
		}
	}
	fmt.Fprintln(m.out)
	if run(args, m.out, m.errw) == exitOK {
		if abs, err := filepath.Abs(dir); err == nil {
			m.current = abs
			remember(abs)
		}
	}
	return true
}

func (m *menuState) prepareGit() bool {
	args := []string{"git", m.current}
	if ask(m.out, "Also add a GitHub CI workflow that builds on every push?", false) {
		args = append(args, "-ci")
	}
	run(args, m.out, m.errw)
	return true
}

// prompt asks for a line of text. Surrounding quotes are dropped, since
// Explorer's "Copy as path" adds them.
func prompt(w io.Writer, question string) string {
	fmt.Fprint(w, question)
	line, _ := readLine()
	return strings.Trim(line, `"`)
}

// readLine reads one line of input; false means input has ended.
func readLine() (string, bool) {
	line, err := sys.in.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimSpace(line), true
}

// suggestName turns a folder name into a valid project name: lowercase,
// with anything outside [a-z0-9_] as an underscore.
func suggestName(dir string) string {
	base := strings.ToLower(filepath.Base(filepath.Clean(dir)))
	var b strings.Builder
	for _, r := range base {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if !strings.HasSuffix(b.String(), "_") {
			b.WriteByte('_')
		}
	}
	if name := strings.Trim(b.String(), "_"); name != "" {
		return name
	}
	return "world"
}

func remember(dir string) {
	if sys.recentPath != "" {
		project.AddRecent(sys.recentPath, dir)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
