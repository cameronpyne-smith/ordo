package store

import (
	"errors"
	"testing"
)

func intp(n int) *int { return &n }

// A session leaves the task open with what is left, and the estimate stays
// the first guess.
func TestWorkLeavesTheRest(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	report := create(t, st, &Task{Title: "Write the report", EstimateMinutes: 300})

	got, err := st.Work(report.ID, 60, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusOpen || got.RemainingMinutes != 240 || got.EstimateMinutes != 300 || got.DoneAt != nil {
		t.Fatalf("task = %+v, want open with 240 left of an estimate still 300", got)
	}
	got, err = st.Work(report.ID, 60, intp(250))
	if err != nil {
		t.Fatal(err)
	}
	if got.RemainingMinutes != 250 {
		t.Fatalf("remaining = %d, want the 250 said rather than 180 worked out", got.RemainingMinutes)
	}
	history, err := st.Completions(report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || !history[0].Partial || history[0].Left != 250 || history[0].Minutes != 60 {
		t.Fatalf("history = %+v, want two partial sessions, the latest leaving 250", history)
	}
}

// Undo takes sessions back one at a time, to what was there before each,
// including a number typed by hand in between.
func TestUndoPutsBackWhatWasLeft(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	report := create(t, st, &Task{Title: "Write the report", EstimateMinutes: 300})

	if _, err := st.Work(report.ID, 60, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Edit(report.ID, Edit{Remaining: intp(400)}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Work(report.ID, 60, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int{400, 0} {
		got, err := st.Undo(report.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.RemainingMinutes != want || got.Status != StatusOpen {
			t.Fatalf("after undo: remaining = %d, status = %s; want %d and open", got.RemainingMinutes, got.Status, want)
		}
	}
	if _, err := st.Undo(report.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want nothing left to undo", err)
	}
}

// Nothing left is finished: a completion like any other, and undoing it
// reopens the task with what it had.
func TestWorkWithNothingLeftFinishes(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	report := create(t, st, &Task{Title: "Write the report", EstimateMinutes: 300})
	if _, err := st.Work(report.ID, 200, nil); err != nil {
		t.Fatal(err)
	}

	got, err := st.Work(report.ID, 100, intp(0))
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusDone || got.DoneAt == nil {
		t.Fatalf("task = %+v, want done", got)
	}
	history, _ := st.Completions(report.ID)
	if len(history) != 2 || history[0].Partial || history[0].Minutes != 100 {
		t.Fatalf("history = %+v, want the last row a completion of 100 minutes", history)
	}
	back, err := st.Undo(report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if back.Status != StatusOpen || back.RemainingMinutes != 100 || back.DoneAt != nil {
		t.Fatalf("task = %+v, want open again with 100 left", back)
	}
}

// Undoing a completion looks past sessions for the done_at to restore, since
// a session never finished anything.
func TestUndoingACompletionLooksPastSessions(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	report := create(t, st, &Task{Title: "Write the report", EstimateMinutes: 300})
	if _, err := st.Work(report.ID, 60, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Done(report.ID, 30); err != nil {
		t.Fatal(err)
	}
	back, err := st.Undo(report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if back.DoneAt != nil {
		t.Fatalf("done_at = %v, want none: the session before it finished nothing", back.DoneAt)
	}
}

func TestWorkIsRefusedWhereItMeansNothing(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	bins := create(t, st, &Task{Title: "Bins", RecurKind: RecurEvery, RecurRule: "weekly on tue", EstimateMinutes: 90})
	vague := create(t, st, &Task{Title: "Sort the loft"})
	finished := create(t, st, &Task{Title: "Send the CV", EstimateMinutes: 90})
	if _, err := st.Done(finished.ID, 0); err != nil {
		t.Fatal(err)
	}

	for name, id := range map[string]int64{"recurring": bins.ID, "no estimate": vague.ID, "done": finished.ID} {
		if _, err := st.Work(id, 30, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if got, err := st.Work(vague.ID, 30, intp(120)); err != nil || got.RemainingMinutes != 120 {
		t.Errorf("no estimate but left given: %+v, %v; want 120 left", got, err)
	}
}

// An occurrence of a recurring task is done in one go: setting what is left
// on one is refused, and a task that becomes recurring drops what it had.
func TestRecurringTasksHaveNothingLeft(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	bins := create(t, st, &Task{Title: "Bins", RecurKind: RecurEvery, RecurRule: "weekly on tue"})
	if _, err := st.Edit(bins.ID, Edit{Remaining: intp(30)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}

	report := create(t, st, &Task{Title: "Write the report", EstimateMinutes: 300})
	if _, err := st.Work(report.ID, 60, nil); err != nil {
		t.Fatal(err)
	}
	kind, rule := RecurEvery, "weekly on fri"
	got, err := st.Edit(report.ID, Edit{RecurKind: &kind, RecurRule: &rule})
	if err != nil {
		t.Fatal(err)
	}
	if got.RemainingMinutes != 0 {
		t.Fatalf("remaining = %d, want it dropped", got.RemainingMinutes)
	}
}

// A quick win is about time, whatever the difficulty, and the filter and
// the rule agree on every case.
func TestQuickWinsAreAboutTime(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	cases := []struct {
		task  Task
		quick bool
	}{
		{Task{Title: "hard but short", Difficulty: DifficultyHigh, EstimateMinutes: 10}, true},
		{Task{Title: "easy, no estimate", Difficulty: DifficultyLow}, true},
		{Task{Title: "medium, no estimate", Difficulty: DifficultyMedium}, false},
		{Task{Title: "nothing known"}, false},
		{Task{Title: "easy but an evening", Difficulty: DifficultyLow, EstimateMinutes: 120}, false},
		{Task{Title: "nearly finished", Difficulty: DifficultyHigh, EstimateMinutes: 300, RemainingMinutes: 10}, true},
		{Task{Title: "bigger than thought", EstimateMinutes: 20, RemainingMinutes: 90}, false},
	}
	want := map[int64]bool{}
	for _, c := range cases {
		remaining := c.task.RemainingMinutes
		c.task.RemainingMinutes = 0
		created := create(t, st, &c.task)
		if remaining > 0 {
			if _, err := st.Edit(created.ID, Edit{Remaining: &remaining}); err != nil {
				t.Fatal(err)
			}
		}
		want[created.ID] = c.quick
		if got := QuickWin(c.task.Difficulty, c.task.EstimateMinutes, remaining); got != c.quick {
			t.Errorf("QuickWin(%s) = %v, want %v", c.task.Title, got, c.quick)
		}
	}
	listed, err := st.List(Filter{Quick: true})
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]bool{}
	for _, task := range listed {
		got[task.ID] = true
	}
	for id, quick := range want {
		if got[id] != quick {
			t.Errorf("task %d: listed as quick = %v, want %v", id, got[id], quick)
		}
	}
}
