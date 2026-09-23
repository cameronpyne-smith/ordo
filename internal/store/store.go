package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/cameronpyne-smith/ordo/internal/recur"
)

type Store struct {
	db       *sql.DB
	snapshot string
}

// Open prepares the database file, applying any outstanding migrations. The
// pool is capped at one connection: a single writer is the only shape this
// daemon has, and it removes every lock-contention question at a scale where
// serialised reads cost nothing.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.migrate(path); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// MigrationSnapshot is where the database was copied to before this open
// migrated it, or empty when nothing needed migrating.
func (s *Store) MigrationSnapshot() string { return s.snapshot }

var migrations = []string{
	`CREATE TABLE tasks (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		title            TEXT NOT NULL,
		notes            TEXT,
		status           TEXT NOT NULL DEFAULT 'open',
		difficulty       TEXT,
		priority         TEXT,
		estimate_minutes INTEGER,
		due              TEXT,
		recur_kind       TEXT,
		recur_rule       TEXT,
		mnemo_slug       TEXT,
		mnemo_title      TEXT,
		created_at       TEXT NOT NULL,
		updated_at       TEXT NOT NULL,
		done_at          TEXT,
		enriched_at      TEXT
	);
	CREATE TABLE completions (
		id       INTEGER PRIMARY KEY,
		task_id  INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
		done_at  TEXT NOT NULL,
		minutes  INTEGER
	);
	CREATE INDEX tasks_status_due ON tasks(status, due);
	CREATE INDEX completions_task ON completions(task_id);`,

	// Recording the due date an occurrence had makes undo exact rather than
	// derived, and turns the history into a record of what was done on time.
	`ALTER TABLE completions ADD COLUMN due TEXT;`,

	// A pin is a date, not a flag: it says "this one, that day". A flag would
	// quietly carry yesterday's intention into today, which is exactly the
	// stale nagging a day view has to avoid. Preferences are one row, so the
	// id is fixed at 1 and the table can never grow a second opinion.
	`ALTER TABLE tasks ADD COLUMN pinned_on TEXT;
	CREATE TABLE preferences (
		id                INTEGER PRIMARY KEY CHECK (id = 1),
		day_start         TEXT NOT NULL,
		day_end           TEXT NOT NULL,
		work_start        TEXT NOT NULL,
		work_end          TEXT NOT NULL,
		work_days         TEXT NOT NULL,
		deep_start        TEXT NOT NULL,
		deep_end          TEXT NOT NULL,
		buffer_minutes    INTEGER NOT NULL,
		min_block_minutes INTEGER NOT NULL,
		max_minutes_day   INTEGER NOT NULL
	);
	CREATE INDEX tasks_pinned ON tasks(pinned_on);`,

	// Working hours were a preference until the day they could not say "except
	// lunch". The calendar already says when anything takes the day, so it
	// says it for work too, and one source of busy time cannot disagree with
	// another.
	`ALTER TABLE preferences DROP COLUMN work_start;
	ALTER TABLE preferences DROP COLUMN work_end;
	ALTER TABLE preferences DROP COLUMN work_days;`,

	// A task too big for one sitting is worked on over several days. What is
	// left is its own column so the estimate stays the first guess, and a
	// session is a completion row marked partial so undo stays "remove the
	// last thing logged" over one table. remaining_before is what the task
	// had before that session, which makes undoing one exact even when the
	// number was edited by hand in between.
	`ALTER TABLE tasks ADD COLUMN remaining_minutes INTEGER;
	ALTER TABLE completions ADD COLUMN partial INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE completions ADD COLUMN left_minutes INTEGER;
	ALTER TABLE completions ADD COLUMN remaining_before INTEGER;
	ALTER TABLE preferences ADD COLUMN max_block_minutes INTEGER NOT NULL DEFAULT 60;`,

	// A start date says when a task can begin, where the due date says when
	// it has to be done. A dependency is a pair of tasks rather than a column,
	// since one task can wait on several and hold up several; either end
	// being deleted takes the pair with it, which is what releases whatever
	// a deleted task was holding up.
	`ALTER TABLE tasks ADD COLUMN start_on TEXT;
	CREATE TABLE blockers (
		task_id    INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
		blocker_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
		PRIMARY KEY (task_id, blocker_id)
	);
	CREATE INDEX blockers_blocker ON blockers(blocker_id);`,
}

