package editor

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/gmreyer/lorekeep/internal/world"
)

// Task 4 owns this file: save → rebuild → validate.

func (s *Server) saveRoutes() {
	s.mux.HandleFunc("POST /entity/{id}/save", s.save)
	s.mux.HandleFunc("POST /entity/{id}/discard", s.discard)
}

// notice is what a save answers with, in the toolbar's notice area.
type notice struct {
	Text string
	Tone tone
	// Reload is the entity whose draft the notice offers to drop; empty
	// offers nothing.
	Reload string
}

type tone int

const (
	toneQuiet tone = iota
	tonePass
	toneError
)

func (n notice) class() string {
	switch n.Tone {
	case tonePass:
		return "lk-pass"
	case toneError:
		return "lk-error"
	}
	return "lk-quiet"
}

// save commits the entity's draft to disk, then rebuilds and validates the
// world (Step 6 plan Task 4).
//
// The drafts lock is held for the whole save, so no edit lands in the draft
// between computing its bytes and dropping it. The rebuild inside takes the
// build lock, which is the legal order.
//
// A file changed on disk since the draft began is not overwritten and
// nothing is merged (Q5): the writer reloads. A save that writes leaves the
// files saved even when the world then has errors; those are the writer's
// to fix.
func (s *Server) save(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	n := s.withDrafts(func(m map[string]*Draft) notice {
		d, ok := m[id]
		if !ok || !d.Dirty() {
			return notice{Text: "Nothing to save"}
		}
		n, saved := s.commit(d)
		if saved {
			delete(m, id)
			trigger(w, eventSaved)
		}
		return n
	})
	render(w, r, saveNotice(n))
}

// discard drops the entity's draft and reloads the page from disk: the way
// out of a save refused because the file changed outside the editor.
func (s *Server) discard(w http.ResponseWriter, r *http.Request) {
	s.dropDraft(r.PathValue("id"))
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

// withDrafts runs fn under the drafts lock on the open drafts.
func (s *Server) withDrafts(fn func(m map[string]*Draft) notice) notice {
	s.drafts.mu.Lock()
	defer s.drafts.mu.Unlock()
	return fn(s.drafts.m)
}

// commit writes d and rebuilds. saved reports that the draft is now on disk
// and may be dropped. The caller holds the drafts lock.
func (s *Server) commit(d *Draft) (n notice, saved bool) {
	name := draftName(d)
	refuse := func(text string) (notice, bool) {
		return notice{Text: text, Tone: toneError, Reload: d.ID}, false
	}

	failed := func(text string) (notice, bool) {
		return notice{Text: "Not saved: " + text, Tone: toneError}, false
	}

	// Q5: the file must still be what the draft began from. A file that
	// cannot be read now is reported as that, not as an outside change.
	disk, stamp, err := s.readWorldFile(d.File)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return refuse(fmt.Sprintf("%s %s", name, errChangedOutside))
	case err != nil:
		return failed(fmt.Sprintf("%s: %v", name, err))
	case !stamp.ModTime.Equal(d.Stamp.ModTime) || stamp.Hash != d.Stamp.Hash:
		return refuse(fmt.Sprintf("%s %s", name, errChangedOutside))
	}
	for _, c := range d.Created {
		if !validCreatedFile(c.File) {
			return failed(fmt.Sprintf("%s: %q is not a Markdown file inside world/", c.Name, c.File))
		}
		if _, err := os.Lstat(s.worldPath(c.File)); !errors.Is(err, fs.ErrNotExist) {
			return refuse(fmt.Sprintf("%s %s: its file already exists", c.Name, errChangedOutside))
		}
	}

	out, err := d.Bytes()
	if err != nil {
		return failed(err.Error())
	}

	// Created entities first, so the entity's links never name a missing
	// file. One written is on disk for good: it leaves the draft, so a retry
	// does not refuse it as existing, and a failure after it names it.
	var written []string
	partial := func(what string, err error) (notice, bool) {
		text := fmt.Sprintf("%s: %v", what, err)
		if len(written) > 0 {
			text += " · already written: " + strings.Join(written, ", ")
		}
		return failed(text)
	}
	for len(d.Created) > 0 {
		c := d.Created[0]
		if err := writeAtomic(s.worldPath(c.File), c.Content, false); err != nil {
			return partial(c.Name, err)
		}
		written = append(written, path.Join(index.WorldDir, c.File))
		d.Created = d.Created[1:]
	}
	// D10: a file is written only when its bytes change.
	if !bytes.Equal(out, disk) {
		if err := writeAtomic(s.worldPath(d.File), out, true); err != nil {
			return partial(name, err)
		}
	}

	var findings world.Findings
	err = s.withBuildLock(func() error {
		var err error
		findings, err = s.rebuild()
		return err
	})
	switch {
	case errors.Is(err, errBuildLocked):
		// The files are saved; the next read sees them changed and rebuilds.
		return notice{Text: "Saved · not checked yet: another save was rebuilding the world", Tone: toneError}, true
	case err != nil:
		return notice{Text: "Saved · the world did not build: " + err.Error(), Tone: toneError}, true
	case findings.Errors() > 0:
		return notice{Text: "Saved · the world has " + plural(findings.Errors(), "error", "errors"), Tone: toneError}, true
	}
	return notice{Text: "Saved", Tone: tonePass}, true
}

