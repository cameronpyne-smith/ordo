// Package schedule turns a day's free time and the daemon's ordered task
// list into a plan for that day. It is deterministic: the same tasks, the
// same calendar and the same preferences always give the same blocks, and no
// language model is anywhere near it. A plan you cannot predict is a plan you
// end up arguing with.
//
// Every block carries the reason it exists, built from the fields that put it
// there, for the same reason the list view explains its order: a position you
// have to take on trust is one you stop trusting.
package schedule

import (
	"fmt"
	"sort"
	"time"

	"github.com/cameronpyne-smith/ordo/internal/calendar"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

// Window is a stretch of a day with nothing in it yet.
type Window struct {
	Start time.Time
	End   time.Time
}

func (w Window) Minutes() int { return int(w.End.Sub(w.Start) / time.Minute) }

// Block is one task placed at a time, with the reason it landed there. Left
// is set only when the block is a piece of a bigger task, and is what the
// task has left before it.
type Block struct {
	Task    *store.Task
	Start   time.Time
	End     time.Time
	Minutes int
	Left    int
	Reason  string
}

// Skip is a task that was a candidate and did not fit. Answering "why is that
// not in my day" matters as much as filling the day.
type Skip struct {
	Task   *store.Task
	Reason string
}

type Day struct {
	Date    string
	Blocks  []Block
	Busy    []calendar.Busy
	Free    []Window
	Skipped []Skip

	Planned int // minutes given to blocks
	Budget  int // minutes the preferences allowed
}

type Options struct {
	Day   string
	Tasks []*store.Task
	Busy  []calendar.Busy
	Prefs store.Preferences
}

// chunkFloor is the shortest piece of a big task worth starting. Under half
// an hour the sitting goes on getting back into it.
const chunkFloor = 30

// ask is what one day asks of a task. Most tasks are done in one go, so the
// day wants all of it and nothing less will do. A one-off with more left
// than one sitting is worked on a piece a day instead: a sitting's worth, or
// more when the days before the deadline are too few for that, and it will
// shrink into a shorter gap as long as the gap is worth starting in.
type ask struct {
	minutes int
	floor   int
	left    int
	catchUp bool
}

func askOf(day string, t *store.Task, p store.Preferences) ask {
	left := t.Left()
	if t.Recurring() || left <= p.MaxBlockMinutes {
		return ask{minutes: left, floor: left}
	}
	a := ask{minutes: p.MaxBlockMinutes, left: left}
	if due := t.Deadline(); due != "" {
		daysLeft := max(daysBetween(day, due)+1, 1)
		if need := (left + daysLeft - 1) / daysLeft; need > a.minutes {
			a.minutes, a.catchUp = need, true
		}
	}
	a.floor = min(chunkFloor, a.minutes)
	return a
}

// Plan places what it can of the day's candidates into the day's free time.
func Plan(o Options) (Day, error) {
	date, err := store.ParseDate(o.Day)
	if err != nil {
		return Day{}, fmt.Errorf("planning %q: %w", o.Day, store.ErrInvalid)
	}
	free := Windows(date, o.Prefs, o.Busy)
	// A plan for today starts now. Laying blocks into hours already gone
	// answers "what should I have done", which nobody asks, and a calendar
	// this is published to would show the morning's plan all afternoon.
	if now := store.Now(); o.Day == now.Format(store.DateFormat) {
		free = clip(free, ceil(now, clipGrain), o.Prefs.MinBlockMinutes)
	}
	day := Day{
		Date:   o.Day,
		Busy:   o.Busy,
		Budget: o.Prefs.MaxMinutesDay,
	}

	deep := Window{Start: o.Prefs.DeepStart.On(date), End: o.Prefs.DeepEnd.On(date)}
	budget := o.Prefs.MaxMinutesDay
	cands := candidates(o.Day, o.Tasks)

	buffer := time.Duration(o.Prefs.BufferMinutes) * time.Minute
	place := func(t *store.Task, a ask, minutes, i int, start time.Time, inDeep bool) {
		end := start.Add(time.Duration(minutes) * time.Minute)
		b := Block{Task: t, Start: start, End: end, Minutes: minutes, Reason: reason(o.Day, t, inDeep)}
		if a.left > minutes {
			b.Left = a.left
			b.Reason += " · " + pieceReason(a, o.Prefs)
		}
		day.Blocks = append(day.Blocks, b)
		day.Planned += minutes
		budget -= minutes
		free = reserve(free, i, start, end, buffer, o.Prefs.MinBlockMinutes)
	}

	// Demanding work claims the deep-work window first. Filling in list order
	// alone would let whatever happened to be next eat the morning, which is
	// the one placement people actually ask a scheduler for. Both passes walk
	// the same order, so the result is still fixed.
	done := map[int64]bool{}
	for _, t := range cands {
		if t.Difficulty != store.DifficultyHigh {
			continue
		}
		a := askOf(o.Day, t, o.Prefs)
		if a.minutes > budget {
			continue
		}
		i, start := fitDeep(free, a.minutes, deep)
		if i < 0 {
			continue
		}
		place(t, a, a.minutes, i, start, true)
		done[t.ID] = true
	}

	for _, t := range cands {
		if done[t.ID] {
			continue
		}
		a := askOf(o.Day, t, o.Prefs)
		want := min(a.minutes, budget)
		if want < a.floor {
			day.Skipped = append(day.Skipped, Skip{Task: t, Reason: skipReason(budget, a.floor, o.Prefs)})
			continue
		}
		i := fitAny(free, want)
		if i < 0 && a.floor < want {
			if j, room := longest(free); room >= a.floor {
				i, want = j, room
			}
		}
		if i < 0 {
			day.Skipped = append(day.Skipped, Skip{Task: t,
				Reason: fmt.Sprintf("no free stretch left that is %d minutes long", a.floor)})
			continue
		}
		place(t, a, want, i, free[i].Start, false)
	}

	sort.SliceStable(day.Blocks, func(i, j int) bool { return day.Blocks[i].Start.Before(day.Blocks[j].Start) })
	day.Free = free
	return day, nil
}

// candidates are the tasks eligible for this day, in the daemon's order with
// pins lifted to the front. A one-off task's due date is a deadline, so it is
// eligible every day until then, and the order puts it ahead of undated work
// as the date nears. A recurring task's due date is the occurrence itself:
// the bins go out on Tuesday, not on Monday because there was room, so it
// waits for its own day. An undated task may fill space on any day, which is
// what "someday" means in practice. A task that is waiting, on another task
// or for its start date, is not a candidate at all. A pin to a later day
// holds the task back for that day. A pin to this day wins over everything,
// waiting included: it is someone saying "today, regardless". A pin to a day
// that has gone is spent: the task was not done on the day it was meant
// for, so it goes back to its place in the order rather than vanishing from
// every plan after it.
func candidates(day string, tasks []*store.Task) []*store.Task {
	var pinned, rest []*store.Task
	for _, t := range tasks {
		if t.Status != store.StatusOpen {
			continue
		}
		switch {
		case t.PinnedFor(day):
			pinned = append(pinned, t)
		case t.PinnedOn > day:
			// Pinned to a later day; it belongs there, not here.
		case t.Waiting(day):
		case !t.Recurring() || t.Due == "" || t.Due <= day:
			rest = append(rest, t)
		}
	}
	return append(pinned, rest...)
}

// fitDeep finds where a demanding task can sit wholly inside the deep-work
// window, returning the window to take it from and the time to start. The
// block starts at the later of the free time and the deep window, so a day
// that opens before deep work begins still gets its morning used properly. A
// task that cannot fit inside the window entirely gains nothing from
// starting there and is left to the ordinary pass.
func fitDeep(free []Window, want int, deep Window) (int, time.Time) {
	length := time.Duration(want) * time.Minute
	for i, w := range free {
		start := w.Start
		if deep.Start.After(start) {
			start = deep.Start
		}
		end := start.Add(length)
		if !end.After(w.End) && !end.After(deep.End) && !start.Before(w.Start) {
			return i, start
		}
	}
	return -1, time.Time{}
}

// longest is the biggest free window, for a piece of work that can shrink to
// fit when nothing takes the whole of it.
func longest(free []Window) (int, int) {
	best, room := -1, 0
	for i, w := range free {
		if w.Minutes() > room {
			best, room = i, w.Minutes()
		}
	}
	return best, room
}

func fitAny(free []Window, want int) int {
	for i, w := range free {
		if w.Minutes() >= want {
			return i
		}
	}
	return -1
}

// reserve takes a block out of a window and gives back whatever is still
// usable either side of it, buffered. A block placed part way into a window
// leaves free time in front of it, which is what makes the deep-work window
// usable on a day that starts earlier than it does.
func reserve(free []Window, i int, start, end time.Time, buffer time.Duration, minBlock int) []Window {
	w := free[i]
	out := make([]Window, 0, len(free)+1)
	out = append(out, free[:i]...)
	if lead := (Window{Start: w.Start, End: start.Add(-buffer)}); lead.Minutes() >= minBlock {
		out = append(out, lead)
	}
	if tail := (Window{Start: end.Add(buffer), End: w.End}); tail.Minutes() >= minBlock {
		out = append(out, tail)
	}
	return append(out, free[i+1:]...)
}

func reason(day string, t *store.Task, inDeep bool) string {
	var parts []string
	due := t.Deadline()
	switch {
	case t.PinnedFor(day):
		parts = append(parts, "pinned to this day")
		if waits := t.WaitingOn(); len(waits) > 0 {
			parts = append(parts, "still waiting on "+ids(waits))
		}
	case due != "" && due < day:
		parts = append(parts, fmt.Sprintf("overdue by %s", days(daysBetween(due, day))))
	case due == day:
		parts = append(parts, "due today")
	case due != "":
		parts = append(parts, fmt.Sprintf("due in %s", days(daysBetween(day, due))))
	default:
		parts = append(parts, "nothing forced it, so the next one on the list")
	}
	if t.DueFor != 0 && !t.PinnedFor(day) {
		parts[len(parts)-1] += fmt.Sprintf(" so %d can follow in time", t.DueFor)
	}
	if t.Priority == store.PriorityHigh {
		parts = append(parts, "high priority")
	}
	if inDeep {
		parts = append(parts, "demanding, so it takes the deep-work window")
	}
	if t.EstimateMinutes == 0 {
		parts = append(parts, fmt.Sprintf("%d min guessed from its difficulty", t.Minutes()))
	}
	return join(parts)
}

// pieceReason says what a piece is a piece of, and when it is bigger than a
// sitting, that the deadline is why.
func pieceReason(a ask, p store.Preferences) string {
	out := fmt.Sprintf("%d min left", a.left)
	if a.catchUp {
		out += fmt.Sprintf(", more than the usual %d a day to make the deadline", p.MaxBlockMinutes)
	}
	return out
}

func skipReason(budget, want int, p store.Preferences) string {
	if budget <= 0 {
		return fmt.Sprintf("the day is already full at %d minutes", p.MaxMinutesDay)
	}
	return fmt.Sprintf("needs %d minutes and only %d of the day's %d are left", want, budget, p.MaxMinutesDay)
}

// Windows is the day's free time: the hours the day is available, less
// whatever the calendar says is taken, work included.
func Windows(date time.Time, p store.Preferences, busy []calendar.Busy) []Window {
	free := []Window{{Start: p.DayStart.On(date), End: p.DayEnd.On(date)}}
	for _, b := range busy {
		free = subtract(free, b.Start, b.End)
	}
	out := make([]Window, 0, len(free))
	for _, w := range free {
		if w.Minutes() >= p.MinBlockMinutes {
			out = append(out, w)
		}
	}
	return out
}

// clipGrain keeps a plan made mid-afternoon from starting at 15:03.
const clipGrain = 5 * time.Minute

// clip drops what is before from and shortens what straddles it, then
// applies the same floor Windows does, since a sliver of a window is no more
// usable for having been whole a moment ago.
func clip(windows []Window, from time.Time, minBlock int) []Window {
	out := make([]Window, 0, len(windows))
	for _, w := range windows {
		if !w.End.After(from) {
			continue
		}
		if w.Start.Before(from) {
			w.Start = from
		}
		if w.Minutes() >= minBlock {
			out = append(out, w)
		}
	}
	return out
}

func ceil(t time.Time, grain time.Duration) time.Time {
	r := t.Truncate(grain)
	if r.Before(t) {
		r = r.Add(grain)
	}
	return r
}

func subtract(windows []Window, start, end time.Time) []Window {
	out := make([]Window, 0, len(windows)+1)
	for _, w := range windows {
		switch {
		case !end.After(w.Start) || !start.Before(w.End):
			out = append(out, w)
		case !start.After(w.Start) && !end.Before(w.End):
			// Swallowed whole.
		case !start.After(w.Start):
			out = append(out, Window{Start: end, End: w.End})
		case !end.Before(w.End):
			out = append(out, Window{Start: w.Start, End: start})
		default:
			out = append(out, Window{Start: w.Start, End: start}, Window{Start: end, End: w.End})
		}
	}
	return out
}

func overlaps(a, b Window) bool { return a.Start.Before(b.End) && b.Start.Before(a.End) }

func daysBetween(from, to string) int {
	a, errA := store.ParseDate(from)
	b, errB := store.ParseDate(to)
	if errA != nil || errB != nil {
		return 0
	}
	return int(b.Sub(a).Hours() / 24)
}

func days(n int) string {
	if n == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", n)
}

func ids(deps []store.Dep) string {
	out := ""
	for i, d := range deps {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprint(d.ID)
	}
	return out
}

func join(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " · "
		}
		out += p
	}
	return out
}
