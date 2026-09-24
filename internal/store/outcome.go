package store

import (
	"fmt"
	"strings"
	"time"
)

// A run of this many on time is worth saying has ended, where losing a
// short one is not news.
const brokenRunWorthSaying = 5

// Outcome is what finishing a task, or taking that back, changed beyond the
// task itself: what can be started now or waits again, the run a repeating
// task is on, and whether a whole chain of tasks, or everything that came
// out of a note, has just been finished. It is worked out here, once, so
// every client says the same thing.
type Outcome struct {
	Freed     []Freed
	WaitAgain []Dep
	Streak    int
	StreakWas int
	Chain     int
	Note      string
	NoteDone  int
}

// Freed is a task nothing open holds up any more. Start is set when it is
// still waiting for its start date.
type Freed struct {
	ID    int64
	Title string
	Start string
}

func (s *Store) doneOutcome(before, after *Task) (*Outcome, error) {
	if before.Status != StatusOpen {
		return nil, nil
	}
	if after.Recurring() {
		out := &Outcome{Streak: after.Streak}
		if after.Streak == 1 {
			was, err := s.runBefore(after.ID)
			if err != nil {
				return nil, err
			}
			if was >= brokenRunWorthSaying {
				out.StreakWas = was
			}
		}
		return out, nil
	}

	out := &Outcome{}
	today := Today()
	for _, d := range before.Blocks {
		w, err := s.Get(d.ID)
		if err != nil {
			return nil, err
		}
		if w.Status != StatusOpen || w.Blocked() {
			continue
		}
		f := Freed{ID: w.ID, Title: w.Title}
		if w.Start > today {
			f.Start = w.Start
		}
		out.Freed = append(out.Freed, f)
	}
	if len(after.BlockedBy) > 0 && len(after.Blocks) == 0 {
		size, err := s.chainTo(after.ID)
		if err != nil {
			return nil, err
		}
		if size >= 3 {
			out.Chain = size
		}
	}
	if after.MnemoSlug != "" {
		var total, open int
		if err := s.db.QueryRow(`SELECT COUNT(*), COUNT(*) FILTER (WHERE status = ?) FROM tasks
			WHERE mnemo_slug = ? AND COALESCE(recur_kind, '') = ''`, string(StatusOpen), after.MnemoSlug).
			Scan(&total, &open); err != nil {
			return nil, fmt.Errorf("counting the tasks from %s: %w", after.MnemoSlug, err)
		}
		if open == 0 && total >= 2 {
			out.Note, out.NoteDone = after.MnemoSlug, total
		}
	}
	return out, nil
}

// undoOutcome names what waits on the task again: the tasks it had released,
// which nothing else still open holds up.
func (s *Store) undoOutcome(before, after *Task) (*Outcome, error) {
	if before.Status != StatusDone || after.Status != StatusOpen {
		return nil, nil
	}
	out := &Outcome{}
	for _, d := range after.Blocks {
		w, err := s.Get(d.ID)
		if err != nil {
			return nil, err
		}
		only := true
		for _, b := range w.BlockedBy {
			if b.ID != after.ID && !b.Done {
				only = false
				break
			}
		}
		if only {
			out.WaitAgain = append(out.WaitAgain, Dep{ID: w.ID, Title: w.Title})
		}
	}
	return out, nil
}

// chainTo is the size of the chain that ends at id: the task and everything
// it waited on, directly or through others.
func (s *Store) chainTo(id int64) (int, error) {
	edges, err := s.edges()
	if err != nil {
		return 0, err
	}
	blockers := map[int64][]int64{}
	for _, e := range edges {
		blockers[e.task.ID] = append(blockers[e.task.ID], e.blocker.ID)
	}
	seen := map[int64]bool{id: true}
	queue := []int64{id}
	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]
		for _, b := range blockers[at] {
			if !seen[b] {
				seen[b] = true
				queue = append(queue, b)
			}
		}
	}
	return len(seen), nil
}

// streaks fills in the run each repeating task is on.
func (s *Store) streaks(tasks []*Task) error {
	var ids []any
	byID := map[int64]*Task{}
	for _, t := range tasks {
		t.Streak = 0
		if t.Recurring() {
			ids = append(ids, t.ID)
			byID[t.ID] = t
		}
	}
	if len(ids) == 0 {
		return nil
	}
	onTime, err := s.onTime(`task_id IN (?`+strings.Repeat(",?", len(ids)-1)+`)`, ids...)
	if err != nil {
		return err
	}
	for id, done := range onTime {
		byID[id].Streak = run(done)
	}
	return nil
}

// runBefore is the run a repeating task was on before its latest completion.
func (s *Store) runBefore(id int64) (int, error) {
	onTime, err := s.onTime(`task_id = ?`, id)
	if err != nil || len(onTime[id]) < 2 {
		return 0, err
	}
	return run(onTime[id][1:]), nil
}

// onTime reads whether each completion was on time, newest first by task:
// done on or before the day the occurrence was due. Doing Tuesday's chore on
// Monday evening counts, and a missed occurrence stays due until it is done,
// so it shows up as the next completion being late.
func (s *Store) onTime(where string, args ...any) (map[int64][]bool, error) {
	rows, err := s.db.Query(`SELECT task_id, done_at, COALESCE(due, '') FROM completions
		WHERE partial = 0 AND `+where+` ORDER BY task_id, done_at DESC, id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("reading streaks: %w", err)
	}
	defer rows.Close()
	out := map[int64][]bool{}
	for rows.Next() {
		var id int64
		var doneAt, due string
		if err := rows.Scan(&id, &doneAt, &due); err != nil {
			return nil, fmt.Errorf("reading streaks: %w", err)
		}
		day := parseStamp(doneAt).Format(DateFormat)
		out[id] = append(out[id], due == "" || day <= due)
	}
	return out, rows.Err()
}

// run counts back from the newest completion while each was on time, and
// takes in the late one the run started from.
func run(onTime []bool) int {
	n := 0
	for _, ok := range onTime {
		n++
		if !ok {
			break
		}
	}
	return n
}

// Logged is one completion or session on a day, named for showing what the
// day got done.
type Logged struct {
	TaskID  int64
	Title   string
	At      time.Time
	Minutes int
	Partial bool
}

// LoggedOn is everything done or worked on during day, in the order it
// happened.
func (s *Store) LoggedOn(day string) ([]Logged, error) {
	rows, err := s.db.Query(`SELECT c.task_id, t.title, c.done_at, COALESCE(c.minutes, 0), c.partial
		FROM completions c JOIN tasks t ON t.id = c.task_id
		WHERE substr(c.done_at, 1, 10) = ? ORDER BY c.done_at, c.id`, day)
	if err != nil {
		return nil, fmt.Errorf("reading what was done on %s: %w", day, err)
	}
	defer rows.Close()
	var out []Logged
	for rows.Next() {
		var l Logged
		var at string
		if err := rows.Scan(&l.TaskID, &l.Title, &at, &l.Minutes, &l.Partial); err != nil {
			return nil, fmt.Errorf("reading what was done on %s: %w", day, err)
		}
		l.At = parseStamp(at)
		out = append(out, l)
	}
	return out, rows.Err()
}
