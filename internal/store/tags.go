package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// A tag is a group a task belongs to that is not a note: "house" for the
// renovation, "career" for the move. It is nothing but the word, so there is
// no table of tags to keep tidy; a tag exists while a task carries it and is
// gone when the last one drops it.

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

// setTags replaces a task's tags with ones already normalised.
func setTags(tx *sql.Tx, id int64, tags []string) error {
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

// TagCount is one tag in use and how many tasks carry it, open and done.
type TagCount struct {
	Tag  string
	Open int
	Done int
}

// Tags is every tag some task carries, in alphabetical order.
func (s *Store) Tags() ([]TagCount, error) {
	rows, err := s.db.Query(`SELECT g.tag,
		COUNT(*) FILTER (WHERE t.status = ?),
		COUNT(*) FILTER (WHERE t.status = ?)
		FROM task_tags g JOIN tasks t ON t.id = g.task_id
		GROUP BY g.tag ORDER BY g.tag`, string(StatusOpen), string(StatusDone))
	if err != nil {
		return nil, fmt.Errorf("listing tags: %w", err)
	}
	defer rows.Close()
	out := []TagCount{}
	for rows.Next() {
		var c TagCount
		if err := rows.Scan(&c.Tag, &c.Open, &c.Done); err != nil {
			return nil, fmt.Errorf("listing tags: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
