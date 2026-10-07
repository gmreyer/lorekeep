package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"gopkg.in/yaml.v3"
)

// ProjectServerName is the key of the entry in a project's .mcp.json.
const ProjectServerName = "lorekeep"

// ProjectConfigFile is the file Claude Code reads for a project's servers.
const ProjectConfigFile = ".mcp.json"

// DesktopConfigFile is Claude Desktop's config, inside its config directory.
const DesktopConfigFile = "claude_desktop_config.json"

// serversKey holds the server entries in both config formats.
const serversKey = "mcpServers"

// Entry is one server in an MCP client's config: a command the client
// launches and talks to over stdio.
type Entry struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// NewEntry is the entry that launches this lorekeep on the project in dir.
func NewEntry(exe, dir string) Entry {
	return Entry{Command: exe, Args: []string{"mcp", dir}}
}

// DesktopServerName is the name of a project's entry in Claude Desktop's
// config, where every project's server sits side by side.
func DesktopServerName(project string) string { return "lorekeep-" + project }

// ProjectName reads the project's name from schema/pack.yaml. When that file
// has none it falls back to the directory's base name, and says so.
func ProjectName(dir string) (name string, fromPack bool) {
	if data, err := os.ReadFile(filepath.Join(dir, "schema", "pack.yaml")); err == nil {
		var meta struct {
			Name string `yaml:"name"`
		}
		if yaml.Unmarshal(data, &meta) == nil && meta.Name != "" {
			return meta.Name, true
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	return filepath.Base(abs), false
}

// DesktopConfigPaths are the config files Claude Desktop may be reading,
// most likely first; more than one means the caller must ask which.
//
// The Microsoft Store build of Claude Desktop does not read %APPDATA%: Windows
// gives a packaged app its own copy of AppData under its package folder, so a
// file written to %APPDATA%\Claude is never seen and is overwritten when the
// app next saves its settings. A Store install is found by its package folder,
// Claude_<publisher hash>, and its copy is used; %APPDATA%\Claude only when
// there is none.
func DesktopConfigPaths() ([]string, error) {
	conf, err := os.UserConfigDir()
	if err != nil && runtime.GOOS != "windows" {
		return nil, fmt.Errorf("cannot find Claude Desktop's config directory: %w", err)
	}
	paths := desktopConfigCandidates(runtime.GOOS, os.Getenv("APPDATA"), os.Getenv("LOCALAPPDATA"), conf)
	if len(paths) == 0 {
		return nil, errors.New("cannot find Claude Desktop's config directory: APPDATA is not set")
	}
	return paths, nil
}

// desktopConfigCandidates is DesktopConfigPaths with its environment passed
// in, so tests can build a fake one.
func desktopConfigCandidates(goos, appdata, localAppdata, userConfig string) []string {
	if goos != "windows" {
		return []string{filepath.Join(userConfig, "Claude", DesktopConfigFile)}
	}
	var store []string
	if localAppdata != "" {
		pkgs, _ := filepath.Glob(filepath.Join(localAppdata, "Packages", "Claude_*"))
		for _, pkg := range pkgs {
			if info, err := os.Stat(pkg); err == nil && info.IsDir() {
				store = append(store, filepath.Join(pkg, "LocalCache", "Roaming", "Claude", DesktopConfigFile))
			}
		}
	}
	if len(store) > 0 {
		slices.Sort(store)
		return store
	}
	if appdata == "" {
		return nil
	}
	return []string{filepath.Join(appdata, "Claude", DesktopConfigFile)}
}

// Merge adds entry under name in a config file's mcpServers, keeping every
// other key and server as it was. existing may be empty, for a file that does
// not exist yet. changed is false when name already holds exactly entry, so
// running install twice leaves the file alone.
func Merge(existing []byte, name string, entry Entry) (out []byte, changed bool, err error) {
	top := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &top); err != nil {
			return nil, false, fmt.Errorf("existing config is not a JSON object: %w", err)
		}
	}
	servers := map[string]json.RawMessage{}
	if raw, ok := top[serversKey]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, false, fmt.Errorf("existing %s is not an object: %w", serversKey, err)
		}
	}
	want, err := json.Marshal(entry)
	if err != nil {
		return nil, false, err
	}
	if have, ok := servers[name]; ok && sameJSON(have, want) {
		return existing, false, nil
	}
	servers[name] = want
	if top[serversKey], err = json.Marshal(servers); err != nil {
		return nil, false, err
	}
	out, err = json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(out, '\n'), true, nil
}

// sameJSON compares two JSON values regardless of formatting.
func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return bytes.Equal(xa, ya)
}

// WriteConfig merges entry into the config file at path. A file that exists
// is first copied to path + ".bak" when backup is set; a missing one is
// created, with its directory, and has nothing to back up. It reports whether
// anything was written.
func WriteConfig(path, name string, entry Entry, backup bool) (changed bool, err error) {
	existing, err := os.ReadFile(path)
	missing := os.IsNotExist(err)
	if err != nil && !missing {
		return false, err
	}
	out, changed, err := Merge(existing, name, entry)
	if err != nil || !changed {
		return false, err
	}
	if missing {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return false, err
		}
	} else if backup {
		if err := os.WriteFile(path+".bak", existing, 0o644); err != nil {
			return false, fmt.Errorf("backing up %s: %w", path, err)
		}
	}
	return true, os.WriteFile(path, out, 0o644)
}