// draftName is the entity's name as it was on disk when the draft began,
// for a notice. IDs are never shown on an entity page.
func draftName(d *Draft) string {
	doc, _ := world.Parse(d.File, d.Base)
	if doc.Entity != nil && strings.TrimSpace(doc.Entity.Name) != "" {
		return doc.Entity.Name
	}
	return "This entity"
}

// validCreatedFile reports whether a created entity's world-relative path
// stays inside world/ and names a Markdown file.
func validCreatedFile(rel string) bool {
	return filepath.IsLocal(filepath.FromSlash(rel)) && strings.HasSuffix(rel, ".md")
}

// writeAtomic writes data to target through a temporary file in the same
// folder, renamed over target once written and closed, so a reader sees the
// old file or the new one and never half of one. With replace unset it
// refuses a target that exists, and creates the folder if needed. On failure
// the temporary file is removed.
func writeAtomic(target string, data []byte, replace bool) (err error) {
	dir := filepath.Dir(target)
	if !replace {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
			if err == nil {
				err = fs.ErrExist
			}
			return err
		}
	}
	f, err := os.CreateTemp(dir, ".lorekeep-save-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// The build lock is a file in build/ that every writer of the world takes
// around its rebuild: the editor's save now, and proposal Accept (S4).
//
// The rules: the lock is created exclusively and holds a token unique to
// its holder. A writer that finds it waits, retrying every buildLockPoll,
// for up to buildLockWait. A lock whose file is older than buildLockStale
// was left by a lorekeep that stopped before removing it, since a rebuild
// takes seconds; it is taken over, but only if it still holds the contents
// read before the age check, so two writers never both take over one lock.
// A holder removes the lock only while it still holds its own token.
const (
	buildLockFile  = "editor.lock"
	buildLockWait  = 5 * time.Second
	buildLockPoll  = 50 * time.Millisecond
	buildLockStale = 2 * time.Minute
)

// errBuildLocked is a rebuild that waited buildLockWait for the lock.
var errBuildLocked = errors.New("another save is rebuilding the world")

// withBuildLock runs fn holding build/editor.lock, creating build/ if it is
// missing, and removes the lock after fn, whatever fn returns.
func (s *Server) withBuildLock(fn func() error) error {
	dir := filepath.Join(s.repo, index.BuildDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lock := filepath.Join(dir, buildLockFile)
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	token := []byte(fmt.Sprintf("lorekeep pid %d token %s since %s\n",
		os.Getpid(), hex.EncodeToString(nonce), time.Now().Format(time.RFC3339)))

	deadline := time.Now().Add(buildLockWait)
	for {
		err := createLock(lock, token)
		if err == nil {
			break
		}
		switch {
		case errors.Is(err, fs.ErrExist):
			if takeOverStale(lock) && time.Now().Before(deadline) {
				continue
			}
		case errors.Is(err, fs.ErrPermission):
			// Windows denies a create while a removed lock is still
			// delete-pending (an indexer or a scanner holds it open).
		default:
			return fmt.Errorf("taking the build lock: %w", err)
		}
		if time.Now().After(deadline) {
			if errors.Is(err, fs.ErrPermission) {
				return fmt.Errorf("taking the build lock: %w", err)
			}
			return errBuildLocked
		}
		time.Sleep(buildLockPoll)
	}
	defer removeOwnLock(lock, token)
	return fn()
}

// createLock creates the lock exclusively and writes the token into it.
func createLock(lock string, token []byte) error {
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(token)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(lock)
		return werr
	}
	return nil
}

// takeOverStale removes a stale lock and reports whether it did. A lock that
// cannot be read, is not a file, is not stale yet, changed since it was
// read, or cannot be removed is not taken over.
func takeOverStale(lock string) bool {
	held, err := os.ReadFile(lock)
	if err != nil {
		return false
	}
	info, err := os.Stat(lock)
	if err != nil || !info.Mode().IsRegular() || time.Since(info.ModTime()) <= buildLockStale {
		return false
	}
	if now, err := os.ReadFile(lock); err != nil || !bytes.Equal(now, held) {
		return false
	}
	return os.Remove(lock) == nil
}

// removeOwnLock removes the lock if it still holds token: a lock taken over
// as stale while fn ran belongs to its new holder.
func removeOwnLock(lock string, token []byte) {
	if held, err := os.ReadFile(lock); err == nil && bytes.Equal(held, token) {
		_ = os.Remove(lock)
	}
}
