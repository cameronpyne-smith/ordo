package store

import (
	"errors"
	"strings"
	"testing"
)

func waitOn(t *testing.T, st *Store, id int64, blockers ...int64) *Task {
	t.Helper()
	got, err := st.Edit(id, Edit{BlockedBy: &blockers})
	if err != nil {
		t.Fatalf("making %d wait on %v: %v", id, blockers, err)
	}
	return got
}

func get(t *testing.T, st *Store, id int64) *Task {
	t.Helper()
	got, err := st.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A task is held up for exactly as long as what it waits on is open: done
// releases it, and undoing that holds it up again.
func TestWaitingLastsAsLongAsTheBlocker(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	read := create(t, st, &Task{Title: "Read AFML chapter 11"})
	rerate := create(t, st, &Task{Title: "Re-rate the skills matrix"})

	got := waitOn(t, st, rerate.ID, read.ID)
	if !got.Blocked() || len(got.BlockedBy) != 1 || got.BlockedBy[0].Title != "Read AFML chapter 11" {
		t.Fatalf("task = %+v, want it blocked by the reading, named", got)
	}
	if b := get(t, st, read.ID); len(b.Blocks) != 1 || b.Blocks[0].ID != rerate.ID {
		t.Fatalf("blocks = %+v, want the re-rate", b.Blocks)
	}
	if _, err := st.Done(read.ID, 0); err != nil {
		t.Fatal(err)
	}
	if got := get(t, st, rerate.ID); got.Blocked() || !got.BlockedBy[0].Done {
		t.Fatalf("task = %+v, want it released with the blocker ticked", got)
	}
	if _, err := st.Undo(read.ID); err != nil {
		t.Fatal(err)
	}
	if got := get(t, st, rerate.ID); !got.Blocked() {
		t.Fatal("want the task held up again once the blocker is reopened")
	}
}

func TestBlockersThatCannotBe(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	a := create(t, st, &Task{Title: "a"})
	b := create(t, st, &Task{Title: "b"})
	c := create(t, st, &Task{Title: "c"})
	daily := create(t, st, &Task{Title: "drill", RecurKind: RecurEvery, RecurRule: "daily"})
	finished := create(t, st, &Task{Title: "finished"})
	if _, err := st.Done(finished.ID, 0); err != nil {
		t.Fatal(err)
	}
	waitOn(t, st, b.ID, a.ID)
	waitOn(t, st, c.ID, b.ID)

	for _, tc := range []struct {
		name     string
		id       int64
		blockers []int64
		want     string
	}{
		{"itself", a.ID, []int64{a.ID}, "itself"},
		{"a repeating task", a.ID, []int64{daily.ID}, "never finished"},
		{"something already done", a.ID, []int64{finished.ID}, "already done"},
		{"no such task", a.ID, []int64{999}, "no such task"},
		{"a loop", a.ID, []int64{c.ID}, "1 → 3 → 2 → 1"},
	} {
		blockers := tc.blockers
		_, err := st.Edit(tc.id, Edit{BlockedBy: &blockers})
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it refused mentioning %q", tc.name, err, tc.want)
		}
	}
	if got := get(t, st, a.ID); len(got.BlockedBy) != 0 {
		t.Fatalf("blocked by = %+v, want refused lists to leave nothing behind", got.BlockedBy)
	}
}

// A blocker already done stays when the list is saved again, so undoing it
// still holds the task up; only a new one has to be open.
func TestADoneBlockerCanBeKept(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	a := create(t, st, &Task{Title: "a"})
	b := create(t, st, &Task{Title: "b"})
	c := create(t, st, &Task{Title: "c"})
	waitOn(t, st, c.ID, a.ID)
	if _, err := st.Done(a.ID, 0); err != nil {
		t.Fatal(err)
	}
	got := waitOn(t, st, c.ID, a.ID, b.ID)
	if len(got.BlockedBy) != 2 {
		t.Fatalf("blocked by = %+v, want both kept", got.BlockedBy)
	}
	if got := waitOn(t, st, c.ID); len(got.BlockedBy) != 0 {
		t.Fatalf("blocked by = %+v, want an empty list to clear it", got.BlockedBy)
	}
}

func TestDeletingABlockerReleasesWhatItHeldUp(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	a := create(t, st, &Task{Title: "a"})
	b := create(t, st, &Task{Title: "b"})
	waitOn(t, st, b.ID, a.ID)
	if err := st.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	if got := get(t, st, b.ID); got.Blocked() || len(got.BlockedBy) != 0 {
		t.Fatalf("task = %+v, want it released", got)
	}
}

func TestATaskWaitedOnCannotStartRepeating(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	a := create(t, st, &Task{Title: "a"})
	b := create(t, st, &Task{Title: "b"})
	waitOn(t, st, b.ID, a.ID)
	kind, rule := RecurEvery, "daily"
	_, err := st.Edit(a.ID, Edit{RecurKind: &kind, RecurRule: &rule})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "waited on by 2") {
		t.Fatalf("err = %v, want it refused naming what waits on it", err)
	}
}

// A repeating task can wait on a one-off: the drill that needs the book.
func TestARepeatingTaskCanWait(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	book := create(t, st, &Task{Title: "Buy the Green Book"})
	drill := create(t, st, &Task{Title: "Green Book drill", RecurKind: RecurEvery, RecurRule: "daily",
		BlockedBy: []Dep{{ID: book.ID}}})
	if !drill.Blocked() {
		t.Fatalf("drill = %+v, want it created waiting on the book", drill)
	}
}

