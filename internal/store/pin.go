package store

import "fmt"

// Pin marks a task as wanted on a particular day. It is the one input to the
// schedule that the daemon's ordering cannot work out for itself: "whatever
// the list says, I am doing this one today."
func (s *Store) Pin(id int64, day string) (*Task, error) {
	if day == "" {
		day = Today()
	}
	if _, err := ParseDate(day); err != nil {
		return nil, fmt.Errorf("pin: day %q must be YYYY-MM-DD: %w", day, ErrInvalid)
	}
	t, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if t.Status == StatusDone {
		return nil, fmt.Errorf("pin: task %d is already done: %w", id, ErrInvalid)
	}
	t.PinnedOn = day
	return s.save(t)
}

func (s *Store) Unpin(id int64) (*Task, error) {
	t, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	t.PinnedOn = ""
	return s.save(t)
}

// Pinned lists the tasks pinned to a day, in the daemon's usual order. The
// packer places these before it considers anything else.
func (s *Store) Pinned(day string) ([]*Task, error) {
	if _, err := ParseDate(day); err != nil {
		return nil, fmt.Errorf("pinned: day %q must be YYYY-MM-DD: %w", day, ErrInvalid)
	}
	rows, err := s.db.Query(`SELECT `+taskColumns+` FROM tasks WHERE pinned_on = ? AND status = ?`,
		day, string(StatusOpen))
	if err != nil {
		return nil, fmt.Errorf("listing pinned tasks: %w", err)
	}
	defer rows.Close()

	tasks := []*Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("listing pinned tasks: %w", err)
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing pinned tasks: %w", err)
	}
	Sort(tasks)
	return tasks, nil
}
