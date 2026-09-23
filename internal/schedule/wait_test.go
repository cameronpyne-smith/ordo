package schedule

import (
	"strings"
	"testing"

	"github.com/cameronpyne-smith/ordo/internal/store"
)

func waitsOn(ids ...int64) func(*store.Task) {
	return func(t *store.Task) {
		for _, id := range ids {
			t.BlockedBy = append(t.BlockedBy, store.Dep{ID: id, Title: "blocker"})
		}
	}
}

func starts(d string) func(*store.Task) {
	return func(t *store.Task) { t.Start = d }
}

// Nothing waiting is planned: not a task held up by an open one, and not one
// whose start date has not come. A blocker already done holds nothing up.
func TestWaitingTasksAreNotPlanned(t *testing.T) {
	done := func(t *store.Task) { t.BlockedBy = []store.Dep{{ID: 9, Done: true}} }
	plan := planOne(t, nil,
		task(1, "blocked", waitsOn(9), est(30)),
		task(2, "not yet", starts("2026-09-22"), est(30)),
		task(3, "from today", starts(monday), est(30)),
		task(4, "released", done, est(30)),
	)
	var placed []string
	for _, b := range plan.Blocks {
		placed = append(placed, b.Task.Title)
	}
	if strings.Join(placed, "|") != "from today|released" {
		t.Fatalf("placed = %v, want only what can be started today", placed)
	}
}

// A pin is "today, regardless", so it wins over waiting, and the reason says
// what the task is still waiting on.
func TestAPinWinsOverWaiting(t *testing.T) {
	plan := planOne(t, nil, task(1, "blocked", waitsOn(7, 8), pinnedTo(monday), est(30)))
	if len(plan.Blocks) != 1 {
		t.Fatalf("blocks = %+v, want the pinned task placed", plan.Blocks)
	}
	if r := plan.Blocks[0].Reason; !strings.Contains(r, "pinned") || !strings.Contains(r, "still waiting on 7, 8") {
		t.Fatalf("reason = %q, want the pin and what it still waits on", r)
	}
}

// A deadline passed down from a waiting task counts like the task's own:
// it sizes the pieces and the reason says whose it is.
func TestAPassedDownDeadlineDrivesThePlan(t *testing.T) {
	passed := func(t *store.Task) { t.EffectiveDue, t.DueFor = "2026-09-22", 12 }
	plan := planOne(t, nil, task(1, "read", est(300), passed))
	b := plan.Blocks[0]
	if b.Minutes != 150 {
		t.Fatalf("piece = %d min, want 150 with two days to the passed-down deadline", b.Minutes)
	}
	if !strings.Contains(b.Reason, "due in 1 day so 12 can follow in time") {
		t.Fatalf("reason = %q, want the deadline and whose it is", b.Reason)
	}
}
