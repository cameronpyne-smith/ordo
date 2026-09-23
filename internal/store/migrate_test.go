package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// atVersion writes a database as it was at an older schema version, with a
// row in it, so a migration has something it could lose.
func atVersion(t *testing.T, path string, version int) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for v := 0; v < version; v++ {
		if _, err := raw.Exec(migrations[v]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO schema_version (version) VALUES (?)`, version); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO tasks (title, status, created_at, updated_at)
		VALUES ('Move sofa into office', 'open', '2026-09-22T13:41:05+01:00', '2026-09-22T13:41:05+01:00')`); err != nil {
		t.Fatal(err)
	}
}

func TestMigratingSnapshotsWhatItIsAboutToRewrite(t *testing.T) {
	fixedNow(t, "2026-09-22T14:00:00Z")
	path := filepath.Join(t.TempDir(), "ordo.db")
	atVersion(t, path, 1)

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	snapshot := st.MigrationSnapshot()
	if snapshot == "" {
		t.Fatal("a database with data in it migrated without being copied first")
	}
	// Named for the version it is about to become, beside the database, so
	// the file you want is the one you can find.
	if dir := filepath.Dir(snapshot); dir != filepath.Dir(path) {
		t.Errorf("snapshot went to %s, want it beside the database", dir)
	}
	prefix := fmt.Sprintf("ordo-pre-v%d-", len(migrations))
	if name := filepath.Base(snapshot); !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".db") {
		t.Errorf("snapshot is called %q", name)
	}

	// The copy has to be the old database: the row still there, and the
	// column the migration was about still absent.
	raw, err := sql.Open("sqlite", "file:"+snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var title string
	if err := raw.QueryRow(`SELECT title FROM tasks`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Move sofa into office" {
		t.Errorf("snapshot holds %q", title)
	}
	if _, err := raw.Query(`SELECT pinned_on FROM tasks`); err == nil {
		t.Error("the snapshot has the new column, so it was taken after the migration")
	}

	// And the live database is migrated, with the row carried through.
	tasks, err := st.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Title != "Move sofa into office" {
		t.Fatalf("tasks = %+v", tasks)
	}
}

func TestAFreshDatabaseIsNotSnapshotted(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "ordo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if got := st.MigrationSnapshot(); got != "" {
		t.Fatalf("a new database was copied to %s; there was nothing to lose", got)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*-pre-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("snapshots written: %v", files)
	}
}

func TestReopeningACurrentDatabaseIsNotSnapshotted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ordo.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	create(t, first, &Task{Title: "Move sofa into office"})
	first.Close()

	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if got := second.MigrationSnapshot(); got != "" {
		t.Fatalf("nothing needed migrating, yet %s was written", got)
	}
}

// Dropping the working hours must keep the rest of the day as it was set:
// the migration removes three columns, not the row they were in.
func TestDroppingWorkKeepsTheRestOfTheDay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ordo.db")
	atVersion(t, path, 3)
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`INSERT INTO preferences (id, day_start, day_end, work_start, work_end, work_days,
		deep_start, deep_end, buffer_minutes, min_block_minutes, max_minutes_day)
		VALUES (1, '08:30', '23:59', '09:00', '17:00', 'mon,tue,wed,thu,fri', '22:00', '23:59', 5, 20, 300)`)
	raw.Close()
	if err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, err := st.Preferences()
	if err != nil {
		t.Fatal(err)
	}
	want := Preferences{
		DayStart: Clock{8, 30}, DayEnd: Clock{23, 59},
		DeepStart: Clock{22, 0}, DeepEnd: Clock{23, 59},
		BufferMinutes: 5, MinBlockMinutes: 20, MaxMinutesDay: 300,
	}
	if got != want {
		t.Fatalf("preferences = %+v, want %+v", got, want)
	}
	if _, err := st.SetPreferences(got); err != nil {
		t.Fatalf("writing after the migration: %v", err)
	}
}
