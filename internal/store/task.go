package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cameronpyne-smith/ordo/internal/recur"

	// Windows has no system zoneinfo; embed it so Europe/London resolves
	// everywhere the binary is built.
	_ "time/tzdata"
)

var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid")
)

// Location is fixed: a single user in one timezone. Due dates are plain
// calendar dates interpreted here, not instants.
var Location = mustLoad("Europe/London")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(fmt.Sprintf("loading timezone %s: %v", name, err))
	}
	return loc
}

const DateFormat = "2006-01-02"

// Today is the current calendar date in Location.
func Today() string { return Now().Format(DateFormat) }

// Now is overridable in tests.
var Now = func() time.Time { return time.Now().In(Location) }

type Status string

const (
	StatusOpen Status = "open"
	StatusDone Status = "done"
)

// Difficulty is how hard a task is, not how long it takes. Empty means the
// model has not inferred one yet.
type Difficulty string

const (
	DifficultyLow    Difficulty = "low"
	DifficultyMedium Difficulty = "medium"
	DifficultyHigh   Difficulty = "high"
)

// QuickWinMinutes is the longest a quick win can take.
const QuickWinMinutes = 30

// QuickWin is about time, not difficulty: ten minutes of something hard is
// still ten minutes, and the last stretch of a big task you are already into
// is a quick win to finish off. Only when nothing says how long it takes does
// difficulty stand in, and then only low difficulty counts as short.
func QuickWin(d Difficulty, estimate, remaining int) bool {
	switch {
	case remaining > 0:
		return remaining <= QuickWinMinutes
	case estimate > 0:
		return estimate <= QuickWinMinutes
	}
	return d == DifficultyLow
}

// Priority is empty until the user or the model sets one; views treat empty
// as normal.
type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
)

type RecurKind string

const (
	RecurEvery RecurKind = "every"
	RecurAfter RecurKind = "after"
)

type Task struct {
	ID              int64
	Title           string
	Notes           string
	Status          Status
	Difficulty      Difficulty
	Priority        Priority
	EstimateMinutes int // 0 = unset
	// RemainingMinutes is what is left once work has started, 0 until then.
	// The estimate stays the first guess, so a finished task can be compared
	// with what it really took.
	RemainingMinutes int
	Due              string
	// Start holds a one-off task back from the plan until that day. The due
	// date stays the deadline.
	Start      string
	RecurKind  RecurKind
	RecurRule  string
	MnemoSlug  string
	MnemoTitle string
	PinnedOn   string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DoneAt     *time.Time
	EnrichedAt *time.Time

	// BlockedBy and Blocks are the task's dependencies either way round, and
	// EffectiveDue is the deadline it has once the tasks waiting on it have
	// passed theirs down, with DueFor the one that set it. All are read
	// alongside the task and never written through it; BlockedBy is written
	// only on Create, by ID.
	BlockedBy    []Dep
	Blocks       []Dep
	EffectiveDue string
	DueFor       int64

	// Streak is how many of a repeating task's occurrences in a row were done
	// on time, read alongside it. Outcome is set only on what finishing it,
	// or taking that back, returns.
	Streak  int
	Outcome *Outcome
}

// Dep is one end of a dependency, named so a view can say what is being
// waited on without looking it up.
type Dep struct {
	ID    int64
	Title string
	Done  bool
}

// Deadline is the date the task has to be done by: its own due date, or an
// earlier one passed down from a task waiting on it.
func (t *Task) Deadline() string {
	if t.EffectiveDue != "" {
		return t.EffectiveDue
	}
	return t.Due
}

// Overdue reports whether an open task's deadline has passed.
func (t *Task) Overdue() bool {
	return t.Status == StatusOpen && t.Deadline() != "" && t.Deadline() < Today()
}

// Blocked reports whether anything this task waits on is still open.
func (t *Task) Blocked() bool {
	for _, d := range t.BlockedBy {
		if !d.Done {
			return true
		}
	}
	return false
}

// Waiting reports whether the task cannot be worked on the given day: it is
// blocked, or it has not started yet.
func (t *Task) Waiting(day string) bool {
	return t.Blocked() || t.Start > day
}

// WaitingOn is what still holds the task up, in the order it was read.
func (t *Task) WaitingOn() []Dep {
	var out []Dep
	for _, d := range t.BlockedBy {
		if !d.Done {
			out = append(out, d)
		}
	}
	return out
}

// Estimates for a task the model has not measured, by difficulty. These are
// the numbers completion history is meant to replace once there is enough of
// it; until then they are a stated guess rather than a hidden one.
const (
	EstimateLow     = 15
	EstimateMedium  = 45
	EstimateHigh    = 90
	EstimateUnknown = 30
)

// Minutes is how long a task is taken to need: its estimate, or a guess from
// its difficulty.
func (t *Task) Minutes() int {
	if t.EstimateMinutes > 0 {
		return t.EstimateMinutes
	}
	switch t.Difficulty {
	case DifficultyLow:
		return EstimateLow
	case DifficultyMedium:
		return EstimateMedium
	case DifficultyHigh:
		return EstimateHigh
	}
	return EstimateUnknown
}

// Left is how much of a task is still to do: what was left after the last
// session on it, or all of it when none has been logged.
func (t *Task) Left() int {
	if t.RemainingMinutes > 0 {
		return t.RemainingMinutes
	}
	return t.Minutes()
}

// Recurring reports whether completing this task advances it rather than
// closing it.
func (t *Task) Recurring() bool { return t.RecurKind != "" }

// Started reports whether work has been logged against this task without
// finishing it.
func (t *Task) Started() bool { return t.RemainingMinutes > 0 }