func (s *Store) migrate(path string) error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("migrating: %w", err)
	}
	var current int
	err := s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&current)
	if err == sql.ErrNoRows {
		if _, err := s.db.Exec(`INSERT INTO schema_version (version) VALUES (0)`); err != nil {
			return fmt.Errorf("migrating: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("migrating: %w", err)
	}
	// A migration is the one moment code that has never run against this
	// data rewrites it, and the nightly backup can be most of a day old by
	// the time a new version is deployed. Version 0 is a database with
	// nothing in it yet, which is the only case with nothing to lose.
	if current > 0 && current < len(migrations) {
		snapshot, err := s.snapshotBefore(path, len(migrations))
		if err != nil {
			return err
		}
		s.snapshot = snapshot
	}
	for v := current; v < len(migrations); v++ {
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("migrating to %d: %w", v+1, err)
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrating to %d: %w", v+1, err)
		}
		if _, err := tx.Exec(`UPDATE schema_version SET version = ?`, v+1); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrating to %d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migrating to %d: %w", v+1, err)
		}
	}
	return nil
}

// snapshotBefore copies the database beside itself, named for the version
// it is about to become, so the file you want is the one you can find.
func (s *Store) snapshotBefore(path string, to int) (string, error) {
	dir, name := filepath.Split(path)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	out := filepath.Join(dir, fmt.Sprintf("%s-pre-v%d-%s.db", name, to, Now().Format("20060102-150405")))
	if _, err := s.db.Exec(`VACUUM INTO ?`, out); err != nil {
		return "", fmt.Errorf("snapshotting before migrating to %d: %w", to, err)
	}
	return out, nil
}

const taskColumns = `id, title, notes, status, difficulty, priority, estimate_minutes, remaining_minutes, due, start_on,
	recur_kind, recur_rule, mnemo_slug, mnemo_title, pinned_on, created_at, updated_at, done_at, enriched_at`

