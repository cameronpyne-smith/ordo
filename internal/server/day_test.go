package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

func TestPreferencesRoundTripOverHTTP(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := request(t, h, http.MethodGet, "/preferences", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got api.Preferences
	decodeInto(t, rec, &got)
	if got.DayStart != "07:00" {
		t.Fatalf("day_start = %q, want the default", got.DayStart)
	}

	end := "23:59"
	rec = request(t, h, http.MethodPost, "/preferences", api.PreferencesRequest{DayEnd: &end})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	decodeInto(t, rec, &got)
	if got.DayEnd != "23:59" {
		t.Fatalf("preferences = %+v", got)
	}
	if got.DayStart != "07:00" {
		t.Errorf("day_start = %q, want the untouched default", got.DayStart)
	}
}

func TestPreferencesRejectTheIncoherent(t *testing.T) {
	h, _, _ := newTestServer(t)

	bad := "not a time"
	rec := request(t, h, http.MethodPost, "/preferences", api.PreferencesRequest{DayStart: &bad})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	rec = request(t, h, http.MethodPost, "/preferences", api.PreferencesRequest{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty request: status = %d, want 400", rec.Code)
	}
}

func TestTodayOverHTTP(t *testing.T) {
	h, st, _ := newTestServer(t)

	if _, err := st.Create(&store.Task{Title: "Send the CV", Due: store.Today(), EstimateMinutes: 40}); err != nil {
		t.Fatal(err)
	}
	rec := request(t, h, http.MethodGet, "/today", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var plan api.TodayResponse
	decodeInto(t, rec, &plan)
	if plan.Date != store.Today() {
		t.Fatalf("date = %q", plan.Date)
	}
	if len(plan.Blocks) != 1 || plan.Blocks[0].Minutes != 40 {
		t.Fatalf("blocks = %+v", plan.Blocks)
	}
	if plan.Blocks[0].Reason == "" {
		t.Error("a block with no reason defeats the point of the view")
	}
}

func TestTodayTakesADayParameter(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := request(t, h, http.MethodGet, "/today?day=2026-11-03", nil)
	var plan api.TodayResponse
	decodeInto(t, rec, &plan)
	if plan.Date != "2026-11-03" {
		t.Fatalf("date = %q", plan.Date)
	}
	rec = request(t, h, http.MethodGet, "/today?day=nonsense", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestPinAndUnpinOverHTTP(t *testing.T) {
	h, st, _ := newTestServer(t)

	task, err := st.Create(&store.Task{Title: "Rewrite the pricing model"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/tasks/" + itoa(task.ID)

	rec := request(t, h, http.MethodPost, path+"/pin", api.PinRequest{Day: "2026-10-05"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var got api.Task
	decodeInto(t, rec, &got)
	if got.PinnedOn != "2026-10-05" {
		t.Fatalf("pinned_on = %q", got.PinnedOn)
	}

	// A fresh value, because pinned_on is omitempty: decoding a cleared task
	// over the old one would leave the stale date sitting there.
	rec = request(t, h, http.MethodPost, path+"/unpin", nil)
	var cleared api.Task
	decodeInto(t, rec, &cleared)
	if cleared.PinnedOn != "" {
		t.Fatalf("pinned_on = %q, want it cleared", cleared.PinnedOn)
	}
}

func TestPinMapsItsErrors(t *testing.T) {
	h, st, _ := newTestServer(t)

	rec := request(t, h, http.MethodPost, "/tasks/999/pin", api.PinRequest{})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	task, err := st.Create(&store.Task{Title: "Open"})
	if err != nil {
		t.Fatal(err)
	}
	rec = request(t, h, http.MethodPost, "/tasks/"+itoa(task.ID)+"/pin", api.PinRequest{Day: "tomorrow"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "YYYY-MM-DD") {
		t.Errorf("body = %s, want it to say the format", rec.Body)
	}
}
