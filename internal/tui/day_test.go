package tui

import (
	"strings"
	"testing"

	"github.com/cameronpyne-smith/ordo/internal/api"
)

func plan(blocks []api.Block, busy []api.Busy, mutate ...func(*api.TodayResponse)) *api.TodayResponse {
	p := &api.TodayResponse{Date: "2026-09-21", Blocks: blocks, Busy: busy, BudgetMinutes: 240}
	for _, m := range mutate {
		m(p)
	}
	return p
}

func block(start, end, title string, mutate ...func(*api.Block)) api.Block {
	b := api.Block{
		Task:    api.Task{ID: 1, Title: title, Status: "open", Enriched: true},
		Start:   start,
		End:     end,
		Minutes: 30,
		Reason:  "due today",
	}
	for _, m := range mutate {
		m(&b)
	}
	return b
}

// The timeline has to read top to bottom as the day happens, which means
// interleaving what is planned with what the calendar already had.
func TestDayEntriesInterleaveBlocksAndBusyTime(t *testing.T) {
	entries := dayEntries(plan(
		[]api.Block{
			block("07:00", "07:30", "early"),
			block("19:00", "19:30", "evening"),
		},
		[]api.Busy{{Start: "12:00", End: "13:00", Summary: "lunch"}},
	))
	var order []string
	for _, e := range entries {
		if e.block != nil {
			order = append(order, e.block.Task.Title)
			continue
		}
		order = append(order, e.busy.Summary)
	}
	if strings.Join(order, ",") != "early,lunch,evening" {
		t.Fatalf("order = %v", order)
	}
}

// The cursor is for acting on tasks, so it must never land on a meeting
// there is nothing to do about.
func TestDayCursorSkipsBusyTime(t *testing.T) {
	m := New(nil)
	m = m.applyDay(plan(
		[]api.Block{block("07:00", "07:30", "first"), block("19:00", "19:30", "second")},
		[]api.Busy{{Start: "12:00", End: "13:00", Summary: "lunch"}},
	), nil)

	if got, ok := m.dayCursorTask(); !ok || got.Title != "first" {
		t.Fatalf("cursor starts on %+v, want the first block", got)
	}
	m.dayCursor = stepDay(dayEntries(m.day), m.dayCursor, 1)
	got, ok := m.dayCursorTask()
	if !ok || got.Title != "second" {
		t.Fatalf("cursor = %+v, want the meeting stepped over", got)
	}
}

// A day with nothing but meetings has no task to select, and that must not
// crash the view or offer a phantom one.
func TestDayCursorOnADayOfMeetingsOnly(t *testing.T) {
	m := New(nil)
	m = m.applyDay(plan(nil, []api.Busy{{Start: "09:00", End: "17:00", Summary: "conference"}}), nil)
	if _, ok := m.dayCursorTask(); ok {
		t.Fatal("there is no task to act on")
	}
	m.width, m.height = 90, 24
	m.mode = modeDay
	if strings.TrimSpace(m.View()) == "" {
		t.Fatal("rendered nothing")
	}
}

func TestDayViewRendersWithinTheTerminal(t *testing.T) {
	m := New(nil)
	m.width, m.height = 90, 24
	m.mode = modeDay
	m = m.applyDay(plan(
		[]api.Block{
			block("07:00", "08:30", "Wooldridge chapter 2", func(b *api.Block) {
				b.Minutes = 90
				b.Task.Difficulty = "high"
				b.Reason = "due today · demanding, so it takes the deep-work window"
			}),
			block("18:00", "18:20", "Water the plants", func(b *api.Block) {
				b.Task.ID, b.Task.PinnedOn = 2, "2026-09-21"
				b.Minutes = 20
			}),
		},
		[]api.Busy{{Start: "12:30", End: "13:00", Summary: "call with the recruiter"}},
		func(p *api.TodayResponse) { p.PlannedMinutes = 110 },
	), nil)

	frame := m.View()
	if lines := len(strings.Split(frame, "\n")); lines > m.height {
		t.Errorf("rendered %d lines into a %d-line terminal", lines, m.height)
	}
	for _, want := range []string{"Wooldridge", "07:00-08:30", "call with the recruiter", "110 of 240"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame is missing %q:\n%s", want, frame)
		}
	}
	// The promise the list makes, kept in the day: the line underneath says
	// why the selected block is where it is.
	if !strings.Contains(frame, "deep-work window") {
		t.Errorf("the reason for the selected block is missing:\n%s", frame)
	}
}

// The view must render before the first response arrives, because the first
// frame goes out before the daemon has answered.
func TestDayViewBeforeThePlanArrives(t *testing.T) {
	m := New(nil)
	m.mode = modeDay
	m.width, m.height = 80, 24
	if !strings.Contains(m.View(), "ordo") {
		t.Fatal("rendered nothing usable while waiting")
	}
}

func TestDayNoteSaysWhatIsMissing(t *testing.T) {
	cases := []struct {
		name string
		plan *api.TodayResponse
		want string
	}{
		{"no feed", plan(nil, nil), "no calendar feed"},
		{"feed broken", plan(nil, nil, func(p *api.TodayResponse) {
			p.Calendar, p.CalendarError = true, "403 Forbidden"
		}), "unreadable"},
		{"one left out", plan(nil, nil, func(p *api.TodayResponse) {
			p.Calendar = true
			p.Skipped = []api.Skip{{Reason: "no room"}}
		}), "1 task did not fit"},
		{"several left out", plan(nil, nil, func(p *api.TodayResponse) {
			p.Calendar = true
			p.Skipped = []api.Skip{{Reason: "a"}, {Reason: "b"}}
		}), "2 tasks did not fit"},
		{"nothing to say", plan(nil, nil, func(p *api.TodayResponse) { p.Calendar = true }), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dayNote(c.plan)
			if c.want == "" && got != "" {
				t.Fatalf("note = %q, want nothing to say", got)
			}
			if !strings.Contains(got, c.want) {
				t.Fatalf("note = %q, want it to mention %q", got, c.want)
			}
		})
	}
}

// Completing something reshapes the day, so the cursor has to land on a real
// block after the plan comes back shorter.
func TestDayCursorSurvivesTheDayShrinking(t *testing.T) {
	m := New(nil)
	m = m.applyDay(plan([]api.Block{
		block("07:00", "07:30", "a"),
		block("08:00", "08:30", "b"),
		block("09:00", "09:30", "c"),
	}, nil), nil)
	m.dayCursor = 2

	m = m.applyDay(plan([]api.Block{block("07:00", "07:30", "a")}, nil), nil)
	if _, ok := m.dayCursorTask(); !ok {
		t.Fatalf("cursor = %d over %d entries, want a block", m.dayCursor, len(dayEntries(m.day)))
	}
}

// A block is a task like any other, so enter opens it from the day the way
// it does from the list, and esc comes back to the day, replanned.
func TestEnterOpensABlocksTaskAndEscReturnsToTheDay(t *testing.T) {
	m := New(nil)
	m.mode = modeDay
	m = m.applyDay(plan([]api.Block{
		block("17:00", "17:20", "Green Book problems", func(b *api.Block) { b.Task.ID = 7 }),
	}, nil), nil)

	m, _ = press(t, m, "enter")
	if m.mode != modeEdit || m.edit.ID != 7 {
		t.Fatalf("mode = %v, opened #%d; want task 7 open", m.mode, m.edit.ID)
	}
	m, cmd := press(t, m, "esc")
	if m.mode != modeDay {
		t.Fatalf("esc went to mode %v, want the day", m.mode)
	}
	if cmd == nil {
		t.Error("coming back did not replan the day")
	}
}
