package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/mnemo"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

const testToken = "secret"

// queueSpy stands in for the enrichment worker so the tests can see which
// requests ask for a task to be read.
type queueSpy struct {
	ids []int64
}

func (q *queueSpy) Queue(id int64) { q.ids = append(q.ids, id) }

func newTestServer(t *testing.T) (http.Handler, *store.Store, *queueSpy) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ordo.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	spy := &queueSpy{}
	return New(Options{Store: st, Token: testToken, Enrich: spy}), st, spy
}

// newLinkedServer wires the handler to a stub vault, so the tests exercise
// the real mnemo client over a real connection rather than a fake of it.
func newLinkedServer(t *testing.T, vault http.HandlerFunc) (http.Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ordo.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv := httptest.NewServer(vault)
	t.Cleanup(srv.Close)
	return New(Options{Store: st, Token: testToken, Vault: mnemo.New(srv.URL, "")}), st
}

func request(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encoding body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeInto(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decoding response %s: %v", rec.Body.String(), err)
	}
}

func TestAuthRequired(t *testing.T) {
	h, _, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d for a wrong token, want 401", rec.Code)
	}
}

func TestCreateAndList(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Put the bins out", Difficulty: "low"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	var created api.Task
	decodeInto(t, rec, &created)
	if created.ID == 0 || created.Title != "Put the bins out" || created.Enriched {
		t.Fatalf("created = %+v", created)
	}

	rec = request(t, h, http.MethodGet, "/tasks", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var list api.ListResponse
	decodeInto(t, rec, &list)
	if list.Count != 1 || list.Tasks[0].ID != created.ID {
		t.Fatalf("list = %+v", list)
	}
}

func TestCreateValidationIsBadRequest(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "x", Difficulty: "trivial"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
	var failure api.ErrorResponse
	decodeInto(t, rec, &failure)
	if failure.Error == "" {
		t.Fatal("expected the error to name the problem")
	}
}

func TestGetMissingIsNotFound(t *testing.T) {
	h, _, _ := newTestServer(t)
	if rec := request(t, h, http.MethodGet, "/tasks/404", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGetNonNumericIdIsBadRequest(t *testing.T) {
	h, _, _ := newTestServer(t)
	if rec := request(t, h, http.MethodGet, "/tasks/abc", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestEditOnlyTouchesGivenFields(t *testing.T) {
	h, _, _ := newTestServer(t)
	rec := request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Draft CV", Due: "2026-09-25"})
	var created api.Task
	decodeInto(t, rec, &created)

	priority := "high"
	rec = request(t, h, http.MethodPost, "/tasks/"+itoa(created.ID)+"/edit", api.EditRequest{Priority: &priority})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var edited api.Task
	decodeInto(t, rec, &edited)
	if edited.Priority != "high" || edited.Due != "2026-09-25" || edited.Title != "Draft CV" {
		t.Fatalf("edited = %+v", edited)
	}
}

func TestDoneThenUndo(t *testing.T) {
	h, _, _ := newTestServer(t)
	rec := request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Draft CV"})
	var created api.Task
	decodeInto(t, rec, &created)

	rec = request(t, h, http.MethodPost, "/tasks/"+itoa(created.ID)+"/done", api.DoneRequest{Minutes: 25})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var done api.Task
	decodeInto(t, rec, &done)
	if done.Status != "done" || done.DoneAt == "" {
		t.Fatalf("done = %+v", done)
	}

	rec = request(t, h, http.MethodPost, "/tasks/"+itoa(created.ID)+"/undo", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var reopened api.Task
	decodeInto(t, rec, &reopened)
	if reopened.Status != "open" || reopened.DoneAt != "" {
		t.Fatalf("reopened = %+v", reopened)
	}
}

func TestDelete(t *testing.T) {
	h, _, _ := newTestServer(t)
	rec := request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Draft CV"})
	var created api.Task
	decodeInto(t, rec, &created)

	if rec := request(t, h, http.MethodDelete, "/tasks/"+itoa(created.ID), nil); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec := request(t, h, http.MethodGet, "/tasks/"+itoa(created.ID), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d after delete, want 404", rec.Code)
	}
}

func TestStatusCounts(t *testing.T) {
	h, _, _ := newTestServer(t)
	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "One"})

	rec := request(t, h, http.MethodGet, "/status", nil)
	var status api.StatusResponse
	decodeInto(t, rec, &status)
	if status.Open != 1 || status.Today == "" {
		t.Fatalf("status = %+v", status)
	}
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }
