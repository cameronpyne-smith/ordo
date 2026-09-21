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
