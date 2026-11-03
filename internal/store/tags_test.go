package store

import (
	"errors"
	"fmt"
	"testing"
)

// A tag is stored as one word, lower case, however it was typed; anything
// that is not a word is refused rather than guessed at.
func TestTagsAreNormalisedOrRefused(t *testing.T) {
	for typed, want := range map[string]string{"House": "house", " #house-reno ": "house-reno", "2026": "2026"} {
		if got, err := NormaliseTag(typed); err != nil || got != want {
			t.Errorf("NormaliseTag(%q) = %q, %v; want %q", typed, got, err, want)
		}
	}
	for _, bad := range []string{"", "#", "house reno", "house_reno", "héllo", "a-tag-that-goes-on-for-far-too-long"} {
		if _, err := NormaliseTag(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("NormaliseTag(%q): err = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestATaskCarriesItsTags(t *testing.T) {
	st := open(t)
	paint := create(t, st, &Task{Title: "Paint the living room", Tags: []string{"House", "#house", "reno"}})
	if fmt.Sprint(paint.Tags) != "[house reno]" {
		t.Fatalf("tags = %v, want deduplicated, lower case and sorted", paint.Tags)
	}
	if _, err := st.Create(&Task{Title: "x", Tags: []string{"no good"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a bad tag on create: err = %v, want ErrInvalid", err)
	}

	tags := []string{"career"}
	moved, err := st.Edit(paint.ID, Edit{Tags: &tags})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(moved.Tags) != "[career]" {
		t.Fatalf("tags after edit = %v, want the whole set replaced", moved.Tags)
	}
	// A tag that is refused leaves the task as it was, title included.
	bad, title := []string{"not ok"}, "Renamed"
	if _, err := st.Edit(paint.ID, Edit{Title: &title, Tags: &bad}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a bad tag on edit: err = %v, want ErrInvalid", err)
	}
	if again := get(t, st, paint.ID); again.Title != "Paint the living room" || fmt.Sprint(again.Tags) != "[career]" {
		t.Fatalf("a refused edit changed the task: %q %v", again.Title, again.Tags)
	}
	none := []string{}
	if cleared, err := st.Edit(paint.ID, Edit{Tags: &none}); err != nil || len(cleared.Tags) != 0 {
		t.Fatalf("clearing: tags = %v err %v", cleared.Tags, err)
	}
}

// Filtering by tag narrows the list like any other filter, and composes
// with them; the tags in use count what carries each, open and done.
func TestTagsFilterAndCount(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	paint := create(t, st, &Task{Title: "Paint", Tags: []string{"house"}, Due: "2026-09-10"})
	shelves := create(t, st, &Task{Title: "Shelves", Tags: []string{"house", "diy"}})
	cv := create(t, st, &Task{Title: "CV", Tags: []string{"career"}})
	create(t, st, &Task{Title: "Bins"})
	done(t, st, cv.ID)

	tasks, err := st.List(Filter{Tag: "#House"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].ID != paint.ID || tasks[1].ID != shelves.ID {
		t.Fatalf("house = %v, want the two house tasks in order", tasks)
	}
	if tasks, _ = st.List(Filter{Tag: "house", Overdue: true}); len(tasks) != 1 || tasks[0].ID != paint.ID {
		t.Fatalf("house and overdue = %v, want the painting alone", tasks)
	}
	if tasks, _ = st.List(Filter{Tag: "career"}); len(tasks) != 0 {
		t.Fatalf("career open = %v, want nothing: the CV is done", tasks)
	}
	if _, err := st.List(Filter{Tag: "not a tag"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a bad tag filter: err = %v, want ErrInvalid", err)
	}

	counts, err := st.Tags()
	if err != nil {
		t.Fatal(err)
	}
	var said []string
	for _, c := range counts {
		said = append(said, fmt.Sprintf("%s %d %d", c.Tag, c.Open, c.Done))
	}
	if got := fmt.Sprint(said); got != "[career 0 1 diy 1 0 house 2 0]" {
		t.Fatalf("tags = %s", got)
	}
	if err := st.Delete(shelves.ID); err != nil {
		t.Fatal(err)
	}
	if counts, _ = st.Tags(); len(counts) != 2 || counts[1].Tag != "house" || counts[1].Open != 1 {
		t.Fatalf("after deleting the shelves, tags = %v; diy should have gone with them", counts)
	}
}

// Tags that hash to the same colour are still told apart, and a tag keeps
// its colour when a later one arrives.
func TestTagsGetDistinctColours(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")
	st := open(t)
	create(t, st, &Task{Title: "Rent", Tags: []string{"rent"}})
	fixedNow(t, "2026-09-21T09:00:00Z")
	create(t, st, &Task{Title: "Quant", Tags: []string{"quant"}})
	before, err := st.Tags()
	if err != nil {
		t.Fatal(err)
	}
	if before[0].Colour == before[1].Colour {
		t.Fatalf("quant and rent share colour %d", before[0].Colour)
	}
	fixedNow(t, "2026-09-22T09:00:00Z")
	create(t, st, &Task{Title: "House", Tags: []string{"house"}})
	after, _ := st.Tags()
	if after[1].Colour != before[0].Colour || after[2].Colour != before[1].Colour {
		t.Fatalf("a new tag changed the colours of the old ones: %v then %v", before, after)
	}
	seen := map[int]bool{}
	for _, c := range after {
		if seen[c.Colour] {
			t.Fatalf("three tags, two of one colour: %v", after)
		}
		seen[c.Colour] = true
	}
}
