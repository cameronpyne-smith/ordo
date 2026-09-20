package store

import (
	"errors"
	"testing"
)

func TestRecurringTaskGetsItsFirstDue(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z") // a Monday
	st := open(t)

	bins := create(t, st, &Task{Title: "Put the bins out", RecurKind: RecurEvery, RecurRule: "Weekly on TUE"})
	if bins.Due != "2026-09-22" {
		t.Fatalf("due = %q, want the coming Tuesday", bins.Due)
	}
	if bins.RecurRule != "weekly on tue" {
		t.Fatalf("rule = %q, want the canonical spelling", bins.RecurRule)
	}

	water := create(t, st, &Task{Title: "Water the plants", RecurKind: RecurAfter, RecurRule: "3d"})
	if water.Due != "2026-09-21" {
		t.Fatalf("due = %q, want today: an interval task is on the list from the start", water.Due)
	}
}

func TestRecurringTaskKeepsAnExplicitDue(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	st := open(t)

	bins := create(t, st, &Task{Title: "Bins", Due: "2026-10-06", RecurKind: RecurEvery, RecurRule: "weekly on tue"})
	if bins.Due != "2026-10-06" {
		t.Fatalf("due = %q, want the date given", bins.Due)
	}
}

func TestCreateRejectsBadRule(t *testing.T) {
	st := open(t)
	cases := []*Task{
		{Title: "no rule", RecurKind: RecurEvery},
		{Title: "bad day", RecurKind: RecurEvery, RecurRule: "weekly on funday"},
		{Title: "bad kind", RecurKind: "sometimes", RecurRule: "daily"},
		{Title: "rule without kind", RecurRule: "daily"},
		{Title: "bad interval", RecurKind: RecurAfter, RecurRule: "soon"},
	}
	for _, task := range cases {
		if _, err := st.Create(task); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", task.Title, err)
		}
	}
}

func TestDoneAdvancesEvery(t *testing.T) {
	fixedNow(t, "2026-09-22T20:00:00Z") // the Tuesday it is due
	st := open(t)

	bins := create(t, st, &Task{Title: "Bins", RecurKind: RecurEvery, RecurRule: "weekly on tue"})
	done, err := st.Done(bins.ID, 5)
	if err != nil {
		t.Fatalf("completing: %v", err)
	}
	if done.Status != StatusOpen {
		t.Fatalf("status = %q, want open: a chore never finishes", done.Status)
	}
	if done.Due != "2026-09-29" {
		t.Fatalf("due = %q, want the following Tuesday", done.Due)
	}
	if done.DoneAt == nil {
		t.Fatal("done_at should record the last completion")
	}
	if done.ID != bins.ID {
		t.Fatalf("id changed from %d to %d", bins.ID, done.ID)
	}
}

// Doing Tuesday's chore on Monday evening should not make it due again
// tomorrow: the occurrence just completed is the one that advances.
func TestDoneEarlyAdvancesFromTheDueDate(t *testing.T) {
	fixedNow(t, "2026-09-21T20:00:00Z") // Monday
	st := open(t)

	bins := create(t, st, &Task{Title: "Bins", Due: "2026-09-22", RecurKind: RecurEvery, RecurRule: "weekly on tue"})
	done, err := st.Done(bins.ID, 0)
	if err != nil {
		t.Fatalf("completing: %v", err)
	}
	if done.Due != "2026-09-29" {
		t.Fatalf("due = %q, want a week on from the occurrence just done", done.Due)
	}
}

// Missed weeks must not queue up: a chore that nags about the past gets
// abandoned.
func TestDoneLateSkipsMissedOccurrences(t *testing.T) {
	fixedNow(t, "2026-10-15T09:00:00Z") // a Thursday, three weeks late
	st := open(t)

	bins := create(t, st, &Task{Title: "Bins", Due: "2026-09-22", RecurKind: RecurEvery, RecurRule: "weekly on tue"})
	done, err := st.Done(bins.ID, 0)
	if err != nil {
		t.Fatalf("completing: %v", err)
	}
	if done.Due != "2026-10-20" {
		t.Fatalf("due = %q, want the next Tuesday from today", done.Due)
	}
}

func TestDoneAdvancesAfterFromToday(t *testing.T) {
	fixedNow(t, "2026-09-25T09:00:00Z")
	st := open(t)

	water := create(t, st, &Task{Title: "Water the plants", Due: "2026-09-21", RecurKind: RecurAfter, RecurRule: "3d"})
	done, err := st.Done(water.ID, 0)
	if err != nil {
		t.Fatalf("completing: %v", err)
	}
	if done.Due != "2026-09-28" {
		t.Fatalf("due = %q, want three days from when it was actually done", done.Due)
	}
}

