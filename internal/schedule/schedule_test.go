package schedule

import (
	"strings"
	"testing"
	"time"

	"github.com/cameronpyne-smith/ordo/internal/calendar"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

// 2026-09-21 is a Monday and 2026-09-26 a Saturday, which is the whole of
// the weekday/weekend distinction these tests need.
const (
	monday   = "2026-09-21"
	saturday = "2026-09-26"
)

func prefs() store.Preferences { return store.DefaultPreferences() }

// work is the job as the calendar has it, which is the only way the
// scheduler hears about it.
func work(day string) []calendar.Busy {
	return []calendar.Busy{{Start: at(day, "09:00"), End: at(day, "17:30"), Summary: "Work"}}
}

func at(day, clock string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", day+" "+clock, store.Location)
	if err != nil {
		panic(err)
	}
	return t
}

func task(id int64, title string, mutate ...func(*store.Task)) *store.Task {
	t := &store.Task{ID: id, Title: title, Status: store.StatusOpen}
	for _, m := range mutate {
		m(t)
	}
	return t
}

func est(n int) func(*store.Task) {
	return func(t *store.Task) { t.EstimateMinutes = n }
}

func due(d string) func(*store.Task) {
	return func(t *store.Task) { t.Due = d }
}

func hard(t *store.Task)   { t.Difficulty = store.DifficultyHigh }
func urgent(t *store.Task) { t.Priority = store.PriorityHigh }

func pinnedTo(day string) func(*store.Task) {
	return func(t *store.Task) { t.PinnedOn = day }
}

func spans(windows []Window) []string {
	var out []string
	for _, w := range windows {
		out = append(out, w.Start.Format("15:04")+"-"+w.End.Format("15:04"))
	}
	return out
}

func TestWindowsCutWorkOutWhereTheCalendarPutsIt(t *testing.T) {
	day, _ := store.ParseDate(monday)
	got := spans(Windows(day, prefs(), work(monday)))
	want := []string{"07:00-09:00", "17:30-22:00"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("windows = %v, want %v", got, want)
	}
}

// Work is not a preference, so a weekday with nothing on the calendar is as
// free as a Saturday: a day's leave is a deleted event, not a setting.
func TestWindowsLeaveAnEmptyDayWhole(t *testing.T) {
	for _, d := range []string{monday, saturday} {
		day, _ := store.ParseDate(d)
		got := spans(Windows(day, prefs(), nil))
		if len(got) != 1 || got[0] != "07:00-22:00" {
			t.Fatalf("%s windows = %v, want one all-day window", d, got)
		}
	}
}

func TestWindowsSubtractTheCalendar(t *testing.T) {
	day, _ := store.ParseDate(saturday)
	busy := []calendar.Busy{
		{Start: at(saturday, "10:00"), End: at(saturday, "11:00"), Summary: "climbing"},
		{Start: at(saturday, "19:00"), End: at(saturday, "20:30"), Summary: "dinner"},
	}
	got := spans(Windows(day, prefs(), busy))
	want := []string{"07:00-10:00", "11:00-19:00", "20:30-22:00"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("windows = %v, want %v", got, want)
	}
}

// A sliver nobody could use is not free time; offering it would only produce
// blocks too short to start.
func TestWindowsDropStretchesTooShortToUse(t *testing.T) {
	day, _ := store.ParseDate(saturday)
	p := prefs()
	p.MinBlockMinutes = 30
	busy := []calendar.Busy{{Start: at(saturday, "07:20"), End: at(saturday, "21:00")}}
	got := spans(Windows(day, p, busy))
	if len(got) != 1 || got[0] != "21:00-22:00" {
		t.Fatalf("windows = %v, want the 20-minute sliver dropped", got)
	}
}

func TestPlanPlacesInTheDaemonsOrder(t *testing.T) {
	plan, err := Plan(Options{
		Day:   monday,
		Prefs: prefs(),
		Tasks: []*store.Task{
			task(1, "first", due(monday), est(30)),
			task(2, "second", due(monday), est(30)),
			task(3, "third", due(monday), est(30)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, b := range plan.Blocks {
		titles = append(titles, b.Task.Title)
	}
	if strings.Join(titles, ",") != "first,second,third" {
		t.Fatalf("titles = %v", titles)
	}
	if got := plan.Blocks[0].Start.Format("15:04"); got != "07:00" {
		t.Errorf("first block starts %s, want the day to open at 07:00", got)
	}
	// 30 minutes of work then the 10-minute buffer.
	if got := plan.Blocks[1].Start.Format("15:04"); got != "07:40" {
		t.Errorf("second block starts %s, want the buffer respected", got)
	}
}

func TestPlanPutsPinsFirstWhateverTheOrderSays(t *testing.T) {
	plan, err := Plan(Options{
		Day:   monday,
		Prefs: prefs(),
		Tasks: []*store.Task{
			task(1, "overdue and urgent", due("2026-09-01"), urgent, est(30)),
			task(2, "the one I said I would do", pinnedTo(monday), est(30)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Blocks[0].Task.ID != 2 {
		t.Fatalf("first block is %d, want the pinned task", plan.Blocks[0].Task.ID)
	}
	if !strings.Contains(plan.Blocks[0].Reason, "pinned") {
		t.Errorf("reason = %q, want it to say it was pinned", plan.Blocks[0].Reason)
	}
}

// A pin is for a day. Someone else's day must not leak into this one.
func TestPlanIgnoresAPinForAnotherDay(t *testing.T) {
	plan, err := Plan(Options{
		Day:   monday,
		Prefs: prefs(),
		Tasks: []*store.Task{task(1, "tomorrow's", pinnedTo("2026-09-22"), est(30))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blocks) != 0 {
		t.Fatalf("blocks = %d, want a pin for another day left alone", len(plan.Blocks))
	}
}

// A task dated in the future belongs on that day, not brought forward into
// this one; an undated task may fill any space.
func TestPlanHoldsFutureDatedTasksBack(t *testing.T) {
	plan, err := Plan(Options{
		Day:   monday,
		Prefs: prefs(),
		Tasks: []*store.Task{
			task(1, "next week", due("2026-09-28"), est(30)),
			task(2, "someday", est(30)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blocks) != 1 || plan.Blocks[0].Task.ID != 2 {
		t.Fatalf("blocks = %+v, want only the undated task", plan.Blocks)
	}
}

func TestPlanGivesTheDeepWindowToDemandingWork(t *testing.T) {
	plan, err := Plan(Options{
		Day:   monday,
		Prefs: prefs(),
		Tasks: []*store.Task{
			task(1, "easy", due(monday), est(60)),
			task(2, "demanding", due(monday), hard, est(60)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var demanding *Block
	for i := range plan.Blocks {
		if plan.Blocks[i].Task.ID == 2 {
			demanding = &plan.Blocks[i]
		}
	}
	if demanding == nil {
		t.Fatal("the demanding task was not placed")
	}
	if got := demanding.Start.Format("15:04"); got != "07:00" {
		t.Errorf("demanding work starts %s, want the 07:00 deep window", got)
	}
	if !strings.Contains(demanding.Reason, "deep-work") {
		t.Errorf("reason = %q, want it to say why it is in the morning", demanding.Reason)
	}
}

// A demanding task too long for the deep window must still be placed, just
// not there. Preferring a window is not the same as requiring one.
func TestPlanStillPlacesDemandingWorkTooLongForTheDeepWindow(t *testing.T) {
	plan, err := Plan(Options{
		Day:   monday,
		Prefs: prefs(),
		Tasks: []*store.Task{task(1, "a long haul", due(monday), hard, est(180))},
		Busy:  work(monday),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blocks) != 1 {
		t.Fatalf("blocks = %d, want it placed outside the deep window", len(plan.Blocks))
	}
	if got := plan.Blocks[0].Start.Format("15:04"); got != "17:30" {
		t.Errorf("starts %s, want the evening window", got)
	}
}

func TestPlanStopsAtTheDailyCap(t *testing.T) {
	p := prefs()
	p.MaxMinutesDay = 60
	plan, err := Plan(Options{
		Day:   saturday,
		Prefs: p,
		Tasks: []*store.Task{
			task(1, "in", est(45)),
			task(2, "out", est(45)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blocks) != 1 || plan.Planned != 45 {
		t.Fatalf("planned %d minutes in %d blocks, want one 45-minute block", plan.Planned, len(plan.Blocks))
	}
	if len(plan.Skipped) != 1 {
		t.Fatalf("skipped = %+v, want the second task explained", plan.Skipped)
	}
	if !strings.Contains(plan.Skipped[0].Reason, "45") {
		t.Errorf("skip reason = %q, want it to name what would not fit", plan.Skipped[0].Reason)
	}
}

func TestPlanExplainsATaskWithNowhereToGo(t *testing.T) {
	p := prefs()
	busy := []calendar.Busy{{Start: at(saturday, "07:00"), End: at(saturday, "21:50")}}
	plan, err := Plan(Options{
		Day:   saturday,
		Prefs: p,
		Busy:  busy,
		Tasks: []*store.Task{task(1, "needs an hour", est(60))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blocks) != 0 {
		t.Fatalf("blocks = %d, want none", len(plan.Blocks))
	}
	if len(plan.Skipped) != 1 || !strings.Contains(plan.Skipped[0].Reason, "60 minutes long") {
		t.Fatalf("skipped = %+v, want it to say no stretch was long enough", plan.Skipped)
	}
}

func TestEstimateFallsBackToDifficulty(t *testing.T) {
	cases := []struct {
		task *store.Task
		want int
	}{
		{task(1, "measured", est(25)), 25},
		{task(2, "low", func(x *store.Task) { x.Difficulty = store.DifficultyLow }), estimateLow},
		{task(3, "medium", func(x *store.Task) { x.Difficulty = store.DifficultyMedium }), estimateMedium},
		{task(4, "high", hard), estimateHigh},
		{task(5, "unread"), estimateUnknown},
	}
	for _, c := range cases {
		if got := Estimate(c.task); got != c.want {
			t.Errorf("%s: estimate = %d, want %d", c.task.Title, got, c.want)
		}
	}
}

// A guessed estimate has to say so, because a block built on one is a block
// worth doubting.
func TestReasonAdmitsAGuessedEstimate(t *testing.T) {
	plan, err := Plan(Options{
		Day:   saturday,
		Prefs: prefs(),
		Tasks: []*store.Task{task(1, "unmeasured"), task(2, "measured", est(20))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Blocks[0].Reason, "guessed") {
		t.Errorf("reason = %q, want it to admit the estimate was guessed", plan.Blocks[0].Reason)
	}
	if strings.Contains(plan.Blocks[1].Reason, "guessed") {
		t.Errorf("reason = %q, want no guess claimed for a measured task", plan.Blocks[1].Reason)
	}
}

func TestReasonNamesWhyTheTaskIsEligible(t *testing.T) {
	plan, err := Plan(Options{
		Day:   monday,
		Prefs: prefs(),
		Tasks: []*store.Task{
			task(1, "late", due("2026-09-18"), est(20)),
			task(2, "today", due(monday), est(20)),
			task(3, "spare", est(20)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"overdue by 3 days", "due today", "next one on the list"}
	for i, w := range want {
		if !strings.Contains(plan.Blocks[i].Reason, w) {
			t.Errorf("block %d reason = %q, want it to mention %q", i, plan.Blocks[i].Reason, w)
		}
	}
}

// The same inputs must always give the same day. A plan you cannot predict
// is one you end up arguing with, and this is the property that rules out
// ever letting a model near it.
func TestPlanIsDeterministic(t *testing.T) {
	tasks := []*store.Task{
		task(1, "a", due(monday), hard, est(45)),
		task(2, "b", est(30)),
		task(3, "c", pinnedTo(monday), est(15)),
		task(4, "d", due("2026-09-10"), urgent, est(90)),
	}
	busy := []calendar.Busy{{Start: at(monday, "18:00"), End: at(monday, "19:00"), Summary: "call"}}

	var first string
	for i := 0; i < 5; i++ {
		plan, err := Plan(Options{Day: monday, Prefs: prefs(), Tasks: tasks, Busy: busy})
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for _, block := range plan.Blocks {
			b.WriteString(block.Start.Format("15:04") + block.Task.Title + block.Reason + "|")
		}
		if i == 0 {
			first = b.String()
			continue
		}
		if b.String() != first {
			t.Fatalf("run %d differed:\n%s\n%s", i, first, b.String())
		}
	}
}

func TestPlanSkipsDoneTasks(t *testing.T) {
	plan, err := Plan(Options{
		Day:   saturday,
		Prefs: prefs(),
		Tasks: []*store.Task{task(1, "finished", func(x *store.Task) { x.Status = store.StatusDone })},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blocks) != 0 || len(plan.Skipped) != 0 {
		t.Fatalf("a done task reached the plan: %+v %+v", plan.Blocks, plan.Skipped)
	}
}

func TestPlanRejectsABadDay(t *testing.T) {
	if _, err := Plan(Options{Day: "21-09-2026", Prefs: prefs()}); err == nil {
		t.Fatal("want an error for a malformed day")
	}
}

// Blocks never overlap the time the calendar already had, which is the one
// guarantee the whole thing rests on.
func TestBlocksNeverOverlapBusyTime(t *testing.T) {
	busy := []calendar.Busy{
		{Start: at(saturday, "09:00"), End: at(saturday, "10:30")},
		{Start: at(saturday, "14:00"), End: at(saturday, "15:00")},
	}
	var tasks []*store.Task
	for i := 1; i <= 8; i++ {
		tasks = append(tasks, task(int64(i), "t", est(30)))
	}
	p := prefs()
	p.MaxMinutesDay = 600
	plan, err := Plan(Options{Day: saturday, Prefs: p, Tasks: tasks, Busy: busy})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blocks) == 0 {
		t.Fatal("nothing was planned, so the test proves nothing")
	}
	for _, b := range plan.Blocks {
		for _, z := range busy {
			if b.Start.Before(z.End) && z.Start.Before(b.End) {
				t.Errorf("%s at %s-%s overlaps busy %s-%s", b.Task.Title,
					b.Start.Format("15:04"), b.End.Format("15:04"),
					z.Start.Format("15:04"), z.End.Format("15:04"))
			}
		}
	}
}

// A day that opens before deep work begins must still put demanding work
// inside the window rather than merely near it, and must leave the time in
// front of it free for something else.
func TestDeepWorkStartsInsideItsWindow(t *testing.T) {
	p := prefs()
	p.DeepStart, p.DeepEnd = store.Clock{Hour: 8, Minute: 0}, store.Clock{Hour: 9, Minute: 0}
	plan, err := Plan(Options{
		Day:   monday,
		Prefs: p,
		Tasks: []*store.Task{task(1, "demanding", hard, est(60))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blocks) != 1 {
		t.Fatalf("blocks = %+v", plan.Blocks)
	}
	if got := plan.Blocks[0].Start.Format("15:04"); got != "08:00" {
		t.Fatalf("starts %s, want 08:00 inside the deep window", got)
	}
	if !strings.Contains(plan.Blocks[0].Reason, "deep-work") {
		t.Errorf("reason = %q", plan.Blocks[0].Reason)
	}
	// 07:00 to 07:50, the hour in front less the buffer, stays usable.
	if len(plan.Free) == 0 || plan.Free[0].Start.Format("15:04") != "07:00" {
		t.Fatalf("free = %v, want the time before the deep block kept", spans(plan.Free))
	}
}

// One too long for the window claims nothing and is placed normally, with a
// reason that does not pretend otherwise.
func TestDeepWorkTooLongForTheWindowClaimsNothing(t *testing.T) {
	p := prefs()
	p.DeepStart, p.DeepEnd = store.Clock{Hour: 8, Minute: 0}, store.Clock{Hour: 9, Minute: 0}
	plan, err := Plan(Options{
		Day:   monday,
		Prefs: p,
		Tasks: []*store.Task{task(1, "too long", hard, est(90))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Blocks[0].Start.Format("15:04"); got != "07:00" {
		t.Fatalf("starts %s, want the ordinary pass to place it", got)
	}
	if strings.Contains(plan.Blocks[0].Reason, "deep-work") {
		t.Errorf("reason = %q, want no deep-work claim", plan.Blocks[0].Reason)
	}
}

// The space in front of a deep block is real free time, so a later task can
// use it.
func TestTheGapBeforeDeepWorkGetsUsed(t *testing.T) {
	p := prefs()
	p.DeepStart, p.DeepEnd = store.Clock{Hour: 8, Minute: 0}, store.Clock{Hour: 9, Minute: 0}
	plan, err := Plan(Options{
		Day:   monday,
		Prefs: p,
		Tasks: []*store.Task{
			task(1, "demanding", hard, est(60)),
			task(2, "quick", est(20)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var quick *Block
	for i := range plan.Blocks {
		if plan.Blocks[i].Task.ID == 2 {
			quick = &plan.Blocks[i]
		}
	}
	if quick == nil {
		t.Fatal("the quick task was not placed")
	}
	if got := quick.Start.Format("15:04"); got != "07:00" {
		t.Fatalf("quick task starts %s, want the 07:00 gap used", got)
	}
}
