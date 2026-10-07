package editor

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/a-h/templ"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/gmreyer/lorekeep/internal/resolve"
	"github.com/gmreyer/lorekeep/internal/schema"
	"github.com/gmreyer/lorekeep/internal/world"
)

// Server is one editor over one project.
type Server struct {
	repo  string
	token string
	// host is the Host every request must carry: the listener's address.
	host string
	mux  *http.ServeMux

	// mu guards the build state below. A reload takes it for writing; a read
	// holds it for reading for its whole length.
	mu sync.RWMutex
	// stamp is the latest modification time under schema/ and world/ when
	// the last build started.
	stamp time.Time
	// cur is the last good build; nil until one succeeds.
	cur *loaded
	// findings are the last build's findings, good or failed.
	findings world.Findings
	// buildErr says why the last build could not run at all; empty otherwise.
	buildErr string

	drafts drafts
	prefs  *prefs
}

// loaded is one good build: the index, the resolver over it, and the merged
// schema pack the world was built against.
type loaded struct {
	idx  *index.Index
	res  *resolve.Resolver
	pack *schema.Pack
}

// Request headers and parameters of the token check.
const (
	tokenParam = "t"
	// tokenCookie starts the cookie's name, which ends with the port:
	// cookies are kept per host, not per port, so two editors open at once
	// must not overwrite each other's.
	tokenCookie = "lorekeep_token_"
	// tokenHeader is sent by htmx on every request, from the hx-headers the
	// layout puts on <body>.
	tokenHeader = "X-Lorekeep-Token"
)

// New builds the world under cfg.Repo and returns an editor over it. A world
// with errors still opens: the editor shows the findings, and the last good
// build once there is one.
func New(cfg Config) (*Server, error) {
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		return nil, err
	}
	p, err := loadPrefs(cfg.Settings)
	if err != nil {
		return nil, err
	}
	s := &Server{
		repo:   cfg.Repo,
		token:  hex.EncodeToString(tok),
		drafts: drafts{m: map[string]*Draft{}},
		prefs:  p,
	}
	if err := s.reload(true); err != nil {
		return nil, err
	}
	s.mux = http.NewServeMux()
	s.routes()
	return s, nil
}

// routes registers every handler. Each task's file registers its own routes
// in the function named for it; this table is the only place they meet.
func (s *Server) routes() {
	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err) // the embed pattern names the directory
	}
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	s.mux.HandleFunc("GET /{$}", s.home)
	s.mux.HandleFunc("GET /tabs", s.tabStrip)
	s.mux.HandleFunc("GET /status", s.statusChips)
	s.keyRoutes()
	s.entityRoutes()   // entity.go: the entity page
	s.fieldRoutes()    // fields.go: the fields form
	s.relationRoutes() // relations.go: relations panel, picker, create
	s.saveRoutes()     // save.go: save, rebuild, validate
}

// Handler is the editor behind its Host and token checks.
func (s *Server) Handler() http.Handler { return s.guard(s.mux) }

