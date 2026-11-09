package store

import (
	"database/sql"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
)

// A tag is a group a task belongs to that is not a note: "house" for the
// renovation, "career" for the move. It is nothing but the word: a tag
// exists while a task carries it and is gone when the last one drops it.
// The only thing kept about a tag itself is its colour.

// maxTagLength keeps a tag a badge rather than a title.
const maxTagLength = 32

// NormaliseTag is the stored form of a tag as typed: trimmed, without the #
// someone wrote in front of it, and lower case. Anything that is not letters,
// digits and dashes is refused rather than guessed at, so "House reno" is an
// error and "house-reno" is the tag.
func NormaliseTag(typed string) (string, error) {
	tag := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(typed), "#"))
	if tag == "" {
		return "", fmt.Errorf("a tag needs a word: %w", ErrInvalid)
	}
	if len(tag) > maxTagLength {
		return "", fmt.Errorf("tag %q is too long for a badge; %d characters at most: %w", typed, maxTagLength, ErrInvalid)
	}
	for _, r := range tag {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			continue
		}
		return "", fmt.Errorf("tag %q: use letters, digits and dashes, like house-reno: %w", typed, ErrInvalid)
	}
	return tag, nil
}

// normaliseTags is a task's tags as stored: each normalised, no repeats, in
// alphabetical order so two tasks with the same tags read the same.
func normaliseTags(typed []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, raw := range typed {
		tag, err := NormaliseTag(raw)
		if err != nil {
			return nil, err
		}
		if !seen[tag] {
			seen[tag] = true
			out = append(out, tag)
		}
	}
	sort.Strings(out)
	return out, nil
}

// setTags replaces a task's tags with ones already normalised. A tag no task
// carries yet is given its colour here, before the task takes it.
func setTags(tx *sql.Tx, id int64, tags []string) error {
	var fresh []string
	for _, tag := range tags {
		var carried bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM task_tags WHERE tag = ?)`, tag).Scan(&carried); err != nil {
			return fmt.Errorf("tagging task %d: %w", id, err)
		}
		if !carried {
			fresh = append(fresh, tag)
		}
	}
	if err := place(tx, fresh); err != nil {
		return fmt.Errorf("tagging task %d: %w", id, err)
	}
	if _, err := tx.Exec(`DELETE FROM task_tags WHERE task_id = ?`, id); err != nil {
		return fmt.Errorf("tagging task %d: %w", id, err)
	}
	for _, tag := range tags {
		if _, err := tx.Exec(`INSERT INTO task_tags (task_id, tag) VALUES (?, ?)`, id, tag); err != nil {
			return fmt.Errorf("tagging task %d: %w", id, err)
		}
	}
	return nil
}

// tags fills in each task's tags from the whole table, which is one read
// however many tasks are being listed.
func (s *Store) tags(tasks []*Task) error {
	for _, t := range tasks {
		t.Tags = nil
	}
	rows, err := s.db.Query(`SELECT task_id, tag FROM task_tags ORDER BY task_id, tag`)
	if err != nil {
		return fmt.Errorf("reading tags: %w", err)
	}
	defer rows.Close()
	byID := map[int64]*Task{}
	for _, t := range tasks {
		byID[t.ID] = t
	}
	for rows.Next() {
		var id int64
		var tag string
		if err := rows.Scan(&id, &tag); err != nil {
			return fmt.Errorf("reading tags: %w", err)
		}
		if t, ok := byID[id]; ok {
			t.Tags = append(t.Tags, tag)
		}
	}
	return rows.Err()
}

// TagCount is one tag in use, how many tasks carry it, open and done, and
// the colour it shows in.
type TagCount struct {
	Tag    string
	Open   int
	Done   int
	Colour int
}

// Colours is how many colours a surface has for tags. A colour is given out
// here rather than in the terminal because telling tags apart needs the
// whole set, which only the daemon has.
const Colours = 10

// Tags is every tag some task carries, in alphabetical order, each with its
// colour.
func (s *Store) Tags() ([]TagCount, error) {
	rows, err := s.db.Query(`SELECT g.tag,
		COUNT(*) FILTER (WHERE t.status = ?),
		COUNT(*) FILTER (WHERE t.status = ?),
		COALESCE(c.colour, -1)
		FROM task_tags g JOIN tasks t ON t.id = g.task_id LEFT JOIN tags c ON c.tag = g.tag
		GROUP BY g.tag ORDER BY g.tag`, string(StatusOpen), string(StatusDone))
	if err != nil {
		return nil, fmt.Errorf("listing tags: %w", err)
	}
	defer rows.Close()
	out := []TagCount{}
	for rows.Next() {
		var c TagCount
		if err := rows.Scan(&c.Tag, &c.Open, &c.Done, &c.Colour); err != nil {
			return nil, fmt.Errorf("listing tags: %w", err)
		}
		if c.Colour < 0 {
			c.Colour = hashSlot(c.Tag)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// place gives each of these tags, none of which any task carries yet, its
// colour: the one its name hashes to when no tag in use has it, and the
// next free one otherwise. Tags in use keep theirs, which is the point; a
// tag that died out and is back is placed again, against what is in use
// now. Hashing first means a tag's colour is usually the same on a fresh
// database too.
func place(tx *sql.Tx, tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	rows, err := tx.Query(`SELECT colour FROM tags WHERE tag IN (SELECT tag FROM task_tags)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	taken := make([]bool, Colours)
	for rows.Next() {
		var slot int
		if err := rows.Scan(&slot); err != nil {
			return err
		}
		taken[((slot%Colours)+Colours)%Colours] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, tag := range tags {
		slot := hashSlot(tag)
		for probe := 0; probe < Colours && taken[slot]; probe++ {
			slot = (slot + 1) % Colours
		}
		taken[slot] = true
		if _, err := tx.Exec(`INSERT INTO tags (tag, colour) VALUES (?, ?)
			ON CONFLICT (tag) DO UPDATE SET colour = excluded.colour`, tag, slot); err != nil {
			return err
		}
	}
	return nil
}

func hashSlot(tag string) int {
	h := fnv.New32a()
	h.Write([]byte(tag))
	return int(h.Sum32() % Colours)
}

// colourTags places every tag in use that has no colour yet, which is the
// tags from before colours were kept: once, in the order the tasks carrying
// them were made, so what was on screen the day before stays put.
func (s *Store) colourTags() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("colouring tags: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT g.tag FROM task_tags g JOIN tasks t ON t.id = g.task_id
		WHERE g.tag NOT IN (SELECT tag FROM tags)
		GROUP BY g.tag ORDER BY MIN(t.created_at), g.tag`)
	if err != nil {
		return fmt.Errorf("colouring tags: %w", err)
	}
	var missing []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			rows.Close()
			return fmt.Errorf("colouring tags: %w", err)
		}
		missing = append(missing, tag)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("colouring tags: %w", err)
	}
	if err := place(tx, missing); err != nil {
		return fmt.Errorf("colouring tags: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("colouring tags: %w", err)
	}
	return nil
}