// Create writes a new task, and what it waits on when BlockedBy names any,
// as one step.
func (s *Store) Create(t *Task) (*Task, error) {
	if t.Status == "" {
		t.Status = StatusOpen
	}
	if err := t.validate(); err != nil {
		return nil, err
	}
	now := Now()
	t.CreatedAt, t.UpdatedAt = now, now
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("creating task: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO tasks
		(title, notes, status, difficulty, priority, estimate_minutes, due, start_on,
		 recur_kind, recur_rule, mnemo_slug, mnemo_title, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.Title, nullStr(t.Notes), string(t.Status), nullStr(string(t.Difficulty)), nullStr(string(t.Priority)),
		nullInt(t.EstimateMinutes), nullStr(t.Due), nullStr(t.Start), nullStr(string(t.RecurKind)), nullStr(t.RecurRule),
		nullStr(t.MnemoSlug), nullStr(t.MnemoTitle), stamp(now), stamp(now))
	if err != nil {
		return nil, fmt.Errorf("creating task: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("creating task: %w", err)
	}
	if len(t.BlockedBy) > 0 {
		ids := make([]int64, 0, len(t.BlockedBy))
		for _, d := range t.BlockedBy {
			ids = append(ids, d.ID)
		}
		if err := setBlockers(tx, id, ids); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("creating task: %w", err)
	}
	return s.Get(id)
}

func (s *Store) Get(id int64) (*Task, error) {
	row := s.db.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("task %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("reading task %d: %w", id, err)
	}
	if err := s.annotate([]*Task{t}); err != nil {
		return nil, err
	}
	return t, nil
}

// Filter narrows a listing. The zero value lists open tasks only.
type Filter struct {
	Status     Status
	All        bool
	Difficulty Difficulty
	Priority   Priority
	Overdue    bool
	Linked     bool
	Note       string
	Recurring  bool
	Quick      bool
	Limit      int
}

func (s *Store) List(f Filter) ([]*Task, error) {
	var where []string
	var args []any
	switch {
	case f.All:
	case f.Status != "":
		if err := validStatus(f.Status); err != nil {
			return nil, err
		}
		where, args = append(where, "status = ?"), append(args, string(f.Status))
	default:
		where, args = append(where, "status = ?"), append(args, string(StatusOpen))
	}
	if f.Difficulty != "" {
		if err := validDifficulty(f.Difficulty); err != nil {
			return nil, err
		}
		where, args = append(where, "difficulty = ?"), append(args, string(f.Difficulty))
	}
	if f.Priority != "" {
		if err := validPriority(f.Priority); err != nil {
			return nil, err
		}
		if f.Priority == PriorityNormal {
			where = append(where, "(priority = ? OR priority IS NULL)")
		} else {
			where = append(where, "priority = ?")
		}
		args = append(args, string(f.Priority))
	}
	if f.Linked {
		where = append(where, "mnemo_slug IS NOT NULL")
	}
	if f.Note != "" {
		where, args = append(where, "mnemo_slug = ?"), append(args, f.Note)
	}
	if f.Recurring {
		where = append(where, "recur_kind IS NOT NULL")
	}
	if f.Quick {
		where = append(where, "(COALESCE(remaining_minutes, estimate_minutes) <= ? OR "+
			"(remaining_minutes IS NULL AND estimate_minutes IS NULL AND difficulty = ?))")
		args = append(args, QuickWinMinutes, string(DifficultyLow))
	}

	query := `SELECT ` + taskColumns + ` FROM tasks`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	defer rows.Close()

	tasks := []*Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("listing tasks: %w", err)
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	rows.Close()
	if err := s.annotate(tasks); err != nil {
		return nil, err
	}
	// Overdue is judged on the deadline a task has been passed, and a quick
	// win is something you could start now, so both are decided once the
	// dependencies are known rather than in SQL.
	if f.Overdue || f.Quick {
		today := Today()
		kept := tasks[:0]
		for _, t := range tasks {
			if f.Overdue && !t.Overdue() || f.Quick && t.Waiting(today) {
				continue
			}
			kept = append(kept, t)
		}
		tasks = kept
	}
	Sort(tasks)
	if f.Limit > 0 && len(tasks) > f.Limit {
		tasks = tasks[:f.Limit]
	}
	return tasks, nil
}

// Edit carries only the fields a caller wants changed. An empty string clears
// a nullable text field; a zero estimate clears the estimate, and a zero
// remaining puts the task back to its whole estimate. BlockedBy replaces
// everything the task waits on, and an empty list clears it.
type Edit struct {
	Title      *string
	Notes      *string
	Status     *Status
	Difficulty *Difficulty
	Priority   *Priority
	Estimate   *int
	Remaining  *int
	Due        *string
	Start      *string
	RecurKind  *RecurKind
	RecurRule  *string
	MnemoSlug  *string
	MnemoTitle *string
	BlockedBy  *[]int64
}

// Empty reports whether the edit would change nothing.
func (e Edit) Empty() bool {
	return e.Title == nil && e.Notes == nil && e.Status == nil && e.Difficulty == nil &&
		e.Priority == nil && e.Estimate == nil && e.Remaining == nil && e.Due == nil && e.Start == nil &&
		e.RecurKind == nil && e.RecurRule == nil && e.MnemoSlug == nil && e.MnemoTitle == nil && e.BlockedBy == nil
}

// TitleChanged reports whether this edit rewrites the title, the only change
// that can alter what a task means and so the only one that re-queues
// enrichment.
func (e Edit) TitleChanged(before string) bool {
	return e.Title != nil && strings.TrimSpace(*e.Title) != before
}

func (s *Store) Edit(id int64, e Edit) (*Task, error) {
	if e.Empty() {
		return nil, fmt.Errorf("edit: nothing to change: %w", ErrInvalid)
	}
	t, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	wasKind, wasRule := t.RecurKind, t.RecurRule
	// A new title is a new sentence to read, so the task stops counting as
	// enriched until the worker has been back to it.
	if e.TitleChanged(t.Title) {
		t.EnrichedAt = nil
	}
	if e.Title != nil {
		t.Title = *e.Title
	}
	if e.Notes != nil {
		t.Notes = *e.Notes
	}
	if e.Status != nil {
		t.Status = *e.Status
	}
	if e.Difficulty != nil {
		t.Difficulty = *e.Difficulty
	}
	if e.Priority != nil {
		t.Priority = *e.Priority
	}
	if e.Estimate != nil {
		t.EstimateMinutes = *e.Estimate
	}
	if e.Remaining != nil {
		t.RemainingMinutes = *e.Remaining
	}
	if e.Due != nil {
		t.Due = *e.Due
	}
	if e.Start != nil {
		t.Start = *e.Start
	}
	if e.RecurKind != nil {
		t.RecurKind = *e.RecurKind
	}
	if e.RecurRule != nil {
		t.RecurRule = *e.RecurRule
	}
	if e.MnemoSlug != nil {
		t.MnemoSlug = *e.MnemoSlug
	}
	if e.MnemoTitle != nil {
		t.MnemoTitle = *e.MnemoTitle
	}
	// A task that has just become recurring drops its start date the way it
	// drops what was left of it; asking for both at once is refused below.
	if t.Recurring() && e.Start == nil {
		t.Start = ""
	}
	if err := t.validate(); err != nil {
		return nil, err
	}
	if t.Recurring() && len(t.Blocks) > 0 {
		return nil, fmt.Errorf("task %d is waited on by %s, and a repeating task is never finished: %w",
			id, depIDs(t.Blocks), ErrInvalid)
	}
	// An occurrence of a recurring task is done in one go, so there is never
	// anything left of one. Asking for it is a mistake worth saying; a task
	// that has just become recurring simply drops what it had.
	if t.Recurring() {
		if e.Remaining != nil && *e.Remaining > 0 {
			return nil, fmt.Errorf("a recurring task is done in one go and has nothing left over: %w", ErrInvalid)
		}
		t.RemainingMinutes = 0
	}
	// A new schedule supersedes the old one's date: "every weekly on mon"
	// means the coming Monday, not whenever the previous rule had landed.
	if e.Due == nil && t.Recurring() && (t.RecurKind != wasKind || t.RecurRule != wasRule) {
		t.Due = ""
		if err := t.ensureDue(); err != nil {
			return nil, err
		}
	}
	// A recurring task is always open and still carries the last completion,
	// so only a one-off loses its done_at on reopening.
	if t.Status == StatusOpen && !t.Recurring() {
		t.DoneAt = nil
	}
	if e.BlockedBy == nil {
		return s.save(t)
	}
	return s.saveWith(t, func(tx *sql.Tx) error { return setBlockers(tx, id, *e.BlockedBy) })
}

func (s *Store) save(t *Task) (*Task, error) { return s.saveWith(t, nil) }

// saveWith writes the task and whatever else has to change with it in one
// transaction, so an edit that is refused halfway leaves nothing behind.
func (s *Store) saveWith(t *Task, also func(*sql.Tx) error) (*Task, error) {
	t.UpdatedAt = Now()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("saving task %d: %w", t.ID, err)
	}
	defer tx.Rollback()
	_, err = tx.Exec(`UPDATE tasks SET
		title = ?, notes = ?, status = ?, difficulty = ?, priority = ?, estimate_minutes = ?,
		remaining_minutes = ?, due = ?, start_on = ?, recur_kind = ?, recur_rule = ?, mnemo_slug = ?, mnemo_title = ?,
		pinned_on = ?, updated_at = ?, done_at = ?, enriched_at = ?
		WHERE id = ?`,
		t.Title, nullStr(t.Notes), string(t.Status), nullStr(string(t.Difficulty)), nullStr(string(t.Priority)),
		nullInt(t.EstimateMinutes), nullInt(t.RemainingMinutes), nullStr(t.Due), nullStr(t.Start),
		nullStr(string(t.RecurKind)), nullStr(t.RecurRule),
		nullStr(t.MnemoSlug), nullStr(t.MnemoTitle), nullStr(t.PinnedOn),
		stamp(t.UpdatedAt), stampPtr(t.DoneAt), stampPtr(t.EnrichedAt),
		t.ID)
	if err != nil {
		return nil, fmt.Errorf("saving task %d: %w", t.ID, err)
	}
	if also != nil {
		if err := also(tx); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("saving task %d: %w", t.ID, err)
	}
	return s.Get(t.ID)
}

// advanceFrom is the date a completion counts from. An every rule measures
// from the occurrence just completed, so doing Tuesday's chore on Monday
// evening moves it to next Tuesday rather than tomorrow; once the date has
// passed, today takes over and the missed weeks do not queue up. An after
// rule measures the interval from the moment it was actually done.
func (t *Task) advanceFrom() string {
	today := Today()
	if t.RecurKind == RecurEvery && t.Due > today {
		return t.Due
	}
	return today
}

// Done records a completion. A one-off task closes; recurrence advances the
// same row instead, so a recurring task keeps one stable id for its lifetime.
func (s *Store) Done(id int64, minutes int) (*Task, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("completing task %d: %w", id, err)
	}
	defer tx.Rollback()

	t, err := getTx(tx, id)
	if err != nil {
		return nil, err
	}
	now := Now()
	if _, err := tx.Exec(`INSERT INTO completions (task_id, done_at, due, minutes) VALUES (?,?,?,?)`,
		id, stamp(now), nullStr(t.Due), nullInt(minutes)); err != nil {
		return nil, fmt.Errorf("completing task %d: %w", id, err)
	}
	if t.Recurring() {
		next, err := recur.Next(string(t.RecurKind), t.RecurRule, t.advanceFrom())
		if err != nil {
			return nil, fmt.Errorf("completing task %d: %v: %w", id, err, ErrInvalid)
		}
		if _, err := tx.Exec(`UPDATE tasks SET due = ?, done_at = ?, updated_at = ? WHERE id = ?`,
			next, stamp(now), stamp(now), id); err != nil {
			return nil, fmt.Errorf("completing task %d: %w", id, err)
		}
	} else if _, err := tx.Exec(`UPDATE tasks SET status = ?, done_at = ?, updated_at = ? WHERE id = ?`,
		string(StatusDone), stamp(now), stamp(now), id); err != nil {
		return nil, fmt.Errorf("completing task %d: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("completing task %d: %w", id, err)
	}
	return s.Get(id)
}

// Work logs a session on a task without finishing it. left is what is still
// to do afterwards; nil means what was left less the minutes spent. Time
// spent is not progress, so the caller can say otherwise, and 0 left is the
// task finished. A recurring task is done in one go and is refused.
func (s *Store) Work(id int64, minutes int, left *int) (*Task, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("logging work on task %d: %w", id, err)
	}
	defer tx.Rollback()

	t, err := getTx(tx, id)
	if err != nil {
		return nil, err
	}
	if t.Recurring() {
		return nil, fmt.Errorf("task %d repeats, and an occurrence is done in one go; complete it instead: %w", id, ErrInvalid)
	}
	if t.Status != StatusOpen {
		return nil, fmt.Errorf("task %d is already done: %w", id, ErrInvalid)
	}
	if minutes < 0 {
		return nil, fmt.Errorf("minutes must not be negative: %w", ErrInvalid)
	}
	before := t.RemainingMinutes
	if before == 0 {
		before = t.EstimateMinutes
	}
	var rest int
	switch {
	case left != nil:
		rest = *left
	case before == 0:
		return nil, fmt.Errorf("task %d has no estimate to count down from; say how much is left: %w", id, ErrInvalid)
	default:
		rest = max(before-minutes, 0)
	}
	if rest < 0 {
		return nil, fmt.Errorf("left must not be negative: %w", ErrInvalid)
	}
	if rest == 0 {
		tx.Rollback()
		return s.Done(id, minutes)
	}

	now := Now()
	if _, err := tx.Exec(`INSERT INTO completions (task_id, done_at, due, minutes, partial, left_minutes, remaining_before)
		VALUES (?,?,?,?,1,?,?)`,
		id, stamp(now), nullStr(t.Due), nullInt(minutes), rest, nullInt(t.RemainingMinutes)); err != nil {
		return nil, fmt.Errorf("logging work on task %d: %w", id, err)
	}
	if _, err := tx.Exec(`UPDATE tasks SET remaining_minutes = ?, updated_at = ? WHERE id = ?`,
		rest, stamp(now), id); err != nil {
		return nil, fmt.Errorf("logging work on task %d: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("logging work on task %d: %w", id, err)
	}
	return s.Get(id)
}

// Undo removes the most recent thing logged against a task, covering the one
// mistake that actually happens: a wrong tick. A completion reopens the task;
// a session of work puts back what was left before it.
func (s *Store) Undo(id int64) (*Task, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("undoing task %d: %w", id, err)
	}
	defer tx.Rollback()

	t, err := getTx(tx, id)
	if err != nil {
		return nil, err
	}
	var completionID int64
	var completionDue sql.NullString
	var partial bool
	var remainingBefore sql.NullInt64
	err = tx.QueryRow(`SELECT id, due, partial, remaining_before FROM completions
		WHERE task_id = ? ORDER BY done_at DESC, id DESC LIMIT 1`, id).
		Scan(&completionID, &completionDue, &partial, &remainingBefore)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("task %d has no completion to undo: %w", id, ErrInvalid)
	}
	if err != nil {
		return nil, fmt.Errorf("undoing task %d: %w", id, err)
	}
	if _, err := tx.Exec(`DELETE FROM completions WHERE id = ?`, completionID); err != nil {
		return nil, fmt.Errorf("undoing task %d: %w", id, err)
	}
	if partial {
		if _, err := tx.Exec(`UPDATE tasks SET remaining_minutes = ?, updated_at = ? WHERE id = ?`,
			nullInt(int(remainingBefore.Int64)), stamp(Now()), id); err != nil {
			return nil, fmt.Errorf("undoing task %d: %w", id, err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("undoing task %d: %w", id, err)
		}
		return s.Get(id)
	}
	var previous any
	if err := tx.QueryRow(`SELECT done_at FROM completions WHERE task_id = ? AND partial = 0
		ORDER BY done_at DESC, id DESC LIMIT 1`, id).
		Scan(&previous); err == sql.ErrNoRows {
		previous = nil
	} else if err != nil {
		return nil, fmt.Errorf("undoing task %d: %w", id, err)
	}
	due := any(nullStr(t.Due))
	if t.Recurring() && completionDue.Valid {
		due = completionDue.String
	}
	if _, err := tx.Exec(`UPDATE tasks SET status = ?, done_at = ?, due = ?, updated_at = ? WHERE id = ?`,
		string(StatusOpen), previous, due, stamp(Now()), id); err != nil {
		return nil, fmt.Errorf("undoing task %d: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("undoing task %d: %w", id, err)
	}
	return s.Get(id)
}

// Delete is permanent and takes the task's completions with it. There is no
// trash: git is not backing this up, the nightly snapshot is.
func (s *Store) Delete(id int64) error {
	res, err := s.db.Exec(`DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting task %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("deleting task %d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("task %d: %w", id, ErrNotFound)
	}
	return nil
}

// Completion is one thing logged against a task: finishing it, or a session
// of work on it that left some to do, in which case Partial is set and Left
// is what was still to go afterwards.
type Completion struct {
	ID      int64
	TaskID  int64
	DoneAt  time.Time
	Due     string
	Minutes int
	Partial bool
	Left    int
}

func (s *Store) Completions(id int64) ([]Completion, error) {
	rows, err := s.db.Query(`SELECT id, task_id, done_at, due, minutes, partial, left_minutes FROM completions
		WHERE task_id = ? ORDER BY done_at DESC, id DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("reading completions for %d: %w", id, err)
	}
	defer rows.Close()

	out := []Completion{}
	for rows.Next() {
		var c Completion
		var doneAt string
		var due sql.NullString
		var minutes, left sql.NullInt64
		if err := rows.Scan(&c.ID, &c.TaskID, &doneAt, &due, &minutes, &c.Partial, &left); err != nil {
			return nil, fmt.Errorf("reading completions for %d: %w", id, err)
		}
		c.DoneAt = parseStamp(doneAt)
		c.Due = due.String
		c.Minutes = int(minutes.Int64)
		c.Left = int(left.Int64)
		out = append(out, c)
	}
	return out, rows.Err()
}

type Summary struct {
	Open       int
	Done       int
	Overdue    int
	Unenriched int
}

func (s *Store) Summary() (Summary, error) {
	var sum Summary
	row := s.db.QueryRow(`SELECT
		COUNT(*) FILTER (WHERE status = 'open'),
		COUNT(*) FILTER (WHERE status = 'done'),
		COUNT(*) FILTER (WHERE status = 'open' AND due IS NOT NULL AND due < ?),
		COUNT(*) FILTER (WHERE enriched_at IS NULL)
		FROM tasks`, Today())
	if err := row.Scan(&sum.Open, &sum.Done, &sum.Overdue, &sum.Unenriched); err != nil {
		return sum, fmt.Errorf("summarising: %w", err)
	}
	return sum, nil
}

// Backup writes a consistent copy of the database into dir and prunes all but
// the newest keep snapshots.
func (s *Store) Backup(dir string, keep int) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("backup: creating %s: %w", dir, err)
	}
	path := filepath.Join(dir, "ordo-"+Now().Format("20060102-150405")+".db")
	if _, err := s.db.Exec(`VACUUM INTO ?`, path); err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	if keep > 0 {
		if err := prune(dir, keep); err != nil {
			return path, err
		}
	}
	return path, nil
}

