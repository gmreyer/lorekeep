package editor

import "net/http"

// Task 4 owns this file: save → rebuild → validate.

func (s *Server) saveRoutes() {
	s.mux.HandleFunc("POST /entity/{id}/save", s.save)
}

// save writes the entity's draft. Until Task 4 it refuses.
func (s *Server) save(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "saving arrives with Task 4", http.StatusNotImplemented)
}
