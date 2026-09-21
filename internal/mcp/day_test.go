package mcp

import (
	"strings"
	"testing"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

func ptrBool(b bool) *bool { return &b }

// Read before write is the shape the tool description promises, so an empty
// call has to be a read rather than an error or a no-op write.
func TestPreferencesReadsWhenGivenNothing(t *testing.T) {
	sess, _ := newSession(t, nil)

	got := call[api.Preferences](t, sess, "todo_preferences", PrefsArgs{})
	want := store.DefaultPreferences()
	if got.WorkStart != want.WorkStart.String() || got.MaxMinutesDay != want.MaxMinutesDay {
		t.Fatalf("preferences = %+v, want the defaults", got)
	}
}

func TestPreferencesChangesOnlyWhatItIsGiven(t *testing.T) {
	sess, _ := newSession(t, nil)

	deep := "08:00"
	end := "09:00"
	got := call[api.Preferences](t, sess, "todo_preferences", PrefsArgs{DeepStart: &deep, DeepEnd: &end})
	if got.DeepStart != "08:00" || got.DeepEnd != "09:00" {
		t.Fatalf("deep window = %s-%s, want it moved", got.DeepStart, got.DeepEnd)
	}
	// Everything not mentioned has to survive, or "move deep work to
	// mornings" would quietly reset the working week.
	if got.WorkStart != "09:00" || got.WorkDays != "mon,tue,wed,thu,fri" {
		t.Errorf("preferences = %+v, want the rest untouched", got)
	}
}

// A deep-work window outside the usable day could never be honoured, so it
// is refused rather than silently ignored, and the message says so.
func TestPreferencesRefuseDeepWorkOutsideTheDay(t *testing.T) {
	sess, _ := newSession(t, nil)

	deep := "06:00"
	msg := callErr(t, sess, "todo_preferences", PrefsArgs{DeepStart: &deep})
	if !strings.Contains(msg, "inside the day") {
		t.Fatalf("error = %q, want it to explain the clash", msg)
	}
}

// A day that cannot exist is refused whole rather than half-written, which
// is why the request is applied over the stored row before validating.
func TestPreferencesRefuseAnIncoherentDay(t *testing.T) {
	sess, _ := newSession(t, nil)

	start := "23:00"
	if msg := callErr(t, sess, "todo_preferences", PrefsArgs{DayStart: &start}); !strings.Contains(msg, "day_start") {
		t.Fatalf("error = %q, want it to name the field", msg)
	}
	after := call[api.Preferences](t, sess, "todo_preferences", PrefsArgs{})
	if after.DayStart != "07:00" {
		t.Fatalf("day_start = %s, want the refused change not to have landed", after.DayStart)
	}
}

func TestTodayPlansTheDay(t *testing.T) {
	sess, _ := newSession(t, nil)

	call[api.Task](t, sess, "todo_add", AddArgs{Title: "Water the plants", Due: store.Today(), EstimateMinutes: 20})
	call[api.Task](t, sess, "todo_add", AddArgs{Title: "Send the CV", Due: store.Today(), EstimateMinutes: 45})

	plan := call[api.TodayResponse](t, sess, "todo_today", TodayArgs{})
	if plan.Date != store.Today() {
		t.Fatalf("date = %q, want today", plan.Date)
	}
	if len(plan.Blocks) != 2 {
		t.Fatalf("blocks = %+v, want both tasks placed", plan.Blocks)
	}
	if plan.PlannedMinutes != 65 {
		t.Errorf("planned = %d, want 65", plan.PlannedMinutes)
	}
	for _, b := range plan.Blocks {
		if b.Reason == "" {
			t.Errorf("block %q has no reason", b.Task.Title)
		}
		if b.Start == "" || b.End == "" {
			t.Errorf("block %q has no time", b.Task.Title)
		}
	}
	// No feed configured is a normal state and has to be visible as one.
	if plan.Calendar {
		t.Error("no calendar is configured, so the plan must not claim one")
	}
}

func TestTodayTakesADay(t *testing.T) {
	sess, _ := newSession(t, nil)

	plan := call[api.TodayResponse](t, sess, "todo_today", TodayArgs{Day: "2026-12-25"})
	if plan.Date != "2026-12-25" {
		t.Fatalf("date = %q", plan.Date)
	}
	if msg := callErr(t, sess, "todo_today", TodayArgs{Day: "25-12-2026"}); !strings.Contains(msg, "YYYY-MM-DD") {
		t.Errorf("error = %q, want it to say the format", msg)
	}
}

func TestPinLiftsATaskToTheFrontOfTheDay(t *testing.T) {
	sess, _ := newSession(t, nil)

	call[api.Task](t, sess, "todo_add", AddArgs{Title: "Overdue and urgent",
		Due: "2026-01-01", Priority: "high", EstimateMinutes: 30})
	later := call[api.Task](t, sess, "todo_add", AddArgs{Title: "The one I said I would do",
		EstimateMinutes: 30})

	pinned := call[api.Task](t, sess, "todo_pin", PinArgs{ID: later.ID})
	if pinned.PinnedOn != store.Today() {
		t.Fatalf("pinned_on = %q, want today by default", pinned.PinnedOn)
	}
	plan := call[api.TodayResponse](t, sess, "todo_today", TodayArgs{})
	if plan.Blocks[0].Task.ID != later.ID {
		t.Fatalf("first block is %d, want the pinned task", plan.Blocks[0].Task.ID)
	}
	if !strings.Contains(plan.Blocks[0].Reason, "pinned") {
		t.Errorf("reason = %q, want it to say why", plan.Blocks[0].Reason)
	}
}

// Pinning and unpinning are one tool, the way linking and unlinking are.
func TestPinAndUnpinAreOneTool(t *testing.T) {
	sess, _ := newSession(t, nil)

	task := call[api.Task](t, sess, "todo_add", AddArgs{Title: "Book the MOT"})
	pinned := call[api.Task](t, sess, "todo_pin", PinArgs{ID: task.ID, Day: "2026-10-02"})
	if pinned.PinnedOn != "2026-10-02" {
		t.Fatalf("pinned_on = %q", pinned.PinnedOn)
	}
	released := call[api.Task](t, sess, "todo_pin", PinArgs{ID: task.ID, Pinned: ptrBool(false)})
	if released.PinnedOn != "" {
		t.Fatalf("pinned_on = %q, want it released", released.PinnedOn)
	}
}

func TestPinRefusesAMissingTask(t *testing.T) {
	sess, _ := newSession(t, nil)
	if msg := callErr(t, sess, "todo_pin", PinArgs{ID: 404}); !strings.Contains(msg, "not found") {
		t.Fatalf("error = %q", msg)
	}
}

// A task that did not fit has to be explained, because "why is that not in
// my day" is the first thing anyone asks a scheduler.
func TestTodayExplainsWhatDidNotFit(t *testing.T) {
	sess, _ := newSession(t, nil)

	cap := 30
	call[api.Preferences](t, sess, "todo_preferences", PrefsArgs{MaxMinutesDay: &cap})
	call[api.Task](t, sess, "todo_add", AddArgs{Title: "Fits", EstimateMinutes: 30})
	call[api.Task](t, sess, "todo_add", AddArgs{Title: "Does not", EstimateMinutes: 30})

	plan := call[api.TodayResponse](t, sess, "todo_today", TodayArgs{})
	if len(plan.Blocks) != 1 {
		t.Fatalf("blocks = %+v, want one", plan.Blocks)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].Reason == "" {
		t.Fatalf("skipped = %+v, want the second task explained", plan.Skipped)
	}
}
