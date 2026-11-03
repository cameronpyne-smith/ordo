package store

import (
	"database/sql"
	"fmt"
	"hash/fnv"
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
		MIN(t.created_at)
		FROM task_tags g JOIN tasks t ON t.id = g.task_id
		GROUP BY g.tag ORDER BY MIN(t.created_at), g.tag`, string(StatusOpen), string(StatusDone))
	if err != nil {
		return nil, fmt.Errorf("listing tags: %w", err)
	}
	defer rows.Close()
	out := []TagCount{}
	var first string
	for rows.Next() {
		var c TagCount
		if err := rows.Scan(&c.Tag, &c.Open, &c.Done, &first); err != nil {
			return nil, fmt.Errorf("listing tags: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	colour(out)
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out, nil
}

// colour gives each tag a colour, in the order the tags came into use. A
// tag takes the colour its name hashes to when no earlier tag has it, and
// the next free one otherwise, so tags are told apart up to the number of
// colours there are, and a new tag never changes the colour of one already
// in use. Hashing first means a tag's colour is usually the same on a fresh
// database too.
func colour(tags []TagCount) {
	taken := make([]bool, Colours)
	for i := range tags {
		h := fnv.New32a()
		h.Write([]byte(tags[i].Tag))
		slot := int(h.Sum32() % Colours)
		for probe := 0; probe < Colours && taken[slot]; probe++ {
			slot = (slot + 1) % Colours
		}
		taken[slot] = true
		tags[i].Colour = slot
	}
}
