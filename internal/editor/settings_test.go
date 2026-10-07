package editor

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestNormalizeCombo(t *testing.T) {
	for in, want := range map[string]string{
		"Ctrl+K":       "Ctrl+K",
		"ctrl+k":       "Ctrl+K",
		"Shift+Alt+[":  "Alt+Shift+[",
		"Alt+.":        "Alt+.",
		"F2":           "F2",
		"Ctrl+Shift+S": "Ctrl+Shift+S",
	} {
		if got, ok := normalizeCombo(in); !ok || got != want {
			t.Errorf("normalizeCombo(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "K", "Shift+K", "Ctrl+", "Ctrl+Alt", "Hyper+K"} {
		if got, ok := normalizeCombo(in); ok {
			t.Errorf("normalizeCombo(%q) = %q; want refused", in, got)
		}
	}
}

// TestDefaultsAvoidTheBrowser keeps round 3's promise: no default is a
// combination the browser keeps, and no two actions share one.
func TestDefaultsAvoidTheBrowser(t *testing.T) {
	seen := map[string]string{}
	for _, a := range actions {
		norm, ok := normalizeCombo(a.Default)
		if !ok || norm != a.Default {
			t.Errorf("%s: default %q is not normalised (%q)", a.Name, a.Default, norm)
		}
		if why, ok := reserved[a.Default]; ok {
			t.Errorf("%s: default %s %s", a.Name, a.Default, why)
		}
		if other, ok := seen[a.Default]; ok {
			t.Errorf("%s and %s share %s", a.Name, other, a.Default)
		}
		seen[a.Default] = a.Name
	}
}

func TestBindRefusesReservedAndClash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lorekeep", "editor.json")
	p, err := loadPrefs(path)
	if err != nil {
		t.Fatal(err)
	}
	var refused errBinding
	for _, c := range []struct{ action, combo, says string }{
		{"save", "Ctrl+W", "closes the browser tab"},
		{"save", "Ctrl+3", "switches browser tabs"},
		{"save", "Alt+F", "opens a browser menu"},
		{"save", "Ctrl+K", "already quick switcher"},
		{"save", "S", "not a shortcut"},
		{"nothing", "Ctrl+J", "no action"},
	} {
		err := p.bind(c.action, c.combo)
		if !errors.As(err, &refused) || !strings.Contains(err.Error(), c.says) {
			t.Errorf("bind(%s, %s) = %v; want a refusal saying %q", c.action, c.combo, err, c.says)
		}
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a refused binding wrote the settings file")
	}

	if err := p.bind("save", "ctrl+shift+s"); err != nil {
		t.Fatal(err)
	}
	q, err := loadPrefs(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := q.keyMap()["Ctrl+Shift+S"]; got != "save" {
		t.Errorf("after reload, Ctrl+Shift+S = %q, want save", got)
	}
	if err := q.resetKeys(); err != nil {
		t.Fatal(err)
	}
	if got := q.keyMap()["Ctrl+S"]; got != "save" {
		t.Errorf("after reset, Ctrl+S = %q, want save", got)
	}
}

// TestKeysPageRebinds drives the settings page as keys.js does: a refused
// combination comes back with its reason, a good one reloads the page.
func TestKeysPageRebinds(t *testing.T) {
	s, _ := newTestServer(t)
	w := do(t, s, "POST", "/settings/keys", form("action", "save", "keys", "Ctrl+T"))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "opens a browser tab") {
		t.Errorf("reserved: %d %s", w.Code, w.Body)
	}
	w = do(t, s, "POST", "/settings/keys", form("action", "save", "keys", "Ctrl+Shift+S"))
	if w.Header().Get("HX-Redirect") != "/settings/keys" {
		t.Errorf("saved binding: headers %v", w.Header())
	}
	page := do(t, s, "GET", "/settings/keys", nil).Body.String()
	if !strings.Contains(page, "Ctrl+Shift+S") {
		t.Error("the page does not show the new binding")
	}
}

func TestTabs(t *testing.T) {
	r := httptest.NewRequest("GET", "/entity/b?tabs=a,b,c,a,Bad,../x", nil)
	tabs := parseTabs(r, "b")
	if diff := cmp.Diff(Tabs{IDs: []string{"a", "b", "c"}, Active: "b"}, tabs, cmp.AllowUnexported(Tabs{})); diff != "" {
		t.Fatalf("tabsFrom (-want +got):\n%s", diff)
	}
	for _, c := range []struct {
		name string
		got  Tabs
		want string
	}{
		{"open replaces the active tab", tabs.Open("d"), "/entity/d?tabs=a%2Cd%2Cc"},
		{"open an open one switches", tabs.Open("c"), "/entity/c?tabs=a%2Cb%2Cc"},
		{"add opens after the active", tabs.Add("d"), "/entity/d?tabs=a%2Cb%2Cd%2Cc"},
		{"background stays", tabs.Background("d"), "/entity/b?tabs=a%2Cb%2Cd%2Cc"},
		{"close active shows the left", tabs.Close("b"), "/entity/a?tabs=a%2Cc"},
		{"close first shows the right", Tabs{IDs: []string{"a", "b"}, Active: "a"}.Close("a"), "/entity/b?tabs=b"},
		{"close the last", Tabs{IDs: []string{"a"}, Active: "a"}.Close("a"), "/"},
		{"step wraps", tabs.Step(2), "/entity/a?tabs=a%2Cb%2Cc"},
		{"open keeps an unsaved tab", Tabs{IDs: []string{"a", "b"}, Active: "b", keep: map[string]bool{"b": true}}.Open("d"), "/entity/d?tabs=a%2Cb%2Cd"},
	} {
		if got := c.got.URL(); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func form(kv ...string) map[string][]string {
	out := map[string][]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = append(out[kv[i]], kv[i+1])
	}
	return out
}
