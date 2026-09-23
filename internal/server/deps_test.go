package server

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/cameronpyne-smith/ordo/internal/api"
)

// Dependencies and start dates go over the wire both ways: set on create
// and on edit, read back with both ends named, and a loop is a bad request
// that says where it goes round.
func TestDependenciesOverHTTP(t *testing.T) {
	h, _, _ := newTestServer(t)

	var read, rerate api.Task
	decodeInto(t, request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Read AFML chapter 11", EstimateMinutes: 150}), &read)
	decodeInto(t, request(t, h, http.MethodPost, "/tasks", api.CreateRequest{
		Title: "Re-rate the skills matrix", Due: "2099-10-15", Start: "2099-10-01", EstimateMinutes: 90,
		BlockedBy: []int64{read.ID},
	}), &rerate)
	if !rerate.Blocked || len(rerate.BlockedBy) != 1 || rerate.BlockedBy[0].Title != "Read AFML chapter 11" || rerate.Start != "2099-10-01" {
		t.Fatalf("task = %+v, want it created waiting on the reading from the 1st", rerate)
	}

	var got api.Task
	decodeInto(t, request(t, h, http.MethodGet, "/tasks/"+strconv.FormatInt(read.ID, 10), nil), &got)
	if got.EffectiveDue != "2099-10-13" || got.DueFor != rerate.ID || len(got.Blocks) != 1 {
		t.Fatalf("blocker = %+v, want it due 2099-10-13 for the re-rate", got)
	}

	loop := []int64{rerate.ID}
	rec := request(t, h, http.MethodPost, "/tasks/"+strconv.FormatInt(read.ID, 10)+"/edit", api.EditRequest{BlockedBy: &loop})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "loop") {
		t.Fatalf("status = %d: %s, want a loop refused", rec.Code, rec.Body.String())
	}

	none := []int64{}
	decodeInto(t, request(t, h, http.MethodPost, "/tasks/"+strconv.FormatInt(rerate.ID, 10)+"/edit", api.EditRequest{BlockedBy: &none}), &got)
	if got.Blocked || len(got.BlockedBy) != 0 {
		t.Fatalf("task = %+v, want an empty list to clear it", got)
	}
}
