package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// edge is one dependency read back with both ends named: task waits on
// blocker.
type edge struct {
	task, blocker Dep
}

// annotate fills in what a task's row cannot say on its own: what it waits
// on, what waits on it, and the deadline those tasks pass down. It reads the
// whole graph every time, which at the size of one person's list is cheaper
// than keeping a copy of it right.
func (s *Store) annotate(tasks []*Task) error {
	if len(tasks) == 0 {
		return nil
	}
	if err := s.streaks(tasks); err != nil {
		return err
	}
	edges, err := s.edges()
	if err != nil {
		return err
	}
	for _, t := range tasks {
		t.BlockedBy, t.Blocks, t.EffectiveDue, t.DueFor = nil, nil, "", 0
	}
	if len(edges) == 0 {
		return nil
	}
	byID := map[int64]*Task{}
	for _, t := range tasks {
		byID[t.ID] = t
	}
	for _, e := range edges {
		if t, ok := byID[e.task.ID]; ok {
			t.BlockedBy = append(t.BlockedBy, e.blocker)
		}
		if t, ok := byID[e.blocker.ID]; ok && !e.task.Done {
			t.Blocks = append(t.Blocks, e.task)
		}
	}

	open, err := s.openTasks()
	if err != nil {
		return err
	}
	prefs, err := s.Preferences()
	if err != nil {
		return err
	}
	deadlines := passDown(open, edges, prefs.MaxBlockMinutes)
	for _, t := range tasks {
		if d, ok := deadlines[t.ID]; ok && t.Status == StatusOpen && d.date != t.Due {
			t.EffectiveDue, t.DueFor = d.date, d.via
		}
	}
	return nil
}

func (s *Store) edges() ([]edge, error) {
	rows, err := s.db.Query(`SELECT b.task_id, t.title, t.status, b.blocker_id, k.title, k.status
		FROM blockers b JOIN tasks t ON t.id = b.task_id JOIN tasks k ON k.id = b.blocker_id
		ORDER BY b.task_id, b.blocker_id`)
	if err != nil {
		return nil, fmt.Errorf("reading dependencies: %w", err)
	}
	defer rows.Close()
	var out []edge
	for rows.Next() {
		var e edge
		var taskStatus, blockerStatus string
		if err := rows.Scan(&e.task.ID, &e.task.Title, &taskStatus, &e.blocker.ID, &e.blocker.Title, &blockerStatus); err != nil {
			return nil, fmt.Errorf("reading dependencies: %w", err)
		}
		e.task.Done = taskStatus == string(StatusDone)
		e.blocker.Done = blockerStatus == string(StatusDone)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) openTasks() (map[int64]*Task, error) {
	rows, err := s.db.Query(`SELECT `+taskColumns+` FROM tasks WHERE status = ?`, string(StatusOpen))
	if err != nil {
		return nil, fmt.Errorf("reading open tasks: %w", err)
	}
	defer rows.Close()
	out := map[int64]*Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("reading open tasks: %w", err)
		}
		out[t.ID] = t
	}
	return out, rows.Err()
}

type deadline struct {
	date string
	via  int64
}

// passDown works out each open task's deadline once the tasks waiting on it
// have had their say. A blocker has to be finished early enough for what
// waits on it to be done in time, which is that task's own deadline less
// the days it needs at a sitting a day, and a whole chain passes its date
// down the same way. A blocker's own earlier date still wins.
func passDown(open map[int64]*Task, edges []edge, maxBlock int) map[int64]deadline {
	waitingOn := map[int64][]int64{}
	for _, e := range edges {
		if _, ok := open[e.task.ID]; ok {
			waitingOn[e.blocker.ID] = append(waitingOn[e.blocker.ID], e.task.ID)
		}
	}
	memo := map[int64]deadline{}
	visiting := map[int64]bool{}
	var of func(id int64) deadline
	of = func(id int64) deadline {
		if d, ok := memo[id]; ok {
			return d
		}
		t := open[id]
		d := deadline{date: t.Due}
		// A loop cannot be written, but a graph read back is trusted no
		// further than it has to be.
		if visiting[id] {
			return d
		}
		visiting[id] = true
		for _, dep := range waitingOn[id] {
			next := of(dep).date
			if next == "" {
				continue
			}
			by := addDays(next, -daysNeeded(open[dep], maxBlock))
			if d.date == "" || by < d.date {
				d = deadline{date: by, via: dep}
			}
		}
		visiting[id] = false
		memo[id] = d
		return d
	}
	for id := range open {
		of(id)
	}
	return memo
}