// guard admits a request only from the editor's own pages.
//
// The Host must be the listener's address, so a page on another origin that
// resolves its own name to 127.0.0.1 (DNS rebinding) is refused. Every page
// needs the token cookie, set by the URL lorekeep printed; the cookie is
// SameSite=Strict, so no other site's page can send it. A request that
// changes anything must also carry the token in a header, which only the
// editor's own pages know to send.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No page may frame the editor (a click on it could be a click on
		// Save), and the browser runs no script but the editor's own files:
		// the no-inline-script rule, enforced.
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", contentPolicy)
		if r.Host != s.host {
			http.Error(w, "lorekeep: wrong host", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/" && r.URL.Query().Has(tokenParam) {
			if !s.tokenOK(r.URL.Query().Get(tokenParam)) {
				http.Error(w, "lorekeep: this link is from an earlier run; open the one lorekeep edit printed", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{
				Name: s.cookieName(), Value: s.token, Path: "/",
				HttpOnly: true, SameSite: http.SameSiteStrictMode,
			})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if isStatic(r) {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(s.cookieName())
		if err != nil || !s.tokenOK(c.Value) {
			http.Error(w, "lorekeep: open the editor from the link lorekeep edit printed", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !s.tokenOK(r.Header.Get(tokenHeader)) {
			http.Error(w, "lorekeep: missing token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// contentPolicy allows scripts, styles, fonts and requests from the editor
// itself only. Inline style attributes stay allowed: htmx sets them while
// swapping.
const contentPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

// Unsaved lists the entities with unsaved changes, by ID.
func (s *Server) Unsaved() []string {
	var out []string
	for id, dirty := range s.snapshot().dirty {
		if dirty {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// cookieName is the token cookie of this editor's port.
func (s *Server) cookieName() string {
	_, port, _ := net.SplitHostPort(s.host)
	return tokenCookie + port
}

func isStatic(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		len(r.URL.Path) > len("/static/") && r.URL.Path[:len("/static/")] == "/static/"
}

func (s *Server) tokenOK(v string) bool {
	return subtle.ConstantTimeCompare([]byte(v), []byte(s.token)) == 1
}

// reload rebuilds the world when a file under schema/ or world/ changed since
// the last build, or always when force is set. It writes build/, as lorekeep
// build does, so search reads the same index. A failed build keeps the last
// good one and records why. The caller holds s.mu for writing, or is New.
func (s *Server) reload(force bool) error {
	stamp, err := latestMtime(filepath.Join(s.repo, index.SchemaDir), filepath.Join(s.repo, index.WorldDir))
	if err != nil {
		return err
	}
	if !force && stamp.Equal(s.stamp) {
		return nil
	}
	s.stamp = stamp

	res, err := index.Build(s.repo, filepath.Join(s.repo, index.BuildDir))
	if err != nil {
		s.buildErr, s.findings = err.Error(), nil
		return nil
	}
	s.buildErr, s.findings = "", res.Findings
	if res.Index == nil {
		return nil
	}
	pack, err := schema.LoadProject(filepath.Join(s.repo, index.SchemaDir))
	if err != nil {
		// The build just loaded the same pack, so this is a file changing
		// under it; the next request rebuilds.
		s.buildErr = err.Error()
		s.stamp = time.Time{}
		return nil
	}
	s.cur = &loaded{idx: res.Index, res: resolve.New(res.Index), pack: pack}
	return nil
}

// rebuild rebuilds the world now, whatever its stamp, and returns the
// findings. It is the end of every save.
func (s *Server) rebuild() (world.Findings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(true); err != nil {
		return nil, err
	}
	if s.buildErr != "" {
		return nil, errors.New(s.buildErr)
	}
	return s.findings, nil
}

// errNoBuild is a page asked for before any build of the world has
// succeeded.
var errNoBuild = errors.New("the world has not built yet")

// read runs fn against the current build, rebuilding first if a file changed
// on disk. fn runs under the read lock and reads through cur.res only, with
// the context readContext gives it.
func (s *Server) read(fn func(cur *loaded) error) error {
	s.mu.Lock()
	err := s.reload(false)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cur == nil {
		return errNoBuild
	}
	return fn(s.cur)
}

// readContext is the one Context the editor reads with: the single author
// role, omniscient at internal visibility, every status (a writer must be
// able to open a draft or a deprecated entity to work on it), under the
// worldline in the user's settings.
func (s *Server) readContext() resolve.Context {
	return resolve.Omniscient(resolve.Worldline(s.prefs.worldline()),
		resolve.WithVisibility(world.VisibilityInternal),
		resolve.WithStatuses(world.StatusCanon, world.StatusDraft, world.StatusDeprecated, world.StatusNonCanon))
}

// render writes a component as the response.
func render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil && !errors.Is(err, context.Canceled) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// fail answers a request that could not be served. A missing build is shown
// as the findings that prevent it.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, errNoBuild):
		code = http.StatusServiceUnavailable
	case errors.Is(err, resolve.ErrUnknownEntity):
		code = http.StatusNotFound
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_ = errorPage(code, err.Error(), s.problemLines()).Render(r.Context(), w)
}

// problemLines are the last build's errors, for an error page.
func (s *Server) problemLines() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	if s.buildErr != "" {
		out = append(out, s.buildErr)
	}
	for _, f := range s.findings {
		if f.Severity == world.SeverityError {
			located := f
			located.File = filepath.ToSlash(filepath.Join(index.WorldDir, f.File))
			out = append(out, located.String())
		}
	}
	return out
}

// badRequest answers a malformed request.
func badRequest(w http.ResponseWriter, format string, args ...any) {
	http.Error(w, fmt.Sprintf(format, args...), http.StatusBadRequest)
}

// latestMtime returns the latest modification time of any file or directory
// under the roots. Directories count, so a removed file is a change. A root
// that does not exist is skipped; the build reports it. It is the MCP
// server's change check, kept here so the two packages stay independent.
func latestMtime(roots ...string) (time.Time, error) {
	var latest time.Time
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if path == root && errors.Is(err, fs.ErrNotExist) {
					return fs.SkipDir
				}
				return err
			}
			info, err := d.Info()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil // removed while walking; its directory moved too
				}
				return err
			}
			if t := info.ModTime(); t.After(latest) {
				latest = t
			}
			return nil
		})
		if err != nil {
			return time.Time{}, fmt.Errorf("checking for changes: %w", err)
		}
	}
	return latest, nil
}
