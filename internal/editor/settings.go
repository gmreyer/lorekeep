package editor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Settings are one user's editor preferences. They live outside the project,
// in the user's cache directory, and lorekeep writes them: the user never
// edits the file.
type Settings struct {
	// Keys are the bindings the user changed, by action; an action not here
	// has its default.
	Keys map[string]string `json:"keys,omitempty"`
	// Worldline is the story branch every view follows (Step 7).
	Worldline map[string]string `json:"worldline,omitempty"`
	// Panels records which panels the user closed.
	Panels map[string]bool `json:"panels,omitempty"`
}

// settingsFile is the file's name in the user's lorekeep folder.
const settingsFile = "editor.json"

// prefs is the settings file and what it holds.
type prefs struct {
	path string
	mu   sync.Mutex
	s    Settings
}

// loadPrefs reads the settings at path, or at the default place when path is
// empty. A missing file is the defaults. A file that does not decode is also
// the defaults: it is only ever written by lorekeep, so it is a leftover of a
// crash, and the next change overwrites it.
func loadPrefs(path string) (*prefs, error) {
	if path == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return nil, fmt.Errorf("finding the settings folder: %w", err)
		}
		path = filepath.Join(dir, "lorekeep", settingsFile)
	}
	p := &prefs{path: path}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("reading %s: %w", path, err)
	default:
		if json.Unmarshal(data, &p.s) != nil {
			p.s = Settings{}
		}
	}
	return p, nil
}

// refresh rereads the file before a change, so a change made by another
// editor (another project open at once) is kept rather than overwritten. A
// file that cannot be read leaves what is in memory. The caller holds p.mu.
func (p *prefs) refresh() {
	data, err := os.ReadFile(p.path)
	if err != nil {
		return
	}
	var s Settings
	if json.Unmarshal(data, &s) == nil {
		p.s = s
	}
}

// save writes the settings atomically: a temporary file renamed over the old
// one, so a crash never leaves half a file. The caller holds p.mu.
func (p *prefs) save() error {
	data, err := json.MarshalIndent(p.s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p.path), settingsFile+".*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(append(data, '\n'))
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), p.path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// worldline returns a copy of the selected worldline.
func (p *prefs) worldline() map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return maps.Clone(p.s.Worldline)
}

// Action is something a key binding does. keys.js knows each by Name.
type Action struct {
	Name    string
	Label   string
	Default string
}

// actions are every bindable action, in the order the settings page lists
// them. The defaults are round 3's, checked against the browsers' own keys.
var actions = []Action{
	{"switcher", "Quick switcher", "Ctrl+K"},
	{"save", "Save", "Ctrl+S"},
	{"close-tab", "Close editor tab", "Alt+W"},
	{"prev-tab", "Previous editor tab", "Alt+["},
	{"next-tab", "Next editor tab", "Alt+]"},
	{"toggle-structure", "Toggle structure panel", "Alt+1"},
	{"toggle-relations", "Toggle relations panel", "Alt+2"},
	{"toggle-graph", "Toggle graph panel", "Alt+3"},
	{"fields", "Expand or fold fields", "Alt+."},
}

// reserved are combinations the browser keeps for itself on Windows, in
// Chrome, Edge or Firefox: a page cannot have them, or must not take them.
// Alt+Left and Alt+Right are back and forward, which the editor rides on.
var reserved = func() map[string]string {
	m := map[string]string{
		"Ctrl+W":         "closes the browser tab",
		"Ctrl+Shift+W":   "closes the browser window",
		"Ctrl+T":         "opens a browser tab",
		"Ctrl+Shift+T":   "reopens a closed browser tab",
		"Ctrl+N":         "opens a browser window",
		"Ctrl+Shift+N":   "opens a private window",
		"Ctrl+Tab":       "switches browser tabs",
		"Ctrl+Shift+Tab": "switches browser tabs",
		"Ctrl+PageUp":    "switches browser tabs",
		"Ctrl+PageDown":  "switches browser tabs",
		"F5":             "reloads the page",
		"Ctrl+R":         "reloads the page",
		"Ctrl+L":         "focuses the address bar",
		"Alt+D":          "focuses the address bar",
		"Alt+F4":         "closes the browser window",
		"Alt+Left":       "is the browser's back",
		"Alt+Right":      "is the browser's forward",
		"Alt+Home":       "opens the browser's home page",
	}
	for i := 1; i <= 9; i++ {
		m[fmt.Sprintf("Ctrl+%d", i)] = "switches browser tabs"
	}
	// Alt and a menu letter opens a browser menu.
	for _, k := range "FEVSBTH" {
		m["Alt+"+string(k)] = "opens a browser menu"
	}
	return m
}()