// Linked reports whether this task points at a note in the vault.
func (t *Task) Linked() bool { return t.MnemoSlug != "" }

// PinnedFor reports whether this task was pinned to the given day. A pin
// names a date, so yesterday's never speaks for today.
func (t *Task) PinnedFor(day string) bool { return t.PinnedOn != "" && t.PinnedOn == day }

// EffectivePriority resolves the unset case for ordering and display.
func (t *Task) EffectivePriority() Priority {
	if t.Priority == "" {
		return PriorityNormal
	}
	return t.Priority
}

func (t *Task) validate() error {
	t.Title = strings.TrimSpace(t.Title)
	if t.Title == "" {
		return fmt.Errorf("title is required: %w", ErrInvalid)
	}
	if err := validStatus(t.Status); err != nil {
		return err
	}
	if err := validDifficulty(t.Difficulty); err != nil {
		return err
	}
	if err := validPriority(t.Priority); err != nil {
		return err
	}
	if err := validDue(t.Due); err != nil {
		return err
	}
	if t.Start != "" {
		if _, err := ParseDate(t.Start); err != nil {
			return fmt.Errorf("start %q must be YYYY-MM-DD: %w", t.Start, ErrInvalid)
		}
		if t.RecurKind != "" {
			return fmt.Errorf("a repeating task has no start date: its next date already says when it comes up: %w", ErrInvalid)
		}
		if t.Due != "" && t.Start > t.Due {
			return fmt.Errorf("start %s is after the due date %s: %w", t.Start, t.Due, ErrInvalid)
		}
	}
	if t.EstimateMinutes < 0 {
		return fmt.Errorf("estimate_minutes must be positive: %w", ErrInvalid)
	}
	if t.RemainingMinutes < 0 {
		return fmt.Errorf("remaining_minutes must be positive: %w", ErrInvalid)
	}
	if t.PinnedOn != "" {
		if _, err := ParseDate(t.PinnedOn); err != nil {
			return fmt.Errorf("pinned_on %q must be YYYY-MM-DD: %w", t.PinnedOn, ErrInvalid)
		}
	}
	t.MnemoSlug = strings.TrimSpace(t.MnemoSlug)
	t.MnemoTitle = strings.TrimSpace(t.MnemoTitle)
	// The remembered description exists only to find the note again after a
	// rename, so it goes when the link goes.
	if t.MnemoSlug == "" {
		t.MnemoTitle = ""
	}
	switch t.RecurKind {
	case "", RecurEvery, RecurAfter:
	default:
		return fmt.Errorf("recur_kind %q must be every or after: %w", t.RecurKind, ErrInvalid)
	}
	if t.RecurKind == "" && strings.TrimSpace(t.RecurRule) != "" {
		return fmt.Errorf("recur_rule needs a recur_kind: %w", ErrInvalid)
	}
	if t.RecurKind != "" {
		rule, err := recur.Normalise(string(t.RecurKind), t.RecurRule)
		if err != nil {
			return fmt.Errorf("%v: %w", err, ErrInvalid)
		}
		t.RecurRule = rule
	}
	return t.ensureDue()
}

// ensureDue gives a recurring task a due date from its own schedule. A
// recurring task without one would sort with the undated and never come up,
// which is the opposite of what a chore is for.
func (t *Task) ensureDue() error {
	if !t.Recurring() || t.Due != "" {
		return nil
	}
	due, err := recur.First(string(t.RecurKind), t.RecurRule, Today())
	if err != nil {
		return fmt.Errorf("%v: %w", err, ErrInvalid)
	}
	t.Due = due
	return nil
}

func validStatus(s Status) error {
	switch s {
	case StatusOpen, StatusDone:
		return nil
	}
	return fmt.Errorf("status %q must be open or done: %w", s, ErrInvalid)
}

func validDifficulty(d Difficulty) error {
	switch d {
	case "", DifficultyLow, DifficultyMedium, DifficultyHigh:
		return nil
	}
	return fmt.Errorf("difficulty %q must be low, medium or high: %w", d, ErrInvalid)
}

func validPriority(p Priority) error {
	switch p {
	case "", PriorityLow, PriorityNormal, PriorityHigh:
		return nil
	}
	return fmt.Errorf("priority %q must be low, normal or high: %w", p, ErrInvalid)
}

func validDue(due string) error {
	if due == "" {
		return nil
	}
	if _, err := ParseDate(due); err != nil {
		return fmt.Errorf("due %q must be YYYY-MM-DD: %w", due, ErrInvalid)
	}
	return nil
}

// typedDates are the ways a date is written by hand in the UK: day first,
// with dashes, slashes or dots, and a two- or four-digit year. The stored
// form is tried first, so an ISO date is never read as anything else.
var typedDates = []string{DateFormat, "2-1-06", "2/1/06", "2.1.06", "2-1-2006", "2/1/2006", "2.1.2006"}

// ReadDate turns a date someone typed into the stored form. It belongs to
// the surfaces a person types into; the API itself only ever takes
// YYYY-MM-DD, so there is one form on the wire and in the database.
func ReadDate(typed string) (string, error) {
	typed = strings.TrimSpace(typed)
	if typed == "" {
		return "", nil
	}
	for _, layout := range typedDates {
		if t, err := time.ParseInLocation(layout, typed, Location); err == nil {
			return t.Format(DateFormat), nil
		}
	}
	return "", fmt.Errorf("%q is not a date: write it as 25/09/26 or 2026-09-25: %w", typed, ErrInvalid)
}

// ParseDate reads a plain calendar date in Location.
func ParseDate(date string) (time.Time, error) {
	return time.ParseInLocation(DateFormat, date, Location)
}
