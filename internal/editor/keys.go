package editor

import (
	"errors"
	"net/http"
)

func (s *Server) keyRoutes() {
	s.mux.HandleFunc("GET /settings/keys", s.keysPage)
	s.mux.HandleFunc("POST /settings/keys", s.bindKey)
	s.mux.HandleFunc("POST /settings/keys/reset", s.resetKeys)
}

func (s *Server) keysPage(w http.ResponseWriter, r *http.Request) {
	p, err := s.page(s.tabsFrom(r, ""), "Keyboard shortcuts", keysView(s.prefs.bindings(), "", ""), nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	render(w, r, layout(p))
}

// bindKey sets one action's keys. A refused combination comes back as the
// list with the reason on its row; a saved one reloads the page, so keys.js
// reads the new key map.
func (s *Server) bindKey(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, "%v", err)
		return
	}
	action := r.PostForm.Get("action")
	err := s.prefs.bind(action, r.PostForm.Get("keys"))
	var refused errBinding
	switch {
	case errors.As(err, &refused):
		render(w, r, keysView(s.prefs.bindings(), action, refused.msg))
	case err != nil:
		s.fail(w, r, err)
	default:
		w.Header().Set("HX-Redirect", "/settings/keys")
	}
}

func (s *Server) resetKeys(w http.ResponseWriter, r *http.Request) {
	if err := s.prefs.resetKeys(); err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/keys")
}
