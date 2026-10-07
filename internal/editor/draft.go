package editor

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/gmreyer/lorekeep/internal/world"
)

// Draft is the unsaved edits to one entity. Nothing reaches disk until the
// writer saves (Ctrl+S); until then every form and panel adds to the draft,
// and renders the entity as the draft would leave it (Draft.Bytes).
//
// The fields form and the relations panel only add to a draft; the save
// only commits one. Drafts live in server memory, so switching or closing an
// editor tab keeps them; stopping lorekeep loses them.
type Draft struct {
	// ID is the entity being edited.
	ID string
	// File is its file, relative to world/, with forward slashes.
	File string
	// Base is the file as read when the draft began, and Stamp what it was on
	// disk then; a save refuses a file that no longer matches Stamp.
	Base  []byte
	Stamp Stamp
	// Changes are frontmatter edits, applied to Base in order.
	Changes []world.Change
	// Body is the new prose, or nil when the prose is unchanged.
	Body *string
	// Created are entities made from this one's relation picker. Each is
	// written with this entity's save, so a link never names a missing file.
	Created []Created
}

// Stamp is what a file was on disk: enough to tell that something else wrote
// it since.
type Stamp struct {
	ModTime time.Time
	Hash    [sha256.Size]byte
}

// Created is a new entity awaiting its first save.
type Created struct {
	ID   string
	Type string
	Name string
	// File is where it will be written, relative to world/, with forward
	// slashes.
	File    string
	Content []byte
}

// Dirty reports whether the draft holds anything to save.
func (d *Draft) Dirty() bool {
	return len(d.Changes) > 0 || d.Body != nil || len(d.Created) > 0
}

// Bytes is the entity's file as the draft would leave it.
func (d *Draft) Bytes() ([]byte, error) {
	out := d.Base
	if len(d.Changes) > 0 {
		var err error
		if out, _, err = world.EditFrontmatter(out, d.Changes); err != nil {
			return nil, err
		}
	}
	if d.Body != nil {
		var err error
		if out, _, err = world.ReplaceBody(out, *d.Body); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Entity parses the file as the draft would leave it. Findings are the
// parser's: a draft can be wrong, and the form shows where.
func (d *Draft) Entity() (*world.Entity, []world.Finding, error) {
	data, err := d.Bytes()
	if err != nil {
		return nil, nil, err
	}
	doc, fs := world.Parse(d.File, data)
	if doc.Entity == nil {
		return nil, fs, fmt.Errorf("%s does not read as an entity", d.File)
	}
	return doc.Entity, fs, nil
}

// clone copies a draft deep enough that the copy can be read without the
// drafts lock.
func (d *Draft) clone() Draft {
	c := *d
	c.Changes = slices.Clone(d.Changes)
	c.Created = slices.Clone(d.Created)
	if d.Body != nil {
		b := *d.Body
		c.Body = &b
	}
	return c
}

// drafts are the open drafts, by entity ID.
//
// Lock order: the drafts lock, then the build lock (Server.mu). Nothing that
// holds the build lock may take the drafts lock.
type drafts struct {
	mu sync.Mutex
	m  map[string]*Draft
}

// editDraft runs fn on the draft of the entity id, starting one from its file
// on disk if none is open. fn runs under the drafts lock; it must not call
// back into the drafts.
func (s *Server) editDraft(id string, fn func(d *Draft) error) error {
	s.drafts.mu.Lock()
	defer s.drafts.mu.Unlock()
	d, ok := s.drafts.m[id]
	if !ok {
		var err error
		if d, err = s.startDraft(id); err != nil {
			return err
		}
	}
	if err := fn(d); err != nil {
		return err
	}
	s.drafts.m[id] = d
	return nil
}

// draft returns a copy of the draft of id, or a fresh one read from disk if
// none is open. The fresh one is not kept: only an edit opens a draft.
func (s *Server) draft(id string) (Draft, error) {
	s.drafts.mu.Lock()
	defer s.drafts.mu.Unlock()
	if d, ok := s.drafts.m[id]; ok {
		return d.clone(), nil
	}
	d, err := s.startDraft(id)
	if err != nil {
		return Draft{}, err
	}
	return *d, nil
}

// dirty reports whether id has unsaved edits.
func (s *Server) dirty(id string) bool {
	s.drafts.mu.Lock()
	defer s.drafts.mu.Unlock()
	d, ok := s.drafts.m[id]
	return ok && d.Dirty()
}

// dropDraft forgets the draft of id: after a save, or when the writer
// discards it.
func (s *Server) dropDraft(id string) {
	s.drafts.mu.Lock()
	defer s.drafts.mu.Unlock()
	delete(s.drafts.m, id)
}

// created finds an entity created in some open draft and not yet saved, with
// the ID of the entity whose save will write it.
func (s *Server) created(id string) (c Created, origin string, ok bool) {
	s.drafts.mu.Lock()
	defer s.drafts.mu.Unlock()
	for _, d := range s.drafts.m {
		for _, c := range d.Created {
			if c.ID == id {
				return c, d.ID, true
			}
		}
	}
	return Created{}, "", false
}

// snapshot is what the drafts say about every entity, read at once so a
// caller can use it under the build lock.
type snapshot struct {
	dirty   map[string]bool
	created map[string]Created
}

func (s *Server) snapshot() snapshot {
	s.drafts.mu.Lock()
	defer s.drafts.mu.Unlock()
	out := snapshot{dirty: map[string]bool{}, created: map[string]Created{}}
	for id, d := range s.drafts.m {
		out.dirty[id] = d.Dirty()
		for _, c := range d.Created {
			out.created[c.ID] = c
		}
	}
	return out
}

// startDraft reads the entity's file from disk. The caller holds the drafts
// lock.
func (s *Server) startDraft(id string) (*Draft, error) {
	var file string
	err := s.read(func(cur *loaded) error {
		e, err := cur.res.Entity(s.readContext(), id)
		file = e.File
		return err
	})
	if err != nil {
		return nil, err
	}
	data, stamp, err := s.readWorldFile(file)
	if err != nil {
		return nil, err
	}
	return &Draft{ID: id, File: file, Base: data, Stamp: stamp}, nil
}

// worldPath is a world-relative, forward-slash path on disk.
func (s *Server) worldPath(rel string) string {
	return filepath.Join(s.repo, index.WorldDir, filepath.FromSlash(rel))
}

// readWorldFile reads a file under world/ with its stamp.
func (s *Server) readWorldFile(rel string) ([]byte, Stamp, error) {
	path := s.worldPath(rel)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, Stamp{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, Stamp{}, err
	}
	return data, Stamp{ModTime: info.ModTime(), Hash: sha256.Sum256(data)}, nil
}

// errChangedOutside is a save of a file that something else wrote since the
// draft began (Step 6 plan Q5). The writer reloads; nothing is merged.
var errChangedOutside = errors.New("changed outside the editor")

// HX-Trigger events the shell listens for, sent by a handler that changed
// what the tab strip or the status chips show.
const (
	// eventDirty is sent after an edit lands in a draft.
	eventDirty = "lk-dirty"
	// eventSaved is sent after a save, when the tabs and the build changed.
	eventSaved = "lk-saved"
)

// trigger asks htmx to fire event on <body> once the response is swapped in.
// htmx reads one HX-Trigger header, a comma-separated list.
func trigger(w http.ResponseWriter, event string) {
	if prev := w.Header().Get("HX-Trigger"); prev != "" {
		event = prev + ", " + event
	}
	w.Header().Set("HX-Trigger", event)
}
