package store

import (
	"errors"
	"fmt"
	"testing"
)

func done(t *testing.T, st *Store, id int64) *Task {
	t.Helper()
	got, err := st.Done(id, 0)
	if err != nil {
		t.Fatalf("completing %d: %v", id, err)
	}
	return got
}

func freedIDs(o *Outcome) []string {
	var out []string
	for _, f := range o.Freed {
		out = append(out, fmt.Sprintf("%d%s", f.ID, f.Start))
	}
	return out
}

// Finishing a blocker names what can start now, and when a start date still
// holds one back; a task still waiting on something else open is not freed.
// Undoing it names the tasks held up again.
func TestFinishingABlockerSaysWhatItFreed(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	space := create(t, st, &Task{Title: "Make space in office"})
	other := create(t, st, &Task{Title: "Buy shelves"})
	sofa := create(t, st, &Task{Title: "Move sofa", BlockedBy: []Dep{{ID: space.ID}}})
	later := create(t, st, &Task{Title: "Paint", Start: "2026-10-01", BlockedBy: []Dep{{ID: space.ID}}})
	create(t, st, &Task{Title: "Fill shelves", BlockedBy: []Dep{{ID: space.ID}, {ID: other.ID}}})

	got := done(t, st, space.ID)
	want := fmt.Sprint([]string{fmt.Sprint(sofa.ID), fmt.Sprint(later.ID) + "2026-10-01"})
	if got.Outcome == nil || fmt.Sprint(freedIDs(got.Outcome)) != want {
		t.Fatalf("freed = %v, want %s", got.Outcome, want)
	}

	back, err := st.Undo(space.ID)
	if err != nil {
		t.Fatal(err)
	}
	if o := back.Outcome; o == nil || len(o.WaitAgain) != 2 || o.WaitAgain[0].ID != sofa.ID || o.WaitAgain[1].ID != later.ID {
		t.Fatalf("wait again = %+v, want the sofa and the painting", back.Outcome)
	}
}

func TestTheEndOfAChainIsSaid(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	a := create(t, st, &Task{Title: "a"})
	b := create(t, st, &Task{Title: "b", BlockedBy: []Dep{{ID: a.ID}}})
	c := create(t, st, &Task{Title: "c", BlockedBy: []Dep{{ID: b.ID}}})
	done(t, st, a.ID)
	if got := done(t, st, b.ID); got.Outcome.Chain != 0 {
		t.Fatalf("chain = %d, want nothing said while c still waits on b", got.Outcome.Chain)
	}
	if got := done(t, st, c.ID); got.Outcome.Chain != 3 {
		t.Fatalf("chain = %d, want 3", got.Outcome.Chain)
	}
}

// The last one-off from a note says so; a single linked task, or a repeating
// one that never finishes, does not count.
func TestFinishingEverythingFromANote(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	slug := "career-transition-quantitative-researcher"
	first := create(t, st, &Task{Title: "Read AFML", MnemoSlug: slug})
	second := create(t, st, &Task{Title: "Re-rate", MnemoSlug: slug})
	create(t, st, &Task{Title: "Drill", MnemoSlug: slug, RecurKind: RecurEvery, RecurRule: "daily", Due: "2026-09-20"})
	alone := create(t, st, &Task{Title: "Only one", MnemoSlug: "latent"})

	if got := done(t, st, first.ID); got.Outcome.Note != "" {
		t.Fatalf("note = %q with one still open", got.Outcome.Note)
	}
	if got := done(t, st, second.ID); got.Outcome.Note != slug || got.Outcome.NoteDone != 2 {
		t.Fatalf("outcome = %+v, want everything from the note said", got.Outcome)
	}
	if got := done(t, st, alone.ID); got.Outcome.Note != "" {
		t.Fatalf("note = %q, want a note with one task left alone", got.Outcome.Note)
	}
}

// A streak counts occurrences done on time in a row; a late one starts it
// again, and says what a long run it ended.
func TestStreaks(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	drill := create(t, st, &Task{Title: "Green Book drill", RecurKind: RecurEvery, RecurRule: "daily", Due: "2026-09-20"})
	for day := 20; day <= 24; day++ {
		fixedNow(t, fmt.Sprintf("2026-09-%dT09:00:00Z", day))
		got := done(t, st, drill.ID)
		if got.Streak != day-19 || got.Outcome.Streak != day-19 {
			t.Fatalf("day %d: streak = %d, want %d", day, got.Streak, day-19)
		}
	}
	fixedNow(t, "2026-09-27T09:00:00Z")
	got := done(t, st, drill.ID)
	if got.Streak != 1 || got.Outcome.StreakWas != 5 {
		t.Fatalf("streak = %d was %d, want back to 1 from 5", got.Streak, got.Outcome.StreakWas)
	}
	if again := get(t, st, drill.ID); again.Streak != 1 {
		t.Fatalf("read back streak = %d, want 1", again.Streak)
	}
}

func TestLoggedOnADay(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	a := create(t, st, &Task{Title: "a", EstimateMinutes: 120})
	b := create(t, st, &Task{Title: "b"})
	if _, err := st.Work(a.ID, 30, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Done(b.ID, 40); err != nil {
		t.Fatal(err)
	}
	logged, err := st.LoggedOn("2026-09-20")
	if err != nil {
		t.Fatal(err)
	}
	if len(logged) != 2 || !logged[0].Partial || logged[0].Minutes != 30 || logged[1].Title != "b" || logged[1].Partial {
		t.Fatalf("logged = %+v, want the session then the completion", logged)
	}
	if other, _ := st.LoggedOn("2026-09-21"); len(other) != 0 {
		t.Fatalf("logged on another day = %+v", other)
	}
}

// A repeating task done today says so and sinks below the work still to do,
// and a second completion that day is refused rather than taking tomorrow's.
// Undo takes today's back, and the next day it is due like anything else.
func TestARepeatingTaskIsDoneForTheDay(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	drill := create(t, st, &Task{Title: "Green Book drill", RecurKind: RecurEvery, RecurRule: "daily", Due: "2026-09-20"})
	later := create(t, st, &Task{Title: "Read AFML chapter 7", Due: "2026-10-30"})
	if got := done(t, st, drill.ID); !got.DoneToday || got.Due != "2026-09-21" {
		t.Fatalf("done today = %v due %s, want true and 2026-09-21", got.DoneToday, got.Due)
	}
	tasks, err := st.List(Filter{Status: StatusOpen})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].ID != later.ID || !tasks[1].DoneToday {
		t.Fatalf("order = %d, %d; want the drill done today last", tasks[0].ID, tasks[1].ID)
	}
	if _, err := st.Done(drill.ID, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("second completion today: err = %v, want ErrInvalid", err)
	}
	if got, err := st.Undo(drill.ID); err != nil || got.DoneToday || got.Due != "2026-09-20" {
		t.Fatalf("undo: done today = %v due %s err %v", got.DoneToday, got.Due, err)
	}
	done(t, st, drill.ID)
	fixedNow(t, "2026-09-21T09:00:00Z")
	if got := get(t, st, drill.ID); got.DoneToday {
		t.Fatal("still done today the next day")
	}
	done(t, st, drill.ID)
}