// daysNeeded is how many days a task takes at one sitting a day, and never
// less than one: finishing a blocker the day its dependent is due leaves no
// room for the dependent.
func daysNeeded(t *Task, maxBlock int) int {
	if maxBlock <= 0 {
		return 1
	}
	return max((t.Left()+maxBlock-1)/maxBlock, 1)
}

func addDays(date string, n int) string {
	d, err := ParseDate(date)
	if err != nil {
		return date
	}
	return d.AddDate(0, 0, n).Format(DateFormat)
}

// setBlockers replaces what a task waits on. Every rule is checked before
// anything is written, so a refused list leaves the old one in place.
func setBlockers(tx *sql.Tx, id int64, ids []int64) error {
	current, err := waitsOn(tx, id)
	if err != nil {
		return err
	}
	existing := map[int64]bool{}
	for _, b := range current {
		existing[b] = true
	}

	seen := map[int64]bool{}
	var keep []int64
	for _, b := range ids {
		if seen[b] {
			continue
		}
		seen[b] = true
		if b == id {
			return fmt.Errorf("a task cannot wait on itself: %w", ErrInvalid)
		}
		blocker, err := getTx(tx, b)
		if err != nil {
			return fmt.Errorf("waits on %d: there is no such task: %w", b, ErrInvalid)
		}
		if blocker.Recurring() {
			return fmt.Errorf("task %d repeats and is never finished, so nothing can wait on it: %w", b, ErrInvalid)
		}
		// One already waited on stays, done or not, so undoing it holds this
		// task up again; a new one has to be something still to do.
		if blocker.Status == StatusDone && !existing[b] {
			return fmt.Errorf("task %d is already done: %w", b, ErrInvalid)
		}
		if chain, err := loop(tx, id, b); err != nil {
			return err
		} else if chain != nil {
			return fmt.Errorf("that would go round in a loop: %s: %w", joinIDs(chain, " → "), ErrInvalid)
		}
		keep = append(keep, b)
	}

	if _, err := tx.Exec(`DELETE FROM blockers WHERE task_id = ?`, id); err != nil {
		return fmt.Errorf("setting what task %d waits on: %w", id, err)
	}
	for _, b := range keep {
		if _, err := tx.Exec(`INSERT INTO blockers (task_id, blocker_id) VALUES (?, ?)`, id, b); err != nil {
			return fmt.Errorf("setting what task %d waits on: %w", id, err)
		}
	}
	if _, err := tx.Exec(`UPDATE tasks SET updated_at = ? WHERE id = ?`, stamp(Now()), id); err != nil {
		return fmt.Errorf("setting what task %d waits on: %w", id, err)
	}
	return nil
}

// loop reports the chain that making id wait on blocker would close, from id
// round to id again, or nil when there is none. It follows what blocker
// already waits on; id's own list is the one being replaced, and the walk
// stops on reaching id before it would read it.
func loop(tx *sql.Tx, id, blocker int64) ([]int64, error) {
	parent := map[int64]int64{blocker: id}
	queue := []int64{blocker}
	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]
		next, err := waitsOn(tx, at)
		if err != nil {
			return nil, err
		}
		for _, n := range next {
			if n == id {
				chain := []int64{id}
				for c := at; c != id; c = parent[c] {
					chain = append([]int64{c}, chain...)
				}
				return append([]int64{id}, chain...), nil
			}
			if _, seen := parent[n]; !seen {
				parent[n] = at
				queue = append(queue, n)
			}
		}
	}
	return nil, nil
}

func waitsOn(tx *sql.Tx, id int64) ([]int64, error) {
	rows, err := tx.Query(`SELECT blocker_id FROM blockers WHERE task_id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("reading what task %d waits on: %w", id, err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var b int64
		if err := rows.Scan(&b); err != nil {
			return nil, fmt.Errorf("reading what task %d waits on: %w", id, err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func depIDs(deps []Dep) string {
	ids := make([]int64, 0, len(deps))
	for _, d := range deps {
		ids = append(ids, d.ID)
	}
	return joinIDs(ids, ", ")
}

func joinIDs(ids []int64, sep string) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprint(id))
	}
	return strings.Join(parts, sep)
}
