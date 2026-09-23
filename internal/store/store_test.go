package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "ordo.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func create(t *testing.T, st *Store, task *Task) *Task {
	t.Helper()
	created, err := st.Create(task)
	if err != nil {
		t.Fatalf("creating task: %v", err)
	}
	return created
}

func TestCreateAndGet(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)

	created := create(t, st, &Task{Title: "  Put the bins out  ", Due: "2026-09-22", Priority: PriorityHigh})
	if created.Title != "Put the bins out" {
		t.Fatalf("title = %q, want trimmed", created.Title)
	}
	if created.Status != StatusOpen {
		t.Fatalf("status = %q, want open", created.Status)
	}
	if created.EnrichedAt != nil {
		t.Fatal("a new task should not be marked enriched")
	}

	got, err := st.Get(created.ID)
	if err != nil {
		t.Fatalf("getting task: %v", err)
	}
	if got.Due != "2026-09-22" || got.Priority != PriorityHigh {
		t.Fatalf("round trip lost fields: %+v", got)
	}
	if got.Difficulty != "" || got.EstimateMinutes != 0 {
		t.Fatalf("unset fields came back populated: %+v", got)
	}
}

func TestGetMissing(t *testing.T) {
	st := open(t)
	if _, err := st.Get(99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestCreateValidation(t *testing.T) {
	st := open(t)

	tests := []struct {
		name string
		task *Task
	}{
		{"empty title", &Task{Title: "   "}},
		{"bad difficulty", &Task{Title: "x", Difficulty: "trivial"}},
		{"bad priority", &Task{Title: "x", Priority: "urgent"}},
		{"bad due", &Task{Title: "x", Due: "next tuesday"}},
		{"recur rule without kind", &Task{Title: "x", RecurRule: "weekly on tue"}},
		{"recur kind without rule", &Task{Title: "x", RecurKind: RecurEvery}},
		{"bad recur kind", &Task{Title: "x", RecurKind: "sometimes", RecurRule: "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := st.Create(tt.task); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestEditPartial(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	created := create(t, st, &Task{Title: "Draft CV", Due: "2026-09-25", Notes: "for the recruiter"})

	high := Priority(PriorityHigh)
	edited, err := st.Edit(created.ID, Edit{Priority: &high})
	if err != nil {
		t.Fatalf("editing: %v", err)
	}
	if edited.Priority != PriorityHigh {
		t.Fatalf("priority = %q, want high", edited.Priority)
	}
	if edited.Due != "2026-09-25" || edited.Notes != "for the recruiter" || edited.Title != "Draft CV" {
		t.Fatalf("untouched fields changed: %+v", edited)
	}
}

func TestEditClearsWithEmptyValue(t *testing.T) {
	st := open(t)
	created := create(t, st, &Task{Title: "Draft CV", Due: "2026-09-25", EstimateMinutes: 45})

	empty, zero := "", 0
	edited, err := st.Edit(created.ID, Edit{Due: &empty, Estimate: &zero})
	if err != nil {
		t.Fatalf("editing: %v", err)
	}
	if edited.Due != "" || edited.EstimateMinutes != 0 {
		t.Fatalf("clear failed: %+v", edited)
	}
}

func TestEditRejectsEmptyAndInvalid(t *testing.T) {
	st := open(t)
	created := create(t, st, &Task{Title: "Draft CV"})

	if _, err := st.Edit(created.ID, Edit{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid for an empty edit", err)
	}
	bad := Difficulty("trivial")
	if _, err := st.Edit(created.ID, Edit{Difficulty: &bad}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	after, err := st.Get(created.ID)
	if err != nil {
		t.Fatalf("getting task: %v", err)
	}
	if after.Difficulty != "" {
		t.Fatal("a rejected edit wrote anyway")
	}
}

func TestDoneRecordsCompletion(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	created := create(t, st, &Task{Title: "Draft CV"})

	done, err := st.Done(created.ID, 30)
	if err != nil {
		t.Fatalf("completing: %v", err)
	}
	if done.Status != StatusDone || done.DoneAt == nil {
		t.Fatalf("task not closed: %+v", done)
	}
	completions, err := st.Completions(created.ID)
	if err != nil {
		t.Fatalf("reading completions: %v", err)
	}
	if len(completions) != 1 || completions[0].Minutes != 30 {
		t.Fatalf("completions = %+v, want one of 30 minutes", completions)
	}
}

func TestDoneHidesFromDefaultList(t *testing.T) {
	st := open(t)
	kept := create(t, st, &Task{Title: "Open one"})
	closed := create(t, st, &Task{Title: "Closed one"})
	if _, err := st.Done(closed.ID, 0); err != nil {
		t.Fatalf("completing: %v", err)
	}

	open, err := st.List(Filter{})
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(open) != 1 || open[0].ID != kept.ID {
		t.Fatalf("default list = %+v, want only the open task", open)
	}

	all, err := st.List(Filter{All: true})
	if err != nil {
		t.Fatalf("listing all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all = %d tasks, want 2", len(all))
	}
}

func TestUndoReopens(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	created := create(t, st, &Task{Title: "Draft CV"})
	if _, err := st.Done(created.ID, 0); err != nil {
		t.Fatalf("completing: %v", err)
	}

	reopened, err := st.Undo(created.ID)
	if err != nil {
		t.Fatalf("undoing: %v", err)
	}
	if reopened.Status != StatusOpen || reopened.DoneAt != nil {
		t.Fatalf("task not reopened: %+v", reopened)
	}
	completions, err := st.Completions(created.ID)
	if err != nil {
		t.Fatalf("reading completions: %v", err)
	}
	if len(completions) != 0 {
		t.Fatalf("completions = %+v, want none", completions)
	}
}

func TestUndoWithoutCompletion(t *testing.T) {
	st := open(t)
	created := create(t, st, &Task{Title: "Draft CV"})
	if _, err := st.Undo(created.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestDeleteCascadesCompletions(t *testing.T) {
	st := open(t)
	created := create(t, st, &Task{Title: "Draft CV"})
	if _, err := st.Done(created.ID, 0); err != nil {
		t.Fatalf("completing: %v", err)
	}
	if err := st.Delete(created.ID); err != nil {
		t.Fatalf("deleting: %v", err)
	}
	if _, err := st.Get(created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	completions, err := st.Completions(created.ID)
	if err != nil {
		t.Fatalf("reading completions: %v", err)
	}
	if len(completions) != 0 {
		t.Fatalf("completions survived the delete: %+v", completions)
	}
}

func TestDeleteMissing(t *testing.T) {
	st := open(t)
	if err := st.Delete(99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListFilters(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	overdue := create(t, st, &Task{Title: "Overdue", Due: "2026-09-01"})
	quick := create(t, st, &Task{Title: "Quick", Difficulty: DifficultyLow})
	long := create(t, st, &Task{Title: "Easy but long", Difficulty: DifficultyLow, EstimateMinutes: 120})
	high := create(t, st, &Task{Title: "Important", Priority: PriorityHigh})
	unset := create(t, st, &Task{Title: "Unset priority"})

	cases := []struct {
		name   string
		filter Filter
		want   []int64
	}{
		{"overdue", Filter{Overdue: true}, []int64{overdue.ID}},
		{"high priority", Filter{Priority: PriorityHigh}, []int64{high.ID}},
		{"low difficulty", Filter{Difficulty: DifficultyLow}, []int64{quick.ID, long.ID}},
		{"quick wins are easy and short", Filter{Quick: true}, []int64{quick.ID}},
		{"normal includes unset", Filter{Priority: PriorityNormal}, []int64{overdue.ID, quick.ID, long.ID, unset.ID}},
		{"limit", Filter{Limit: 1}, []int64{overdue.ID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := st.List(tc.filter)
			if err != nil {
				t.Fatalf("listing: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d tasks, want %d: %+v", len(got), len(tc.want), got)
			}
			for i, id := range tc.want {
				if got[i].ID != id {
					t.Fatalf("task %d = %d, want %d", i, got[i].ID, id)
				}
			}
		})
	}
}

func TestListRejectsBadFilter(t *testing.T) {
	st := open(t)
	if _, err := st.List(Filter{Difficulty: "trivial"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestSummary(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	create(t, st, &Task{Title: "Overdue", Due: "2026-09-01"})
	closed := create(t, st, &Task{Title: "Closed"})
	if _, err := st.Done(closed.ID, 0); err != nil {
		t.Fatalf("completing: %v", err)
	}

	sum, err := st.Summary()
	if err != nil {
		t.Fatalf("summarising: %v", err)
	}
	if sum.Open != 1 || sum.Done != 1 || sum.Overdue != 1 || sum.Unenriched != 2 {
		t.Fatalf("summary = %+v", sum)
	}
}

func TestBackupPrunes(t *testing.T) {
	st := open(t)
	create(t, st, &Task{Title: "Draft CV"})
	dir := t.TempDir()

	for i := range 3 {
		fixedNow(t, []string{"2026-09-18T03:00:00Z", "2026-09-19T03:00:00Z", "2026-09-20T03:00:00Z"}[i])
		if _, err := st.Backup(dir, 2); err != nil {
			t.Fatalf("backup: %v", err)
		}
	}
	entries, err := filepath.Glob(filepath.Join(dir, "ordo-*.db"))
	if err != nil {
		t.Fatalf("globbing: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("kept %d snapshots, want 2", len(entries))
	}
	for _, entry := range entries {
		info, err := os.Stat(entry)
		if err != nil || info.Size() == 0 {
			t.Fatalf("snapshot %s is unusable: %v", entry, err)
		}
	}
}

func TestReopenClearsDoneAt(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	created := create(t, st, &Task{Title: "Draft CV"})
	if _, err := st.Done(created.ID, 0); err != nil {
		t.Fatalf("completing: %v", err)
	}

	openStatus := StatusOpen
	edited, err := st.Edit(created.ID, Edit{Status: &openStatus})
	if err != nil {
		t.Fatalf("editing: %v", err)
	}
	if edited.DoneAt != nil {
		t.Fatalf("done_at survived reopening: %+v", edited)
	}
}

// A deleted id must never come back: ids are the handle the user and Claude
// pass around by hand, so a reused one would silently retarget a stale
// reference.
func TestIDsAreNotReused(t *testing.T) {
	st := open(t)
	for _, title := range []string{"one", "two", "three"} {
		create(t, st, &Task{Title: title})
	}
	if err := st.Delete(3); err != nil {
		t.Fatalf("deleting: %v", err)
	}
	next := create(t, st, &Task{Title: "four"})
	if next.ID != 4 {
		t.Fatalf("next id = %d, want 4: id 3 was reused", next.ID)
	}
}
