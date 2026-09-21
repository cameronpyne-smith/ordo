package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/mnemo"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

// Enqueuer is how the server asks for a task to be read by the model. It is
// an interface, and may be nil, so the daemon runs the same with enrichment
// switched off as with it on.
type Enqueuer interface {
	Queue(id int64)
}

// Options is how the daemon is assembled. Enrichment and the vault are both
// optional: with either left out every other route behaves exactly the same,
// which is what makes "ollama is down" and "mnemo is down" survivable.
type Options struct {
	Store  *store.Store
	Token  string
	Enrich Enqueuer
	Vault  *mnemo.Client
	Log    *slog.Logger
}

type Server struct {
	store  *store.Store
	token  string
	enrich Enqueuer
	vault  *mnemo.Client
	log    *slog.Logger
}

func New(opts Options) http.Handler {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s := &Server{store: opts.Store, token: opts.Token, enrich: opts.Enrich, vault: opts.Vault, log: log}
	mux := http.NewServeMux()
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
	tasks, err := s.store.List(f)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	out := api.FromTasks(tasks)
	// The linked views are the ones whose whole point is the links, so they
	// are the ones that pay to check them. A plain list stays one read.
	if f.Linked || f.Note != "" {
		s.markOrphans(r.Context(), out)
	}
	writeJSON(w, http.StatusOK, api.ListResponse{Tasks: out, Count: len(out)})
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req api.CreateRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	slug, title, err := s.resolveLink(r.Context(), req.MnemoSlug)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	t, err := s.store.Create(&store.Task{
		Title:           req.Title,
		Notes:           req.Notes,
		Difficulty:      store.Difficulty(req.Difficulty),
		Priority:        store.Priority(req.Priority),
		EstimateMinutes: req.EstimateMinutes,
		Due:             req.Due,
		RecurKind:       store.RecurKind(req.RecurKind),
		RecurRule:       req.RecurRule,
		MnemoSlug:       slug,
		MnemoTitle:      title,
	})
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.queue(t.ID)
	writeJSON(w, http.StatusCreated, api.FromTask(t))
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	id, err := taskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.store.Get(id)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, api.FromTask(t))
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
	before, err := s.store.Get(id)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	edit := req.Edit()
	t, err := s.store.Edit(id, edit)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	if edit.TitleChanged(before.Title) {
		s.queue(t.ID)
	}
	writeJSON(w, http.StatusOK, api.FromTask(t))
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
	t, err := s.store.Done(id, req.Minutes)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, api.FromTask(t))
}

func (s *Server) handleUndo(w http.ResponseWriter, r *http.Request) {
	id, err := taskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.store.Undo(id)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, api.FromTask(t))
}

// handleEnrich puts a task back in front of the model on demand, which is the
// way out of a bad extraction: clear the field that is wrong and ask again.
func (s *Server) handleEnrich(w http.ResponseWriter, r *http.Request) {
	id, err := taskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if s.enrich == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("enrichment is off: set [ollama] url and model on the daemon"))
		return
	}
	t, err := s.store.Get(id)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.queue(t.ID)
	writeJSON(w, http.StatusAccepted, api.FromTask(t))
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := taskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.Delete(id); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, api.DeleteResponse{ID: id, Deleted: true})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	sum, err := s.store.Summary()
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, api.StatusResponse{
		Open:       sum.Open,
		Done:       sum.Done,
		Overdue:    sum.Overdue,
		Unenriched: sum.Unenriched,
		Today:      store.Today(),
	})
}

// queue is nil-safe because enrichment is optional: with no model configured
// every surface still works, tasks just stay as they were typed.
func (s *Server) queue(id int64) {
	if s.enrich != nil {
		s.enrich.Queue(id)
	}
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
	if errors.Is(err, errNoVault) {
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
