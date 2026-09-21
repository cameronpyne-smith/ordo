package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Preferences are the shape of a day: when it is available, when work has it,
// and how much of it may be given away. One row, typed columns rather than a
// key-value bag, because the set is small and fixed and every field wants
// validating the way a task's fields do.
type Preferences struct {
	DayStart  Clock
	DayEnd    Clock
	WorkStart Clock
	WorkEnd   Clock
	WorkDays  Weekdays

	DeepStart Clock
	DeepEnd   Clock

	BufferMinutes   int
	MinBlockMinutes int
	MaxMinutesDay   int
}

// DefaultPreferences is a UK working week with the evening and the morning
// either side of it. Deep work lands in the morning because that is where an
// undisturbed hour actually exists on a weekday.
func DefaultPreferences() Preferences {
	return Preferences{
		DayStart:        Clock{7, 0},
		DayEnd:          Clock{22, 0},
		WorkStart:       Clock{9, 0},
		WorkEnd:         Clock{17, 30},
		WorkDays:        Weekdays{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday},
		DeepStart:       Clock{7, 0},
		DeepEnd:         Clock{9, 0},
		BufferMinutes:   10,
		MinBlockMinutes: 15,
		MaxMinutesDay:   240,
	}
}

// Clock is a time of day with no date attached, which is what a preference
// about "mornings" means.
type Clock struct {
	Hour   int
	Minute int
}

const ClockFormat = "15:04"

func ParseClock(s string) (Clock, error) {
	t, err := time.Parse(ClockFormat, strings.TrimSpace(s))
	if err != nil {
		return Clock{}, fmt.Errorf("time %q must be HH:MM: %w", s, ErrInvalid)
	}
	return Clock{t.Hour(), t.Minute()}, nil
}

func (c Clock) String() string { return fmt.Sprintf("%02d:%02d", c.Hour, c.Minute) }

// Minutes is the clock as minutes past midnight, which is the only arithmetic
// anything here does with it.
func (c Clock) Minutes() int { return c.Hour*60 + c.Minute }

// On places the clock on a calendar day in Location.
func (c Clock) On(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), c.Hour, c.Minute, 0, 0, Location)
}

func (c Clock) Before(other Clock) bool { return c.Minutes() < other.Minutes() }

// Weekdays is a set of days, stored and shown as "mon,tue" in the same
// spelling the recurrence grammar uses.
type Weekdays []time.Weekday

var weekdayNames = map[string]time.Weekday{
	"mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday,
	"fri": time.Friday, "sat": time.Saturday, "sun": time.Sunday,
}

func ParseWeekdays(s string) (Weekdays, error) {
	s = strings.ToLower(strings.Join(strings.Fields(s), ""))
	if s == "" {
		return nil, nil
	}
	seen := map[time.Weekday]bool{}
	var out Weekdays
	for _, name := range strings.Split(s, ",") {
		day, ok := weekdayNames[name]
		if !ok {
			return nil, fmt.Errorf("day %q must be one of mon, tue, wed, thu, fri, sat, sun: %w", name, ErrInvalid)
		}
		if !seen[day] {
			seen[day] = true
			out = append(out, day)
		}
	}
	sort.Slice(out, func(i, j int) bool { return weekIndex(out[i]) < weekIndex(out[j]) })
	return out, nil
}

func (w Weekdays) String() string {
	names := make([]string, 0, len(w))
	for _, day := range w {
		names = append(names, strings.ToLower(day.String()[:3]))
	}
	return strings.Join(names, ",")
}

func (w Weekdays) Contains(day time.Weekday) bool {
	for _, d := range w {
		if d == day {
			return true
		}
	}
	return false
}

// weekIndex orders a week Monday first, as the rules read.
func weekIndex(d time.Weekday) int { return (int(d) + 6) % 7 }

