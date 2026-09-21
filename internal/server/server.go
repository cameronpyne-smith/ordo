// Package server is the HTTP face of the daemon. It parses requests, calls
// the todo service, and turns its errors into status codes; the behaviour
// itself lives in that service so the MCP tools get the same one.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/mnemo"
	"github.com/cameronpyne-smith/ordo/internal/store"
	"github.com/cameronpyne-smith/ordo/internal/todo"
)

// Options is how the listener is assembled. MCP may be left out, in which
// case the daemon serves the HTTP API alone.
type Options struct {
	Todo  *todo.Service
	Token string
	MCP   http.Handler
}

type Server struct {
	todo  *todo.Service
	token string
}

func New(opts Options) http.Handler {
	s := &Server{todo: opts.Todo, token: opts.Token}
	mux := http.NewServeMux()
	// The MCP mount sits behind the same bearer token as everything else,
	// because it is the same daemon on the same tailnet port.
	if opts.MCP != nil {
		mux.Handle("/mcp", opts.MCP)
	}
	mux.HandleFunc("GET /tasks", s.handleList)
	mux.HandleFunc("POST /tasks", s.handleCreate)
	mux.HandleFunc("GET /tasks/{id}", s.handleGet)
	mux.HandleFunc("POST /tasks/{id}/edit", s.handleEdit)
	mux.HandleFunc("POST /tasks/{id}/done", s.handleDone)
	mux.HandleFunc("POST /tasks/{id}/undo", s.handleUndo)
	mux.HandleFunc("POST /tasks/{id}/enrich", s.handleEnrich)
	mux.HandleFunc("POST /tasks/{id}/link", s.handleLink)
	mux.HandleFunc("POST /tasks/{id}/unlink", s.handleUnlink)
	mux.HandleFunc("GET /tasks/{id}/related", s.handleRelated)
	mux.HandleFunc("DELETE /tasks/{id}", s.handleDelete)
	mux.HandleFunc("POST /tasks/{id}/pin", s.handlePin)
	mux.HandleFunc("POST /tasks/{id}/unpin", s.handleUnpin)
	mux.HandleFunc("GET /today", s.handleToday)
	mux.HandleFunc("GET /preferences", s.handlePreferences)
	mux.HandleFunc("POST /preferences", s.handleSetPreferences)
	mux.HandleFunc("GET /status", s.handleStatus)
	return s.auth(mux)
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
				writeError(w, http.StatusUnauthorized, errors.New("invalid or missing bearer token"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.Filter{
		Status:     store.Status(q.Get("status")),
		All:        q.Get("status") == "all",
		Difficulty: store.Difficulty(q.Get("difficulty")),
		Priority:   store.Priority(q.Get("priority")),
		Overdue:    q.Get("overdue") == "true",
		Linked:     q.Get("linked") == "true",
		Note:       q.Get("note"),
		Recurring:  q.Get("recurring") == "true",
	}
	if f.All {
		f.Status = ""
	}
	if limit, err := strconv.Atoi(q.Get("limit")); err == nil {
		f.Limit = limit
	}
	resp, err := s.todo.List(r.Context(), f)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req api.CreateRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.todo.Create(r.Context(), req)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	id, err := taskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.todo.Get(id)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleEdit(w http.ResponseWriter, r *http.Request) {
	id, err := taskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req api.EditRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.todo.Edit(id, req)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleDone(w http.ResponseWriter, r *http.Request) {
	id, err := taskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req api.DoneRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.todo.Done(id, req.Minutes)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleUndo(w http.ResponseWriter, r *http.Request) {
	id, err := taskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.todo.Undo(id)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleEnrich(w http.ResponseWriter, r *http.Request) {
	id, err := taskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.todo.Enrich(id)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusAccepted, t)
}

func (s *Server) handleLink(w http.ResponseWriter, r *http.Request) {
	id, err := taskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req api.LinkRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.todo.Link(r.Context(), id, req.Slug)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleUnlink(w http.ResponseWriter, r *http.Request) {
	id, err := taskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.todo.Unlink(id)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleRelated(w http.ResponseWriter, r *http.Request) {
	id, err := taskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.todo.Related(r.Context(), id)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := taskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.todo.Delete(id); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, api.DeleteResponse{ID: id, Deleted: true})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp, err := s.todo.Status()
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func taskID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, errors.New("task id must be a number")
	}
	return id, nil
}

// decode tolerates an empty body so requests whose fields are all optional
// can be sent without one.
func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return errors.New("invalid JSON body: " + err.Error())
	}
	return nil
}

func statusFor(err error) int {
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, mnemo.ErrNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, store.ErrInvalid) {
		return http.StatusBadRequest
	}
	if errors.Is(err, todo.ErrNoVault) || errors.Is(err, todo.ErrNoModel) {
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, api.ErrorResponse{Error: err.Error()})
}
