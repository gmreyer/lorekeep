package editor

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/a-h/templ"

	"github.com/gmreyer/lorekeep/internal/resolve"
	"github.com/gmreyer/lorekeep/internal/world"
)

// Page is everything the shell (layout.templ) draws around a view.
type Page struct {
	// Title is the browser tab's title.
	Title string
	Tabs  Tabs
	// Strip is the tab strip: one item per open tab.
	Strip []TabItem
	// Groups are the structure panel: every entity present, by type.
	Groups []TypeGroup
	Chips  Chips
	// Main is the view: the entity page, or an empty state.
	Main templ.Component
	// Right is the relations panel's content; nil leaves the panel out.
	Right templ.Component
	// Entity is the active entity's ID, for the Save button; empty when no
	// entity is open.
	Entity string
	// Keys is the key map as JSON, for keys.js; Token is the per-run token,
	// for the hx-headers every htmx request sends.
	Keys  string
	Token string
}

// TabItem is one editor tab.
type TabItem struct {
	ID     string
	Name   string
	Active bool
	Dirty  bool
	// URL shows the tab; CloseURL closes it.
	URL, CloseURL string
}

// TypeGroup is one type's entities in the structure panel, sorted by ID.
type TypeGroup struct {
	Type     string
	Entities []resolve.Summary
}

// Chips are the toolbar's status chips.
type Chips struct {
	// Status is the active entity's status; empty with none open.
	Status   string
	Errors   int
	Warnings int
	// Broken says the last build could not run; the chips show it as an
	// error.
	Broken bool
}

// page builds the shell around main and right for the tabs. It reads under
// the build lock; main and right render later, so they must already hold
// what they show.
func (s *Server) page(tabs Tabs, title string, main, right templ.Component) (Page, error) {
	keys, err := json.Marshal(s.prefs.keyMap())
	if err != nil {
		return Page{}, err
	}
	p := Page{
		Title: title, Tabs: tabs, Main: main, Right: right,
		Entity: tabs.Active, Keys: string(keys), Token: s.token,
	}
	snap := s.snapshot() // before the build lock: the drafts lock comes first
	err = s.read(func(cur *loaded) error {
		ctx := s.readContext()
		ents, err := cur.res.Entities(ctx)
		if err != nil {
			return err
		}
		byType := map[string][]resolve.Summary{}
		for _, e := range ents {
			byType[e.Type] = append(byType[e.Type], e)
		}
		for t, es := range byType {
			p.Groups = append(p.Groups, TypeGroup{Type: t, Entities: es})
		}
		slices.SortFunc(p.Groups, func(a, b TypeGroup) int { return cmp.Compare(a.Type, b.Type) })

		p.Strip = strip(cur, ctx, tabs, snap)
		if tabs.Active != "" {
			if e, err := cur.res.Entity(ctx, tabs.Active); err == nil {
				p.Chips.Status = e.Status
			} else if _, ok := snap.created[tabs.Active]; ok {
				p.Chips.Status = string(world.StatusDraft)
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errNoBuild) {
		return Page{}, err
	}
	p.Chips.Errors, p.Chips.Warnings, p.Chips.Broken = s.tally()
	return p, nil
}

// strip names each open tab: the entity's name through the resolver, or a
// created entity's typed name before its first save.
func strip(cur *loaded, ctx resolve.Context, tabs Tabs, snap snapshot) []TabItem {
	out := make([]TabItem, 0, len(tabs.IDs))
	for _, id := range tabs.IDs {
		it := TabItem{
			ID: id, Name: id, Active: id == tabs.Active, Dirty: snap.dirty[id],
			URL: Tabs{IDs: tabs.IDs, Active: id}.URL(), CloseURL: tabs.Close(id).URL(),
		}
		if e, err := cur.res.Entity(ctx, id); err == nil {
			it.Name = e.Name
		} else if c, ok := snap.created[id]; ok {
			it.Name = c.Name
		}
		out = append(out, it)
	}
	return out
}

// tally counts the last build's findings.
func (s *Server) tally() (errs, warns int, broken bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	errs = s.findings.Errors()
	return errs, len(s.findings) - errs, s.buildErr != ""
}

// home is the editor with the tabs in the URL, or an empty state.
func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	tabs := s.tabsFrom(r, "")
	if len(tabs.IDs) > 0 {
		http.Redirect(w, r, tabs.URL(), http.StatusSeeOther)
		return
	}
	p, err := s.page(tabs, "lorekeep", emptyState(), nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	render(w, r, layout(p))
}

// tabStrip re-renders the tab strip, after an edit marks a tab dirty or a
// save clears it.
func (s *Server) tabStrip(w http.ResponseWriter, r *http.Request) {
	tabs := s.tabsFrom(r, r.URL.Query().Get("at"))
	var items []TabItem
	snap := s.snapshot()
	err := s.read(func(cur *loaded) error {
		items = strip(cur, s.readContext(), tabs, snap)
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	render(w, r, tabStrip(tabs, items, false))
}

// openTabs makes a fragment response change the open tabs: the address bar
// takes their URL, and the returned component, rendered anywhere in the
// response, swaps in their strip. Opening a created entity in a background
// tab is the one use so far.
func (s *Server) openTabs(w http.ResponseWriter, tabs Tabs) (templ.Component, error) {
	snap := s.snapshot()
	var items []TabItem
	err := s.read(func(cur *loaded) error {
		items = strip(cur, s.readContext(), tabs, snap)
		return nil
	})
	if err != nil {
		return nil, err
	}
	w.Header().Set("HX-Replace-Url", tabs.URL())
	return tabStrip(tabs, items, true), nil
}

// statusChips re-renders the toolbar's chips after a save.
func (s *Server) statusChips(w http.ResponseWriter, r *http.Request) {
	tabs := s.tabsFrom(r, r.URL.Query().Get("at"))
	p, err := s.page(tabs, "", nil, nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	render(w, r, statusChips(p))
}

func joinIDs(ids []string) string { return strings.Join(ids, ",") }

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
