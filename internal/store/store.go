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
	db *sql.DB
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
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

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
}

func (s *Store) migrate() error {
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

const taskColumns = `id, title, notes, status, difficulty, priority, estimate_minutes, due,
	recur_kind, recur_rule, mnemo_slug, mnemo_title, created_at, updated_at, done_at, enriched_at`

func (s *Store) Create(t *Task) (*Task, error) {
	if t.Status == "" {
		t.Status = StatusOpen
	}
	if err := t.validate(); err != nil {
		return nil, err
	}
	now := Now()
	t.CreatedAt, t.UpdatedAt = now, now
	res, err := s.db.Exec(`INSERT INTO tasks
		(title, notes, status, difficulty, priority, estimate_minutes, due,
		 recur_kind, recur_rule, mnemo_slug, mnemo_title, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.Title, nullStr(t.Notes), string(t.Status), nullStr(string(t.Difficulty)), nullStr(string(t.Priority)),
		nullInt(t.EstimateMinutes), nullStr(t.Due), nullStr(string(t.RecurKind)), nullStr(t.RecurRule),
		nullStr(t.MnemoSlug), nullStr(t.MnemoTitle), stamp(now), stamp(now))
	if err != nil {
		return nil, fmt.Errorf("creating task: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
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
	Recurring  bool
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
	if f.Overdue {
		where, args = append(where, "due IS NOT NULL AND due < ?"), append(args, Today())
	}
	if f.Linked {
		where = append(where, "mnemo_slug IS NOT NULL")
	}
	if f.Recurring {
		where = append(where, "recur_kind IS NOT NULL")
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
	Sort(tasks)
	if f.Limit > 0 && len(tasks) > f.Limit {
		tasks = tasks[:f.Limit]
	}
	return tasks, nil
}

// Edit carries only the fields a caller wants changed. An empty string clears
// a nullable text field; a zero estimate clears the estimate.
type Edit struct {
	Title      *string
	Notes      *string
	Status     *Status
	Difficulty *Difficulty
	Priority   *Priority
	Estimate   *int
	Due        *string
	RecurKind  *RecurKind
	RecurRule  *string
	MnemoSlug  *string
	MnemoTitle *string
}

// Empty reports whether the edit would change nothing.
func (e Edit) Empty() bool {
	return e.Title == nil && e.Notes == nil && e.Status == nil && e.Difficulty == nil &&
		e.Priority == nil && e.Estimate == nil && e.Due == nil && e.RecurKind == nil &&
		e.RecurRule == nil && e.MnemoSlug == nil && e.MnemoTitle == nil
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
	if e.Due != nil {
		t.Due = *e.Due
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
	if err := t.validate(); err != nil {
		return nil, err
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
	return s.save(t)
}

func (s *Store) save(t *Task) (*Task, error) {
	t.UpdatedAt = Now()
	_, err := s.db.Exec(`UPDATE tasks SET
		title = ?, notes = ?, status = ?, difficulty = ?, priority = ?, estimate_minutes = ?,
		due = ?, recur_kind = ?, recur_rule = ?, mnemo_slug = ?, mnemo_title = ?,
		updated_at = ?, done_at = ?, enriched_at = ?
		WHERE id = ?`,
		t.Title, nullStr(t.Notes), string(t.Status), nullStr(string(t.Difficulty)), nullStr(string(t.Priority)),
		nullInt(t.EstimateMinutes), nullStr(t.Due), nullStr(string(t.RecurKind)), nullStr(t.RecurRule),
		nullStr(t.MnemoSlug), nullStr(t.MnemoTitle), stamp(t.UpdatedAt), stampPtr(t.DoneAt), stampPtr(t.EnrichedAt),
		t.ID)
	if err != nil {
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

// Undo removes the most recent completion and reopens the task, covering the
// one mistake that actually happens: a wrong tick.
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
	err = tx.QueryRow(`SELECT id, due FROM completions WHERE task_id = ? ORDER BY done_at DESC, id DESC LIMIT 1`, id).
		Scan(&completionID, &completionDue)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("task %d has no completion to undo: %w", id, ErrInvalid)
	}
	if err != nil {
		return nil, fmt.Errorf("undoing task %d: %w", id, err)
	}
	if _, err := tx.Exec(`DELETE FROM completions WHERE id = ?`, completionID); err != nil {
		return nil, fmt.Errorf("undoing task %d: %w", id, err)
	}
	var previous any
	if err := tx.QueryRow(`SELECT done_at FROM completions WHERE task_id = ? ORDER BY done_at DESC, id DESC LIMIT 1`, id).
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

type Completion struct {
	ID      int64
	TaskID  int64
	DoneAt  time.Time
	Due     string
	Minutes int
}

func (s *Store) Completions(id int64) ([]Completion, error) {
	rows, err := s.db.Query(`SELECT id, task_id, done_at, due, minutes FROM completions
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
		var minutes sql.NullInt64
		if err := rows.Scan(&c.ID, &c.TaskID, &doneAt, &due, &minutes); err != nil {
			return nil, fmt.Errorf("reading completions for %d: %w", id, err)
		}
		c.DoneAt = parseStamp(doneAt)
		c.Due = due.String
		c.Minutes = int(minutes.Int64)
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
		due        sql.NullString
		recurKind  sql.NullString
		recurRule  sql.NullString
		mnemoSlug  sql.NullString
		mnemoTitle sql.NullString
		createdAt  string
		updatedAt  string
		doneAt     sql.NullString
		enrichedAt sql.NullString
	)
	if err := row.Scan(&t.ID, &t.Title, &notes, &t.Status, &difficulty, &priority, &estimate, &due,
		&recurKind, &recurRule, &mnemoSlug, &mnemoTitle, &createdAt, &updatedAt, &doneAt, &enrichedAt); err != nil {
		return nil, err
	}
	t.Notes = notes.String
	t.Difficulty = Difficulty(difficulty.String)
	t.Priority = Priority(priority.String)
	t.EstimateMinutes = int(estimate.Int64)
	t.Due = due.String
	t.RecurKind = RecurKind(recurKind.String)
	t.RecurRule = recurRule.String
	t.MnemoSlug = mnemoSlug.String
	t.MnemoTitle = mnemoTitle.String
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
