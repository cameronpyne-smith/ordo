package api

import (
	"time"

	"github.com/cameronpyne-smith/ordo/internal/store"
)

func FromTask(t *store.Task) Task {
	out := Task{
		ID:              t.ID,
		Title:           t.Title,
		Notes:           t.Notes,
		Status:          string(t.Status),
		Difficulty:      string(t.Difficulty),
		Priority:        string(t.Priority),
		EstimateMinutes: t.EstimateMinutes,
		Due:             t.Due,
		Overdue:         t.Overdue(),
		CreatedAt:       t.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       t.UpdatedAt.Format(time.RFC3339),
		Enriched:        t.EnrichedAt != nil,
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
	return e
}
