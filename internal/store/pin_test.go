package store

import (
	"errors"
	"testing"
)

func TestPinDefaultsToToday(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	st := open(t)

	task := create(t, st, &Task{Title: "Send the CV"})
	pinned, err := st.Pin(task.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if pinned.PinnedOn != "2026-09-21" {
		t.Fatalf("pinned_on = %q, want today", pinned.PinnedOn)
	}
	if !pinned.PinnedFor("2026-09-21") {
		t.Error("PinnedFor should recognise its own day")
	}
	// The whole reason a pin is a date: it does not carry over.
	if pinned.PinnedFor("2026-09-22") {
		t.Error("a pin must not speak for another day")
	}
}

func TestPinAndUnpin(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	st := open(t)

	task := create(t, st, &Task{Title: "Rewrite the pricing model"})
	if _, err := st.Pin(task.ID, "2026-09-24"); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PinnedOn != "2026-09-24" {
		t.Fatalf("pinned_on = %q", got.PinnedOn)
	}
	unpinned, err := st.Unpin(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unpinned.PinnedOn != "" {
		t.Fatalf("pinned_on = %q, want it cleared", unpinned.PinnedOn)
	}
}

func TestPinRejectsWhatCannotBePinned(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	st := open(t)

	done := create(t, st, &Task{Title: "Already finished"})
	if _, err := st.Done(done.ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pin(done.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("pinning a finished task: err = %v, want ErrInvalid", err)
	}

	open_ := create(t, st, &Task{Title: "Open"})
	if _, err := st.Pin(open_.ID, "24-09-2026"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad date: err = %v, want ErrInvalid", err)
	}
	if _, err := st.Pin(9999, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing task: err = %v, want ErrNotFound", err)
	}
}

func TestPinnedListsOnlyThatDay(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	st := open(t)

	today := create(t, st, &Task{Title: "Today's"})
	tomorrow := create(t, st, &Task{Title: "Tomorrow's"})
	create(t, st, &Task{Title: "Unpinned"})
	if _, err := st.Pin(today.ID, "2026-09-21"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pin(tomorrow.ID, "2026-09-22"); err != nil {
		t.Fatal(err)
	}

	got, err := st.Pinned("2026-09-21")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != today.ID {
		t.Fatalf("pinned = %+v, want only today's", got)
	}
}

// Completing a pinned recurring task keeps the row, so the pin has to go
// with the occurrence rather than linger on the next one. It does not, and
// this records that: the pin names a date that has now passed.
func TestPinStaysOnItsOwnDate(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	st := open(t)

	bins := create(t, st, &Task{Title: "Bins", RecurKind: RecurEvery, RecurRule: "weekly on tue"})
	if _, err := st.Pin(bins.ID, "2026-09-21"); err != nil {
		t.Fatal(err)
	}
	done, err := st.Done(bins.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if done.PinnedFor("2026-09-22") {
		t.Fatal("yesterday's pin must not claim the next occurrence")
	}
}
