package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

// The two enums a task is triaged with. Both are short enough that a key
// press per step reaches any value faster than a menu would.
var (
	priorities   = []string{"low", "normal", "high"}
	difficulties = []string{"low", "medium", "high"}
)

// editedMsg is one field coming back changed. It carries the task the daemon
// stored rather than a refetch, so the pane always shows what was saved
// rather than what was typed.
type editedMsg struct {
	task *api.Task
	note string
	err  error
}

// field is one editable line: what it is called, the key that reaches it,
// how it reads, and the text you get to edit when it is not an enum. show
// and text differ because "30 min" is the right thing to read and the wrong
// thing to hand back to someone correcting it.
type field struct {
	name string
	key  string
	show func(api.Task) string
	text func(api.Task) string
	// only, when set, says whether the row is worth showing for this task.
	only func(api.Task) bool
}

var fields = []field{
	{"due", "u", func(t api.Task) string { return t.Due }, func(t api.Task) string { return t.Due }, nil},
	{"priority", "p", func(t api.Task) string { return t.Priority }, nil, nil},
	{"difficulty", "d", func(t api.Task) string { return t.Difficulty }, nil, nil},
	{"estimate", "m", showEstimate, textEstimate, nil},
	{"left", "l", showLeft, textLeft, func(t api.Task) bool { return t.RemainingMinutes > 0 }},
	{"repeats", "r", showRepeats, textRepeats, nil},
	{"notes", "n", func(t api.Task) string { return preview(t.Notes) }, func(t api.Task) string { return t.Notes }, nil},
	{"title", "t", func(t api.Task) string { return t.Title }, func(t api.Task) string { return t.Title }, nil},
}

func (f field) enum() bool { return f.text == nil }

func showEstimate(t api.Task) string {
	if t.EstimateMinutes == 0 {
		return ""
	}
	return fmt.Sprintf("%d min", t.EstimateMinutes)
}

func textEstimate(t api.Task) string {
	if t.EstimateMinutes == 0 {
		return ""
	}
	return strconv.Itoa(t.EstimateMinutes)
}

func showLeft(t api.Task) string {
	if t.RemainingMinutes == 0 {
		return ""
	}
	return fmt.Sprintf("%d min", t.RemainingMinutes)
}

func textLeft(t api.Task) string {
	if t.RemainingMinutes == 0 {
		return ""
	}
	return strconv.Itoa(t.RemainingMinutes)
}

func showRepeats(t api.Task) string {
	if t.Recur == nil {
		return ""
	}
	return recurPhrase(*t.Recur)
}

func textRepeats(t api.Task) string {
	if t.Recur == nil {
		return ""
	}
	return strings.TrimSpace(t.Recur.Kind + " " + t.Recur.Rule)
}

func (m Model) keyEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc", "enter":
		m.mode = m.editFrom
		if m.editFrom == modeDay {
			return m, m.fetchDay()
		}
		return m, m.fetch()
	case "?":
		m.mode, m.helpFrom = modeHelp, modeEdit
		return m, nil
	case "p":
		return m.cycleField("priority", priorities, m.edit.Priority, 1)
	case "P":
		return m.cycleField("priority", priorities, m.edit.Priority, -1)
	case "d":
		return m.cycleField("difficulty", difficulties, m.edit.Difficulty, 1)
	case "D":
		return m.cycleField("difficulty", difficulties, m.edit.Difficulty, -1)
	case "n":
		return m.openNotes()
	case "j", "down":
		return m.scrollNotes(1), nil
	case "k", "up":
		return m.scrollNotes(-1), nil
	}
	for _, f := range fields {
		if f.key == msg.String() && !f.enum() {
			return m.promptFor(f)
		}
	}
	return m, nil
}

// openTask shows one task's fields, from the list or the day. Back returns
// to where it was opened, refetched, since an edit can move the task in
// either.
func (m Model) openTask(t api.Task, from mode) Model {
	m.mode, m.edit, m.editFrom, m.message, m.noteScroll = modeEdit, t, from, "", 0
	return m
}

// promptFor opens the one-line editor over the field, prefilled, because
// changing a due date is nearly always a correction rather than a retype.
func (m Model) promptFor(f field) (tea.Model, tea.Cmd) {
	m.mode, m.field, m.failure = modeField, f.name, ""
	m = m.sizeInput(f.name + ": ")
	m.input.Placeholder = ""
	m.input.SetValue(f.text(m.edit))
	m.input.CursorEnd()
	m.input.Focus()
	return m, textinput.Blink
}

func (m Model) keyField(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeEdit
		m.input.Blur()
		return m, nil
	case "enter":
		name, value := m.field, strings.TrimSpace(m.input.Value())
		m.mode = modeEdit
		m.input.Blur()
		req, err := editRequest(name, value)
		if err != nil {
			m.failure = err.Error()
			return m, nil
		}
		note := name + " set"
		if value == "" {
			note = name + " cleared"
		}
		return m.send(req, note)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) cycleField(name string, values []string, current string, step int) (tea.Model, tea.Cmd) {
	next := cycle(values, current, step)
	req, err := editRequest(name, next)
	if err != nil {
		m.failure = err.Error()
		return m, nil
	}
	return m.send(req, name+" "+next)
}

