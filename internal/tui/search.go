package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cameronpyne-smith/ordo/internal/api"
)

const (
	searchPrompt      = "/ "
	searchPlaceholder = "title, notes, note slug or id"
)

// matches says whether a task answers what was typed: a number is the start
// of an id, and otherwise every word has to turn up somewhere in the title,
// the notes or the slug of the note it points at, in any order. The notes
// count because the title is the sentence as said, and the word you
// remember it by is as likely to be in what was added later.
func matches(t api.Task, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	if _, err := strconv.Atoi(query); err == nil && strings.HasPrefix(strconv.FormatInt(t.ID, 10), query) {
		return true
	}
	text := t.Title + "\n" + t.Notes
	if t.Mnemo != nil {
		text += "\n" + t.Mnemo.Slug
	}
	text = strings.ToLower(text)
	for _, word := range strings.Fields(query) {
		if !strings.Contains(text, word) {
			return false
		}
	}
	return true
}

// searched is what the list shows: the filter's tasks narrowed by the
// search, still in the daemon's order.
func searched(tasks []api.Task, query string) []api.Task {
	if strings.TrimSpace(query) == "" {
		return tasks
	}
	var out []api.Task
	for _, t := range tasks {
		if matches(t, query) {
			out = append(out, t)
		}
	}
	return out
}

// relayout lays the rows out again from the tasks already fetched, keeping
// the cursor on its task where the search still shows it.
func (m Model) relayout() Model {
	selected, had := m.selected()
	m.rows = layout(searched(m.tasks, m.query))
	m.cursor = follow(m.rows, m.cursor, selected.ID, had)
	return m
}

func (m Model) openSearch() (tea.Model, tea.Cmd) {
	m.mode, m.message = modeSearch, ""
	m = m.sizeInput(searchPrompt)
	m.input.Placeholder = searchPlaceholder
	m.input.SetValue(m.query)
	m.input.CursorEnd()
	m.input.Focus()
	return m, textinput.Blink
}

// keySearch narrows the list as each key is typed. Enter keeps the search
// and hands the keys back to the list, so the match can be opened or done;
// esc drops it. The arrows move through what is shown without leaving.
func (m Model) keySearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode, m.query = modeList, ""
		m.input.Blur()
		return m.relayout(), nil
	case "enter":
		m.mode = modeList
		m.input.Blur()
		return m, nil
	case "down":
		m.cursor = move(m.rows, m.cursor, 1)
		return m, nil
	case "up":
		m.cursor = move(m.rows, m.cursor, -1)
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != m.query {
		m.query = m.input.Value()
		m.rows = layout(searched(m.tasks, m.query))
		m.cursor = clampToTask(m.rows, 0)
	}
	return m, cmd
}
