package server

import (
	"net/http"
	"testing"

	"github.com/cameronpyne-smith/ordo/internal/api"
)

// Tags travel with the task, narrow a listing alongside the other filters,
// and are counted at /tags.
func TestTagsRoundTrip(t *testing.T) {
	h, _, _ := newTestServer(t)
	rec := request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Paint the living room", Tags: []string{"House"}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var paint api.Task
	decodeInto(t, rec, &paint)
	if len(paint.Tags) != 1 || paint.Tags[0] != "house" {
		t.Fatalf("tags = %v, want the tag lower case", paint.Tags)
	}
	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Send the CV", Tags: []string{"career"}})

	if rec = request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "x", Tags: []string{"no good"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("a bad tag: status = %d, want 400", rec.Code)
	}

	rec = request(t, h, http.MethodGet, "/tasks?tag=house", nil)
	var list api.ListResponse
	decodeInto(t, rec, &list)
	if list.Count != 1 || list.Tasks[0].ID != paint.ID {
		t.Fatalf("tag=house listed %+v", list)
	}

	tags := []string{"house", "reno"}
	rec = request(t, h, http.MethodPost, "/tasks/"+itoa(paint.ID)+"/edit", api.EditRequest{Tags: &tags})
	var edited api.Task
	decodeInto(t, rec, &edited)
	if len(edited.Tags) != 2 || edited.Tags[1] != "reno" {
		t.Fatalf("edited tags = %v", edited.Tags)
	}

	rec = request(t, h, http.MethodGet, "/tags", nil)
	var counts api.TagsResponse
	decodeInto(t, rec, &counts)
	if len(counts.Tags) != 3 || counts.Tags[1].Tag != "house" || counts.Tags[1].Open != 1 {
		t.Fatalf("tags = %+v", counts.Tags)
	}
}
