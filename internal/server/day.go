package server

import (
	"net/http"

	"github.com/cameronpyne-smith/ordo/internal/api"
)

func (s *Server) handlePin(w http.ResponseWriter, r *http.Request) {
	id, err := taskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req api.PinRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.todo.Pin(id, req.Day)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleUnpin(w http.ResponseWriter, r *http.Request) {
	id, err := taskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.todo.Unpin(id)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleToday(w http.ResponseWriter, r *http.Request) {
	resp, err := s.todo.Today(r.Context(), r.URL.Query().Get("day"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePreferences(w http.ResponseWriter, r *http.Request) {
	p, err := s.todo.Preferences()
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleSetPreferences(w http.ResponseWriter, r *http.Request) {
	var req api.PreferencesRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	p, err := s.todo.SetPreferences(req)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}