func prune(dir string, keep int) error {
	entries, err := filepath.Glob(filepath.Join(dir, "ordo-*.db"))
	if err != nil {
		return fmt.Errorf("backup: pruning %s: %w", dir, err)
	}
	if len(entries) <= keep {
		return nil
	}
	sort.Strings(entries)
	for _, stale := range entries[:len(entries)-keep] {
		if err := os.Remove(stale); err != nil {
			return fmt.Errorf("backup: pruning %s: %w", stale, err)
		}
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(row scanner) (*Task, error) {
	var (
		t          Task
		notes      sql.NullString
		difficulty sql.NullString
		priority   sql.NullString
		estimate   sql.NullInt64
		remaining  sql.NullInt64
		due        sql.NullString
		start      sql.NullString
		recurKind  sql.NullString
		recurRule  sql.NullString
		mnemoSlug  sql.NullString
		mnemoTitle sql.NullString
		pinnedOn   sql.NullString
		createdAt  string
		updatedAt  string
		doneAt     sql.NullString
		enrichedAt sql.NullString
	)
	if err := row.Scan(&t.ID, &t.Title, &notes, &t.Status, &difficulty, &priority, &estimate, &remaining, &due, &start,
		&recurKind, &recurRule, &mnemoSlug, &mnemoTitle, &pinnedOn, &createdAt, &updatedAt, &doneAt, &enrichedAt); err != nil {
		return nil, err
	}
	t.Notes = notes.String
	t.Difficulty = Difficulty(difficulty.String)
	t.Priority = Priority(priority.String)
	t.EstimateMinutes = int(estimate.Int64)
	t.RemainingMinutes = int(remaining.Int64)
	t.Due = due.String
	t.Start = start.String
	t.RecurKind = RecurKind(recurKind.String)
	t.RecurRule = recurRule.String
	t.MnemoSlug = mnemoSlug.String
	t.MnemoTitle = mnemoTitle.String
	t.PinnedOn = pinnedOn.String
	t.CreatedAt = parseStamp(createdAt)
	t.UpdatedAt = parseStamp(updatedAt)
	if doneAt.Valid {
		when := parseStamp(doneAt.String)
		t.DoneAt = &when
	}
	if enrichedAt.Valid {
		when := parseStamp(enrichedAt.String)
		t.EnrichedAt = &when
	}
	return &t, nil
}

func getTx(tx *sql.Tx, id int64) (*Task, error) {
	row := tx.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("task %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("reading task %d: %w", id, err)
	}
	return t, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(n int) any {
	if n <= 0 {
		return nil
	}
	return n
}

func stamp(t time.Time) string { return t.In(Location).Format(time.RFC3339) }

func stampPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return stamp(*t)
}

func parseStamp(s string) time.Time {
	when, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return when.In(Location)
}