func (p *Preferences) validate() error {
	if !p.DayStart.Before(p.DayEnd) {
		return fmt.Errorf("day_start %s must be before day_end %s: %w", p.DayStart, p.DayEnd, ErrInvalid)
	}
	if !p.WorkStart.Before(p.WorkEnd) {
		return fmt.Errorf("work_start %s must be before work_end %s: %w", p.WorkStart, p.WorkEnd, ErrInvalid)
	}
	// Deep work is a preference about where inside the day hard things go, so
	// a window outside the day could never be honoured.
	if !p.DeepStart.Before(p.DeepEnd) {
		return fmt.Errorf("deep_start %s must be before deep_end %s: %w", p.DeepStart, p.DeepEnd, ErrInvalid)
	}
	if p.DeepStart.Minutes() < p.DayStart.Minutes() || p.DeepEnd.Minutes() > p.DayEnd.Minutes() {
		return fmt.Errorf("deep work %s-%s must sit inside the day %s-%s: %w",
			p.DeepStart, p.DeepEnd, p.DayStart, p.DayEnd, ErrInvalid)
	}
	for _, f := range []struct {
		name  string
		value int
	}{
		{"buffer_minutes", p.BufferMinutes},
		{"min_block_minutes", p.MinBlockMinutes},
		{"max_minutes_per_day", p.MaxMinutesDay},
	} {
		if f.value < 0 {
			return fmt.Errorf("%s must not be negative: %w", f.name, ErrInvalid)
		}
	}
	if p.MinBlockMinutes == 0 {
		return fmt.Errorf("min_block_minutes must be at least 1: %w", ErrInvalid)
	}
	if p.MaxMinutesDay == 0 {
		return fmt.Errorf("max_minutes_per_day must be at least 1: %w", ErrInvalid)
	}
	return nil
}

const prefColumns = `day_start, day_end, work_start, work_end, work_days, deep_start, deep_end,
	buffer_minutes, min_block_minutes, max_minutes_day`

// Preferences reads the single row, falling back to the defaults when the
// database has never been written to. A fresh install schedules sensibly
// before anyone has configured anything.
func (s *Store) Preferences() (Preferences, error) {
	var dayStart, dayEnd, workStart, workEnd, workDays, deepStart, deepEnd string
	p := DefaultPreferences()
	err := s.db.QueryRow(`SELECT `+prefColumns+` FROM preferences WHERE id = 1`).Scan(
		&dayStart, &dayEnd, &workStart, &workEnd, &workDays, &deepStart, &deepEnd,
		&p.BufferMinutes, &p.MinBlockMinutes, &p.MaxMinutesDay)
	if err == sql.ErrNoRows {
		return DefaultPreferences(), nil
	}
	if err != nil {
		return p, fmt.Errorf("reading preferences: %w", err)
	}
	for _, f := range []struct {
		text string
		into *Clock
	}{
		{dayStart, &p.DayStart}, {dayEnd, &p.DayEnd},
		{workStart, &p.WorkStart}, {workEnd, &p.WorkEnd},
		{deepStart, &p.DeepStart}, {deepEnd, &p.DeepEnd},
	} {
		c, err := ParseClock(f.text)
		if err != nil {
			return p, fmt.Errorf("reading preferences: %w", err)
		}
		*f.into = c
	}
	days, err := ParseWeekdays(workDays)
	if err != nil {
		return p, fmt.Errorf("reading preferences: %w", err)
	}
	p.WorkDays = days
	return p, nil
}

// SetPreferences replaces the row wholesale. Callers that mean to change one
// field read first and edit the struct, which keeps validation looking at a
// complete day rather than a half-applied one.
func (s *Store) SetPreferences(p Preferences) (Preferences, error) {
	if err := p.validate(); err != nil {
		return p, err
	}
	_, err := s.db.Exec(`INSERT INTO preferences (id, `+prefColumns+`)
		VALUES (1,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			day_start=excluded.day_start, day_end=excluded.day_end,
			work_start=excluded.work_start, work_end=excluded.work_end,
			work_days=excluded.work_days,
			deep_start=excluded.deep_start, deep_end=excluded.deep_end,
			buffer_minutes=excluded.buffer_minutes,
			min_block_minutes=excluded.min_block_minutes,
			max_minutes_day=excluded.max_minutes_day`,
		p.DayStart.String(), p.DayEnd.String(), p.WorkStart.String(), p.WorkEnd.String(),
		p.WorkDays.String(), p.DeepStart.String(), p.DeepEnd.String(),
		p.BufferMinutes, p.MinBlockMinutes, p.MaxMinutesDay)
	if err != nil {
		return p, fmt.Errorf("saving preferences: %w", err)
	}
	return p, nil
}
