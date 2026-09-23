package server

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/cameronpyne-smith/ordo/internal/api"
)

// A session over HTTP leaves the task open with what is left, and a
// recurring task is a bad request rather than a server error.
func TestWorkLogsASessionAndRefusesARecurringTask(t *testing.T) {
	h, _, _ := newTestServer(t)

	var report api.Task
	decodeInto(t, request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Write the report", EstimateMinutes: 300}), &report)
	rec := request(t, h, http.MethodPost, "/tasks/"+strconv.FormatInt(report.ID, 10)+"/work", api.WorkRequest{Minutes: 60})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var after api.Task
	decodeInto(t, rec, &after)
	if after.Status != "open" || after.RemainingMinutes != 240 || after.EstimateMinutes != 300 {
		t.Fatalf("task = %+v, want open with 240 left", after)
	}

	var bins api.Task
	decodeInto(t, request(t, h, http.MethodPost, "/tasks", api.CreateRequest{
		Title: "Bins", RecurKind: "every", RecurRule: "weekly on tue", EstimateMinutes: 90,
	}), &bins)
	rec = request(t, h, http.MethodPost, "/tasks/"+strconv.FormatInt(bins.ID, 10)+"/work", api.WorkRequest{Minutes: 30})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a recurring task", rec.Code)
	}
}