// The blockers of a dated task have to be done in time for it: its deadline
// less the days it needs at a sitting a day, passed down a whole chain, with
// a blocker's own earlier date still winning.
func TestDeadlinesPassDownToBlockers(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	download := create(t, st, &Task{Title: "Download the papers", EstimateMinutes: 15})
	read := create(t, st, &Task{Title: "Read AFML chapter 11", EstimateMinutes: 150})
	early := create(t, st, &Task{Title: "Read CASI", Due: "2026-10-01", EstimateMinutes: 90})
	rerate := create(t, st, &Task{Title: "Re-rate", Due: "2026-10-15", EstimateMinutes: 90})
	waitOn(t, st, rerate.ID, read.ID, early.ID)
	waitOn(t, st, read.ID, download.ID)

	for _, tc := range []struct {
		id    int64
		date  string
		dueOf int64
	}{
		{read.ID, "2026-10-13", rerate.ID},
		{download.ID, "2026-10-10", read.ID},
		{early.ID, "", 0},
		{rerate.ID, "", 0},
	} {
		got := get(t, st, tc.id)
		if got.EffectiveDue != tc.date || got.DueFor != tc.dueOf {
			t.Errorf("%s: effective due = %q for %d, want %q for %d",
				got.Title, got.EffectiveDue, got.DueFor, tc.date, tc.dueOf)
		}
	}

	list, err := st.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, task := range list {
		order = append(order, task.Title)
	}
	want := "Read CASI|Download the papers|Read AFML chapter 11|Re-rate"
	if strings.Join(order, "|") != want {
		t.Fatalf("order = %v, want the blockers sorted by the deadline they were passed", order)
	}
}

func TestQuickWinsAndOverdueKnowAboutWaiting(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	blocker := create(t, st, &Task{Title: "blocker", EstimateMinutes: 60})
	blocked := create(t, st, &Task{Title: "blocked", EstimateMinutes: 10, Due: "2026-09-19",
		BlockedBy: []Dep{{ID: blocker.ID}}})
	create(t, st, &Task{Title: "later", EstimateMinutes: 10, Start: "2026-09-25"})
	create(t, st, &Task{Title: "free", EstimateMinutes: 10})

	quick, err := st.List(Filter{Quick: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(quick) != 1 || quick[0].Title != "free" {
		t.Fatalf("quick wins = %v, want only the one that can be started", titles(quick))
	}
	overdue, err := st.List(Filter{Overdue: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(overdue) != 2 || overdue[0].ID != blocker.ID || overdue[1].ID != blocked.ID {
		t.Fatalf("overdue = %v, want the blocker it passed its date to, then the blocked task", titles(overdue))
	}
}

func titles(tasks []*Task) []string {
	var out []string
	for _, t := range tasks {
		out = append(out, t.Title)
	}
	return out
}

func TestStartDates(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	later := create(t, st, &Task{Title: "Re-rate", Start: "2026-10-15", Due: "2026-10-20"})
	if !later.Waiting("2026-10-14") || later.Waiting("2026-10-15") {
		t.Fatalf("task = %+v, want it waiting only before its start", later)
	}

	for _, tc := range []struct {
		name string
		task *Task
		want string
	}{
		{"after the deadline", &Task{Title: "x", Start: "2026-10-21", Due: "2026-10-20"}, "after the due date"},
		{"on a repeating task", &Task{Title: "x", Start: "2026-10-01", RecurKind: RecurEvery, RecurRule: "daily"}, "repeating"},
		{"not a date", &Task{Title: "x", Start: "soon"}, "YYYY-MM-DD"},
	} {
		if _, err := st.Create(tc.task); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it refused mentioning %q", tc.name, err, tc.want)
		}
	}

	// Becoming a repeating task drops the start the way it drops what was
	// left, rather than refusing the schedule.
	kind, rule := RecurEvery, "weekly on mon"
	got, err := st.Edit(later.ID, Edit{RecurKind: &kind, RecurRule: &rule})
	if err != nil {
		t.Fatal(err)
	}
	if got.Start != "" {
		t.Fatalf("start = %q, want it dropped once the task repeats", got.Start)
	}
}

// A start is taken from the sentence only where it could have been typed.
func TestEnrichStart(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	plain := create(t, st, &Task{Title: "From monday, clear the garage"})
	dated := create(t, st, &Task{Title: "x", Due: "2026-09-22"})
	started := create(t, st, &Task{Title: "y", Start: "2026-09-30"})

	if got, _ := st.Enrich(plain.ID, Inference{Start: "2026-09-21"}); got.Start != "2026-09-21" {
		t.Errorf("start = %q, want the inferred one", got.Start)
	}
	if got, _ := st.Enrich(dated.ID, Inference{Start: "2026-09-25"}); got.Start != "" {
		t.Errorf("start = %q, want a start after the deadline dropped", got.Start)
	}
	got, err := st.Enrich(started.ID, Inference{Start: "2026-09-21", RecurKind: RecurEvery, RecurRule: "daily"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Start != "2026-09-30" || got.Recurring() {
		t.Errorf("task = %+v, want the typed start kept and the guessed schedule dropped", got)
	}
}
