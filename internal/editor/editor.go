// Package editor is the local web editor: lorekeep edit serves it on
// 127.0.0.1 and opens it in the browser.
//
// Pages are templ components rendered on the server, and htmx swaps the
// fragments; the only JavaScript is static/keys.js. All state lives here: the
// compiled index and a resolver over it, the per-user settings, and the
// unsaved edits to each open entity. The open editor tabs are URL state.
//
// Every read goes through internal/resolve, under the one Context the server
// builds (readContext). Writes go to world/ only, through world.EditFrontmatter
// and world.ReplaceBody, and each save rebuilds and revalidates the world.
package editor

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// Config is what an editor is launched with.
type Config struct {
	// Repo is the project directory, holding schema/ and world/.
	Repo string
	// Settings is the per-user settings file. Empty means editor.json in the
	// lorekeep folder of the user's cache directory, beside the release cache.
	Settings string
}

// Serve serves the editor on ln until ctx is done. ln must listen on
// 127.0.0.1; every request must name it as its Host.
func Serve(ctx context.Context, cfg Config, ln net.Listener) error {
	s, err := New(cfg)
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}

// Serve serves s on ln until ctx is done.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.host = ln.Addr().String()
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shut)
		case <-done:
		}
	}()
	err := srv.Serve(ln)
	close(done)
	if errors.Is(err, http.ErrServerClosed) {
		return ctx.Err()
	}
	return err
}

// OpenURL is the address on ln that admits a browser: it carries the per-run
// token, which the first load turns into a cookie.
func (s *Server) OpenURL(ln net.Listener) string {
	return "http://" + ln.Addr().String() + "/?" + tokenParam + "=" + s.token
}
