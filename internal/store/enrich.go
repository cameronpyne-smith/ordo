package store

import (
	"fmt"
)

// Inference is what the model read out of a task's own words. Every field is
// optional, and each is applied only where the task has nothing set already:
// a value the user or Claude chose outranks a guess, and keeping that rule
// here rather than in the worker means no caller can forget it.
type Inference struct {
	Difficulty Difficulty
	Priority   Priority
	Estimate   int
	Due        string
	Start      string
	RecurKind  RecurKind
	RecurRule  string
}

func (i Inference) Empty() bool {
	return i.Difficulty == "" && i.Priority == "" && i.Estimate == 0 &&
		i.Due == "" && i.Start == "" && i.RecurKind == ""
}

// Enrich applies an inference and marks the task enriched. The mark is
// written even when every field was already set, so a task the model had
// nothing to add to is not queued again on every restart.
func (s *Store) Enrich(id int64, in Inference) (*Task, error) {
	t, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if in.Difficulty != "" && t.Difficulty == "" {
		t.Difficulty = in.Difficulty
	}
	if in.Priority != "" && t.Priority == "" {
		t.Priority = in.Priority
	}
	if in.Estimate > 0 && t.EstimateMinutes == 0 {
		t.EstimateMinutes = in.Estimate
	}
	// A start date and a schedule cannot both be set, and one the user gave
	// outranks either guess.
	if in.RecurKind != "" && !t.Recurring() && t.Start == "" {
		t.RecurKind, t.RecurRule = in.RecurKind, in.RecurRule
		// A schedule knows its own first date better than the model does, so
		// an inferred rule computes the due rather than taking the inferred
		// one. A date the user typed is still left alone below.
		in.Due = ""
	}
	if in.Due != "" && t.Due == "" {
		t.Due = in.Due
	}
	// A start is only kept where it could have been typed: on a one-off, and
	// not after the deadline. Anything else is a misreading, and dropping it
	// keeps the rest of the answer.
	if in.Start != "" && t.Start == "" && !t.Recurring() && (t.Due == "" || in.Start <= t.Due) {
		t.Start = in.Start
	}
	if err := t.validate(); err != nil {
		return nil, fmt.Errorf("enriching task %d: %w", id, err)
	}
	now := Now()
	t.EnrichedAt = &now
	return s.save(t)
}

// Unenriched lists the open tasks the worker has never reached. A daemon
// started while ollama was down writes bare rows; this is how the next start
// picks them up instead of leaving them bare for good.
func (s *Store) Unenriched(limit int) ([]int64, error) {
	query := `SELECT id FROM tasks WHERE enriched_at IS NULL AND status = ? ORDER BY id`
	args := []any{string(StatusOpen)}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing unenriched tasks: %w", err)
	}
	defer rows.Close()

	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("listing unenriched tasks: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
