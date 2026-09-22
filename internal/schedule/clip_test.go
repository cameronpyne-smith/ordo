package schedule

import (
	"strings"
	"testing"
	"time"

	"github.com/cameronpyne-smith/ordo/internal/store"
)

func nowIs(t *testing.T, day, clock string) {
	t.Helper()
	when := at(day, clock)
	original := store.Now
	store.Now = func() time.Time { return when }
	t.Cleanup(func() { store.Now = original })
}

// Asked at three in the afternoon, a plan for today has no business putting
// anything at seven in the morning.
func TestPlanForTodayStartsNow(t *testing.T) {
	nowIs(t, monday, "15:00")
	day, err := Plan(Options{Day: monday, Tasks: []*store.Task{task(1, "later", est(30))}, Prefs: prefs()})
	if err != nil {
		t.Fatal(err)
	}
	if len(day.Blocks) != 1 || !day.Blocks[0].Start.Equal(at(monday, "17:30")) {
		t.Fatalf("blocks = %+v, want the first at 17:30 once work is over", day.Blocks)
	}
	for _, w := range day.Free {
		if w.Start.Before(at(monday, "15:00")) {
			t.Errorf("free window %s-%s is in the past", w.Start.Format("15:04"), w.End.Format("15:04"))
		}
	}
}

func TestPlanForTodayRoundsTheStartUp(t *testing.T) {
	nowIs(t, monday, "18:02")
	day, err := Plan(Options{Day: monday, Tasks: []*store.Task{task(1, "now", est(30))}, Prefs: prefs()})
	if err != nil {
		t.Fatal(err)
	}
	if len(day.Blocks) != 1 || !day.Blocks[0].Start.Equal(at(monday, "18:05")) {
		t.Fatalf("blocks = %+v, want a start at 18:05 rather than 18:02", day.Blocks)
	}
}

// The clock only matters for today. Any other day is planned whole, or a
// plan for tomorrow made this evening would begin at bedtime.
func TestPlanForAnotherDayIsWhole(t *testing.T) {
	nowIs(t, monday, "15:00")
	day, err := Plan(Options{Day: saturday, Tasks: []*store.Task{task(1, "weekend", est(30))}, Prefs: prefs()})
	if err != nil {
		t.Fatal(err)
	}
	if len(day.Blocks) != 1 || !day.Blocks[0].Start.Equal(at(saturday, "07:00")) {
		t.Fatalf("blocks = %+v, want the day's first hour", day.Blocks)
	}
}

// Once the deep-work window has passed, demanding work is placed like
// anything else rather than held for a morning that is over.
func TestDeepWorkGoneIsNotWaitedFor(t *testing.T) {
	nowIs(t, monday, "18:00")
	day, err := Plan(Options{Day: monday, Tasks: []*store.Task{task(1, "hard", hard, est(60))}, Prefs: prefs()})
	if err != nil {
		t.Fatal(err)
	}
	if len(day.Blocks) != 1 {
		t.Fatalf("blocks = %+v, want the demanding task placed anyway", day.Blocks)
	}
	b := day.Blocks[0]
	if !b.Start.Equal(at(monday, "18:00")) {
		t.Errorf("start = %s, want 18:00", b.Start.Format("15:04"))
	}
	if strings.Contains(b.Reason, "deep-work") {
		t.Errorf("reason %q claims a window that has gone", b.Reason)
	}
}

func TestClipDropsWhatHasPassedAndShortensWhatStraddles(t *testing.T) {
	windows := []Window{
		{at(monday, "07:00"), at(monday, "09:00")},
		{at(monday, "17:30"), at(monday, "22:00")},
	}
	got := spans(clip(windows, at(monday, "18:00"), 15))
	if strings.Join(got, ",") != "18:00-22:00" {
		t.Fatalf("clipped = %v", got)
	}
	// A window with fourteen minutes left is not a window.
	got = spans(clip(windows, at(monday, "21:46"), 15))
	if len(got) != 0 {
		t.Fatalf("a sliver survived: %v", got)
	}
}

func TestCeilLeavesATidyTimeAlone(t *testing.T) {
	if got := ceil(at(monday, "15:00"), clipGrain); !got.Equal(at(monday, "15:00")) {
		t.Errorf("15:00 became %s", got.Format("15:04"))
	}
	if got := ceil(at(monday, "15:01"), clipGrain); !got.Equal(at(monday, "15:05")) {
		t.Errorf("15:01 became %s, want 15:05", got.Format("15:04"))
	}
}
