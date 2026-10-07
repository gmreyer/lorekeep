package editor

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Tabs are the open editor tabs, which live in the URL: /entity/<active>
// ?tabs=<id>,<id>. Opening an entity is a navigation, so the browser's back
// and forward (Alt+Left, Alt+Right, the mouse buttons) move through it with
// no script.
type Tabs struct {
	IDs    []string
	Active string
	// keep are the tabs with unsaved changes: Open never replaces one.
	keep map[string]bool
}

// tabID keeps a tab list to IDs the validator would accept, so nothing else
// reaches a URL or a lookup.
var tabID = regexp.MustCompile(`^[a-z0-9_]+$`)

// tabsParam is the query parameter holding the open tabs.
const tabsParam = "tabs"

// tabsFrom reads the open tabs from a request, with active as the active one,
// added after the others if the list lacks it. active may be empty. It takes
// the drafts lock, so it is never called inside s.read.
func (s *Server) tabsFrom(r *http.Request, active string) Tabs {
	t := parseTabs(r, active)
	t.keep = s.snapshot().dirty
	return t
}

func parseTabs(r *http.Request, active string) Tabs {
	var t Tabs
	for _, id := range strings.Split(r.URL.Query().Get(tabsParam), ",") {
		if tabID.MatchString(id) && !slices.Contains(t.IDs, id) {
			t.IDs = append(t.IDs, id)
		}
	}
	if active != "" && tabID.MatchString(active) {
		if !slices.Contains(t.IDs, active) {
			t.IDs = append(t.IDs, active)
		}
		t.Active = active
	}
	return t
}

// URL is the address of these tabs, showing the active one; with none open,
// the home page.
func (t Tabs) URL() string {
	if len(t.IDs) == 0 {
		return "/"
	}
	active := t.Active
	if !slices.Contains(t.IDs, active) {
		active = t.IDs[0]
	}
	return "/entity/" + active + "?" + tabsParam + "=" + url.QueryEscape(strings.Join(t.IDs, ","))
}

func (t Tabs) clone() Tabs { return Tabs{IDs: slices.Clone(t.IDs), Active: t.Active, keep: t.keep} }

// Open shows id in the current tab: a plain click on a link. An id already
// open is switched to instead of opened twice, and a current tab with unsaved
// changes is kept: id opens in a new tab beside it.
func (t Tabs) Open(id string) Tabs {
	if t.keep[t.Active] && !slices.Contains(t.IDs, id) {
		return t.Add(id)
	}
	n := t.clone()
	switch i := slices.Index(n.IDs, t.Active); {
	case slices.Contains(n.IDs, id):
	case i < 0:
		n.IDs = append(n.IDs, id)
	default:
		n.IDs[i] = id
	}
	n.Active = id
	return n
}

// Add opens id in a new tab after the active one and shows it: Ctrl+click.
func (t Tabs) Add(id string) Tabs {
	n := t.Background(id)
	n.Active = id
	return n
}

// Background opens id in a new tab after the active one and stays where it
// was: a created entity, so the writer keeps writing.
func (t Tabs) Background(id string) Tabs {
	n := t.clone()
	if slices.Contains(n.IDs, id) {
		return n
	}
	i := slices.Index(n.IDs, t.Active)
	n.IDs = slices.Insert(n.IDs, i+1, id)
	return n
}

// Close closes the tab of id. Closing the active tab shows its left
// neighbour, or the right one when it was first.
func (t Tabs) Close(id string) Tabs {
	n := t.clone()
	i := slices.Index(n.IDs, id)
	if i < 0 {
		return n
	}
	n.IDs = slices.Delete(n.IDs, i, i+1)
	if n.Active == id {
		n.Active = ""
		if len(n.IDs) > 0 {
			n.Active = n.IDs[max(i-1, 0)]
		}
	}
	return n
}

// Step shows the tab delta places from the active one, wrapping around.
func (t Tabs) Step(delta int) Tabs {
	n := t.clone()
	i := slices.Index(n.IDs, n.Active)
	if i < 0 || len(n.IDs) == 0 {
		return n
	}
	n.Active = n.IDs[((i+delta)%len(n.IDs)+len(n.IDs))%len(n.IDs)]
	return n
}
