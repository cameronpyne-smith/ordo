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
	Due             string
	RecurKind       RecurKind
	RecurRule       string
	MnemoSlug       string
	MnemoTitle      string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DoneAt          *time.Time
	EnrichedAt      *time.Time
}

// Overdue reports whether an open task's due date has passed.
func (t *Task) Overdue() bool {
	return t.Status == StatusOpen && t.Due != "" && t.Due < Today()
}

// Recurring reports whether completing this task advances it rather than
// closing it.
func (t *Task) Recurring() bool { return t.RecurKind != "" }

// Linked reports whether this task points at a note in the vault.
func (t *Task) Linked() bool { return t.MnemoSlug != "" }

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
	if t.EstimateMinutes < 0 {
		return fmt.Errorf("estimate_minutes must be positive: %w", ErrInvalid)
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

// ParseDate reads a plain calendar date in Location.
func ParseDate(date string) (time.Time, error) {
	return time.ParseInLocation(DateFormat, date, Location)
}