// modifiers in the order a combination is written.
var modifiers = []string{"Ctrl", "Alt", "Shift"}

// normalizeCombo writes a combination the one way it is stored: modifiers in
// a fixed order, then one key, a letter in upper case. It reports false for
// a combination with no key, or with no modifier on a key that types text.
func normalizeCombo(combo string) (string, bool) {
	parts := strings.Split(combo, "+") // so the + key itself cannot be bound
	key := parts[len(parts)-1]
	mods := map[string]bool{}
	for _, m := range parts[:len(parts)-1] {
		m = strings.TrimSpace(m)
		i := slices.IndexFunc(modifiers, func(x string) bool { return strings.EqualFold(x, m) })
		if i < 0 {
			return "", false
		}
		mods[modifiers[i]] = true
	}
	key = strings.TrimSpace(key)
	if key == "" || slices.ContainsFunc(modifiers, func(x string) bool { return strings.EqualFold(x, key) }) {
		return "", false
	}
	if len([]rune(key)) == 1 {
		key = strings.ToUpper(key)
	}
	isFn := len(key) > 1 && key[0] == 'F' && strings.Trim(key[1:], "0123456789") == ""
	if !mods["Ctrl"] && !mods["Alt"] && !isFn {
		return "", false // a bare or shifted key types text
	}
	if mods["Ctrl"] && mods["Alt"] {
		return "", false // AltGr on Windows: it types text on many layouts
	}
	var b strings.Builder
	for _, m := range modifiers {
		if mods[m] {
			b.WriteString(m + "+")
		}
	}
	b.WriteString(key)
	return b.String(), true
}

// Binding is one action and its current keys.
type Binding struct {
	Action
	Keys string
}

// bindings returns every action with its keys.
func (p *prefs) bindings() []Binding {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bindingsLocked()
}

func (p *prefs) bindingsLocked() []Binding {
	out := make([]Binding, len(actions))
	for i, a := range actions {
		out[i] = Binding{Action: a, Keys: a.Default}
		if k, ok := p.s.Keys[a.Name]; ok {
			out[i].Keys = k
		}
	}
	return out
}

// keyMap is the bindings as keys.js reads them: combination to action.
func (p *prefs) keyMap() map[string]string {
	m := map[string]string{}
	for _, b := range p.bindings() {
		m[b.Keys] = b.Name
	}
	return m
}

// errBinding is a combination the editor will not bind; its text says why.
type errBinding struct{ msg string }

func (e errBinding) Error() string { return e.msg }

// bind sets an action's keys, refusing a combination the browser keeps or
// another action already has.
func (p *prefs) bind(action, combo string) error {
	if !slices.ContainsFunc(actions, func(a Action) bool { return a.Name == action }) {
		return errBinding{fmt.Sprintf("there is no action %q", action)}
	}
	norm, ok := normalizeCombo(combo)
	if !ok {
		return errBinding{fmt.Sprintf("%s is not a shortcut: use Ctrl or Alt (not both: that is AltGr) with a key, or a function key", combo)}
	}
	if why, ok := reserved[norm]; ok {
		return errBinding{fmt.Sprintf("%s %s, so the browser keeps it", norm, why)}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refresh()
	for _, b := range p.bindingsLocked() {
		if b.Keys == norm && b.Name != action {
			return errBinding{fmt.Sprintf("%s is already %s", norm, strings.ToLower(b.Label))}
		}
	}
	if p.s.Keys == nil {
		p.s.Keys = map[string]string{}
	}
	def := actions[slices.IndexFunc(actions, func(a Action) bool { return a.Name == action })].Default
	if norm == def {
		delete(p.s.Keys, action)
	} else {
		p.s.Keys[action] = norm
	}
	return p.save()
}

// resetKeys returns every binding to its default.
func (p *prefs) resetKeys() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refresh()
	p.s.Keys = nil
	return p.save()
}
