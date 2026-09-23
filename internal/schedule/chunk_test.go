package schedule

import (
	"strings"
	"testing"
	"time"

	"github.com/cameronpyne-smith/ordo/internal/calendar"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

func left(n int) func(*store.Task) {
	return func(t *store.Task) { t.RemainingMinutes = n }
}

func planOne(t *testing.T, busy []calendar.Busy, tasks ...*store.Task) Day {
	t.Helper()
	plan, err := Plan(Options{Day: monday, Prefs: prefs(), Busy: busy, Tasks: tasks})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// A task bigger than one sitting gets a sitting's worth a day, from the day
// it exists rather than the day it is due, and says what it is a piece of.
func TestABigTaskGetsAPieceADay(t *testing.T) {
	plan := planOne(t, nil, task(1, "report", due("2026-09-28"), est(300)))
	if len(plan.Blocks) != 1 {
		t.Fatalf("blocks = %+v, want one piece", plan.Blocks)
	}
	b := plan.Blocks[0]
	if b.Minutes != 60 || b.Left != 300 {
		t.Fatalf("piece = %d min of %d left, want 60 of 300", b.Minutes, b.Left)
	}
	if !strings.Contains(b.Reason, "300 min left") || strings.Contains(b.Reason, "deadline") {
		t.Errorf("reason = %q, want what is left and no talk of catching up", b.Reason)
	}
}

// Too few days for a sitting a day, and the piece grows so the deadline is
// still met on paper, saying why.
func TestAPieceGrowsToMakeTheDeadline(t *testing.T) {
	plan := planOne(t, nil, task(1, "report", due("2026-09-22"), est(300)))
	b := plan.Blocks[0]
	if b.Minutes != 150 || b.Left != 300 {
		t.Fatalf("piece = %d min of %d left, want 150 of 300 with two days to go", b.Minutes, b.Left)
	}
	if !strings.Contains(b.Reason, "deadline") {
		t.Errorf("reason = %q, want the deadline named", b.Reason)
	}
}

// Due today or overdue, the whole of what is left is asked for, within the
// day's cap.
func TestAnOverdueBigTaskAsksForAllOfIt(t *testing.T) {
	plan := planOne(t, nil, task(1, "report", due("2026-09-19"), est(300)))
	if b := plan.Blocks[0]; b.Minutes != 240 {
		t.Fatalf("piece = %d min, want the day's whole 240", b.Minutes)
	}
}

// What was left after the last session is what counts, not the estimate.
func TestAPieceComesOutOfWhatIsLeft(t *testing.T) {
	plan := planOne(t, nil, task(1, "report", est(300), left(90)))
	if b := plan.Blocks[0]; b.Minutes != 60 || b.Left != 90 {
		t.Fatalf("piece = %d min of %d left, want 60 of 90", b.Minutes, b.Left)
	}
	plan = planOne(t, nil, task(1, "report", est(300), left(45)))
	if b := plan.Blocks[0]; b.Minutes != 45 || b.Left != 0 {
		t.Fatalf("block = %d min, left = %d; want the last 45 as an ordinary block", b.Minutes, b.Left)
	}
}

// busyAllBut leaves one gap of the given length at the start of the day.
func busyAllBut(minutes int) []calendar.Busy {
	start := at(monday, "07:00").Add(timeMinutes(minutes))
	return []calendar.Busy{{Start: start, End: at(monday, "22:00"), Summary: "away"}}
}

// A piece shrinks into a shorter gap rather than waiting for a whole one,
// down to half an hour.
func TestAPieceShrinksIntoTheGapThereIs(t *testing.T) {
	plan := planOne(t, busyAllBut(40), task(1, "report", est(300)))
	if len(plan.Blocks) != 1 || plan.Blocks[0].Minutes != 40 {
		t.Fatalf("blocks = %+v, want a 40 minute piece", plan.Blocks)
	}
	plan = planOne(t, busyAllBut(20), task(1, "report", est(300)))
	if len(plan.Blocks) != 0 || len(plan.Skipped) != 1 || !strings.Contains(plan.Skipped[0].Reason, "30 minutes") {
		t.Fatalf("blocks = %+v, skipped = %+v; want it skipped for want of half an hour", plan.Blocks, plan.Skipped)
	}
}

// A task meant to be done in one go does not shrink.
func TestAWholeTaskDoesNotShrink(t *testing.T) {
	plan := planOne(t, busyAllBut(40), task(1, "tax return", est(45)))
	if len(plan.Blocks) != 0 {
		t.Fatalf("blocks = %+v, want a 45 minute task left out of a 40 minute gap", plan.Blocks)
	}
}

// An occurrence of a recurring task is done in one go, however long.
func TestARecurringTaskIsNeverCutUp(t *testing.T) {
	long := task(1, "long run", due(monday), est(90), func(t *store.Task) {
		t.RecurKind, t.RecurRule = store.RecurEvery, "weekly on mon"
	})
	plan := planOne(t, nil, long)
	if b := plan.Blocks[0]; b.Minutes != 90 || b.Left != 0 {
		t.Fatalf("block = %d min, left = %d; want all 90 as one", b.Minutes, b.Left)
	}
}

// What the cap has left over is a gap too, so a piece shrinks into it.
func TestAPieceShrinksIntoWhatTheCapLeaves(t *testing.T) {
	plan := planOne(t, nil,
		task(1, "long chore", due(monday), est(200), func(t *store.Task) {
			t.RecurKind, t.RecurRule = store.RecurEvery, "weekly on mon"
		}),
		task(2, "report", est(300)),
	)
	if len(plan.Blocks) != 2 || plan.Blocks[1].Task.ID != 2 || plan.Blocks[1].Minutes != 40 {
		t.Fatalf("blocks = %+v, want the report in the 40 minutes the cap has left", plan.Blocks)
	}
}

// A demanding piece still takes the deep-work window.
func TestADemandingPieceTakesTheDeepWindow(t *testing.T) {
	plan := planOne(t, nil, task(1, "someday", est(30)), task(2, "thesis", hard, est(600)))
	for _, b := range plan.Blocks {
		if b.Task.ID == 2 && (b.Start.Format("15:04") != "07:00" || !strings.Contains(b.Reason, "deep-work")) {
			t.Fatalf("thesis at %s: %q, want the deep window", b.Start.Format("15:04"), b.Reason)
		}
	}
}

func timeMinutes(n int) time.Duration { return time.Duration(n) * time.Minute }