func TestRecurringCompletionsAccumulate(t *testing.T) {
	fixedNow(t, "2026-09-22T20:00:00Z")
	st := open(t)

	bins := create(t, st, &Task{Title: "Bins", RecurKind: RecurEvery, RecurRule: "weekly on tue"})
	if _, err := st.Done(bins.ID, 5); err != nil {
		t.Fatalf("completing: %v", err)
	}
	fixedNow(t, "2026-09-29T20:00:00Z")
	if _, err := st.Done(bins.ID, 7); err != nil {
		t.Fatalf("completing again: %v", err)
	}

	history, err := st.Completions(bins.ID)
	if err != nil {
		t.Fatalf("reading completions: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("got %d completions, want 2", len(history))
	}
	if history[0].Due != "2026-09-29" || history[1].Due != "2026-09-22" {
		t.Fatalf("completions lost the occurrence they closed: %+v", history)
	}
	if history[0].Minutes != 7 || history[1].Minutes != 5 {
		t.Fatalf("minutes not recorded: %+v", history)
	}
}

func TestUndoRestoresTheOccurrence(t *testing.T) {
	fixedNow(t, "2026-09-22T20:00:00Z")
	st := open(t)

	bins := create(t, st, &Task{Title: "Bins", RecurKind: RecurEvery, RecurRule: "weekly on tue"})
	if _, err := st.Done(bins.ID, 0); err != nil {
		t.Fatalf("completing: %v", err)
	}
	back, err := st.Undo(bins.ID)
	if err != nil {
		t.Fatalf("undoing: %v", err)
	}
	if back.Due != "2026-09-22" {
		t.Fatalf("due = %q, want the occurrence that was ticked by mistake", back.Due)
	}
	if back.DoneAt != nil {
		t.Fatal("done_at should be gone with the only completion")
	}
	history, err := st.Completions(bins.ID)
	if err != nil {
		t.Fatalf("reading completions: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("got %d completions, want none", len(history))
	}
}

// An unrelated edit must not wipe the last completion off a recurring task,
// which is always open and so would otherwise look never done.
func TestEditKeepsDoneAtOnRecurring(t *testing.T) {
	fixedNow(t, "2026-09-22T20:00:00Z")
	st := open(t)

	bins := create(t, st, &Task{Title: "Bins", RecurKind: RecurEvery, RecurRule: "weekly on tue"})
	if _, err := st.Done(bins.ID, 0); err != nil {
		t.Fatalf("completing: %v", err)
	}
	high := PriorityHigh
	edited, err := st.Edit(bins.ID, Edit{Priority: &high})
	if err != nil {
		t.Fatalf("editing: %v", err)
	}
	if edited.DoneAt == nil {
		t.Fatal("editing a recurring task erased its last completion")
	}
	if edited.Due != "2026-09-29" {
		t.Fatalf("due = %q, want the schedule left alone", edited.Due)
	}
}

func TestEditNewScheduleMovesTheDueDate(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z") // Monday
	st := open(t)

	bins := create(t, st, &Task{Title: "Bins", RecurKind: RecurEvery, RecurRule: "weekly on tue"})
	kind, rule := RecurEvery, "weekly on thu"
	edited, err := st.Edit(bins.ID, Edit{RecurKind: &kind, RecurRule: &rule})
	if err != nil {
		t.Fatalf("editing: %v", err)
	}
	if edited.Due != "2026-09-24" {
		t.Fatalf("due = %q, want the coming Thursday", edited.Due)
	}
}

func TestEditNewScheduleRespectsAnExplicitDue(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	st := open(t)

	bins := create(t, st, &Task{Title: "Bins", RecurKind: RecurEvery, RecurRule: "weekly on tue"})
	kind, rule, due := RecurEvery, "weekly on thu", "2026-10-01"
	edited, err := st.Edit(bins.ID, Edit{RecurKind: &kind, RecurRule: &rule, Due: &due})
	if err != nil {
		t.Fatalf("editing: %v", err)
	}
	if edited.Due != "2026-10-01" {
		t.Fatalf("due = %q, want the date given", edited.Due)
	}
}

func TestEditClearsRecurrence(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	st := open(t)

	bins := create(t, st, &Task{Title: "Bins", RecurKind: RecurEvery, RecurRule: "weekly on tue"})
	none, empty := RecurKind(""), ""
	edited, err := st.Edit(bins.ID, Edit{RecurKind: &none, RecurRule: &empty})
	if err != nil {
		t.Fatalf("editing: %v", err)
	}
	if edited.Recurring() {
		t.Fatalf("still recurring: %+v", edited)
	}
	if edited.Due != "2026-09-22" {
		t.Fatalf("due = %q, want the date it already had", edited.Due)
	}

	done, err := st.Done(edited.ID, 0)
	if err != nil {
		t.Fatalf("completing: %v", err)
	}
	if done.Status != StatusDone {
		t.Fatalf("status = %q, want done now it is a one-off", done.Status)
	}
}

func TestEditRejectsBadRule(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	st := open(t)

	bins := create(t, st, &Task{Title: "Bins", RecurKind: RecurEvery, RecurRule: "weekly on tue"})
	rule := "weekly on funday"
	if _, err := st.Edit(bins.ID, Edit{RecurRule: &rule}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	after, err := st.Get(bins.ID)
	if err != nil {
		t.Fatalf("getting task: %v", err)
	}
	if after.RecurRule != "weekly on tue" {
		t.Fatalf("a rejected edit changed the rule to %q", after.RecurRule)
	}
}
