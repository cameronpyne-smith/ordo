package server

import (
	"net/http"
	"testing"

	"github.com/cameronpyne-smith/ordo/internal/api"
)

func TestCreateRecurring(t *testing.T) {
	h, _ := newTestServer(t)

	rec := request(t, h, http.MethodPost, "/tasks", api.CreateRequest{
		Title: "Put the bins out", RecurKind: "every", RecurRule: "weekly on tue",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d, want 201: %s", rec.Code, rec.Body)
	}
	var created api.Task
	decodeInto(t, rec, &created)
	if created.Recur == nil || created.Recur.Rule != "weekly on tue" {
		t.Fatalf("recurrence did not survive the wire: %+v", created.Recur)
	}
	if created.Due == "" {
		t.Fatal("a recurring task should come back with a due date")
	}

	done := request(t, h, http.MethodPost, "/tasks/1/done", api.DoneRequest{})
	if done.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200: %s", done.Code, done.Body)
	}
	var after api.Task
	decodeInto(t, done, &after)
	if after.Status != "open" {
		t.Fatalf("status = %q, want open", after.Status)
	}
	if after.Due <= created.Due {
		t.Fatalf("due went from %s to %s, want it to advance", created.Due, after.Due)
	}
}

func TestCreateRejectsBadRule(t *testing.T) {
	h, _ := newTestServer(t)

	rec := request(t, h, http.MethodPost, "/tasks", api.CreateRequest{
		Title: "Bins", RecurKind: "every", RecurRule: "weekly on funday",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestEditRecurrence(t *testing.T) {
	h, _ := newTestServer(t)

	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Bins"})

	kind, rule := "every", "daily"
	rec := request(t, h, http.MethodPost, "/tasks/1/edit", api.EditRequest{RecurKind: &kind, RecurRule: &rule})
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200: %s", rec.Code, rec.Body)
	}
	var edited api.Task
	decodeInto(t, rec, &edited)
	if edited.Recur == nil || edited.Recur.Kind != "every" {
		t.Fatalf("recurrence not set: %+v", edited.Recur)
	}

	none, empty := "", ""
	rec = request(t, h, http.MethodPost, "/tasks/1/edit", api.EditRequest{RecurKind: &none, RecurRule: &empty})
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200: %s", rec.Code, rec.Body)
	}
	var cleared api.Task
	decodeInto(t, rec, &cleared)
	if cleared.Recur != nil {
		t.Fatalf("recurrence not cleared: %+v", cleared.Recur)
	}
}

func TestListRecurringOnly(t *testing.T) {
	h, _ := newTestServer(t)

	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "One off"})
	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{
		Title: "Bins", RecurKind: "every", RecurRule: "weekly on tue",
	})

	rec := request(t, h, http.MethodGet, "/tasks?recurring=true", nil)
	var list api.ListResponse
	decodeInto(t, rec, &list)
	if list.Count != 1 || list.Tasks[0].Title != "Bins" {
		t.Fatalf("got %+v, want only the recurring task", list.Tasks)
	}
}
