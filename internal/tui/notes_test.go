package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cameronpyne-smith/ordo/internal/api"
)

func longNotes(lines int) string {
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(&b, "paragraph %d of a note far too long for the one row the pane gives it\n", i)
	}
	return strings.TrimSpace(b.String())
}

func opened(notes string, width, height int) Model {
	m := New(nil)
	m.mode = modeEdit
	m.edit = task(1, "Evaluate password managers", func(x *api.Task) { x.Notes = notes })
	m.width, m.height = width, height
	return m
}

// A note the row cannot hold is shown whole underneath, so the end of it is
// on screen and not only its first sixty characters.
func TestALongNoteIsShownWhole(t *testing.T) {
	notes := "1password versus self-hosting (Vaultwarden, KeePass): is the cost justified, " +
		"and can I run it myself on the box next to mnemo without another thing to babysit"
	frame := opened(notes, 80, 30).View()
	if !strings.Contains(frame, "babysit") {
		t.Fatalf("the end of the note is not on screen:\n%s", frame)
	}
	if lines := strings.Count(frame, "\n") + 1; lines != 30 {
		t.Errorf("rendered %d lines into a 30-line terminal", lines)
	}
}

// A note that fits on its row is not repeated under it.
func TestAShortNoteIsNotShownTwice(t *testing.T) {
	frame := opened("ask Jenny first", 80, 30).View()
	if n := strings.Count(frame, "ask Jenny first"); n != 1 {
		t.Fatalf("the note appears %d times, want once:\n%s", n, frame)
	}
}

// A note taller than the screen scrolls, and the scroll stops at its ends
// rather than running off into blank space.
func TestALongNoteScrollsAndStopsAtItsEnds(t *testing.T) {
	m := opened(longNotes(40), 100, 24)
	if frame := m.View(); strings.Contains(frame, "paragraph 40 ") || !strings.Contains(frame, "j k scroll") {
		t.Fatalf("want the top of the note and a way to scroll:\n%s", frame)
	}
	for range 100 {
		m, _ = press(t, m, "j")
	}
	frame := m.View()
	if !strings.Contains(frame, "paragraph 40 ") {
		t.Fatalf("scrolled to the end and the last line is not there:\n%s", frame)
	}
	if lines := strings.Count(frame, "\n") + 1; lines != 24 {
		t.Errorf("rendered %d lines into a 24-line terminal", lines)
	}
	m, _ = press(t, m, "k")
	if !strings.Contains(m.View(), "paragraph 39 ") {
		t.Error("one k from the end did not move back a line")
	}
	for range 100 {
		m, _ = press(t, m, "k")
	}
	if m.noteScroll != 0 {
		t.Errorf("scroll = %d at the top, want 0", m.noteScroll)
	}
}

// n edits the note over several lines: enter is a new line inside it, and
// only ctrl+s saves.
func TestNotesAreEditedOverSeveralLines(t *testing.T) {
	c, got := edited(t, task(1, "x"))
	m := New(c)
	m.mode = modeEdit
	m.edit = task(1, "x", func(x *api.Task) { x.Notes = "first line" })

	m, _ = press(t, m, "n")
	if m.mode != modeNotes || m.notes.Value() != "first line" {
		t.Fatalf("mode = %v, editor = %q; want the note in the editor", m.mode, m.notes.Value())
	}
	m, cmd := press(t, m, "enter")
	if m.mode != modeNotes {
		t.Fatalf("enter left the editor, mode = %v", m.mode)
	}
	if cmd != nil {
		cmd()
	}
	if got.Notes != nil {
		t.Fatal("enter saved the note")
	}
	for _, r := range "second" {
		m, _ = press(t, m, string(r))
	}
	m, cmd = press(t, m, "ctrl+s")
	if cmd == nil {
		t.Fatal("ctrl+s sent nothing")
	}
	cmd()
	if got.Notes == nil || *got.Notes != "first line\nsecond" {
		t.Fatalf("sent %+v, want both lines", got.Notes)
	}
	if m.mode != modeEdit {
		t.Fatalf("mode = %v, want the task back", m.mode)
	}
}

func TestEscapeDiscardsANoteBeingEdited(t *testing.T) {
	c, got := edited(t, task(1, "x"))
	m := New(c)
	m.mode = modeEdit
	m.edit = task(1, "x", func(x *api.Task) { x.Notes = "kept" })

	m, _ = press(t, m, "n")
	m, _ = press(t, m, "z")
	m, cmd := press(t, m, "esc")
	if cmd != nil {
		cmd()
	}
	if got.Notes != nil || m.mode != modeEdit {
		t.Fatalf("sent %+v, mode = %v; want nothing sent and the task back", got.Notes, m.mode)
	}
}

// The list pane has one line for a note, so a note of several lines must
// not push the list about.
func TestAManyLineNoteKeepsTheListPaneOneLine(t *testing.T) {
	frame := func(notes string) string {
		m := New(nil)
		m = m.applyTasks(tasksMsg{resp: &api.ListResponse{Tasks: []api.Task{
			task(1, "x", func(x *api.Task) { x.Notes = notes }),
		}}})
		m.width, m.height = 100, 24
		return m.View()
	}
	long, short := frame(longNotes(5)), frame("one line")
	if a, b := strings.Count(long, "\n"), strings.Count(short, "\n"); a != b {
		t.Fatalf("a five-line note took %d lines, a one-line note %d:\n%s", a, b, long)
	}
	if strings.Contains(long, "paragraph 2 ") {
		t.Error("the list pane showed more than the note's first line")
	}
}
