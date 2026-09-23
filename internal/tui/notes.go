package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// A note is the one field with no natural length, so it is the one that
// gets room of its own: a preview on its row, the whole of it wrapped under
// the fields when the row cannot hold it, and an editor of several lines,
// since a note worth reading in full is one worth writing across lines.

// notesRowWidth is what the notes row spends before its value: the indent,
// the key and the padded field name.
const notesRowWidth = 17

func newNotesEditor() textarea.Model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.MaxHeight = 0
	ta.CharLimit = 0
	ta.Placeholder = "anything worth keeping with the task"
	return ta
}

// preview is a note's first line, marked when there is more after it.
func preview(notes string) string {
	first, rest, _ := strings.Cut(notes, "\n")
	if strings.TrimSpace(rest) != "" {
		return first + " …"
	}
	return first
}

// notesFit reports whether the notes row already shows the whole note, in
// which case repeating it underneath would only be noise.
func notesFit(notes string, width int) bool {
	return !strings.Contains(notes, "\n") && lipgloss.Width(notes) <= width-notesRowWidth
}

func wrapNotes(notes string, width int) []string {
	if width < 10 {
		width = 10
	}
	lines := strings.Split(lipgloss.NewStyle().Width(width).Render(notes), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}

// noteVisible is how many lines of a note fit in room, keeping a blank line
// above it and, when it does not all fit, a line saying where you are.
func noteVisible(total, room int) int {
	visible := room - 1
	if total > visible {
		visible--
	}
	return visible
}

func clampScroll(scroll, total, visible int) int {
	return max(0, min(scroll, total-visible))
}

// noteArea is the width of the screen and the lines left under the fields
// for the full note.
func (m Model) noteArea() (int, int) {
	width, height := m.size()
	used := lipgloss.Height(m.editHeader(width)) + lipgloss.Height(m.editBody(width)) + lipgloss.Height(m.editFooter(width))
	return width, height - used
}

// fullNotes is exactly room lines: the note wrapped to the screen and
// scrolled, or blank when the notes row already shows all of it.
func (m Model) fullNotes(width, room int) []string {
	out := make([]string, 0, max(room, 0))
	notes := m.edit.Notes
	if notes != "" && !notesFit(notes, width) && room >= 3 {
		lines := wrapNotes(notes, width-4)
		visible := noteVisible(len(lines), room)
		scroll := clampScroll(m.noteScroll, len(lines), visible)
		out = append(out, "")
		for _, l := range lines[scroll:min(scroll+visible, len(lines))] {
			out = append(out, "  "+l)
		}
		if len(lines) > visible {
			out = append(out, faintStyle.Render(fmt.Sprintf("  lines %d–%d of %d · j k scroll",
				scroll+1, min(scroll+visible, len(lines)), len(lines))))
		}
	}
	for len(out) < room {
		out = append(out, "")
	}
	return out
}

// scrollNotes moves the note by step lines, held to what there is to show.
func (m Model) scrollNotes(step int) Model {
	width, room := m.noteArea()
	if m.edit.Notes == "" || notesFit(m.edit.Notes, width) {
		return m
	}
	total := len(wrapNotes(m.edit.Notes, width-4))
	m.noteScroll = clampScroll(m.noteScroll+step, total, noteVisible(total, room))
	return m
}

// openNotes puts the note into the editor, sized to the screen under the
// task's title.
func (m Model) openNotes() (tea.Model, tea.Cmd) {
	m.mode, m.failure = modeNotes, ""
	m = m.sizeNotes()
	m.notes.SetValue(m.edit.Notes)
	return m, m.notes.Focus()
}

func (m Model) sizeNotes() Model {
	width, height := m.size()
	m.notes.SetWidth(width - 2)
	m.notes.SetHeight(max(height-lipgloss.Height(m.editHeader(width))-lipgloss.Height(m.editFooter(width))-3, 3))
	return m
}

// keyNotes leaves enter to the editor, where it starts a new line, so saving
// is ctrl+s. esc discards, as it backs out of everything else.
func (m Model) keyNotes(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeEdit
		m.notes.Blur()
		return m, nil
	case "ctrl+s":
		value := strings.TrimSpace(m.notes.Value())
		m.mode, m.noteScroll = modeEdit, 0
		m.notes.Blur()
		req, err := editRequest("notes", value)
		if err != nil {
			m.failure = err.Error()
			return m, nil
		}
		note := "notes set"
		if value == "" {
			note = "notes cleared"
		}
		return m.send(req, note)
	}
	var cmd tea.Cmd
	m.notes, cmd = m.notes.Update(msg)
	return m, cmd
}

func (m Model) notesView(width, height int) string {
	header := m.editHeader(width)
	footer := m.editFooter(width)
	body := strings.Join([]string{"", "  " + truncate(selectStyle.Render(m.edit.Title), width-2), "", m.notes.View()}, "\n")
	gap := max(height-lipgloss.Height(header)-lipgloss.Height(body)-lipgloss.Height(footer), 0)
	return strings.Join([]string{header, body + strings.Repeat("\n", gap), footer}, "\n")
}
