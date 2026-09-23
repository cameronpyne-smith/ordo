package api

import (
	"time"

	"github.com/cameronpyne-smith/ordo/internal/schedule"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

func FromTask(t *store.Task) Task {
	out := Task{
		ID:               t.ID,
		Title:            t.Title,
		Notes:            t.Notes,
		Status:           string(t.Status),
		Difficulty:       string(t.Difficulty),
		Priority:         string(t.Priority),
		EstimateMinutes:  t.EstimateMinutes,
		RemainingMinutes: t.RemainingMinutes,
		Due:              t.Due,
		Overdue:          t.Overdue(),
		PinnedOn:         t.PinnedOn,
		CreatedAt:        t.CreatedAt.Format(time.RFC3339),
		UpdatedAt:        t.UpdatedAt.Format(time.RFC3339),
		Enriched:         t.EnrichedAt != nil,
	}
	if t.Recurring() {
		out.Recur = &Recur{Kind: string(t.RecurKind), Rule: t.RecurRule}
	}
	if t.MnemoSlug != "" {
		out.Mnemo = &Link{Slug: t.MnemoSlug, Title: t.MnemoTitle}
	}
	if t.DoneAt != nil {
		out.DoneAt = t.DoneAt.Format(time.RFC3339)
	}
	return out
}

func FromTasks(tasks []*store.Task) []Task {
	out := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, FromTask(t))
	}
	return out
}

func (r EditRequest) Edit() store.Edit {
	var e store.Edit
	e.Title = r.Title
	e.Notes = r.Notes
	e.Due = r.Due
	e.Estimate = r.EstimateMinutes
	e.Remaining = r.RemainingMinutes
	if r.Status != nil {
		s := store.Status(*r.Status)
		e.Status = &s
	}
	if r.Difficulty != nil {
		d := store.Difficulty(*r.Difficulty)
		e.Difficulty = &d
	}
	if r.Priority != nil {
		p := store.Priority(*r.Priority)
		e.Priority = &p
	}
	if r.RecurKind != nil {
		k := store.RecurKind(*r.RecurKind)
		e.RecurKind = &k
	}
	e.RecurRule = r.RecurRule
	return e
}

func FromPreferences(p store.Preferences) Preferences {
	return Preferences{
		DayStart:        p.DayStart.String(),
		DayEnd:          p.DayEnd.String(),
		DeepStart:       p.DeepStart.String(),
		DeepEnd:         p.DeepEnd.String(),
		BufferMinutes:   p.BufferMinutes,
		MinBlockMinutes: p.MinBlockMinutes,
		MaxMinutesDay:   p.MaxMinutesDay,
		MaxBlockMinutes: p.MaxBlockMinutes,
	}
}

// Apply lays a partial request over the preferences already stored, so the
// daemon validates one complete day rather than a field in isolation.
func (r PreferencesRequest) Apply(p store.Preferences) (store.Preferences, error) {
	for _, f := range []struct {
		value *string
		into  *store.Clock
	}{
		{r.DayStart, &p.DayStart}, {r.DayEnd, &p.DayEnd},
		{r.DeepStart, &p.DeepStart}, {r.DeepEnd, &p.DeepEnd},
	} {
		if f.value == nil {
			continue
		}
		c, err := store.ParseClock(*f.value)
		if err != nil {
			return p, err
		}
		*f.into = c
	}
	for _, f := range []struct {
		value *int
		into  *int
	}{
		{r.BufferMinutes, &p.BufferMinutes},
		{r.MinBlockMinutes, &p.MinBlockMinutes},
		{r.MaxMinutesDay, &p.MaxMinutesDay},
		{r.MaxBlockMinutes, &p.MaxBlockMinutes},
	} {
		if f.value != nil {
			*f.into = *f.value
		}
	}
	return p, nil
}

// Empty reports whether the request would change nothing.
func (r PreferencesRequest) Empty() bool {
	return r.DayStart == nil && r.DayEnd == nil && r.DeepStart == nil && r.DeepEnd == nil && r.BufferMinutes == nil &&
		r.MinBlockMinutes == nil && r.MaxMinutesDay == nil && r.MaxBlockMinutes == nil
}

func FromDay(d schedule.Day, hasCalendar bool, calErr string) TodayResponse {
	out := TodayResponse{
		Date:           d.Date,
		Blocks:         make([]Block, 0, len(d.Blocks)),
		PlannedMinutes: d.Planned,
		BudgetMinutes:  d.Budget,
		Calendar:       hasCalendar,
		CalendarError:  calErr,
	}
	for _, b := range d.Blocks {
		out.Blocks = append(out.Blocks, Block{
			Task:    FromTask(b.Task),
			Start:   clock(b.Start),
			End:     clock(b.End),
			Minutes: b.Minutes,
			Left:    b.Left,
			Reason:  b.Reason,
		})
	}
	for _, b := range d.Busy {
		out.Busy = append(out.Busy, Busy{Start: clock(b.Start), End: clock(b.End), Summary: b.Summary})
	}
	for _, w := range d.Free {
		out.Free = append(out.Free, Window{Start: clock(w.Start), End: clock(w.End), Minutes: w.Minutes()})
	}
	for _, s := range d.Skipped {
		out.Skipped = append(out.Skipped, Skip{Task: FromTask(s.Task), Reason: s.Reason})
	}
	return out
}

// clock is how a time is shown in a day view: one user, one timezone, so a
// wall clock says everything an instant would.
func clock(t time.Time) string { return t.In(store.Location).Format(store.ClockFormat) }
