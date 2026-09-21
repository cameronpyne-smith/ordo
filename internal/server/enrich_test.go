package server

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

func TestCreateQueuesEnrichment(t *testing.T) {
	h, _, spy := newTestServer(t)

	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Bins"})
	if len(spy.ids) != 1 || spy.ids[0] != 1 {
		t.Fatalf("queued %v, want the new task", spy.ids)
	}
}

func TestOnlyATitleEditQueuesEnrichment(t *testing.T) {
	h, _, spy := newTestServer(t)

	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Bins"})
	spy.ids = nil

	priority := "high"
	request(t, h, http.MethodPost, "/tasks/1/edit", api.EditRequest{Priority: &priority})
	if len(spy.ids) != 0 {
		t.Fatalf("queued %v, want nothing: a priority does not change what a task means", spy.ids)
	}

	same := "Bins"
	request(t, h, http.MethodPost, "/tasks/1/edit", api.EditRequest{Title: &same})
	if len(spy.ids) != 0 {
		t.Fatalf("queued %v, want nothing: the title did not actually change", spy.ids)
	}

	changed := "Put the bins out on friday"
	rec := request(t, h, http.MethodPost, "/tasks/1/edit", api.EditRequest{Title: &changed})
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200: %s", rec.Code, rec.Body)
	}
	if len(spy.ids) != 1 {
		t.Fatalf("queued %v, want the retitled task", spy.ids)
	}
	var edited api.Task
	decodeInto(t, rec, &edited)
	if edited.Enriched {
		t.Fatal("a retitled task should read as pending until the worker has been back")
	}
}

func TestEnrichEndpointQueues(t *testing.T) {
	h, _, spy := newTestServer(t)

	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Bins"})
	spy.ids = nil

	rec := request(t, h, http.MethodPost, "/tasks/1/enrich", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202: %s", rec.Code, rec.Body)
	}
	if len(spy.ids) != 1 || spy.ids[0] != 1 {
		t.Fatalf("queued %v, want the task", spy.ids)
	}

	if rec := request(t, h, http.MethodPost, "/tasks/99/enrich", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

func TestEnrichEndpointSaysWhenItIsOff(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "ordo.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	h := New(Options{Store: st, Token: testToken})

	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Bins"})
	rec := request(t, h, http.MethodPost, "/tasks/1/enrich", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503: %s", rec.Code, rec.Body)
	}
}