func (m Model) send(req api.EditRequest, note string) (tea.Model, tea.Cmd) {
	id, c := m.edit.ID, m.client
	m.failure = ""
	return m, func() tea.Msg {
		t, err := c.Edit(id, req)
		return editedMsg{task: t, note: note, err: err}
	}
}

// cycle steps through an enum and wraps. It never lands on the empty value:
// clearing a field is deliberate enough to be worth typing, and one key
// press too many should not throw away what the model worked out.
func cycle(values []string, current string, step int) string {
	for i, v := range values {
		if v == current {
			return values[((i+step)%len(values)+len(values))%len(values)]
		}
	}
	if step > 0 {
		return values[0]
	}
	return values[len(values)-1]
}

// editRequest turns one field and the text given for it into the same edit
// the CLI would send, so a task changes one way however you reach it.
func editRequest(name, value string) (api.EditRequest, error) {
	var req api.EditRequest
	switch name {
	case "due":
		due, err := store.ReadDate(value)
		if err != nil {
			return req, err
		}
		req.Due = &due
	case "priority":
		req.Priority = &value
	case "difficulty":
		req.Difficulty = &value
	case "notes":
		req.Notes = &value
	case "title":
		if value == "" {
			return req, fmt.Errorf("a task needs a title")
		}
		req.Title = &value
	case "estimate":
		minutes := 0
		if value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return req, fmt.Errorf("estimate %q must be a number of minutes", value)
			}
			minutes = n
		}
		req.EstimateMinutes = &minutes
	case "left":
		minutes := 0
		if value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return req, fmt.Errorf("left %q must be a number of minutes", value)
			}
			minutes = n
		}
		req.RemainingMinutes = &minutes
	case "repeats":
		kind, rule := "", ""
		if value != "" {
			kind, rule, _ = strings.Cut(value, " ")
			kind, rule = strings.TrimSpace(kind), strings.TrimSpace(rule)
			if kind != "every" && kind != "after" {
				return req, fmt.Errorf("repeats reads %q or %q, then the rule", "every", "after")
			}
			if rule == "" {
				return req, fmt.Errorf("%s needs a rule after it", kind)
			}
		}
		req.RecurKind, req.RecurRule = &kind, &rule
	default:
		return req, fmt.Errorf("unknown field %q", name)
	}
	return req, nil
}

func (m Model) editView() string {
	width, room := m.noteArea()
	parts := []string{m.editHeader(width), m.editBody(width)}
	parts = append(parts, m.fullNotes(width, room)...)
	return strings.Join(append(parts, m.editFooter(width)), "\n")
}

func (m Model) editHeader(width int) string {
	right := ""
	if !m.edit.Enriched {
		right = faintStyle.Render("the model is still reading it")
	}
	if m.edit.Status == "done" {
		right = faintStyle.Render("done " + m.edit.DoneAt)
	}
	return pad(fmt.Sprintf("ordo · #%d", m.edit.ID), right, width) + "\n" + rule(width)
}

func (m Model) editBody(width int) string {
	lines := append(append([]string{""}, m.titleLines(width)...), "")
	// The key sits in front of the field it changes, the way the footer
	// reads everywhere else in ordo, rather than stranded at the far edge
	// of a wide terminal.
	for _, f := range fields {
		if f.only != nil && !f.only(m.edit) {
			continue
		}
		value := f.show(m.edit)
		if value == "" {
			value = faintStyle.Render("—")
		}
		row := "  " + faintStyle.Render(f.key) + "  " + faintStyle.Render(fmt.Sprintf("%-12s", f.name)) + value
		lines = append(lines, truncate(row, width))
	}
	if m.edit.Mnemo != nil {
		lines = append(lines, "", truncate("  "+linkStyle.Render("[["+m.edit.Mnemo.Slug+"]]")+
			faintStyle.Render(" "+m.edit.Mnemo.Title), width))
	}
	if m.edit.PinnedOn != "" {
		lines = append(lines, "", faintStyle.Render("  pinned to "+m.edit.PinnedOn))
	}
	return strings.Join(lines, "\n")
}

// sizeInput fits the one-line editor between its prompt and the edge, so a
// long title scrolls sideways under the cursor instead of running off the
// screen and pushing the pane up.
func (m Model) sizeInput(prompt string) Model {
	width, _ := m.size()
	m.input.Width = max(width-lipgloss.Width(prompt)-lipgloss.Width(m.input.Prompt)-2, 10)
	return m
}

// titleLines is the whole title, wrapped. The rows of the list and the day
// have one line for it; the open task is where the rest can be read.
func (m Model) titleLines(width int) []string {
	lines := wrapNotes(m.edit.Title, width-4)
	for i, l := range lines {
		lines[i] = "  " + selectStyle.Render(l)
	}
	return lines
}

func (m Model) editFooter(width int) string {
	if m.mode == modeField {
		return rule(width) + "\n" + m.field + ": " + m.input.View()
	}
	if m.mode == modeNotes {
		return rule(width) + "\n" + faintStyle.Render("enter new line · ctrl+s save · esc discard")
	}
	status := m.message
	if m.failure != "" {
		status = errorStyle.Render(m.failure)
	}
	keys := faintStyle.Render("p d cycle · shift reverses · u m l r n t edit · esc back · ? help · q quit")
	return rule(width) + "\n" + pad(keys, status, width)
}
