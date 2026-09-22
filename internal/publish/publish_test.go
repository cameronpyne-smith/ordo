package publish

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/cameronpyne-smith/ordo/internal/gcal"
	"github.com/cameronpyne-smith/ordo/internal/schedule"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

// memCal is a calendar in memory, enough to see what the publisher did to it.
type memCal struct {
	events   map[string]gcal.Event
	next     int
	inserted int
	updated  int
	deleted  int
}

func newMemCal() *memCal { return &memCal{events: map[string]gcal.Event{}} }

func (c *memCal) List(_ context.Context, from, to time.Time) ([]gcal.Event, error) {
	var out []gcal.Event
	for _, e := range c.events {
		if !e.Start.Before(from) && e.Start.Before(to) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (c *memCal) Insert(_ context.Context, e gcal.Event) (gcal.Event, error) {
	c.next++
	e.ID = fmt.Sprintf("e%d", c.next)
	c.events[e.ID] = e
	c.inserted++
	return e, nil
}

func (c *memCal) Update(_ context.Context, e gcal.Event) (gcal.Event, error) {
	if _, ok := c.events[e.ID]; !ok {
		return gcal.Event{}, fmt.Errorf("no event %s", e.ID)
	}
	c.events[e.ID] = e
	c.updated++
	return e, nil
}

func (c *memCal) Delete(_ context.Context, id string) error {
	if _, ok := c.events[id]; !ok {
		return fmt.Errorf("no event %s", id)
	}
	delete(c.events, id)
	c.deleted++
	return nil
}

func (c *memCal) forTask(id int64) []gcal.Event {
	var out []gcal.Event
	for _, e := range c.events {
		if e.Task == id {
			out = append(out, e)
		}
	}
	return out
}

// fixedPlan is a planner that always says the same thing, so the tests
// choose the plan and watch the calendar follow.
type fixedPlan struct{ day schedule.Day }

func (p fixedPlan) Plan(context.Context, string) (schedule.Day, error) { return p.day, nil }

var today = store.Today()

func at(clock string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", today+" "+clock, store.Location)
	if err != nil {
		panic(err)
	}
	return t
}

func block(id int64, title, start, end, reason string) schedule.Block {
	return schedule.Block{
		Task:   &store.Task{ID: id, Title: title, Status: store.StatusOpen},
		Start:  at(start),
		End:    at(end),
		Reason: reason,
	}
}

func day(blocks ...schedule.Block) schedule.Day {
	return schedule.Day{Date: today, Blocks: blocks}
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestSyncCreatesAnEventPerBlock(t *testing.T) {
	cal := newMemCal()
	p := New(cal, 1, quiet())
	plan := fixedPlan{day(
		block(1, "Move sofa into office", "17:30", "18:00", "nothing forced it"),
		block(2, "Call mum", "18:10", "18:25", "due today"),
	)}

	r, err := p.Sync(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if r != (Result{Created: 2}) {
		t.Fatalf("result = %+v", r)
	}
	sofa := cal.forTask(1)
	if len(sofa) != 1 {
		t.Fatalf("events for task 1 = %+v", sofa)
	}
	e := sofa[0]
	if e.Summary != "Move sofa into office" || e.Description != "nothing forced it" {
		t.Errorf("event = %+v, want the title as summary and the reason as description", e)
	}
	if !e.Start.Equal(at("17:30")) || !e.End.Equal(at("18:00")) {
		t.Errorf("event spans %s-%s", e.Start.Format("15:04"), e.End.Format("15:04"))
	}
}

// The second pass over an unchanged plan must be a no-op, or every pass
// would churn the calendar and every phone it syncs to.
func TestSyncLeavesAMatchingCalendarAlone(t *testing.T) {
	cal := newMemCal()
	p := New(cal, 1, quiet())
	plan := fixedPlan{day(block(1, "Move sofa into office", "17:30", "18:00", "nothing forced it"))}

	if _, err := p.Sync(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	r, err := p.Sync(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if r != (Result{Kept: 1}) {
		t.Fatalf("second pass = %+v, want everything kept", r)
	}
	if cal.inserted != 1 || cal.updated != 0 || cal.deleted != 0 {
		t.Fatalf("calendar saw %d inserts, %d updates, %d deletes", cal.inserted, cal.updated, cal.deleted)
	}
}

func TestSyncFollowsThePlan(t *testing.T) {
	cal := newMemCal()
	p := New(cal, 1, quiet())
	if _, err := p.Sync(context.Background(), fixedPlan{day(
		block(1, "moved", "17:30", "18:00", "first"),
		block(2, "finished", "18:10", "18:25", "first"),
	)}); err != nil {
		t.Fatal(err)
	}

	r, err := p.Sync(context.Background(), fixedPlan{day(
		block(1, "moved", "19:00", "19:30", "later"),
		block(3, "new", "19:40", "19:55", "first"),
	)})
	if err != nil {
		t.Fatal(err)
	}
	if r != (Result{Created: 1, Updated: 1, Removed: 1}) {
		t.Fatalf("result = %+v", r)
	}
	moved := cal.forTask(1)
	if len(moved) != 1 || !moved[0].Start.Equal(at("19:00")) || moved[0].Description != "later" {
		t.Errorf("task 1 = %+v, want it moved in place", moved)
	}
	if len(cal.forTask(2)) != 0 {
		t.Error("the finished task is still on the calendar")
	}
	if len(cal.forTask(3)) != 1 {
		t.Error("the new task is not on the calendar")
	}
}

// An event with no task property was made by a person. Whatever the plan
// says, it is not ordo's to touch.
func TestSyncLeavesHandMadeEventsAlone(t *testing.T) {
	cal := newMemCal()
	theirs, _ := cal.Insert(context.Background(), gcal.Event{
		Summary: "Dentist", Start: at("14:00"), End: at("14:30"),
	})
	p := New(cal, 1, quiet())

	r, err := p.Sync(context.Background(), fixedPlan{day()})
	if err != nil {
		t.Fatal(err)
	}
	if r != (Result{}) {
		t.Fatalf("result = %+v, want nothing done", r)
	}
	if got, ok := cal.events[theirs.ID]; !ok || got.Summary != "Dentist" {
		t.Fatalf("their event became %+v", got)
	}
}

// Two events for one task can only be a previous pass that died halfway.
// One is kept and brought into line, the other goes.
func TestSyncRemovesADuplicate(t *testing.T) {
	cal := newMemCal()
	for range 2 {
		cal.Insert(context.Background(), gcal.Event{Task: 1, Summary: "twice", Start: at("17:30"), End: at("18:00")})
	}
	p := New(cal, 1, quiet())

	r, err := p.Sync(context.Background(), fixedPlan{day(block(1, "twice", "17:30", "18:00", "once"))})
	if err != nil {
		t.Fatal(err)
	}
	if r.Removed != 1 || r.Created != 0 {
		t.Fatalf("result = %+v", r)
	}
	if got := cal.forTask(1); len(got) != 1 || got[0].Description != "once" {
		t.Fatalf("task 1 = %+v, want exactly one, corrected", got)
	}
}

func TestDesiredMarksAPinnedBlock(t *testing.T) {
	b := block(1, "Book the MOT", "17:30", "18:00", "pinned to today")
	b.Task.PinnedOn = today
	events := desired(day(b))
	if len(events) != 1 || !strings.HasPrefix(events[0].Summary, "📌 ") {
		t.Fatalf("events = %+v, want the pin shown", events)
	}
}

func TestNudgeNeverBlocksAndIsNilSafe(t *testing.T) {
	var none *Publisher
	none.Nudge()
	none.Run(context.Background(), fixedPlan{})

	p := New(newMemCal(), 1, quiet())
	for range 5 {
		p.Nudge()
	}
}

// Run publishes once before it waits for anything, so a daemon that has
// just started does not leave the calendar stale until the first nudge.
func TestRunPublishesAtStart(t *testing.T) {
	cal := newMemCal()
	p := New(cal, 1, quiet())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p.Run(ctx, fixedPlan{day(block(1, "first", "17:30", "18:00", "start"))})
	if cal.inserted != 1 {
		t.Fatalf("inserted %d, want the one block published before Run returned", cal.inserted)
	}
}

func TestNewInsistsOnAtLeastToday(t *testing.T) {
	if p := New(newMemCal(), 0, quiet()); p.days != 1 {
		t.Fatalf("days = %d", p.days)
	}
}
