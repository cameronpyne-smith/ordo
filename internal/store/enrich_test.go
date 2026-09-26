package store

import (
	"errors"
	"testing"
)

func TestEnrichFillsEmptyFields(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	st := open(t)

	created := create(t, st, &Task{Title: "Send the CV by Friday"})
	got, err := st.Enrich(created.ID, Inference{
		Difficulty: DifficultyMedium,
		Priority:   PriorityHigh,
		Due:        "2026-09-25",
	})
	if err != nil {
		t.Fatalf("enriching: %v", err)
	}
	if got.Difficulty != DifficultyMedium || got.Priority != PriorityHigh || got.Due != "2026-09-25" {
		t.Fatalf("got %+v, want the inference applied", got)
	}
	if got.EnrichedAt == nil {
		t.Fatal("task should be marked enriched")
	}
}

func TestEnrichNeverOverwrites(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	st := open(t)

	created := create(t, st, &Task{
		Title:      "Send the CV by Friday",
		Difficulty: DifficultyHigh,
		Priority:   PriorityLow,
		Due:        "2026-10-01",
	})
	got, err := st.Enrich(created.ID, Inference{
		Difficulty: DifficultyLow,
		Priority:   PriorityHigh,
		Due:        "2026-09-25",
		RecurKind:  RecurEvery,
		RecurRule:  "daily",
	})
	if err != nil {
		t.Fatalf("enriching: %v", err)
	}
	if got.Difficulty != DifficultyHigh || got.Priority != PriorityLow || got.Due != "2026-10-01" {
		t.Fatalf("got %+v, want the chosen values kept", got)
	}
	if !got.Recurring() {
		t.Fatal("an unset recurrence should still have been filled in")
	}
}

func TestEnrichRecurrenceComputesItsOwnDue(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	st := open(t)

	created := create(t, st, &Task{Title: "Put the bins out every tuesday"})
	got, err := st.Enrich(created.ID, Inference{
		RecurKind: RecurEvery,
		RecurRule: "weekly on tue",
		Due:       "2026-09-21",
	})
	if err != nil {
		t.Fatalf("enriching: %v", err)
	}
	if got.Due != "2026-09-22" {
		t.Fatalf("due = %s, want the rule's first occurrence", got.Due)
	}
}

func TestEnrichRecurrenceKeepsAChosenDue(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	st := open(t)

	created := create(t, st, &Task{Title: "Put the bins out", Due: "2026-09-29"})
	got, err := st.Enrich(created.ID, Inference{RecurKind: RecurEvery, RecurRule: "weekly on tue"})
	if err != nil {
		t.Fatalf("enriching: %v", err)
	}
	if got.Due != "2026-09-29" {
		t.Fatalf("due = %s, want the chosen date kept", got.Due)
	}
}

func TestEnrichRejectsBadValues(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	st := open(t)

	created := create(t, st, &Task{Title: "Bins"})
	if _, err := st.Enrich(created.ID, Inference{Difficulty: "tricky"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if _, err := st.Enrich(created.ID, Inference{RecurKind: RecurEvery, RecurRule: "weekly on funday"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestEnrichMarksEvenWhenNothingToFill(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	st := open(t)

	created := create(t, st, &Task{Title: "Bins"})
	got, err := st.Enrich(created.ID, Inference{})
	if err != nil {
		t.Fatalf("enriching: %v", err)
	}
	if got.EnrichedAt == nil {
		t.Fatal("task should be marked enriched")
	}
}

func TestUnenrichedListsOpenTasksOnly(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	st := open(t)

	first := create(t, st, &Task{Title: "One"})
	create(t, st, &Task{Title: "Two"})
	third := create(t, st, &Task{Title: "Three"})
	if _, err := st.Enrich(first.ID, Inference{Priority: PriorityLow}); err != nil {
		t.Fatalf("enriching: %v", err)
	}
	if _, err := st.Done(third.ID, 0); err != nil {
		t.Fatalf("completing: %v", err)
	}

	ids, err := st.Unenriched(0)
	if err != nil {
		t.Fatalf("listing unenriched: %v", err)
	}
	if len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("got %v, want only the open unenriched task", ids)
	}
}

// The date comes out of the title once the task holds the same date, whoever
// set it, and stays when the two disagree or the title was renamed meanwhile.
func TestEnrichCutsARedundantDateFromTheTitle(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	st := open(t)
	cut := func(title, due string, in Inference) string {
		t.Helper()
		created := create(t, st, &Task{Title: title, Due: due})
		got, err := st.Enrich(created.ID, in)
		if err != nil {
			t.Fatalf("enriching %q: %v", title, err)
		}
		return got.Title
	}
	bins := "take the bins out on tuesday"
	short := Inference{Due: "2026-09-22", Title: "take the bins out", ReadTitle: bins}
	if got := cut(bins, "", short); got != "take the bins out" {
		t.Fatalf("inferred date: title = %q", got)
	}
	if got := cut(bins, "2026-09-22", short); got != "take the bins out" {
		t.Fatalf("date already set to match: title = %q", got)
	}
	if got := cut(bins, "2026-09-23", short); got != bins {
		t.Fatalf("date set to another day: title = %q, want it kept", got)
	}
	if got := cut(bins, "", Inference{Title: "take the bins out", ReadTitle: bins}); got != bins {
		t.Fatalf("no date read: title = %q, want it kept", got)
	}
	renamed := short
	renamed.ReadTitle = "take the bins out tuesday"
	if got := cut(bins, "", renamed); got != bins {
		t.Fatalf("renamed since read: title = %q, want it kept", got)
	}
	weekly := Inference{RecurKind: RecurEvery, RecurRule: "weekly on tue", Title: "Put the bins out", ReadTitle: "Put the bins out every tuesday"}
	if got := cut("Put the bins out every tuesday", "", weekly); got != "Put the bins out" {
		t.Fatalf("schedule: title = %q", got)
	}
}
