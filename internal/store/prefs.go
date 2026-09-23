package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Preferences are the shape of a day: when it is available, where hard work
// goes in it, and how much of it may be given away. What takes the day,
// work included, is the calendar's to say and not a preference: a job has
// lunch breaks, leave and late meetings that two clock times cannot
// describe. One row, typed columns rather than a key-value bag, because the
// set is small and fixed and every field wants validating the way a task's
// fields do.
type Preferences struct {
	DayStart Clock
	DayEnd   Clock

	DeepStart Clock
	DeepEnd   Clock

	BufferMinutes   int
	MinBlockMinutes int
	MaxMinutesDay   int
	// MaxBlockMinutes is one sitting. A one-off task with more left than
	// this is worked on a piece a day rather than waiting for a gap it could
	// never get.
	MaxBlockMinutes int
}

// DefaultPreferences is a waking day with deep work at the start of it, the
// hour most likely to be undisturbed.
func DefaultPreferences() Preferences {
	return Preferences{
		DayStart:        Clock{7, 0},
		DayEnd:          Clock{22, 0},
		DeepStart:       Clock{7, 0},
		DeepEnd:         Clock{9, 0},
		BufferMinutes:   10,
		MinBlockMinutes: 15,
		MaxMinutesDay:   240,
		MaxBlockMinutes: 60,
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

func (p *Preferences) validate() error {
	if !p.DayStart.Before(p.DayEnd) {
		return fmt.Errorf("day_start %s must be before day_end %s: %w", p.DayStart, p.DayEnd, ErrInvalid)
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
		{"max_block_minutes", p.MaxBlockMinutes},
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
	if p.MaxBlockMinutes == 0 {
		return fmt.Errorf("max_block_minutes must be at least 1: %w", ErrInvalid)
	}
	return nil
}

const prefColumns = `day_start, day_end, deep_start, deep_end,
	buffer_minutes, min_block_minutes, max_minutes_day, max_block_minutes`

// Preferences reads the single row, falling back to the defaults when the
// database has never been written to. A fresh install schedules sensibly
// before anyone has configured anything.
func (s *Store) Preferences() (Preferences, error) {
	var dayStart, dayEnd, deepStart, deepEnd string
	p := DefaultPreferences()
	err := s.db.QueryRow(`SELECT `+prefColumns+` FROM preferences WHERE id = 1`).Scan(
		&dayStart, &dayEnd, &deepStart, &deepEnd,
		&p.BufferMinutes, &p.MinBlockMinutes, &p.MaxMinutesDay, &p.MaxBlockMinutes)
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
		{deepStart, &p.DeepStart}, {deepEnd, &p.DeepEnd},
	} {
		c, err := ParseClock(f.text)
		if err != nil {
			return p, fmt.Errorf("reading preferences: %w", err)
		}
		*f.into = c
	}
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
		VALUES (1,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			day_start=excluded.day_start, day_end=excluded.day_end,
			deep_start=excluded.deep_start, deep_end=excluded.deep_end,
			buffer_minutes=excluded.buffer_minutes,
			min_block_minutes=excluded.min_block_minutes,
			max_minutes_day=excluded.max_minutes_day,
			max_block_minutes=excluded.max_block_minutes`,
		p.DayStart.String(), p.DayEnd.String(), p.DeepStart.String(), p.DeepEnd.String(),
		p.BufferMinutes, p.MinBlockMinutes, p.MaxMinutesDay, p.MaxBlockMinutes)
	if err != nil {
		return p, fmt.Errorf("saving preferences: %w", err)
	}
	return p, nil
}
