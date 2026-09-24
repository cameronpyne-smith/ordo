package tui

import (
	"strings"
	"testing"

	"github.com/cameronpyne-smith/ordo/internal/api"
)

func update(t *testing.T, m Model, msg any) Model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(Model)
}

// A finished task stays ticked where it was, out of reach of the keys, while
// what the daemon sends back waits; once it has gone the cursor takes the
// row that slid into its place.
func TestAFinishedTaskIsSeenOff(t *testing.T) {
	space, sofa, bins := task(6, "Make space in office"), task(1, "Move sofa"), task(4, "Bins")
	m := listOf(nil, space, sofa, bins)
	m.width, m.height = 100, 20
	if got, _ := m.selected(); got.ID != 6 {
		t.Fatalf("selected %d, want to start on the space", got.ID)
	}

	m = update(t, m, actedMsg{note: "done — can start now: 1 Move sofa", finished: 6})
	if view := m.View(); !strings.Contains(view, "✓   6") || !strings.Contains(view, "can start now: 1 Move sofa") {
		t.Errorf("want the row ticked and the note said:\n%s", view)
	}
	if _, ok := m.selected(); ok {
		t.Fatal("want the leaving row out of reach of the keys")
	}
	after := tasksMsg{resp: &api.ListResponse{Tasks: []api.Task{sofa, bins}, Today: &api.Tally{Done: 1, Minutes: 40}}}
	m = m.applyTasks(after)
	if len(shownIDs(m)) != 3 {
		t.Fatalf("shown %v, want the list held while the row lingers", shownIDs(m))
	}

	next, cmd := m.Update(leftMsg{})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("want the list fetched once the row has gone")
	}
	m = m.applyTasks(after)
	if got, ok := m.selected(); !ok || got.ID != 1 {
		t.Fatalf("selected %d, want the sofa that slid into its place", got.ID)
	}
	if view := m.View(); !strings.Contains(view, "1 done · 40 min today") {
		t.Errorf("want today's tally in the header:\n%s", view)
	}
}

// A repeating task moves on to its next date instead of leaving, and the
// cursor stays where it was rather than following it down the list.
func TestTheCursorDoesNotFollowARepeatingTaskOnward(t *testing.T) {
	daily := func(x *api.Task) { x.Recur = &api.Recur{Kind: "every", Rule: "daily"} }
	drill := task(3, "Drill", due("2099-01-01"), daily)
	m := listOf(nil, drill, task(1, "Read", due("2099-01-02")), task(2, "Write", due("2099-01-03")))
	m = update(t, m, actedMsg{note: "done", finished: 3})
	m = update(t, m, leftMsg{})
	moved := task(3, "Drill", due("2099-01-05"), daily)
	m = m.applyTasks(tasksMsg{resp: &api.ListResponse{Tasks: []api.Task{task(1, "Read", due("2099-01-02")), task(2, "Write", due("2099-01-03")), moved}}})
	if got, _ := m.selected(); got.ID != 1 {
		t.Fatalf("selected %d, want the task that took the drill's place", got.ID)
	}
}

// The day shows what is already done under what is left, and the tally with
// what is still planned.
func TestTheDayShowsWhatIsDone(t *testing.T) {
	m := New(nil)
	m.mode, m.width, m.height = modeDay, 100, 30
	m.day = &api.TodayResponse{
		Date:           "2026-09-24",
		Blocks:         []api.Block{{Task: task(1, "Move sofa"), Start: "14:00", End: "15:00", Minutes: 60}},
		PlannedMinutes: 60, BudgetMinutes: 240,
		Tally: &api.Tally{Done: 1, Minutes: 70},
		Done: []api.Logged{
			{ID: 6, Title: "Make space in office", At: "10:42", Minutes: 40},
			{ID: 3, Title: "Read AFML chapter 7", At: "11:30", Minutes: 30, Partial: true},
		},
	}
	view := m.View()
	for _, want := range []string{"1 done · 1h10 · 60 of 240 min still planned", "done today",
		"✓ 10:42", "Make space in office", "worked 30 min"} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q\n%s", want, view)
		}
	}
	m, _ = press(t, m, "j")
	if _, ok := m.dayCursorTask(); ok {
		t.Fatal("want d and w to leave a done row alone")
	}
	if !strings.Contains(m.View(), "done at 10:42 · 40 min") {
		t.Errorf("want the line under the day to say when it was done:\n%s", m.View())
	}
}
